package main

// Minimal DHCPv4 (RFC 2131/2132) parsing and building, just enough for a PXE proxyDHCP responder (PXE 2.1, RFC 4578).
// Hand-rolled on purpose: the fixed BOOTP header plus TLV options is ~150 lines, and the well-known library would add several
// transitive modules (and its current version needs a newer Go than this module's go directive).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

const (
	bootRequest = 1
	bootReply   = 2

	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpAck      = 5

	optPad            = 0
	optVendorSpecific = 43
	optMsgType        = 53
	optServerID       = 54
	optVendorClass    = 60
	optTFTPServer     = 66
	optBootFile       = 67
	optUserClass      = 77
	optClientArch     = 93
	optClientUUID     = 97
	optIPXEEncap      = 175
	optEnd            = 255

	dhcpHeaderLen = 236
	dhcpMinReply  = 300 // BOOTP minimum; some PXE ROMs drop shorter replies
)

var dhcpMagic = []byte{99, 130, 83, 99}

// dhcpPacket holds the fields the PXE responder reads. Options that appear more than once are concatenated (RFC 3396).
// Option overload (52) is not supported: the sname/file fields are never read as options.
type dhcpPacket struct {
	op, htype, hlen byte
	xid             [4]byte
	flags           uint16
	ciaddr, giaddr  net.IP
	chaddr          [16]byte
	opts            map[byte][]byte
}

var errMalformed = errors.New("malformed DHCP packet")

func parseDHCP(b []byte) (*dhcpPacket, error) {
	if len(b) < dhcpHeaderLen+len(dhcpMagic) {
		return nil, fmt.Errorf("%w: %d bytes", errMalformed, len(b))
	}
	if !bytes.Equal(b[dhcpHeaderLen:dhcpHeaderLen+4], dhcpMagic) {
		return nil, fmt.Errorf("%w: no magic cookie", errMalformed)
	}
	p := &dhcpPacket{op: b[0], htype: b[1], hlen: b[2], flags: binary.BigEndian.Uint16(b[10:12]),
		ciaddr: net.IP(append([]byte(nil), b[12:16]...)), giaddr: net.IP(append([]byte(nil), b[24:28]...)), opts: map[byte][]byte{}}
	copy(p.xid[:], b[4:8])
	copy(p.chaddr[:], b[28:44])
	o := b[dhcpHeaderLen+4:]
	for i := 0; i < len(o); {
		code := o[i]
		if code == optPad {
			i++
			continue
		}
		if code == optEnd {
			return p, nil
		}
		if i+1 >= len(o) {
			return nil, fmt.Errorf("%w: option %d has no length", errMalformed, code)
		}
		n := int(o[i+1])
		if i+2+n > len(o) {
			return nil, fmt.Errorf("%w: option %d overruns the packet", errMalformed, code)
		}
		p.opts[code] = append(p.opts[code], o[i+2:i+2+n]...)
		i += 2 + n
	}
	return nil, fmt.Errorf("%w: no end option", errMalformed)
}

// mac returns the client hardware address in normMAC form; ok is false unless it is Ethernet (htype 1, hlen 6).
func (p *dhcpPacket) mac() (string, bool) {
	if p.htype != 1 || p.hlen != 6 {
		return "", false
	}
	return net.HardwareAddr(p.chaddr[:6]).String(), true
}

func (p *dhcpPacket) msgType() byte {
	if v := p.opts[optMsgType]; len(v) == 1 {
		return v[0]
	}
	return 0
}

// arch is the first client system architecture in option 93 (RFC 4578).
func (p *dhcpPacket) arch() (uint16, bool) {
	v := p.opts[optClientArch]
	if len(v) < 2 || len(v)%2 != 0 {
		return 0, false
	}
	return binary.BigEndian.Uint16(v[:2]), true
}

func (p *dhcpPacket) isPXEClient() bool {
	return bytes.HasPrefix(p.opts[optVendorClass], []byte("PXEClient"))
}

// isIPXE: iPXE sends user class "iPXE" (option 77) and its own encapsulated options (175). Firmware PXE sends neither.
func (p *dhcpPacket) isIPXE() bool {
	if _, ok := p.opts[optIPXEEncap]; ok {
		return true
	}
	return bytes.Contains(p.opts[optUserClass], []byte("iPXE"))
}

// buildProxyReply builds a proxyDHCP OFFER or ACK. It never carries an address or lease options: yiaddr stays 0.0.0.0 and there
// is no subnet, router, DNS or lease time, so the network's own DHCP server stays the only one handing out addresses.
func buildProxyReply(req *dhcpPacket, msgType byte, serverIP net.IP, bootFile string) []byte {
	b := make([]byte, dhcpHeaderLen, dhcpMinReply+64)
	b[0], b[1], b[2] = bootReply, req.htype, req.hlen
	copy(b[4:8], req.xid[:])
	binary.BigEndian.PutUint16(b[10:12], req.flags)
	copy(b[12:16], req.ciaddr.To4())
	// yiaddr (16:20) stays zero.
	copy(b[20:24], serverIP.To4())
	copy(b[24:28], req.giaddr.To4())
	copy(b[28:44], req.chaddr[:])
	if len(bootFile) < 128 { // the file field needs a NUL; option 67 always carries the name
		copy(b[108:236], bootFile)
	}
	b = append(b, dhcpMagic...)
	opt := func(code byte, v []byte) { b = append(append(b, code, byte(len(v))), v...) }
	opt(optMsgType, []byte{msgType})
	opt(optServerID, serverIP.To4())
	opt(optVendorClass, []byte("PXEClient"))
	if uuid, ok := req.opts[optClientUUID]; ok && len(uuid) <= 255 {
		opt(optClientUUID, uuid)
	}
	// PXE vendor options: PXE_DISCOVERY_CONTROL (6) = 8, "download the boot file in this packet": no menu, no boot-server discovery.
	opt(optVendorSpecific, []byte{6, 1, 8, optEnd})
	if !isURL(bootFile) {
		opt(optTFTPServer, []byte(serverIP.To4().String()))
	}
	opt(optBootFile, []byte(bootFile))
	b = append(b, optEnd)
	for len(b) < dhcpMinReply {
		b = append(b, optPad)
	}
	return b
}

func isURL(s string) bool { return bytes.HasPrefix([]byte(s), []byte("http://")) }
