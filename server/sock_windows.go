package main

import (
	"errors"
	"net"
	"syscall"
)

const havePktinfo = false

const pxePlatformOK = true

const privHint = "On Windows, run minute2win-server as Administrator and allow it through the firewall " +
	"(for example: New-NetFirewallRule -DisplayName minute2win-pxe -Direction Inbound -Protocol UDP -LocalPort 67,69,4011 -Action Allow)."

func sockSetup(fd uintptr, reuse bool) error {
	if reuse {
		return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}
	return nil
}

func readUDP(c *net.UDPConn, buf, _ []byte) (int, *net.UDPAddr, localInfo, error) {
	n, src, err := c.ReadFromUDP(buf)
	return n, src, localInfo{}, err
}

func writeUDP(c *net.UDPConn, b []byte, dst *net.UDPAddr, _ localInfo, _ bool) error {
	_, err := c.WriteToUDP(b, dst)
	return err
}

const (
	wsaeAccess      = syscall.Errno(10013) // WSAEACCES: also returned when another socket holds the port with SO_EXCLUSIVEADDRUSE
	wsaeAddrInUse   = syscall.Errno(10048) // WSAEADDRINUSE
	errAccessDenied = syscall.Errno(5)
)

func isPermErr(err error) bool {
	return errors.Is(err, wsaeAccess) || errors.Is(err, errAccessDenied)
}

func isInUseErr(err error) bool { return errors.Is(err, wsaeAddrInUse) }
