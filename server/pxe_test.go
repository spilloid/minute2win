package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	armedMAC   = "52:54:00:12:34:56"
	unarmedMAC = "52:54:00:aa:bb:cc"
)

var testServerIP = net.IPv4(192, 0, 2, 10).To4()

// dhcpReq describes a client request to craft.
type dhcpReq struct {
	mac       string
	msgType   byte
	vendor    string // option 60; "" omits it
	arch      int    // option 93; -1 omits it
	userClass string // option 77
	ipxeEncap bool   // option 175
	uuid      []byte // option 97
	serverID  net.IP // option 54
	ciaddr    net.IP
	giaddr    net.IP
	flags     uint16
}

func pxeDiscover(mac string, arch int) dhcpReq {
	return dhcpReq{mac: mac, msgType: dhcpDiscover, vendor: "PXEClient:Arch:00007:UNDI:003016", arch: arch}
}

func (r dhcpReq) bytes() []byte {
	b := make([]byte, dhcpHeaderLen)
	b[0], b[1], b[2] = bootRequest, 1, 6
	copy(b[4:8], []byte{0xde, 0xad, 0xbe, 0xef})
	binary.BigEndian.PutUint16(b[10:], r.flags)
	if r.ciaddr != nil {
		copy(b[12:16], r.ciaddr.To4())
	}
	if r.giaddr != nil {
		copy(b[24:28], r.giaddr.To4())
	}
	hw, err := net.ParseMAC(r.mac)
	if err != nil {
		panic(err)
	}
	copy(b[28:], hw)
	b = append(b, dhcpMagic...)
	opt := func(c byte, v []byte) { b = append(append(b, c, byte(len(v))), v...) }
	opt(optMsgType, []byte{r.msgType})
	if r.vendor != "" {
		opt(optVendorClass, []byte(r.vendor))
	}
	if r.arch >= 0 {
		opt(optClientArch, []byte{byte(r.arch >> 8), byte(r.arch)})
	}
	if r.userClass != "" {
		opt(optUserClass, []byte(r.userClass))
	}
	if r.ipxeEncap {
		opt(optIPXEEncap, []byte{0xb1, 1, 1})
	}
	if r.uuid != nil {
		opt(optClientUUID, r.uuid)
	}
	if r.serverID != nil {
		opt(optServerID, r.serverID.To4())
	}
	return append(b, optEnd)
}

// pxeFixture: a Server with one armed MAC and a PXE front end that is not bound (for decision tests) or bound on loopback.
func newPXEFixture(t *testing.T) (*fixture, *PXE) {
	t.Helper()
	f := newFixture(t, "")
	f.arm(t, armedMAC)
	p := &PXE{s: f.s, cfg: PXEConfig{FileX64: "ipxe.efi", FileBIOS: "undionly.kpxe", FileARM64: "ipxe-arm64.efi", HTTPPort: 8080, ClientPort: 68},
		lim: newLogLimiter(time.Minute, f.s.Now)}
	return f, p
}

