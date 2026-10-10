package main

// Built-in PXE front end (opt-in, -pxe): a proxyDHCP responder and a read-only TFTP server, so no dnsmasq is needed.
//
// Policy (deliberate; see README "Built-in PXE"):
//   - proxyDHCP only. Replies never offer an address or lease options; the network's DHCP server keeps doing that.
//   - Only ARMED MACs get a reply. Everything else gets silence and boots its disk. The PXE path never consumes the arm; only
//     GET /boot/{mac} does, so after the deploy starts the machine gets no more offers (no re-wipe loop on PXE-first firmware).
//   - Firmware PXE gets the iPXE binary for its architecture over TFTP; iPXE itself gets http://<server>/boot/<mac>.
//
// Reply destinations (PXE 2.1 / RFC 2131 rules for a server that assigns no address):
//   - port 67: via the relay (giaddr:67) if relayed; unicast to ciaddr:68 if the client already has an address; otherwise
//     broadcast to 255.255.255.255:68 (yiaddr is 0, so there is nothing to unicast to). On Linux the broadcast leaves on the
//     interface the request arrived on (IP_PKTINFO).
//   - port 4011: unicast back to the request's source address and port.

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type PXEConfig struct {
	DHCPPort   int // 67; 0 picks a free port (tests)
	ProxyPort  int // 4011
	TFTPPort   int // 69
	ClientPort int // where port-67 replies go: 68

	Dir                                    string // TFTP root: only regular files directly inside it are served
	FileX64, FileBIOS, FileARM64, FileIA32 string // FileIA32 "" = 32-bit UEFI clients get no offer

	AdvertiseIP net.IP // -pxe-ip: overrides the address put in siaddr and the iPXE URL
	HTTPPort    int    // port of this server's HTTP listener, for the iPXE URL

	ListenIP      net.IP // tests: bind to this address instead of all addresses
	BroadcastAddr net.IP // tests: where "broadcast" replies go; default 255.255.255.255
	TFTPTimeout   time.Duration
	TFTPLimits    tftpLimits // zero fields take the defaults
}

// archFile picks the boot file by option 93 (RFC 4578). Anything else is ignored.
func (c *PXEConfig) archFile(arch uint16) (string, bool) {
	switch arch {
	case 0:
		return c.FileBIOS, true
	case 6: // x86 (IA32) UEFI: a 64-bit binary would not run, so only its own file, and none by default
		return c.FileIA32, true
	case 7, 9:
		return c.FileX64, true
	case 11:
		return c.FileARM64, true
	}
	return "", false
}

type PXE struct {
	s     *Server
	cfg   PXEConfig
	dhcp  *net.UDPConn
	proxy *net.UDPConn
	tftp  *tftpServer
	// fallbackIP is used when the receiving interface's address can't be learned from the socket (non-Linux).
	fallbackIP net.IP
	wg         sync.WaitGroup
	lim        *logLimiter
	beforeSend atomic.Pointer[func()] // tests: runs between the decision and the final re-check
}

type pxeAction int

const (
	pxeSilent    pxeAction = iota // not a PXE request we answer; say nothing, log nothing
	pxeNotArmed                   // a PXE client that is not armed: no reply, rate-limited log line
	pxeReply                      // answer it
	pxeNoAddress                  // armed, but this server cannot tell which address to advertise
)

type pxeDecision struct {
	action  pxeAction
	mac     string
	arch    uint16
	file    string
	msgType byte // reply type: OFFER or ACK
}

// pxeArmed: is this MAC armed right now? Expired arms are dropped first, and a degraded server deploys nothing, so it offers nothing.
func (s *Server) pxeArmed(mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	return s.st.Armed[mac] != nil && !s.degraded
}

