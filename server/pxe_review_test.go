package main

// Tests added for PXE adversarial review round 1 (docs/dev-process.md). Each one failed against the code before its fix.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #1: an arm consumed (or disarmed, or a server degraded) after the decision but before the send gets no reply.
func TestPXERecheckBeforeSend(t *testing.T) {
	for _, change := range []string{"boot", "disarm", "degrade", "expire"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, "")
			client, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			defer client.Close()
			p := startLoopbackPXE(t, f, t.TempDir(), client)
			f.arm(t, armedMAC)
			hook := func() {
				switch change {
				case "boot":
					f.boot(t, armedMAC)
				case "disarm":
					f.s.doDisarm(armedMAC)
				case "expire":
					f.s.mu.Lock() // the fixture clock is read by the server under s.mu
					f.now = f.now.Add(3 * time.Hour)
					f.s.mu.Unlock()
				case "degrade":
					f.s.mu.Lock()
					f.s.degraded = true
					f.s.mu.Unlock()
				}
			}
			p.beforeSend.Store(&hook)
			client.WriteToUDP(pxeDiscover(armedMAC, 7).bytes(), p.DHCPAddr())
			if r := recvDHCP(t, client, 400*time.Millisecond); r != nil {
				t.Fatalf("replied after the arm changed (%s)", change)
			}
		})
	}
}

// overloaded builds a DISCOVER whose options field carries only 53 and 52, with the rest in file and/or sname.
func overloaded(overload byte, opts, file, sname []byte) []byte {
	b := make([]byte, dhcpHeaderLen)
	b[0], b[1], b[2] = bootRequest, 1, 6
	hw, _ := net.ParseMAC(armedMAC)
	copy(b[28:], hw)
	copy(b[44:108], sname)
	copy(b[108:236], file)
	b = append(b, dhcpMagic...)
	b = append(b, optMsgType, 1, dhcpDiscover, optOverload, 1, overload)
	b = append(b, opts...)
	return append(b, optEnd)
}

