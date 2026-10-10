package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tftpGet is a minimal RFC 1350/2347 client for tests. opts may request blksize/tsize.
func tftpGet(server *net.UDPAddr, name string, opts map[string]string) ([]byte, error) {
	return tftpRequest(server, tftpRRQ, name, "octet", opts)
}

func tftpRequest(server *net.UDPAddr, op uint16, name, mode string, opts map[string]string) ([]byte, error) {
	return tftpRequestQuiet(server, op, name, mode, opts, 0)
}

// tftpRequestQuiet also fails if the server sends anything within quiet after the final block (an extra block, a resend).
func tftpRequestQuiet(server *net.UDPAddr, op uint16, name, mode string, opts map[string]string, quiet time.Duration) ([]byte, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	req := []byte{0, byte(op)}
	req = append(append(append(append(req, name...), 0), mode...), 0)
	for k, v := range opts {
		req = append(append(append(append(req, k...), 0), v...), 0)
	}
	if _, err := c.WriteToUDP(req, server); err != nil {
		return nil, err
	}
	blk := 512
	if v, ok := opts["blksize"]; ok {
		blk, _ = strconv.Atoi(v)
	}
	var out []byte
	buf := make([]byte, 70000)
	next := uint16(1)
	for {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return out, err
		}
		p := buf[:n]
		ack := func(b uint16) { c.WriteToUDP([]byte{0, tftpACK, byte(b >> 8), byte(b)}, from) }
		switch binary.BigEndian.Uint16(p) {
		case tftpERROR:
			return out, fmt.Errorf("tftp error %d: %s", binary.BigEndian.Uint16(p[2:]), strings.TrimRight(string(p[4:]), "\x00"))
		case tftpOACK:
			f := bytes.Split(p[2:n-1], []byte{0})
			for i := 0; i+1 < len(f); i += 2 {
				if string(f[i]) == "blksize" {
					blk, _ = strconv.Atoi(string(f[i+1]))
				}
			}
			ack(0)
		case tftpDATA:
			b := binary.BigEndian.Uint16(p[2:])
			if b == next {
				out = append(out, p[4:]...)
				next++
			}
			ack(b)
			if b == next-1 && n-4 < blk {
				if quiet > 0 {
					_ = c.SetReadDeadline(time.Now().Add(quiet))
					if n, _, err := c.ReadFromUDP(buf); err == nil {
						return out, fmt.Errorf("server sent %d more bytes after the final block", n)
					}
				}
				return out, nil
			}
		}
	}
}

func newTestTFTP(t *testing.T, dir string) *tftpServer {
	t.Helper()
	return newTestTFTPLimits(t, dir, tftpLimits{})
}

func newTestTFTPLimits(t *testing.T, dir string, limits tftpLimits) *tftpServer {
	t.Helper()
	return newTestTFTPFull(t, dir, 200*time.Millisecond, limits)
}

func newTestTFTPTimeout(t *testing.T, dir string, timeout time.Duration) *tftpServer {
	t.Helper()
	return newTestTFTPFull(t, dir, timeout, tftpLimits{})
}