// decide applies the policy to a parsed request. onProxyPort is true for port 4011.
func (p *PXE) decide(req *dhcpPacket, onProxyPort bool, serverIP net.IP) pxeDecision {
	if req.op != bootRequest || !req.isPXEClient() {
		return pxeDecision{action: pxeSilent}
	}
	mac, ok := req.mac()
	if !ok {
		return pxeDecision{action: pxeSilent}
	}
	arch, ok := req.arch()
	if !ok {
		return pxeDecision{action: pxeSilent}
	}
	d := pxeDecision{mac: mac, arch: arch}
	switch req.msgType() {
	case dhcpDiscover:
		d.msgType = dhcpOffer
	case dhcpRequest:
		d.msgType = dhcpAck
		// On port 67 a REQUEST is the client accepting some DHCP server's offer. Answer only if it names us; otherwise it is the
		// real DHCP server's business (and without option 54 it is a renewal/init-reboot, also not ours).
		if !onProxyPort {
			sid := req.opts[optServerID]
			if serverIP == nil || len(sid) != 4 || !net.IP(sid).Equal(serverIP) {
				return pxeDecision{action: pxeSilent}
			}
		}
	default:
		return pxeDecision{action: pxeSilent}
	}
	file, ok := p.cfg.archFile(arch)
	if !ok || file == "" {
		return pxeDecision{action: pxeSilent}
	}
	if !p.s.pxeArmed(mac) {
		d.action = pxeNotArmed
		return d
	}
	if serverIP == nil {
		d.action = pxeNoAddress
		return d
	}
	if req.isIPXE() {
		// Already running iPXE: don't hand it iPXE again (that loops). Give it the boot script; fetching it consumes the arm.
		file = "http://" + net.JoinHostPort(serverIP.String(), strconv.Itoa(p.cfg.HTTPPort)) + "/boot/" + mac
	}
	d.action, d.file = pxeReply, file
	return d
}

// replyDest: see the package comment. src is the request's UDP source; localPort is the port it arrived on.
func (p *PXE) replyDest(req *dhcpPacket, src *net.UDPAddr, onProxyPort bool) (dst *net.UDPAddr, broadcast bool) {
	if onProxyPort {
		return src, false
	}
	if !req.giaddr.Equal(net.IPv4zero) {
		return &net.UDPAddr{IP: req.giaddr, Port: p.dhcpPort()}, false
	}
	if !req.ciaddr.Equal(net.IPv4zero) {
		return &net.UDPAddr{IP: req.ciaddr, Port: p.cfg.ClientPort}, false
	}
	bc := p.cfg.BroadcastAddr
	if bc == nil {
		bc = net.IPv4bcast
	}
	return &net.UDPAddr{IP: bc, Port: p.cfg.ClientPort}, true
}

func (p *PXE) dhcpPort() int { return p.dhcp.LocalAddr().(*net.UDPAddr).Port }

// DHCPAddr, ProxyAddr and TFTPAddr report the bound addresses (useful when a port was 0).
func (p *PXE) DHCPAddr() *net.UDPAddr  { return p.dhcp.LocalAddr().(*net.UDPAddr) }
func (p *PXE) ProxyAddr() *net.UDPAddr { return p.proxy.LocalAddr().(*net.UDPAddr) }
func (p *PXE) TFTPAddr() *net.UDPAddr  { return p.tftp.conn.LocalAddr().(*net.UDPAddr) }

// serverIP: which of our addresses to advertise for a request that arrived as described by li.
func (p *PXE) serverIP(li localInfo) net.IP {
	if p.cfg.AdvertiseIP != nil {
		return p.cfg.AdvertiseIP.To4()
	}
	if ip := li.specDst.To4(); ip != nil && !ip.IsUnspecified() && !ip.Equal(net.IPv4bcast) {
		return ip
	}
	if li.ifIndex > 0 {
		if ifc, err := net.InterfaceByIndex(li.ifIndex); err == nil {
			if ip := firstIPv4(ifc); ip != nil {
				return ip
			}
		}
	}
	return p.fallbackIP
}

func firstIPv4(ifc *net.Interface) net.IP {
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.To4()
		}
	}
	return nil
}

// localIPv4s lists the up, non-loopback IPv4 addresses, as "iface=ip" strings and as IPs.
func localIPv4s() ([]string, []net.IP) {
	var names []string
	var ips []net.IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				names = append(names, ifc.Name+"="+n.IP.String())
				ips = append(ips, n.IP.To4())
			}
		}
	}
	return names, ips
}