func tlv(code byte, v string) []byte { return append([]byte{code, byte(len(v))}, v...) }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// #2: option overload.
func TestDHCPOptionOverload(t *testing.T) {
	_, p := newPXEFixture(t)
	end := []byte{optEnd}
	pxe := cat(tlv(optVendorClass, "PXEClient:Arch:00007"), tlv(optClientArch, "\x00\x07"))

	// Everything PXE-relevant in file: seen, answered.
	pk, err := parseDHCP(overloaded(1, nil, cat(pxe, end), nil))
	if err != nil {
		t.Fatal(err)
	}
	if d := p.decide(pk, false, testServerIP); d.action != pxeReply || d.file != "ipxe.efi" {
		t.Fatalf("PXE options in the file field were not seen: %+v", d)
	}
	// iPXE user class hidden in sname (overload 3): gets the boot URL.
	pk, err = parseDHCP(overloaded(3, nil, cat(pxe, end), cat(tlv(optUserClass, "iPXE"), end)))
	if err != nil {
		t.Fatal(err)
	}
	if d := p.decide(pk, false, testServerIP); !strings.HasPrefix(d.file, "http://") {
		t.Fatalf("iPXE user class in sname not seen: %+v", d)
	}
	// RFC 3396 order: options field, then file, then sname.
	pk, err = parseDHCP(overloaded(3, tlv(optClientUUID, "A"), cat(tlv(optClientUUID, "B"), end), cat(tlv(optClientUUID, "C"), end)))
	if err != nil || string(pk.opts[optClientUUID]) != "ABC" {
		t.Fatalf("concatenation order: %q %v", pk.opts[optClientUUID], err)
	}
	// Overload 1 only: sname is NOT parsed as options (it's a server name here).
	pk, err = parseDHCP(overloaded(1, nil, cat(pxe, end), cat(tlv(optUserClass, "iPXE"), end)))
	if err != nil || pk.isIPXE() {
		t.Fatalf("sname parsed without overload 2: %v %v", pk.isIPXE(), err)
	}
	// Malformed: the packet is ignored.
	for name, b := range map[string][]byte{
		"overload 0":             overloaded(0, nil, cat(pxe, end), nil),
		"overload 4":             overloaded(4, nil, cat(pxe, end), nil),
		"file without end":       overloaded(1, nil, bytes.Repeat([]byte{optPad}, 128), nil),
		"file option overruns":   overloaded(1, nil, append(bytes.Repeat([]byte{optPad}, 126), optVendorClass, 9), nil),
		"sname without end":      overloaded(2, nil, nil, bytes.Repeat([]byte{optPad}, 64)),
		"overload inside file":   overloaded(1, nil, cat(tlv(optOverload, "\x02"), end), nil),
		"overload value 2 bytes": cat(overloaded(1, nil, cat(pxe, end), nil)[:dhcpHeaderLen+4], []byte{optMsgType, 1, 1, optOverload, 2, 1, 1, optEnd}),
	} {
		if _, err := parseDHCP(b); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

// #4: the limiter's key map stays under its cap and total output is budgeted, whatever keys an attacker makes up.
func TestLogLimiterBounded(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLogLimiter(time.Minute, func() time.Time { return now })
	lines := 0
	var last string
	l.out = func(f string, a ...any) { lines++; last = fmt.Sprintf(f, a...) }
	for i := 0; i < 100000; i++ {
		l.logf(fmt.Sprintf("tftp-deny:%d", i), "x %d", i)
		if len(l.last) > logKeyCap {
			t.Fatalf("map has %d keys, cap %d", len(l.last), logKeyCap)
		}
	}
	if lines > int(logBurst) {
		t.Fatalf("%d lines from a burst, budget %v", lines, logBurst)
	}
	now = now.Add(time.Second)
	before := lines
	for i := 0; i < 1000; i++ {
		l.logf(fmt.Sprintf("again:%d", i), "y")
	}
	if got := lines - before; got > int(logRate)+1 || got == 0 {
		t.Fatalf("%d lines after 1 s, want <= %v plus the suppression notice", got, logRate)
	}
	l.out = func(f string, a ...any) { lines++; last = fmt.Sprintf(f, a...) }
	now = now.Add(time.Minute)
	l.logf("z", "z")
	if last != "z" {
		t.Fatalf("last line %q", last)
	}
}

// File names from the wire are logged quoted, so control characters can't forge log lines.
func TestTFTPLogsQuoteFileNames(t *testing.T) {
	dir := t.TempDir()
	served := "ok\nFAKE served"
	if runtime.GOOS != "windows" {
		os.WriteFile(filepath.Join(dir, served), []byte("x"), 0o644)
	}
	ts := newTestTFTP(t, dir)
	var mu sync.Mutex
	var out []string
	ts.lim.setOut(func(f string, a ...any) { mu.Lock(); out = append(out, fmt.Sprintf(f, a...)); mu.Unlock() })
	tftpGet(ts.conn.LocalAddr().(*net.UDPAddr), "x\nFAKE: refused", nil)
	if runtime.GOOS != "windows" {
		if _, err := tftpGet(ts.conn.LocalAddr().(*net.UDPAddr), served, nil); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond) // the "sent" line is logged after the last ACK
	mu.Lock()
	defer mu.Unlock()
	for _, l := range out {
		if strings.Contains(l, "\n") {
			t.Fatalf("raw newline in log line %q", l)
		}
	}
	if len(out) < 2 && runtime.GOOS != "windows" {
		t.Fatalf("expected a refusal and a sent line: %q", out)
	}
}

// rawRRQ sends an RRQ from c and returns the first reply (and its source).
func rawRRQ(t *testing.T, c *net.UDPConn, to *net.UDPAddr, file string, opts ...string) ([]byte, *net.UDPAddr) {
	t.Helper()
	req := append([]byte{0, tftpRRQ}, file+"\x00octet\x00"...)
	for _, o := range opts {
		req = append(append(req, o...), 0)
	}
	c.WriteToUDP(req, to)
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := c.ReadFromUDP(buf)
	if err != nil {
		return nil, nil
	}
	return buf[:n], from
}

// #5: timeout option limits.
func TestTFTPTimeoutNegotiation(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(100), 0o644)
	ts := newTestTFTP(t, dir)
	for v, want := range map[string]string{"255": "", "11": "", "0": "", "-1": "", "abc": "", "10": "10", "1": "1", "5": "5"} {
		c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		got, from := rawRRQ(t, c, ts.conn.LocalAddr().(*net.UDPAddr), "f", "timeout", v, "tsize", "0")
		if got == nil {
			t.Fatalf("timeout=%s: no reply", v)
		}
		has := bytes.Contains(got, []byte("timeout\x00"))
		if want == "" && has || want != "" && !bytes.Contains(got, []byte("timeout\x00"+want+"\x00")) {
			t.Errorf("timeout=%s: OACK %q", v, got)
		}
		c.WriteToUDP(tftpErrorPkt(0, "done"), from)
		c.Close()
	}
}

// #5: per-client-IP cap, the global cap filled and recovered, idle limit, lifetime, and shutdown that does not wait for timeouts.
func TestTFTPPerIPCap(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	for i := 0; i < 4; i++ {
		c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		defer c.Close()
		if p, _ := rawRRQ(t, c, addr, "f"); p == nil || binary.BigEndian.Uint16(p) != tftpDATA {
			t.Fatalf("transfer %d refused: %q", i, p)
		}
	}
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	if p, _ := rawRRQ(t, c, addr, "f"); p == nil || binary.BigEndian.Uint16(p) != tftpERROR {
		t.Fatalf("5th transfer from one IP not refused: %q", p)
	}
	if runtime.GOOS == "linux" { // 127.0.0.0/8 is all loopback on Linux
		o, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
		if err != nil {
			t.Skip(err)
		}
		defer o.Close()
		if p, _ := rawRRQ(t, o, addr, "f"); p == nil || binary.BigEndian.Uint16(p) != tftpDATA {
			t.Fatalf("another IP was refused: %q", p)
		}
	}
}

func TestTFTPGlobalCapSaturationAndRecovery(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs several loopback addresses")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	type held struct {
		c    *net.UDPConn
		from *net.UDPAddr
	}
	var hs []held
	for i := 0; i < 64; i++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 1, byte(1+i/4))})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		p, from := rawRRQ(t, c, addr, "f")
		if p == nil || binary.BigEndian.Uint16(p) != tftpDATA {
			t.Fatalf("transfer %d refused: %q", i, p)
		}
		hs = append(hs, held{c, from})
	}
	if n := ts.activeCount(); n != 64 {
		t.Fatalf("active %d", n)
	}
	extra, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 2, 1)})
	defer extra.Close()
	if p, _ := rawRRQ(t, extra, addr, "f"); p == nil || binary.BigEndian.Uint16(p) != tftpERROR {
		t.Fatalf("65th transfer not refused: %q", p)
	}
	for _, h := range hs {
		h.c.WriteToUDP(tftpErrorPkt(0, "abort"), h.from)
	}
	deadline := time.Now().Add(2 * time.Second)
	for ts.activeCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := ts.activeCount(); n != 0 {
		t.Fatalf("slots not released: %d", n)
	}
	if got, err := tftpGet(addr, "f", nil); err != nil || len(got) != 5000 {
		t.Fatalf("no recovery: %v", err)
	}
}

