package main

// A read-only TFTP server (RFC 1350) with the option extensions PXE firmware uses: blksize (RFC 2348), tsize and timeout
// (RFC 2349), negotiated with OACK (RFC 2347). Octet mode only. It serves regular files directly inside one directory and nothing
// else: no subdirectories, no "..", no absolute paths, no symlinks (even ones pointing inside), no writes. Confinement is enforced
// at open time by os.Root, and the opened file must be the very regular file named in the directory.
//
// TFTP has no authentication and cannot know which MAC is asking, so anyone who can reach UDP 69 can read anything in the directory.
// It is meant to hold only the public iPXE binaries. Resource use is bounded: tftpLimits caps concurrent transfers (in total and
// per client IP), each transfer's lifetime, and how long it may sit without progress.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tftpRRQ   = 1
	tftpWRQ   = 2
	tftpDATA  = 3
	tftpACK   = 4
	tftpERROR = 5
	tftpOACK  = 6

	tftpErrUndef    = 0
	tftpErrNotFound = 1
	tftpErrAccess   = 2
	tftpErrIllegal  = 4

	tftpDefaultBlk = 512
	tftpMaxBlk     = 1468 // fits a 1500-byte MTU without fragmentation
	tftpRetries    = 5
	tftpMaxTimeout = 10 // seconds; larger "timeout" options are declined
)

type tftpLimits struct {
	MaxActive int           // concurrent transfers in total (64)
	MaxPerIP  int           // concurrent transfers per client IP (4)
	Lifetime  time.Duration // hard cap on one transfer (60 s)
	Idle      time.Duration // a transfer with no new ACK for this long ends (20 s), whatever timeout was negotiated
	// WriteTimeout bounds every send (DATA, OACK, ERROR, and the listener's refusals): 2 s. A send that fails or times out ends the
	// transfer, so a blocked socket can never hold a slot past the lifetime.
	WriteTimeout time.Duration
}

func (l tftpLimits) withDefaults() tftpLimits {
	if l.MaxActive <= 0 {
		l.MaxActive = 64
	}
	if l.MaxPerIP <= 0 {
		l.MaxPerIP = 4
	}
	if l.Lifetime <= 0 {
		l.Lifetime = 60 * time.Second
	}
	if l.Idle <= 0 {
		l.Idle = 20 * time.Second
	}
	if l.WriteTimeout <= 0 {
		l.WriteTimeout = 2 * time.Second
	}
	return l
}

// tftpConn is a transfer's socket (a connected *net.UDPConn; tests substitute one whose writes block).
type tftpConn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	Close() error
}

// denyWriter is how the listener sends refusals (the listening *net.UDPConn; tests substitute a blocking one).
type denyWriter interface {
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
	SetWriteDeadline(time.Time) error
}

type tftpServer struct {
	conn    *net.UDPConn
	denyOut denyWriter
	dial    func(laddr, raddr *net.UDPAddr) (tftpConn, error)
	dir     string
	timeout time.Duration
	limits  tftpLimits
	lim     *logLimiter

	mu       sync.Mutex
	closed   bool
	active   int
	perIP    map[string]int
	inflight map[string]tftpConn // client addr + NUL + file name -> its transfer socket (nil while dialing)
	wg       sync.WaitGroup
}

func newTFTPServer(c *net.UDPConn, dir string, timeout time.Duration, limits tftpLimits, lim *logLimiter) *tftpServer {
	return &tftpServer{conn: c, denyOut: c, dir: dir, timeout: timeout, limits: limits.withDefaults(), lim: lim,
		perIP: map[string]int{}, inflight: map[string]tftpConn{},
		dial: func(laddr, raddr *net.UDPAddr) (tftpConn, error) { return net.DialUDP("udp4", laddr, raddr) }}
}