func mustParse(t *testing.T, b []byte) *dhcpPacket {
	t.Helper()
	p, err := parseDHCP(b)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPXEDecision(t *testing.T) {
	ipxe := pxeDiscover(armedMAC, 7)
	ipxe.userClass = "iPXE"
	ipxeEncap := pxeDiscover(armedMAC, 7)
	ipxeEncap.ipxeEncap = true
	noVendor := pxeDiscover(armedMAC, 7)
	noVendor.vendor = ""
	httpVendor := pxeDiscover(armedMAC, 16)
	httpVendor.vendor = "HTTPClient:Arch:00016"
	inform := pxeDiscover(armedMAC, 7)
	inform.msgType = 8
	reqOther := pxeDiscover(armedMAC, 7)
	reqOther.msgType, reqOther.serverID = dhcpRequest, net.IPv4(192, 0, 2, 1)
	reqNoID := pxeDiscover(armedMAC, 7)
	reqNoID.msgType = dhcpRequest
	reqUs := pxeDiscover(armedMAC, 7)
	reqUs.msgType, reqUs.serverID = dhcpRequest, testServerIP

	cases := []struct {
		name  string
		req   dhcpReq
		proxy bool
		want  pxeAction
		file  string
		typ   byte
	}{
		{"armed x64 UEFI", pxeDiscover(armedMAC, 7), false, pxeReply, "ipxe.efi", dhcpOffer},
		{"armed IA32 UEFI without -pxe-file-ia32: no x64 file", pxeDiscover(armedMAC, 6), false, pxeSilent, "", 0},
		{"armed EBC", pxeDiscover(armedMAC, 9), false, pxeReply, "ipxe.efi", dhcpOffer},
		{"armed BIOS", pxeDiscover(armedMAC, 0), false, pxeReply, "undionly.kpxe", dhcpOffer},
		{"armed arm64 UEFI", pxeDiscover(armedMAC, 11), false, pxeReply, "ipxe-arm64.efi", dhcpOffer},
		{"armed unknown arch 10 (arm32)", pxeDiscover(armedMAC, 10), false, pxeSilent, "", 0},
		{"armed UEFI HTTP boot arch 16", httpVendor, false, pxeSilent, "", 0},
		{"armed no option 93", pxeDiscover(armedMAC, -1), false, pxeSilent, "", 0},
		{"armed non-PXE DHCP", noVendor, false, pxeSilent, "", 0},
		{"armed INFORM", inform, false, pxeSilent, "", 0},
		{"unarmed MAC", pxeDiscover(unarmedMAC, 7), false, pxeNotArmed, "", 0},
		{"unarmed non-PXE stays silent", dhcpReq{mac: unarmedMAC, msgType: dhcpDiscover, arch: -1}, false, pxeSilent, "", 0},
		{"iPXE user class gets the boot URL", ipxe, false, pxeReply, "http://192.0.2.10:8080/boot/" + armedMAC, dhcpOffer},
		{"iPXE option 175 gets the boot URL", ipxeEncap, false, pxeReply, "http://192.0.2.10:8080/boot/" + armedMAC, dhcpOffer},
		{"REQUEST on 67 for another server", reqOther, false, pxeSilent, "", 0},
		{"REQUEST on 67 without server id", reqNoID, false, pxeSilent, "", 0},
		{"REQUEST on 67 naming us", reqUs, false, pxeReply, "ipxe.efi", dhcpAck},
		{"REQUEST on 4011", reqNoID, true, pxeReply, "ipxe.efi", dhcpAck},
		{"DISCOVER on 4011", pxeDiscover(armedMAC, 0), true, pxeReply, "undionly.kpxe", dhcpOffer},
		{"unarmed REQUEST on 4011", func() dhcpReq { r := reqNoID; r.mac = unarmedMAC; return r }(), true, pxeNotArmed, "", 0},
	}
	_, p := newPXEFixture(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := p.decide(mustParse(t, c.req.bytes()), c.proxy, testServerIP)
			if d.action != c.want || d.file != c.file || d.msgType != c.typ && c.want == pxeReply {
				t.Fatalf("got %+v, want action %d file %q type %d", d, c.want, c.file, c.typ)
			}
		})
	}
}

func TestPXEMACCaseAndNonEthernet(t *testing.T) {
	_, p := newPXEFixture(t)
	up := pxeDiscover(strings.ToUpper(armedMAC), 7) // chaddr is bytes; case can't matter, but the lookup key must be normMAC form
	if d := p.decide(mustParse(t, up.bytes()), false, testServerIP); d.action != pxeReply || d.mac != armedMAC {
		t.Fatalf("got %+v", d)
	}
	b := pxeDiscover(armedMAC, 7).bytes()
	b[1] = 6 // IEEE 802 token ring, not Ethernet
	if d := p.decide(mustParse(t, b), false, testServerIP); d.action != pxeSilent {
		t.Fatalf("non-Ethernet htype answered: %+v", d)
	}
}

