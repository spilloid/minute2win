package main

import (
	"context"
	"net"
	"strconv"
	"syscall"
)

// localInfo is what the OS says about where a datagram arrived (Linux IP_PKTINFO); zero elsewhere.
type localInfo struct {
	ifIndex int
	specDst net.IP // the local address the kernel would answer from on that interface
	dst     net.IP // the datagram's destination address (ours, or broadcast)
}

// listenUDP binds ip:port (all addresses when ip is nil). reuse sets SO_REUSEADDR (and SO_REUSEPORT where it exists) so the
// broadcast DHCP port can be shared with another listener that also allows it.
func listenUDP(ip net.IP, port int, reuse bool) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = sockSetup(fd, reuse) }); err != nil {
			return err
		}
		return serr
	}}
	host := ""
	if ip != nil {
		host = ip.String()
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