// close stops the listener and every transfer at once (closing their sockets unblocks their reads), then waits for them.
func (t *tftpServer) close() {
	t.mu.Lock()
	t.closed = true
	for _, c := range t.inflight {
		if c != nil {
			c.Close()
		}
	}
	t.mu.Unlock()
	t.conn.Close()
	t.wg.Wait()
}

func (t *tftpServer) activeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

// readErrBackoff paces the listener after a failed read.
const readErrBackoff = 50 * time.Millisecond

// readFailed logs a listener read failure. It is a protected (policy) line, not noise: it means the TFTP server itself is in trouble,
// and an attacker flooding unauthenticated requests must not be able to hide it behind the noise budget.
func (t *tftpServer) readFailed(err error) {
	t.lim.logf("tftp-read-err", "tftp: read: %s", errText(err))
}

func (t *tftpServer) serve() {
	buf := make([]byte, 1500)
	oob := make([]byte, 128)
	for {
		n, src, li, err := readUDP(t.conn, buf, oob)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.readFailed(err)
			time.Sleep(readErrBackoff) // a persistent socket error must not spin the CPU or the log
			continue
		}
		t.request(append([]byte(nil), buf[:n]...), src, li)
	}
}

var errTFTPPath = errors.New("not a regular file directly inside the TFTP directory")