func TestPXEArmLifecycle(t *testing.T) {
	f, p := newPXEFixture(t)
	disc := func() pxeAction {
		return p.decide(mustParse(t, pxeDiscover(armedMAC, 7).bytes()), false, testServerIP).action
	}
	for i := 0; i < 3; i++ { // the PXE path must never consume the arm
		if a := disc(); a != pxeReply {
			t.Fatalf("discover %d: %d", i, a)
		}
	}
	f.boot(t, armedMAC) // /boot consumes the arm
	if a := disc(); a != pxeNotArmed {
		t.Fatalf("after /boot consumed the arm, PXE still answered (%d): a PXE-first machine would re-wipe", a)
	}

	// Expiry: re-arm, move past the TTL.
	f.arm(t, armedMAC)
	if a := disc(); a != pxeReply {
		t.Fatal("re-armed MAC not answered")
	}
	f.now = f.now.Add(2 * time.Hour)
	if a := disc(); a != pxeNotArmed {
		t.Fatal("expired arm still answered")
	}

	// Degraded: deploys nothing, so offers nothing.
	f.now = f.now.Add(time.Minute)
	f.arm(t, armedMAC)
	f.s.mu.Lock()
	f.s.degraded = true
	f.s.mu.Unlock()
	if a := disc(); a != pxeNotArmed {
		t.Fatal("degraded server answered PXE")
	}
}

func TestPXENoAddressKnown(t *testing.T) {
	_, p := newPXEFixture(t)
	if d := p.decide(mustParse(t, pxeDiscover(armedMAC, 7).bytes()), false, nil); d.action != pxeNoAddress {
		t.Fatalf("got %+v", d)
	}
}

func TestProxyReplyContents(t *testing.T) {
	uuid := append([]byte{0}, bytes.Repeat([]byte{0x11}, 16)...)
	for _, withUUID := range []bool{true, false} {
		r := pxeDiscover(armedMAC, 7)
		r.flags = 0x8000
		if withUUID {
			r.uuid = uuid
		}
		req := mustParse(t, r.bytes())
		out := buildProxyReply(req, dhcpOffer, testServerIP, "ipxe.efi")
		if len(out) < dhcpMinReply {
			t.Fatalf("reply is %d bytes, want >= %d", len(out), dhcpMinReply)
		}
		rep := mustParse(t, out)
		if rep.op != bootReply || rep.xid != req.xid || rep.flags != 0x8000 || rep.chaddr != req.chaddr {
			t.Fatalf("header not echoed: %+v", rep)
		}
		if !bytes.Equal(out[16:20], []byte{0, 0, 0, 0}) {
			t.Fatalf("yiaddr = %v: a proxyDHCP reply must never offer an address", net.IP(out[16:20]))
		}
		if !net.IP(out[20:24]).Equal(testServerIP) {
			t.Fatalf("siaddr = %v", net.IP(out[20:24]))
		}
		if f := string(bytes.TrimRight(out[108:236], "\x00")); f != "ipxe.efi" {
			t.Fatalf("file field %q", f)
		}
		// Lease/addressing options a proxy must never send: subnet, router, DNS, lease time, renewal, rebinding, broadcast.
		for _, o := range []byte{1, 3, 6, 28, 51, 58, 59} {
			if _, ok := rep.opts[o]; ok {
				t.Errorf("option %d present in a proxyDHCP reply", o)
			}
		}
		want := map[byte][]byte{
			optMsgType: {dhcpOffer}, optServerID: testServerIP, optVendorClass: []byte("PXEClient"),
			optVendorSpecific: {6, 1, 8, 255}, optBootFile: []byte("ipxe.efi"), optTFTPServer: []byte("192.0.2.10"),
		}
		for o, v := range want {
			if !bytes.Equal(rep.opts[o], v) {
				t.Errorf("option %d = %v, want %v", o, rep.opts[o], v)
			}
		}
		if got, ok := rep.opts[optClientUUID]; withUUID && !bytes.Equal(got, uuid) || !withUUID && ok {
			t.Errorf("option 97 = %v (withUUID=%v)", got, withUUID)
		}
	}
	// The iPXE stage: URL in field and option 67, no TFTP server option.
	url := "http://192.0.2.10:8080/boot/" + armedMAC
	rep := mustParse(t, buildProxyReply(mustParse(t, pxeDiscover(armedMAC, 7).bytes()), dhcpAck, testServerIP, url))
	if string(rep.opts[optBootFile]) != url || rep.msgType() != dhcpAck {
		t.Fatalf("iPXE reply: %q type %d", rep.opts[optBootFile], rep.msgType())
	}
	if _, ok := rep.opts[optTFTPServer]; ok {
		t.Fatal("option 66 with an HTTP URL")
	}
}