// StartPXE binds the proxyDHCP (67, 4011) and TFTP (69) sockets and starts serving. A bind failure is returned with a message that
// names the port and says what to do; a TFTP directory without the boot files is logged loudly but does not stop it.
func (s *Server) StartPXE(cfg PXEConfig) (*PXE, error) {
	if !pxePlatformOK {
		return nil, fmt.Errorf("pxe: built-in PXE is not supported on %s/%s; use dnsmasq instead", runtime.GOOS, runtime.GOARCH)
	}
	if cfg.Dir == "" {
		return nil, errors.New("pxe: -pxe-dir is required with -pxe (a folder holding your iPXE binaries)")
	}
	if cfg.TFTPTimeout <= 0 {
		cfg.TFTPTimeout = 2 * time.Second
	}
	p := &PXE{s: s, cfg: cfg, lim: newLogLimiter(time.Minute, s.Now)}
	names, ips := localIPv4s()
	switch {
	case cfg.AdvertiseIP != nil:
		if cfg.AdvertiseIP.To4() == nil {
			return nil, fmt.Errorf("pxe: -pxe-ip %s is not an IPv4 address", cfg.AdvertiseIP)
		}
	case cfg.ListenIP != nil && !cfg.ListenIP.IsUnspecified():
		p.fallbackIP = cfg.ListenIP.To4()
	case len(ips) == 1:
		p.fallbackIP = ips[0]
	case !havePktinfo:
		return nil, fmt.Errorf("pxe: this host has %d IPv4 addresses (%s) and this OS can't tell which one a request arrived on; set -pxe-ip", len(ips), strings.Join(names, ", "))
	}
	var err error
	closeAll := func() {
		for _, c := range []*net.UDPConn{p.dhcp, p.proxy} {
			if c != nil {
				c.Close()
			}
		}
	}
	if p.dhcp, err = listenUDP(cfg.ListenIP, cfg.DHCPPort, true); err != nil {
		return nil, bindError("proxyDHCP", cfg.DHCPPort, err)
	}
	if p.proxy, err = listenUDP(cfg.ListenIP, cfg.ProxyPort, false); err != nil {
		closeAll()
		return nil, bindError("proxyDHCP (PXE port)", cfg.ProxyPort, err)
	}
	tc, err := listenUDP(cfg.ListenIP, cfg.TFTPPort, false)
	if err != nil {
		closeAll()
		return nil, bindError("TFTP", cfg.TFTPPort, err)
	}
	p.tftp = newTFTPServer(tc, cfg.Dir, cfg.TFTPTimeout, cfg.TFTPLimits, p.lim)

	adv := "address of the receiving interface"
	if cfg.AdvertiseIP != nil {
		adv = cfg.AdvertiseIP.String() + " (-pxe-ip, or the address HTTP is bound to)"
	} else if !havePktinfo && p.fallbackIP != nil {
		adv = p.fallbackIP.String()
	}
	log.Printf("pxe: proxyDHCP on UDP %d and %d, TFTP on UDP %d (read-only, %s); advertising %s; HTTP boot port %d",
		p.DHCPAddr().Port, p.ProxyAddr().Port, p.TFTPAddr().Port, cfg.Dir, adv, cfg.HTTPPort)
	log.Printf("pxe: interfaces: %s", strings.Join(names, ", "))
	log.Printf("pxe: only ARMED MACs get an offer; everything else is ignored and boots its own disk")
	for _, line := range checkBootFiles(cfg) {
		log.Print(line)
	}

	p.wg.Add(3)
	go p.serveDHCP(p.dhcp, false)
	go p.serveDHCP(p.proxy, true)
	go func() { defer p.wg.Done(); p.tftp.serve() }()
	return p, nil
}

