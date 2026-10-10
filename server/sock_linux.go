//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || ppc64le || s390x || loong64)

package main

import (
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"unsafe"
)

const havePktinfo = true

const pxePlatformOK = true

// SO_REUSEPORT is 0xf on every architecture this file builds for (package syscall omits it on amd64, 386 and arm). MIPS uses
// 0x200 and SPARC 0x200, which is why the build constraint above lists architectures instead of all of linux.
const soReusePort = 0xf

const privHint = "Ports below 1024 need root or CAP_NET_BIND_SERVICE: run as root, or `sudo setcap cap_net_bind_service=+ep ./minute2win-server`."

func sockSetup(fd uintptr, reuse bool) error {
	if reuse {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			return err
		}
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1); err != nil {
			return err
		}
	}
	return syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_PKTINFO, 1)
}

func readUDP(c *net.UDPConn, buf, oob []byte) (int, *net.UDPAddr, localInfo, error) {
	n, oobn, _, src, err := c.ReadMsgUDP(buf, oob)
	if err != nil {
		return 0, nil, localInfo{}, err
	}
	var li localInfo
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err == nil {
		for _, m := range msgs {
			if m.Header.Level == syscall.IPPROTO_IP && m.Header.Type == syscall.IP_PKTINFO && len(m.Data) >= syscall.SizeofInet4Pktinfo {
				// struct in_pktinfo { int ipi_ifindex; struct in_addr ipi_spec_dst; struct in_addr ipi_addr; }
				li.ifIndex = int(int32(binary.NativeEndian.Uint32(m.Data[0:4])))
				li.specDst = net.IP(append([]byte(nil), m.Data[4:8]...))
				li.dst = net.IP(append([]byte(nil), m.Data[8:12]...))
			}
		}
	}
	return n, src, li, nil
}

// writeUDP sends b to dst. A broadcast is pinned to the interface the request came in on, so a multi-homed host answers on the
// right network instead of wherever the default route points.
func writeUDP(c *net.UDPConn, b []byte, dst *net.UDPAddr, li localInfo, broadcast bool) error {
	var oob []byte
	if broadcast && li.ifIndex > 0 {
		oob = make([]byte, syscall.CmsgSpace(syscall.SizeofInet4Pktinfo))
		h := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
		h.Level = syscall.IPPROTO_IP
		h.Type = syscall.IP_PKTINFO
		h.SetLen(syscall.CmsgLen(syscall.SizeofInet4Pktinfo))
		pi := (*syscall.Inet4Pktinfo)(unsafe.Pointer(&oob[syscall.CmsgLen(0)]))
		pi.Ifindex = int32(li.ifIndex)
	}
	_, _, err := c.WriteMsgUDP(b, oob, dst)
	return err
}

func isPermErr(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func isInUseErr(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
