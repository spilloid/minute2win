//go:build unix && !linux

package main

import (
	"errors"
	"net"
	"syscall"
)

const havePktinfo = false

const privHint = "Ports below 1024 need root on this OS."

func sockSetup(fd uintptr, reuse bool) error {
	if !reuse {
		return nil
	}
	if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		return err
	}
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
}

func readUDP(c *net.UDPConn, buf, _ []byte) (int, *net.UDPAddr, localInfo, error) {
	n, src, err := c.ReadFromUDP(buf)
	return n, src, localInfo{}, err
}

func writeUDP(c *net.UDPConn, b []byte, dst *net.UDPAddr, _ localInfo, _ bool) error {
	_, err := c.WriteToUDP(b, dst)
	return err
}

func isPermErr(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func isInUseErr(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