func TestTFTPIdleLimitIgnoresNegotiatedTimeout(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	ts := newTestTFTPLimits(t, dir, tftpLimits{Idle: 300 * time.Millisecond})
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	if p, _ := rawRRQ(t, c, ts.conn.LocalAddr().(*net.UDPAddr), "f", "timeout", "10"); p == nil {
		t.Fatal("no OACK")
	}
	// Never ACK. With timeout=10 and 5 retries the old code held the slot for a minute.
	deadline := time.Now().Add(2 * time.Second)
	for ts.activeCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := ts.activeCount(); n != 0 {
		t.Fatalf("idle transfer still holds a slot after 2 s")
	}
}

func TestTFTPLifetimeLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big"), pattern(512*1000), 0o644)
	ts := newTestTFTPLimits(t, dir, tftpLimits{Lifetime: 400 * time.Millisecond})
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	start := time.Now()
	p, from := rawRRQ(t, c, ts.conn.LocalAddr().(*net.UDPAddr), "big")
	if p == nil {
		t.Fatal("no data")
	}
	// A slow but steady client: ACK every block after 20 ms. It would need ~20 s; the lifetime must end it at ~400 ms.
	ended := make(chan time.Duration, 1)
	go func() {
		for time.Since(start) < 5*time.Second {
			if ts.activeCount() == 0 {
				ended <- time.Since(start)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		ended <- time.Hour
	}()
	buf := make([]byte, 1500)
	go func() {
		for {
			blk := binary.BigEndian.Uint16(p[2:])
			time.Sleep(20 * time.Millisecond)
			c.WriteToUDP([]byte{0, tftpACK, byte(blk >> 8), byte(blk)}, from)
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _, err := c.ReadFromUDP(buf)
			if err != nil || n < 4 {
				return
			}
			p = append([]byte(nil), buf[:n]...)
		}
	}()
	if d := <-ended; d > 900*time.Millisecond {
		t.Fatalf("transfer ended after %v; lifetime is 400 ms", d)
	}
}

func TestTFTPCloseInterruptsTransfers(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	c, err := listenUDP(net.IPv4(127, 0, 0, 1), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTFTPServer(c, dir, 10*time.Second, tftpLimits{}, newLogLimiter(time.Minute, time.Now))
	go ts.serve()
	cl, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer cl.Close()
	if p, _ := rawRRQ(t, cl, c.LocalAddr().(*net.UDPAddr), "f", "timeout", "10"); p == nil {
		t.Fatal("no OACK")
	}
	done := make(chan struct{})
	start := time.Now()
	go func() { ts.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("close waited for the transfer's timeout")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("close took %v", d)
	}
}

// #6: a name flipped between a regular file and a symlink to an outside file never serves the outside file.
func TestTFTPSymlinkSwapRace(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tftp")
	os.Mkdir(dir, 0o755)
	secret := filepath.Join(root, "secret")
	os.WriteFile(secret, []byte("SECRET"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "probe")); err != nil {
		t.Skip("no symlinks here")
	}
	os.Remove(filepath.Join(dir, "probe"))
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	name := filepath.Join(dir, "f")
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			tmp := filepath.Join(dir, fmt.Sprintf(".tmp%d", i))
			if i%2 == 0 {
				os.WriteFile(tmp, []byte("inside"), 0o644)
			} else {
				os.Symlink(secret, tmp)
			}
			os.Rename(tmp, name)
		}
	}()
	served, refused := 0, 0
	for i := 0; i < 300 || served == 0 || refused == 0; i++ {
		if i >= 3000 {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("race not exercised in 3000 fetches: %d served, %d refused", served, refused)
		}
		got, err := tftpGet(addr, "f", nil)
		if err != nil {
			refused++
		}
		if err == nil {
			served++
			if string(got) != "inside" {
				stop.Store(true)
				wg.Wait()
				t.Fatalf("served %q", got)
			}
		}
	}
	stop.Store(true)
	wg.Wait()
	// Non-vacuous: both paths ran (a normal fetch of the regular file, and a refusal of the swapped-in symlink).
	if served == 0 || refused == 0 {
		t.Fatalf("served %d, refused %d: the race was not exercised", served, refused)
	}
	t.Logf("%d fetches served the inside file, %d were refused", served, refused)
}

