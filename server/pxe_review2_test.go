package main

// Tests added for PXE adversarial review round 2 (docs/dev-process.md). Each one failed against the code before its fix.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingConn behaves like a socket whose send buffer is full: Write blocks until the write deadline (if one is set) or Close.
type blockingConn struct {
	*net.UDPConn
	mu     sync.Mutex
	wdl    time.Time
	closed chan struct{}
	once   sync.Once
	writes atomic.Int32
}

func newBlockingConn(c *net.UDPConn) *blockingConn {
	return &blockingConn{UDPConn: c, closed: make(chan struct{})}
}

func (b *blockingConn) SetWriteDeadline(t time.Time) error {
	b.mu.Lock()
	b.wdl = t
	b.mu.Unlock()
	return nil
}

func (b *blockingConn) block() error {
	b.writes.Add(1)
	b.mu.Lock()
	dl := b.wdl
	b.mu.Unlock()
	var timeout <-chan time.Time
	if !dl.IsZero() {
		timeout = time.After(time.Until(dl))
	}
	select {
	case <-timeout:
		return os.ErrDeadlineExceeded
	case <-b.closed:
		return net.ErrClosed
	}
}

func (b *blockingConn) Write([]byte) (int, error) { return 0, b.block() }

func (b *blockingConn) WriteToUDP([]byte, *net.UDPAddr) (int, error) { return 0, b.block() }

func (b *blockingConn) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.UDPConn.Close()
}

// A: a transfer whose writes block still releases its slot (bounded by the write deadline and the lifetime).
func TestTFTPBlockedWriteReleasesSlot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	var bc atomic.Pointer[blockingConn]
	ts := newTestTFTPWith(t, dir, time.Second, tftpLimits{WriteTimeout: 200 * time.Millisecond, Lifetime: time.Second}, func(ts *tftpServer) {
		ts.dial = func(laddr, raddr *net.UDPAddr) (tftpConn, error) {
			c, err := net.DialUDP("udp4", laddr, raddr)
			if err != nil {
				return nil, err
			}
			b := newBlockingConn(c)
			bc.Store(b)
			return b, nil
		}
	})
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	c.WriteToUDP([]byte("\x00\x01f\x00octet\x00"), ts.conn.LocalAddr().(*net.UDPAddr))
	deadline := time.Now().Add(3 * time.Second)
	for (bc.Load() == nil || ts.activeCount() != 0) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if bc.Load() == nil {
		t.Fatal("transfer never started")
	}
	if n := ts.activeCount(); n != 0 {
		t.Fatalf("a transfer blocked in Write still holds its slot after 3 s (lifetime 1 s)")
	}
}

// A: a blocked refusal write does not stall the listener.
func TestTFTPBlockedDenyDoesNotStallListener(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok"), []byte("ok"), 0o644)
	ts := newTestTFTPWith(t, dir, 200*time.Millisecond, tftpLimits{WriteTimeout: 100 * time.Millisecond}, func(ts *tftpServer) {
		ts.denyOut = newBlockingConn(ts.conn) // refusals block; transfers use their own sockets
	})
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	for i := 0; i < 3; i++ {
		c.WriteToUDP([]byte("\x00\x02x\x00octet\x00"), addr) // WRQ: refused through the blocking writer
	}
	start := time.Now()
	got, err := tftpGet(addr, "ok", nil)
	if err != nil || string(got) != "ok" {
		t.Fatalf("listener stalled behind blocked refusals: %v (after %v)", err, time.Since(start))
	}
}

