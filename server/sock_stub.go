//go:build !windows && !darwin && !freebsd && !netbsd && !(linux && (amd64 || arm64 || 386 || arm || riscv64 || ppc64le || s390x || loong64))

package main

// Every other GOOS/GOARCH (OpenBSD included: there "[::]" is not dual-stack, so a wildcard -listen would not reach IPv4 PXE
// clients): the server builds and runs, but -pxe refuses to start (StartPXE checks pxePlatformOK), because the
// socket options it needs have not been written or tested here.

import (
	"errors"
	"net"
)

const havePktinfo = false

const pxePlatformOK = false

const privHint = ""

var errNoPXE = errors.New("built-in PXE is not supported on this platform")

func sockSetup(fd uintptr, reuse bool) error { return errNoPXE }

func readUDP(c *net.UDPConn, buf, _ []byte) (int, *net.UDPAddr, localInfo, error) {
	n, src, err := c.ReadFromUDP(buf)
	return n, src, localInfo{}, err
}

func writeUDP(c *net.UDPConn, b []byte, dst *net.UDPAddr, _ localInfo, _ bool) error {
	_, err := c.WriteToUDP(b, dst)
	return err
}

func isPermErr(error) bool  { return false }
func isInUseErr(error) bool { return false }
