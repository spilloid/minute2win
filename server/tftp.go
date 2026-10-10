package main

// A read-only TFTP server (RFC 1350) with the option extensions PXE firmware uses: blksize (RFC 2348), tsize and timeout
// (RFC 2349), negotiated with OACK (RFC 2347). Octet mode only. It serves regular files directly inside one directory and nothing
// else: no subdirectories, no "..", no absolute paths, no symlink that resolves outside the directory, no writes.
//
// TFTP has no authentication and cannot know which MAC is asking, so anyone who can reach UDP 69 can read anything in the directory.
// It is meant to hold only the public iPXE binaries.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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
	tftpMaxActive  = 64
)

type tftpServer struct {
	conn    *net.UDPConn
	dir     string
	timeout time.Duration
	lim     *logLimiter
	active  chan struct{}
	wg      sync.WaitGroup
	done    chan struct{}
}

func newTFTPServer(c *net.UDPConn, dir string, timeout time.Duration, lim *logLimiter) *tftpServer {
	return &tftpServer{conn: c, dir: dir, timeout: timeout, lim: lim, active: make(chan struct{}, tftpMaxActive), done: make(chan struct{})}
}

func (t *tftpServer) close() {
	close(t.done)
	t.conn.Close()
	t.wg.Wait()
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
			t.lim.logf("tftp-read-err", "tftp: read: %v", err)
			continue
		}
		t.request(append([]byte(nil), buf[:n]...), src, li)
	}
}

var errTFTPPath = errors.New("not a file directly inside the TFTP directory")

// tftpPath resolves a requested name to a regular file directly inside dir, or fails. Names are taken as-is (no case folding).
func tftpPath(dir, name string) (string, error) {
	if name == "" || len(name) > 255 || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", errTFTPPath
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if filepath.Dir(full) != root {
		return "", errTFTPPath // a symlink that points outside the directory, or into a subdirectory
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errTFTPPath
	}
	return full, nil
}

// openServed opens a path from tftpPath and checks the opened file is still that regular file (closes the swap-after-check window).
func openServed(full string) (*os.File, int64, error) {
	before, err := os.Stat(full)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(before, st) {
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

func (t *tftpServer) deny(src *net.UDPAddr, code uint16, msg, logMsg string) {
	_, _ = t.conn.WriteToUDP(tftpErrorPkt(code, msg), src)
	t.lim.logf("tftp-deny:"+src.IP.String()+":"+logMsg, "tftp: %s: %s", src, logMsg)
}

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
		t.deny(src, tftpErrIllegal, err.Error(), err.Error())
		return
	}
	if r.mode != "octet" {
		t.deny(src, tftpErrIllegal, "only octet mode is supported", "refused mode "+strconv.Quote(r.mode))
		return
	}
	full, err := tftpPath(t.dir, r.file)
	if err != nil {
		t.deny(src, tftpErrNotFound, "file not found", fmt.Sprintf("refused %q (%v)", r.file, err))
		return
	}
	f, size, err := openServed(full)
	if err != nil {
		t.deny(src, tftpErrNotFound, "file not found", fmt.Sprintf("refused %q (%v)", r.file, err))
		return
	}
	select {
	case t.active <- struct{}{}:
	default:
		f.Close()
		t.deny(src, tftpErrUndef, "server busy", "too many transfers")
		return
	}
	// Answer from the address the request was sent to (Linux tells us), so the client sees the server it asked.
	var laddr *net.UDPAddr
	if ip := li.dst.To4(); ip != nil && !ip.IsUnspecified() && !ip.Equal(net.IPv4bcast) && !ip.IsMulticast() {
		laddr = &net.UDPAddr{IP: ip}
	} else if la := t.conn.LocalAddr().(*net.UDPAddr); !la.IP.IsUnspecified() {
		laddr = &net.UDPAddr{IP: la.IP}
	}
	c, err := net.DialUDP("udp4", laddr, src)
	if err != nil {
		<-t.active
		f.Close()
		t.lim.logf("tftp-dial", "tftp: %s: %v", src, err)
		return
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer func() { <-t.active }()
		defer f.Close()
		defer c.Close()
		sent, err := t.transfer(c, f, size, r.opts)
		switch {
		case err == nil:
			t.lim.logf("tftp-ok:"+src.IP.String()+":"+r.file, "tftp: sent %s to %s (%d bytes)", r.file, src.IP, sent)
		case errors.Is(err, errPeerAborted):
			// Normal: UEFI asks for tsize, then aborts and asks again.
		default:
			t.lim.logf("tftp-fail:"+src.IP.String()+":"+r.file, "tftp: %s to %s failed: %v", r.file, src.IP, err)
		}
	}()
}

var errPeerAborted = errors.New("client aborted")

// transfer runs one read: optional OACK, then DATA blocks, each waiting for its ACK with retransmission on timeout.
func (t *tftpServer) transfer(c *net.UDPConn, f io.Reader, size int64, opts map[string]string) (int64, error) {
	blk := tftpDefaultBlk
	timeout := t.timeout
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
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 255 {
			timeout = time.Duration(n) * time.Second
			addOpt("timeout", v)
		}
	}
	rbuf := make([]byte, 1500)
	// sendWait sends pkt and waits for ACK of block want, resending on timeout. Duplicate older ACKs are ignored rather than
	// answered (no Sorcerer's Apprentice).
	sendWait := func(pkt []byte, want uint16) error {
		for try := 0; try <= tftpRetries; try++ {
			if _, err := c.Write(pkt); err != nil {
				return err
			}
			deadline := time.Now().Add(timeout)
			for {
				select {
				case <-t.done:
					return errors.New("server stopping")
				default:
				}
				_ = c.SetReadDeadline(deadline)
				n, err := c.Read(rbuf)
				if err != nil {
					var ne net.Error
					if errors.As(err, &ne) && ne.Timeout() {
						break
					}
					return err
				}
				if n < 4 {
					continue
				}
				switch binary.BigEndian.Uint16(rbuf) {
				case tftpACK:
					if binary.BigEndian.Uint16(rbuf[2:]) == want {
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
			_, _ = c.Write(tftpErrorPkt(tftpErrUndef, "read error"))
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