// #7: a duplicate RRQ while a transfer is running is a retransmission, not a second transfer.
func TestTFTPDuplicateRRQ(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	ts := newTestTFTPTimeout(t, dir, 2*time.Second)
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	req := []byte("\x00\x01f\x00octet\x00")
	to := ts.conn.LocalAddr().(*net.UDPAddr)
	c.WriteToUDP(req, to)
	c.WriteToUDP(req, to)
	time.Sleep(50 * time.Millisecond)
	c.WriteToUDP(req, to)
	tids := map[int]bool{}
	buf := make([]byte, 1500)
	for {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, from, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		tids[from.Port] = true
	}
	if len(tids) != 1 || ts.activeCount() != 1 {
		t.Fatalf("%d transfers started (TIDs %v), active %d", len(tids), tids, ts.activeCount())
	}
}

// Duplicate ACKs are not answered (no Sorcerer's Apprentice): block 2 is sent once.
func TestTFTPDuplicateACK(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(5000), 0o644)
	ts := newTestTFTPTimeout(t, dir, 2*time.Second)
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	p, from := rawRRQ(t, c, ts.conn.LocalAddr().(*net.UDPAddr), "f")
	if p == nil || binary.BigEndian.Uint16(p[2:]) != 1 {
		t.Fatalf("first block: %q", p)
	}
	ack1 := []byte{0, tftpACK, 0, 1}
	c.WriteToUDP(ack1, from)
	c.WriteToUDP(ack1, from)
	c.WriteToUDP(ack1, from)
	count := map[uint16]int{}
	buf := make([]byte, 1500)
	for {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n >= 4 {
			count[binary.BigEndian.Uint16(buf[2:])]++
		}
	}
	if count[2] != 1 || len(count) != 1 {
		t.Fatalf("blocks received after duplicate ACKs: %v", count)
	}
}