func TestReplyDestination(t *testing.T) {
	_, p := newPXEFixture(t)
	p.dhcp, _ = net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer p.dhcp.Close()
	src := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 68}
	disc := pxeDiscover(armedMAC, 7)
	relayed := disc
	relayed.giaddr = net.IPv4(10, 9, 9, 1)
	bound := disc
	bound.ciaddr = net.IPv4(10, 0, 0, 7)
	cases := []struct {
		name  string
		req   dhcpReq
		proxy bool
		dst   string
		bcast bool
	}{
		{"no address yet: broadcast to 68", disc, false, "255.255.255.255:68", true},
		{"relayed: back to the relay on the server port", relayed, false, fmt.Sprintf("10.9.9.1:%d", p.dhcpPort()), false},
		{"client has an address: unicast to it", bound, false, "10.0.0.7:68", false},
		{"port 4011: back to the source", disc, true, "10.0.0.7:68", false},
	}
	for _, c := range cases {
		dst, bc := p.replyDest(mustParse(t, c.req.bytes()), src, c.proxy)
		if dst.String() != c.dst || bc != c.bcast {
			t.Errorf("%s: got %s bcast=%v, want %s bcast=%v", c.name, dst, bc, c.dst, c.bcast)
		}
	}
}

// Truncated, garbage and hostile packets: no panic, no reply.
func TestParseDHCPGarbage(t *testing.T) {
	good := pxeDiscover(armedMAC, 7).bytes()
	cases := map[string][]byte{
		"empty":           {},
		"one byte":        {1},
		"header only":     good[:dhcpHeaderLen],
		"bad cookie":      append(append(append([]byte{}, good[:dhcpHeaderLen]...), 1, 2, 3, 4), good[dhcpHeaderLen+4:]...),
		"no end":          good[:len(good)-1],
		"option no len":   append(append([]byte{}, good[:dhcpHeaderLen+4]...), 60),
		"option overruns": append(append([]byte{}, good[:dhcpHeaderLen+4]...), 60, 200, 'P'),
	}
	for i := 0; i < len(good); i++ {
		cases[fmt.Sprintf("truncated at %d", i)] = good[:i]
	}
	for name, b := range cases {
		if _, err := parseDHCP(b); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	// Well-formed packets with bad option values: parse fine, never answered.
	_, p := newPXEFixture(t)
	withOpt := func(code byte, v ...byte) []byte {
		r := pxeDiscover(armedMAC, -1).bytes()
		if code == optMsgType { // replace the message type: rebuild without it
			r = append(r[:dhcpHeaderLen+4], optVendorClass, 9, 'P', 'X', 'E', 'C', 'l', 'i', 'e', 'n', 't', optClientArch, 2, 0, 7, optEnd)
		} else {
			r = r[:len(r)-1]
			if code != optClientArch {
				r = append(r, optClientArch, 2, 0, 7)
			}
		}
		return append(append(r, code, byte(len(v))), append(v, optEnd)...)
	}
	for name, b := range map[string][]byte{
		"arch 1 byte":      withOpt(optClientArch, 7),
		"arch 3 bytes":     withOpt(optClientArch, 0, 7, 0),
		"arch empty":       withOpt(optClientArch),
		"msg type empty":   withOpt(optMsgType),
		"msg type 2 bytes": withOpt(optMsgType, 1, 1),
		"no msg type":      append(append(pxeDiscover(armedMAC, 7).bytes()[:dhcpHeaderLen+4], optVendorClass, 9), append([]byte("PXEClient"), optClientArch, 2, 0, 7, optEnd)...),
	} {
		if d := p.decide(mustParse(t, b), false, testServerIP); d.action != pxeSilent {
			t.Errorf("%s: answered %+v", name, d)
		}
	}
	// Random-ish bytes through the full handler: must not panic.
	seed := uint32(1)
	for i := 0; i < 2000; i++ {
		b := append([]byte{}, good...)
		for j := 0; j < 8; j++ {
			seed = seed*1664525 + 1013904223
			b[int(seed>>8)%len(b)] = byte(seed >> 24)
		}
		if pk, err := parseDHCP(b[:int(seed)%len(b)+1]); err == nil {
			p.decide(pk, i%2 == 0, testServerIP)
		}
	}
}

func FuzzParseDHCP(f *testing.F) {
	f.Add(pxeDiscover(armedMAC, 7).bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		if pk, err := parseDHCP(b); err == nil {
			pk.mac()
			pk.arch()
			pk.isIPXE()
			buildProxyReply(pk, dhcpOffer, testServerIP, "x")
		}
	})
}

func TestLogLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLogLimiter(time.Minute, func() time.Time { return now })
	var lines []string
	l.out = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	for i := 0; i < 5; i++ {
		l.logf("notarmed:a", "a")
	}
	l.logf("notarmed:b", "b")
	now = now.Add(61 * time.Second)
	l.logf("notarmed:a", "a")
	// The four repeats of "a" are counted and announced (the notice costs a token like any line) before "b".
	if strings.Join(lines, ",") != "a,pxe: 4 policy log line(s) suppressed (repeats within 1m0s, or over the rate limit),b,a" {
		t.Fatalf("lines %v", lines)
	}
}

func TestStartPXERequiresDir(t *testing.T) {
	f := newFixture(t, "")
	if _, err := f.s.StartPXE(PXEConfig{}); err == nil || !strings.Contains(err.Error(), "-pxe-dir") {
		t.Fatalf("got %v", err)
	}
}

func TestBindErrorAdvice(t *testing.T) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	port := c.LocalAddr().(*net.UDPAddr).Port
	_, err = listenUDP(net.IPv4(127, 0, 0, 1), port, false)
	if err == nil {
		t.Fatal("bound a held port")
	}
	if msg := bindError("TFTP", port, err).Error(); !strings.Contains(msg, "another process already holds") || !strings.Contains(msg, fmt.Sprint(port)) {
		t.Fatalf("message: %s", msg)
	}
	if os.Geteuid() != 0 {
		if _, err := listenUDP(net.IPv4(127, 0, 0, 1), 1, false); err != nil && !strings.Contains(bindError("proxyDHCP", 1, err).Error(), "CAP_NET_BIND_SERVICE") {
			t.Fatalf("privileged port message: %v", bindError("proxyDHCP", 1, err))
		}
	}
}

func TestCheckBootFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ipxe.efi"), []byte("x"), 0o644)
	lines := strings.Join(checkBootFiles(PXEConfig{Dir: dir, FileX64: "ipxe.efi", FileBIOS: "undionly.kpxe"}), "\n")
	if !strings.Contains(lines, "x64 UEFI boot file \"ipxe.efi\": ok") || !strings.Contains(lines, `ERROR: BIOS boot file "undionly.kpxe"`) {
		t.Fatal(lines)
	}
	if l := checkBootFiles(PXEConfig{Dir: filepath.Join(dir, "nope")}); !strings.Contains(l[0], "ERROR") {
		t.Fatal(l)
	}
}