// openServed opens name, which must be a regular file directly inside dir (not a symlink, not in a subdirectory). os.Root
// confines the open itself, so a name swapped for a symlink after any check still cannot reach outside dir; the Lstat/SameFile pair
// then refuses anything but the regular file that sits at that name. Names are taken as-is (no case folding).
func openServed(dir, name string) (*os.File, int64, error) {
	if name == "" || len(name) > 255 || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return nil, 0, errTFTPPath
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	// Lstat first so a FIFO or device is never opened (opening a FIFO would block). Swapping one in afterwards needs write access
	// to the directory, which is outside this server's threat model.
	lst, err := root.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !lst.Mode().IsRegular() {
		return nil, 0, errTFTPPath
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(lst, st) {
		f.Close()
		return nil, 0, errTFTPPath
	}
	return f, st.Size(), nil
}

type rrq struct {
	file, mode string
	opts       map[string]string // lower-case names
}

func parseRRQ(b []byte) (*rrq, error) {
	fields := bytes.Split(b[2:], []byte{0})
	// A well-formed request ends in NUL, so the last field is empty.
	if len(fields) < 3 || len(fields[len(fields)-1]) != 0 {
		return nil, errors.New("malformed request")
	}
	fields = fields[:len(fields)-1]
	r := &rrq{file: string(fields[0]), mode: strings.ToLower(string(fields[1])), opts: map[string]string{}}
	rest := fields[2:]
	if len(rest)%2 != 0 {
		return nil, errors.New("malformed options")
	}
	for i := 0; i < len(rest); i += 2 {
		r.opts[strings.ToLower(string(rest[i]))] = string(rest[i+1])
	}
	return r, nil
}

func tftpErrorPkt(code uint16, msg string) []byte {
	b := make([]byte, 4, 5+len(msg))
	binary.BigEndian.PutUint16(b[0:], tftpERROR)
	binary.BigEndian.PutUint16(b[2:], code)
	return append(append(b, msg...), 0)
}

// deny sends a refusal from the listening socket (bounded by WriteTimeout, so a blocked socket can't stall the listener) and logs
// it as network noise. logMsg must already be safe: anything from the wire in it is quoted.
func (t *tftpServer) deny(src *net.UDPAddr, code uint16, msg, logMsg string) {
	_ = t.denyOut.SetWriteDeadline(time.Now().Add(t.limits.WriteTimeout))
	_, _ = t.denyOut.WriteToUDP(tftpErrorPkt(code, msg), src)
	t.lim.noisef("tftp-deny:"+src.IP.String()+":"+logMsg, "tftp: %s: %s", src, logMsg)
}

// errText renders an error for a log line as a quoted string: errors can carry text from the network (a *PathError holds the
// requested file name), and quoting keeps a newline in it from forging a log line.
func errText(err error) string { return strconv.Quote(err.Error()) }

func (t *tftpServer) request(b []byte, src *net.UDPAddr, li localInfo) {
	if len(b) < 2 {
		return
	}
	switch binary.BigEndian.Uint16(b) {
	case tftpRRQ:
	case tftpWRQ:
		t.deny(src, tftpErrAccess, "read-only server", "write request refused (read-only)")
		return
	default:
		return // stray DATA/ACK/ERROR to the listening port: ignore
	}
	r, err := parseRRQ(b)
	if err != nil {
		t.deny(src, tftpErrIllegal, err.Error(), errText(err))
		return
	}
	key := src.String() + "\x00" + r.file
	t.mu.Lock()
	_, dup := t.inflight[key]
	t.mu.Unlock()
	if dup {
		return // the client resent its RRQ before seeing our first packet: that transfer is already running
	}
	if r.mode != "octet" {
		t.deny(src, tftpErrIllegal, "only octet mode is supported", "refused mode "+strconv.Quote(r.mode))
		return
	}
	f, size, err := openServed(t.dir, r.file)
	if err != nil {
		t.deny(src, tftpErrNotFound, "file not found", fmt.Sprintf("refused %q (%s)", r.file, errText(err)))
		return
	}
	ip := src.IP.String()
	t.mu.Lock()
	_, dup = t.inflight[key]
	switch {
	case t.closed || dup:
		t.mu.Unlock()
		f.Close()
		return
	case t.active >= t.limits.MaxActive || t.perIP[ip] >= t.limits.MaxPerIP:
		t.mu.Unlock()
		f.Close()
		t.deny(src, tftpErrUndef, "server busy", "too many transfers")
		return
	}
	t.active++
	t.perIP[ip]++
	t.inflight[key] = nil
	t.wg.Add(1)
	t.mu.Unlock()
	release := func() {
		t.mu.Lock()
		delete(t.inflight, key)
		t.active--
		if t.perIP[ip]--; t.perIP[ip] <= 0 {
			delete(t.perIP, ip)
		}
		t.mu.Unlock()
		t.wg.Done()
	}
	// Answer from the address the request was sent to (Linux tells us), so the client sees the server it asked.
	var laddr *net.UDPAddr
	if dst := li.dst.To4(); dst != nil && !dst.IsUnspecified() && !dst.Equal(net.IPv4bcast) && !dst.IsMulticast() {
		laddr = &net.UDPAddr{IP: dst}
	} else if la := t.conn.LocalAddr().(*net.UDPAddr); !la.IP.IsUnspecified() {
		laddr = &net.UDPAddr{IP: la.IP}
	}
	c, err := t.dial(laddr, src)
	if err != nil {
		f.Close()
		release()
		t.lim.logf("tftp-dial", "tftp: %s: %s", src, errText(err))
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		c.Close()
		f.Close()
		release()
		return
	}
	t.inflight[key] = c
	t.mu.Unlock()
	go func() {
		defer release()
		defer f.Close()
		defer c.Close()
		sent, err := t.transfer(c, f, size, r.opts)
		switch {
		case err == nil:
			t.lim.noisef("tftp-ok:"+ip+":"+r.file, "tftp: sent %q to %s (%d bytes)", r.file, ip, sent)
		case errors.Is(err, errPeerAborted):
			// Normal: UEFI asks for tsize, then aborts and asks again.
		default:
			t.lim.noisef("tftp-fail:"+ip+":"+r.file, "tftp: %q to %s failed: %s", r.file, ip, errText(err))
		}
	}()
}

var errPeerAborted = errors.New("client aborted")

// transfer runs one read: optional OACK, then DATA blocks, each waiting for its ACK with retransmission on timeout. It ends on
// success, on the client's ERROR, after tftpRetries resends, after limits.Idle without a new ACK, or after limits.Lifetime.
func (t *tftpServer) transfer(c tftpConn, f io.Reader, size int64, opts map[string]string) (int64, error) {
	blk := tftpDefaultBlk
	timeout := t.timeout
	start := time.Now()
	end := start.Add(t.limits.Lifetime)
	progress := start
	var oack []byte
	addOpt := func(k, v string) {
		if oack == nil {
			oack = []byte{0, tftpOACK}
		}
		oack = append(append(append(append(oack, k...), 0), v...), 0)
	}
	if v, ok := opts["blksize"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 8 {
			blk = min(n, tftpMaxBlk)
			addOpt("blksize", strconv.Itoa(blk))
		}
	}
	if _, ok := opts["tsize"]; ok {
		addOpt("tsize", strconv.FormatInt(size, 10))
	}
	if v, ok := opts["timeout"]; ok {
		// Accepted only within 1..10 s; anything else is declined (left out of the OACK) and the default applies.
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= tftpMaxTimeout {
			timeout = time.Duration(n) * time.Second
			addOpt("timeout", strconv.Itoa(n))
		}
	}
	// send bounds every write by WriteTimeout (and the lifetime); a failed or timed-out write ends the transfer.
	send := func(pkt []byte) error {
		if time.Now().After(end) {
			return errors.New("transfer lifetime exceeded")
		}
		_ = c.SetWriteDeadline(earliest(time.Now().Add(t.limits.WriteTimeout), end))
		_, err := c.Write(pkt)
		return err
	}
	rbuf := make([]byte, 1500)
	// sendWait sends pkt and waits for ACK of block want, resending on timeout. Duplicate older ACKs are ignored rather than
	// answered (no Sorcerer's Apprentice).
	sendWait := func(pkt []byte, want uint16) error {
		for try := 0; try <= tftpRetries; try++ {
			if err := send(pkt); err != nil {
				return err
			}
			deadline := time.Now().Add(timeout)
			for {
				now := time.Now()
				if now.After(end) {
					return errors.New("transfer lifetime exceeded")
				}
				if now.Sub(progress) > t.limits.Idle {
					return errors.New("no progress, giving up")
				}
				d := earliest(deadline, end, progress.Add(t.limits.Idle))
				_ = c.SetReadDeadline(d)
				n, err := c.Read(rbuf)
				if err != nil {
					var ne net.Error
					if errors.As(err, &ne) && ne.Timeout() {
						if time.Now().Before(deadline) {
							continue // woke for the lifetime/idle limit; the checks above end it
						}
						break
					}
					return err // includes the socket being closed at shutdown
				}
				if n < 4 {
					continue
				}
				switch binary.BigEndian.Uint16(rbuf) {
				case tftpACK:
					if binary.BigEndian.Uint16(rbuf[2:]) == want {
						progress = time.Now()
						return nil
					}
				case tftpERROR:
					return errPeerAborted
				}
			}
		}
		return errors.New("timed out waiting for ACK")
	}
	if oack != nil {
		if err := sendWait(oack, 0); err != nil {
			return 0, err
		}
	}
	pkt := make([]byte, 4+blk)
	binary.BigEndian.PutUint16(pkt, tftpDATA)
	var sent int64
	for block := uint16(1); ; block++ { // wraps to 0 after 65535, as common clients expect
		n, err := io.ReadFull(f, pkt[4:])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			_ = send(tftpErrorPkt(tftpErrUndef, "read error"))
			return sent, err
		}
		binary.BigEndian.PutUint16(pkt[2:], block)
		if err := sendWait(pkt[:4+n], block); err != nil {
			return sent, err
		}
		sent += int64(n)
		if n < blk {
			return sent, nil // a short (possibly empty) block ends the transfer
		}
	}
}

func earliest(ts ...time.Time) time.Time {
	e := ts[0]
	for _, t := range ts[1:] {
		if t.Before(e) {
			e = t
		}
	}
	return e
}