// B: an error that carries the requested name (a *PathError from the lookup) can't forge a log line.
func TestTFTPErrorTextCannotForgeLogLines(t *testing.T) {
	dir := t.TempDir()
	ts := newTestTFTP(t, dir)
	var mu sync.Mutex
	var out []string
	ts.lim.setOut(func(f string, a ...any) { mu.Lock(); out = append(out, fmt.Sprintf(f, a...)); mu.Unlock() })
	name := "x\nFORGED line from the network" // no '/', '\\' or ':', so it reaches the filesystem lookup
	if _, err := tftpGet(ts.conn.LocalAddr().(*net.UDPAddr), name, nil); err == nil {
		t.Fatal("nonexistent file served")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(out) == 0 {
		t.Fatal("refusal not logged")
	}
	for _, l := range out {
		if strings.Contains(l, "\n") {
			t.Fatalf("raw newline in log line (forgeable): %q", l)
		}
		if !strings.Contains(l, `x\nFORGED`) {
			t.Fatalf("name not shown quoted: %q", l)
		}
	}
}

func fixedLimiter() (*logLimiter, *time.Time, *[]string) {
	now := time.Unix(1000, 0)
	l := newLogLimiter(time.Minute, func() time.Time { return now })
	var lines []string
	l.out = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	return l, &now, &lines
}

// C: a flood of noise can't starve a policy line.
func TestLogNoiseCannotStarvePolicy(t *testing.T) {
	l, _, lines := fixedLimiter()
	for i := 0; i < 10000; i++ {
		l.noisef(fmt.Sprintf("tftp-deny:%d", i), "deny %d", i)
	}
	l.logf("offered:aa", "OFFER")
	if (*lines)[len(*lines)-1] != "OFFER" {
		t.Fatalf("policy line starved: last line %q", (*lines)[len(*lines)-1])
	}
	if got := len(*lines); got > int(logNoiseBurst)+1 {
		t.Fatalf("%d lines from a noise burst (burst %v + 1 policy)", got, logNoiseBurst)
	}
}

// C: a line dropped for budget is not stamped, so its retry prints once tokens are back.
func TestLogDroppedLineIsRetried(t *testing.T) {
	l, now, lines := fixedLimiter()
	for i := 0; i < int(logBurst); i++ {
		l.logf(fmt.Sprintf("k%d", i), "fill")
	}
	l.logf("offered:mac", "OFFER") // no tokens: dropped
	if (*lines)[len(*lines)-1] == "OFFER" {
		t.Fatal("budget not enforced")
	}
	*now = now.Add(200 * time.Millisecond) // 4 tokens back
	l.logf("offered:mac", "OFFER")
	if (*lines)[len(*lines)-1] != "OFFER" {
		t.Fatalf("retry of a budget-dropped line suppressed for the per-key period: %q", *lines)
	}
}

// C: exact suppressed counts, and recovery after refill.
func TestLogSuppressedCountsAndRecovery(t *testing.T) {
	l, now, lines := fixedLimiter()
	for i := 0; i < 5; i++ {
		l.noisef("notarmed:a", "a")
	}
	if p, n := l.suppressed(); p != 0 || n != 4 {
		t.Fatalf("suppressed = %d/%d, want 0/4 (per-key repeats count)", p, n)
	}
	for i := 0; i < 100; i++ {
		l.noisef(fmt.Sprintf("x%d", i), "x")
	}
	// Burst 20: "a" (1 token); x0 plus the notice for the 4 repeats (2 tokens); x1..x17 (17 tokens); x18..x99 dropped: 82.
	if (*lines)[1] != "pxe: 4 noise log line(s) suppressed (repeats within 1m0s, or over the rate limit)" {
		t.Fatalf("notice: %q", (*lines)[1])
	}
	if _, n := l.suppressed(); n != 82 {
		t.Fatalf("suppressed noise = %d, want 82", n)
	}
	before := len(*lines)
	*now = now.Add(time.Second) // 5 tokens
	for i := 0; i < 10; i++ {
		l.noisef(fmt.Sprintf("y%d", i), "y")
	}
	got := (*lines)[before:]
	if len(got) != 5 || !strings.Contains(got[0], "82 noise log line(s) suppressed") {
		t.Fatalf("after refill: %q", got)
	}
	if _, n := l.suppressed(); n != 6 { // y0's notice took a token, so y0..y3 printed, y4..y9 dropped
		t.Fatalf("suppressed after refill = %d, want 6", n)
	}
}

// C: total output (notices included) stays within burst + rate x time, under alternating repeats and new keys.
func TestLogOutputRateBound(t *testing.T) {
	l, now, lines := fixedLimiter()
	const secs = 10
	for step := 0; step < secs*100; step++ {
		*now = now.Add(10 * time.Millisecond)
		l.noisef("repeat", "r")                     // denied after the first
		l.noisef(fmt.Sprintf("new%d", step), "n")   // admitted while tokens last
		l.logf(fmt.Sprintf("pol%d", step%7), "p")   // policy, repeats per key
		l.logf(fmt.Sprintf("polnew%d", step), "pn") // policy, new keys
	}
	var noise, policy int
	for _, ln := range *lines {
		switch {
		case ln == "r" || ln == "n" || strings.Contains(ln, "noise log line"):
			noise++
		default:
			policy++
		}
	}
	if max := int(logNoiseBurst + logNoiseRate*secs); noise > max {
		t.Fatalf("noise output %d > %d", noise, max)
	}
	if max := int(logBurst + logRate*secs); policy > max {
		t.Fatalf("policy output %d > %d", policy, max)
	}
}