func newTestTFTPFull(t *testing.T, dir string, timeout time.Duration, limits tftpLimits) *tftpServer {
	t.Helper()
	c, err := listenUDP(net.IPv4(127, 0, 0, 1), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTFTPServer(c, dir, timeout, limits, newLogLimiter(time.Minute, time.Now))
	go ts.serve()
	t.Cleanup(ts.close)
	return ts
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

func TestTFTPServesFilesByteExact(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"empty.bin":   {},
		"small.bin":   pattern(100),
		"exact.bin":   pattern(512 * 4), // exact multiple of 512: needs a final empty block
		"large.bin":   pattern(512*300 + 77),
		"Mixed.EFI":   pattern(1468*3 + 1),
		"exact1468.b": pattern(1468 * 2),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	for _, opts := range []map[string]string{nil, {"blksize": "1468", "tsize": "0"}, {"blksize": "9000"}} {
		for n, want := range files {
			got, err := tftpGet(addr, n, opts)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s %v: err %v, got %d bytes want %d", n, opts, err, len(got), len(want))
			}
		}
	}
	// Names are passed through as-is: on Linux's case-sensitive filesystems the wrong case is not found.
	if _, err := tftpGet(addr, "mixed.efi", nil); runtime.GOOS == "linux" && err == nil {
		t.Error("wrong-case name served")
	}
}

// The transfer ends exactly at the short block: no extra empty block, no resend once the last ACK is in.
func TestTFTPEndsCleanly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "short"), pattern(700), 0o644)
	os.WriteFile(filepath.Join(dir, "exact"), pattern(1024), 0o644)
	os.WriteFile(filepath.Join(dir, "edge"), pattern(1023), 0o644)
	ts := newTestTFTP(t, dir)
	for _, n := range []string{"short", "exact", "edge"} {
		got, err := tftpRequestQuiet(ts.conn.LocalAddr().(*net.UDPAddr), tftpRRQ, n, "octet", nil, 500*time.Millisecond)
		if err != nil || len(got) == 0 {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestTFTPOACKReportsTsize(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(1234), 0o644)
	ts := newTestTFTP(t, dir)
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	c.WriteToUDP([]byte("\x00\x01f\x00octet\x00tsize\x000\x00blksize\x001024\x00"), ts.conn.LocalAddr().(*net.UDPAddr))
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, from, err := c.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "\x00\x06blksize\x001024\x00tsize\x001234\x00" {
		t.Fatalf("OACK %q", got)
	}
	// UEFI's tsize probe: abort after the OACK. The server must just stop.
	c.WriteToUDP(tftpErrorPkt(8, "probe done"), from)
}

func TestTFTPRefuses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "tftp")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "secret"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "inner"), []byte("inner"), 0o644)
	os.WriteFile(filepath.Join(dir, "ok"), []byte("ok"), 0o644)
	symlinks := true
	if err := os.Symlink(filepath.Join(root, "secret"), filepath.Join(dir, "escape")); err != nil {
		symlinks = false // Windows without the privilege
	}
	os.Symlink("sub/inner", filepath.Join(dir, "intosub"))
	os.Symlink("ok", filepath.Join(dir, "alias")) // even inside the directory: refused (only the regular file itself is served)
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	for _, name := range []string{"../secret", "..", ".", "", "sub/inner", "sub", `sub\inner`, filepath.Join(root, "secret"), "/ok", "missing", "C:ok"} {
		if got, err := tftpGet(addr, name, nil); err == nil {
			t.Errorf("%q served (%q)", name, got)
		}
	}
	if symlinks {
		for _, name := range []string{"escape", "intosub", "alias"} {
			if got, err := tftpGet(addr, name, nil); err == nil {
				t.Errorf("symlink %q served (%q)", name, got)
			}
		}
	}
	if _, err := tftpRequest(addr, tftpWRQ, "ok", "octet", nil); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("WRQ: %v", err)
	}
	if _, err := tftpRequest(addr, tftpRRQ, "ok", "netascii", nil); err == nil {
		t.Error("netascii accepted")
	}
	if got, err := tftpGet(addr, "ok", nil); err != nil || string(got) != "ok" {
		t.Errorf("server unhealthy after refusals: %v", err)
	}
}

// Truncated and garbage requests: no panic, server still serves.
func TestTFTPGarbage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok"), []byte("ok"), 0o644)
	ts := newTestTFTP(t, dir)
	addr := ts.conn.LocalAddr().(*net.UDPAddr)
	good := []byte("\x00\x01ok\x00octet\x00blksize\x001024\x00")
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	for i := 0; i < len(good)-1; i++ {
		c.WriteToUDP(good[:i], addr)
	}
	for _, g := range [][]byte{{0, 1, 0}, {0, 1, 'x'}, {0, 1, 'o', 'k', 0, 'o', 'c', 't', 'e', 't', 0, 'b', 0}, {0, 3, 0, 1, 1}, {0, 4, 0, 0}, {0, 9}, {0xff, 0xff, 0xff}} {
		c.WriteToUDP(g, addr)
	}
	for _, b := range [][]byte{good[:3], {0, 1, 0, 0, 0}, {0, 1, 'a', 0, 'o', 'c', 't', 'e', 't', 0, 'k', 0}} {
		if _, err := parseRRQ(b); err == nil {
			t.Errorf("parsed %q", b)
		}
	}
	if got, err := tftpGet(addr, "ok", nil); err != nil || string(got) != "ok" {
		t.Fatalf("after garbage: %v %q", err, got)
	}
}

// A client that stops ACKing: the transfer gives up after bounded retries instead of running forever.
func TestTFTPRetransmitThenGiveUp(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f"), pattern(2000), 0o644)
	ts := newTestTFTP(t, dir)
	c, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer c.Close()
	c.WriteToUDP([]byte("\x00\x01f\x00octet\x00"), ts.conn.LocalAddr().(*net.UDPAddr))
	buf := make([]byte, 1500)
	got := 0
	for {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			t.Fatal(err)
		}
		if n < 4 || binary.BigEndian.Uint16(buf[2:]) != 1 {
			t.Fatalf("unexpected packet %v", buf[:n])
		}
		got++
	}
	if got != tftpRetries+1 {
		t.Fatalf("block 1 sent %d times, want %d", got, tftpRetries+1)
	}
}

func FuzzParseRRQ(f *testing.F) {
	f.Add([]byte("\x00\x01ok\x00octet\x00blksize\x001024\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) >= 2 {
			parseRRQ(b)
		}
	})
}