// checkBootFiles reports, one line each, whether the configured boot files are in the TFTP directory.
func checkBootFiles(cfg PXEConfig) []string {
	if st, err := os.Stat(cfg.Dir); err != nil || !st.IsDir() {
		return []string{fmt.Sprintf("pxe: ERROR: -pxe-dir %s is not a readable directory; PXE clients will get offers but no boot file", cfg.Dir)}
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range []struct{ arch, name string }{{"x64 UEFI", cfg.FileX64}, {"BIOS", cfg.FileBIOS}, {"arm64 UEFI", cfg.FileARM64}, {"IA32 UEFI", cfg.FileIA32}} {
		if f.name == "" || seen[f.name] {
			continue
		}
		seen[f.name] = true
		if fl, _, err := openServed(cfg.Dir, f.name); err != nil {
			out = append(out, fmt.Sprintf("pxe: ERROR: %s boot file %q is not servable from %s (%v)", f.arch, f.name, cfg.Dir, err))
		} else {
			fl.Close()
			out = append(out, fmt.Sprintf("pxe: %s boot file %q: ok", f.arch, f.name))
		}
	}
	sort.Strings(out)
	return out
}

func (p *PXE) serveDHCP(c *net.UDPConn, onProxyPort bool) {
	defer p.wg.Done()
	buf := make([]byte, 1500)
	oob := make([]byte, 128)
	for {
		n, src, li, err := readUDP(c, buf, oob)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			p.lim.logf("read-err", "pxe: read: %s", errText(err))
			continue
		}
		p.handle(c, buf[:n], src, li, onProxyPort)
	}
}