// startLoopbackPXE runs the real listeners on loopback, ephemeral ports, with "broadcast" replies sent to a loopback client socket.
func startLoopbackPXE(t *testing.T, f *fixture, dir string, client *net.UDPConn) *PXE {
	t.Helper()
	lo := net.IPv4(127, 0, 0, 1)
	p, err := f.s.StartPXE(PXEConfig{Dir: dir, FileX64: "ipxe.efi", FileBIOS: "undionly.kpxe", FileARM64: "ipxe-arm64.efi",
		HTTPPort: 8080, ListenIP: lo, BroadcastAddr: lo, ClientPort: client.LocalAddr().(*net.UDPAddr).Port, TFTPTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func recvDHCP(t *testing.T, c *net.UDPConn, wait time.Duration) *dhcpPacket {
	t.Helper()
	buf := make([]byte, 1500)
	_ = c.SetReadDeadline(time.Now().Add(wait))
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	return mustParse(t, buf[:n])
}

// End to end on loopback: arm, DISCOVER on the broadcast port, get the offer, ProxyDHCP REQUEST on 4011, then TFTP the named file.
// Then iPXE asks and gets the /boot URL; /boot consumes the arm; the next DISCOVER gets nothing.
func TestPXEEndToEndLoopback(t *testing.T) {
	f := newFixture(t, "")
	dir := t.TempDir()
	payload := make([]byte, 3000)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	if err := os.WriteFile(filepath.Join(dir, "ipxe.efi"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	p := startLoopbackPXE(t, f, dir, client)

	// Not armed yet: silence.
	if _, err := client.WriteToUDP(pxeDiscover(armedMAC, 7).bytes(), p.DHCPAddr()); err != nil {
		t.Fatal(err)
	}
	if r := recvDHCP(t, client, 300*time.Millisecond); r != nil {
		t.Fatalf("unarmed MAC got a reply: %+v", r)
	}

	f.arm(t, armedMAC)
	client.WriteToUDP(pxeDiscover(armedMAC, 7).bytes(), p.DHCPAddr())
	offer := recvDHCP(t, client, 2*time.Second)
	if offer == nil || offer.msgType() != dhcpOffer {
		t.Fatalf("no offer: %+v", offer)
	}
	file := string(offer.opts[optBootFile])
	siaddr := net.IP(offer.opts[optServerID])
	if file != "ipxe.efi" || !siaddr.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("offer file %q server %v", file, siaddr)
	}

	// ProxyDHCP REQUEST to 4011: ACK comes back to the source port.
	req := pxeDiscover(armedMAC, 7)
	req.msgType, req.ciaddr = dhcpRequest, net.IPv4(127, 0, 0, 1)
	client.WriteToUDP(req.bytes(), p.ProxyAddr())
	ack := recvDHCP(t, client, 2*time.Second)
	if ack == nil || ack.msgType() != dhcpAck || string(ack.opts[optBootFile]) != "ipxe.efi" {
		t.Fatalf("no ACK on the proxy port: %+v", ack)
	}

	got, err := tftpGet(p.TFTPAddr(), file, nil)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("tftp: %v (%d bytes)", err, len(got))
	}

	// iPXE now running: it gets the boot script URL, not iPXE again.
	ip := pxeDiscover(armedMAC, 7)
	ip.userClass = "iPXE"
	client.WriteToUDP(ip.bytes(), p.DHCPAddr())
	r := recvDHCP(t, client, 2*time.Second)
	if r == nil || string(r.opts[optBootFile]) != "http://127.0.0.1:8080/boot/"+armedMAC {
		t.Fatalf("iPXE stage: %+v", r)
	}

	f.boot(t, armedMAC)
	client.WriteToUDP(pxeDiscover(armedMAC, 7).bytes(), p.DHCPAddr())
	if r := recvDHCP(t, client, 300*time.Millisecond); r != nil {
		t.Fatal("offer after /boot consumed the arm")
	}

	// Garbage to both ports: the server keeps running.
	client.WriteToUDP([]byte("garbage"), p.DHCPAddr())
	client.WriteToUDP([]byte{0, 9}, p.TFTPAddr())
	f.arm(t, armedMAC)
	client.WriteToUDP(pxeDiscover(armedMAC, 0).bytes(), p.DHCPAddr())
	if r := recvDHCP(t, client, 2*time.Second); r == nil || string(r.opts[optBootFile]) != "undionly.kpxe" {
		t.Fatalf("after garbage: %+v", r)
	}
}

func TestPXEConcurrentWithArming(t *testing.T) {
	f := newFixture(t, "")
	client, _ := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer client.Close()
	p := startLoopbackPXE(t, f, t.TempDir(), client)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				f.s.doArm(armedMAC, "win11-pro")
				f.s.doDisarm(armedMAC)
			}
		}()
		go func() {
			defer wg.Done()
			c, _ := net.DialUDP("udp4", nil, p.DHCPAddr())
			defer c.Close()
			for j := 0; j < 50; j++ {
				c.Write(pxeDiscover(armedMAC, 7).bytes())
			}
		}()
	}
	wg.Wait()
}