// #8: arch 6 maps only to -pxe-file-ia32.
func TestPXEIA32(t *testing.T) {
	_, p := newPXEFixture(t)
	if d := p.decide(mustParse(t, pxeDiscover(armedMAC, 6).bytes()), false, testServerIP); d.action != pxeSilent {
		t.Fatalf("IA32 got %+v with no IA32 file configured", d)
	}
	p.cfg.FileIA32 = "ipxe-ia32.efi"
	if d := p.decide(mustParse(t, pxeDiscover(armedMAC, 6).bytes()), false, testServerIP); d.file != "ipxe-ia32.efi" {
		t.Fatalf("IA32 got %+v", d)
	}
	for _, a := range []int{7, 9} {
		if d := p.decide(mustParse(t, pxeDiscover(armedMAC, a).bytes()), false, testServerIP); d.file != "ipxe.efi" {
			t.Fatalf("arch %d got %+v", a, d)
		}
	}
}

// #9: options 93 and 94 are echoed (RFC 4578 section 2); 94 only if sent.
func TestProxyReplyEchoesArchAndNDI(t *testing.T) {
	b := pxeDiscover(armedMAC, 7).bytes()
	b = append(b[:len(b)-1], optClientNDI, 3, 1, 3, 16, optEnd)
	rep := mustParse(t, buildProxyReply(mustParse(t, b), dhcpOffer, testServerIP, "ipxe.efi"))
	if !bytes.Equal(rep.opts[optClientArch], []byte{0, 7}) || !bytes.Equal(rep.opts[optClientNDI], []byte{1, 3, 16}) {
		t.Fatalf("93=%v 94=%v", rep.opts[optClientArch], rep.opts[optClientNDI])
	}
	rep = mustParse(t, buildProxyReply(mustParse(t, pxeDiscover(armedMAC, 7).bytes()), dhcpOffer, testServerIP, "ipxe.efi"))
	if _, ok := rep.opts[optClientNDI]; ok {
		t.Fatal("94 invented")
	}
}

// End to end under concurrent arming: every reply that does arrive is a well-formed proxy offer for the armed MAC, read while
// the senders run.
func TestPXEConcurrentRepliesAreWellFormed(t *testing.T) {
	f := newFixture(t, "")
	client, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer client.Close()
	p := startLoopbackPXE(t, f, t.TempDir(), client)
	var replies, bad atomic.Int32
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 1500)
		for {
			client.SetReadDeadline(time.Now().Add(time.Second))
			n, _, err := client.ReadFromUDP(buf)
			if err != nil {
				return
			}
			replies.Add(1)
			r, err := parseDHCP(buf[:n])
			if err != nil || r.op != bootReply || r.msgType() != dhcpOffer || !bytes.Equal(buf[16:20], []byte{0, 0, 0, 0}) ||
				string(r.opts[optBootFile]) != "ipxe.efi" || net.HardwareAddr(r.chaddr[:6]).String() != armedMAC {
				bad.Add(1)
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				f.s.doArm(armedMAC, "win11-pro")
				time.Sleep(time.Millisecond)
				f.s.doDisarm(armedMAC)
			}
		}()
		go func() {
			defer wg.Done()
			c, _ := net.DialUDP("udp4", nil, p.DHCPAddr())
			defer c.Close()
			for j := 0; j < 50; j++ {
				c.Write(pxeDiscover(armedMAC, 7).bytes())
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	// Finally armed: a reply must come.
	f.s.doArm(armedMAC, "win11-pro")
	c, _ := net.DialUDP("udp4", nil, p.DHCPAddr())
	c.Write(pxeDiscover(armedMAC, 7).bytes())
	c.Close()
	<-readerDone
	if bad.Load() != 0 || replies.Load() == 0 {
		t.Fatalf("%d replies, %d malformed", replies.Load(), bad.Load())
	}
}