func (p *PXE) handle(c *net.UDPConn, b []byte, src *net.UDPAddr, li localInfo, onProxyPort bool) {
	req, err := parseDHCP(b)
	if err != nil {
		p.lim.noisef("bad:"+src.IP.String(), "pxe: ignoring packet from %s: %s", src, errText(err))
		return
	}
	serverIP := p.serverIP(li)
	d := p.decide(req, onProxyPort, serverIP)
	switch d.action {
	case pxeSilent:
		return
	case pxeNotArmed:
		p.lim.noisef("notarmed:"+d.mac, "pxe: %s not armed, ignoring", d.mac)
		return
	case pxeNoAddress:
		p.lim.logf("noaddr:"+d.mac, "pxe: %s is armed but this server can't tell which address to advertise; set -pxe-ip", d.mac)
		return
	}
	reply := buildProxyReply(req, d.msgType, serverIP, d.file)
	dst, bcast := p.replyDest(req, src, onProxyPort)
	if hook := p.beforeSend.Load(); hook != nil {
		(*hook)()
	}
	// The arm check in decide released s.mu (holding it across a network write would stall arming and /boot). Re-check right before
	// sending, so an arm that was consumed, disarmed or expired, or a server that degraded, in the meantime gets no reply. What remains
	// is the microseconds between this check and the write: harmless, because an offer only names iPXE or the /boot URL, and /boot,
	// which decides under s.mu, is the only thing that deploys.
	if !p.s.pxeArmed(d.mac) {
		p.lim.noisef("notarmed:"+d.mac, "pxe: %s not armed, ignoring", d.mac)
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
	if err := writeUDP(c, reply, dst, li, bcast); err != nil {
		p.lim.logf("send:"+d.mac, "pxe: reply to %s (%s) failed: %s", d.mac, dst, errText(err))
		return
	}
	kind := "offered"
	if d.msgType == dhcpAck {
		kind = "acked"
	}
	p.lim.logf(kind+":"+d.mac+":"+d.file, "pxe: %s %s arch=%d file=%q", kind, d.mac, d.arch, d.file)
}

// Close stops all listeners and waits for in-flight TFTP transfers to end.
func (p *PXE) Close() error {
	p.dhcp.Close()
	p.proxy.Close()
	p.tftp.close()
	p.wg.Wait()
	return nil
}

// bindError turns a bind failure into advice: privileges, or another DHCP/TFTP server already holding the port.
func bindError(what string, port int, err error) error {
	switch {
	case isPermErr(err):
		return fmt.Errorf("pxe: cannot bind %s on UDP %d: %v. %s", what, port, err, privHint)
	case isInUseErr(err):
		return fmt.Errorf("pxe: cannot bind %s on UDP %d: another process already holds that port (dnsmasq or another DHCP/TFTP server on this host?). "+
			"Stop it, or run minute2win-server with -pxe on another host: %v", what, port, err)
	}
	return fmt.Errorf("pxe: cannot bind %s on UDP %d: %w", what, port, err)
}

// logLimiter keeps logging bounded whatever arrives on the wire. Lines come in two classes with separate token buckets, so a flood
// of attacker-reachable noise cannot starve the lines that matter:
//   - policy (logf): offers/ACKs to armed MACs, send failures, local errors. 20 lines/s, burst 50.
//   - noise (noisef): anything any host can trigger: malformed packets, unarmed MACs (a spoofer can invent any number), every TFTP
//     line. 5 lines/s, burst 20.
//
// Per key (a MAC, a client IP, a file name) a line is printed at most once per period; the key is stamped only when its line is
// actually printed, so a line dropped for budget is retried by the next event. Every dropped line (per-key repeat or budget) is
// counted, and the "N suppressed" notice costs a token like any other line, so output never exceeds the bucket's rate. The key map
// has a hard cap; when full it is replaced by an empty one (O(1); the worst case is a repeated line). Startup lines bypass it.
//
// Callers quote anything from the wire (%q, errText).
type logLimiter struct {
	mu     sync.Mutex
	period time.Duration
	now    func() time.Time
	last   map[string]time.Time
	out    func(string, ...any)
	class  [2]logBucket
}

type logBucket struct {
	name        string
	rate, burst float64
	tokens      float64
	at          time.Time
	dropped     int
}

const (
	logKeyCap     = 4096
	logPolicy     = 0
	logNoise      = 1
	logRate       = 20.0 // policy lines per second
	logBurst      = 50.0
	logNoiseRate  = 5.0
	logNoiseBurst = 20.0
)

func newLogLimiter(period time.Duration, now func() time.Time) *logLimiter {
	t := now()
	return &logLimiter{period: period, now: now, last: map[string]time.Time{}, out: log.Printf, class: [2]logBucket{
		{name: "policy", rate: logRate, burst: logBurst, tokens: logBurst, at: t},
		{name: "noise", rate: logNoiseRate, burst: logNoiseBurst, tokens: logNoiseBurst, at: t},
	}}
}

// setOut swaps the output (tests).
func (l *logLimiter) setOut(out func(string, ...any)) {
	l.mu.Lock()
	l.out = out
	l.mu.Unlock()
}

// suppressed reports the dropped-line counts not yet announced (tests).
func (l *logLimiter) suppressed() (policy, noise int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.class[logPolicy].dropped, l.class[logNoise].dropped
}

func (l *logLimiter) logf(key, format string, args ...any)   { l.emit(logPolicy, key, format, args) }
func (l *logLimiter) noisef(key, format string, args ...any) { l.emit(logNoise, key, format, args) }

func (l *logLimiter) emit(class int, key, format string, args []any) {
	l.mu.Lock()
	now := l.now()
	b := &l.class[class]
	if el := now.Sub(b.at).Seconds(); el > 0 {
		b.tokens = min(b.burst, b.tokens+el*b.rate)
	}
	b.at = now
	k := b.name + "\x00" + key
	if t, ok := l.last[k]; ok && now.Sub(t) < l.period {
		b.dropped++
		l.mu.Unlock()
		return
	}
	if b.tokens < 1 {
		b.dropped++
		l.mu.Unlock()
		return
	}
	b.tokens--
	if len(l.last) >= logKeyCap {
		l.last = make(map[string]time.Time, 64)
	}
	l.last[k] = now
	notice := 0
	if b.dropped > 0 && b.tokens >= 1 {
		b.tokens--
		notice, b.dropped = b.dropped, 0
	}
	out := l.out
	l.mu.Unlock()
	if notice > 0 {
		out("pxe: %d %s log line(s) suppressed (repeats within %s, or over the rate limit)", notice, b.name, l.period)
	}
	out(format, args...)
}
