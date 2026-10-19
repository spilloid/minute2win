// minute2win-server: arm machines for a zero-touch wipe, hand iPXE their boot script, and show deployments live.
//
//	M2W_TOKEN=... minute2win-server -listen :8080 -manifests ../manifests -data ./data -image-base https://<your host>/windows11
//
// Targets' iPXE (dnsmasq dhcp-boot, or a USB boot drive's autoexec) runs: chain http://<this>:8080/boot/${net0/mac}
// It listens on loopback by default. To deploy, set M2W_TOKEN (required to arm and disarm when -listen is reachable from the network)
// and -image-base (the folder holding your boot files).
//
// With -pxe -pxe-dir <folder of iPXE binaries> it also answers PXE itself (proxyDHCP + read-only TFTP), for ARMED MACs only,
// so no dnsmasq is needed. See README "Built-in PXE".
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// findManifests: an explicit -manifests wins; otherwise the first of ./manifests, <exe dir>/manifests, ../manifests that exists,
// so the Windows zip (exe next to manifests/) and a checkout (go run in server/) both just work.
func findManifests(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	cands := []string{"manifests"}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "manifests"))
	}
	cands = append(cands, filepath.Join("..", "manifests"))
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return "manifests"
}

// browserURL turns a listen address into a URL a browser on this machine can open.
func browserURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen + "/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open a browser (%v); open %s yourself", err, url)
	}
}

// lanURLs lists the addresses a target can reach this server at, for the iPXE chain line. It works from the address actually
// bound: a specific IP is the only one advertised; a wildcard bind lists every non-loopback IPv4 address of this machine.
func lanURLs(bound net.Addr) []string {
	tcp, ok := bound.(*net.TCPAddr)
	if !ok {
		return nil
	}
	port := fmt.Sprint(tcp.Port)
	if tcp.IP != nil && !tcp.IP.IsUnspecified() {
		if tcp.IP.IsLoopback() {
			return nil
		}
		return []string{"http://" + net.JoinHostPort(tcp.IP.String(), port)}
	}
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
			out = append(out, "http://"+net.JoinHostPort(ip.IP.String(), port))
		}
	}
	return out
}

// pxeHTTPTarget decides what the iPXE boot URL advertises, from the HTTP listener as actually bound, and checks that it is reachable
// there. The port is the bound one (so -listen :0 works). The address:
//   - HTTP bound to one IPv4 address: that address; -pxe-ip, if given, must be the same (HTTP answers nowhere else).
//   - HTTP bound to loopback, or to a specific IPv6 address (including [::1]): refused, PXE clients (IPv4) can't reach it.
//   - HTTP bound to all addresses (0.0.0.0 or [::], dual-stack on Linux, Windows, macOS and FreeBSD; not on OpenBSD, which the
//     built-in PXE does not support): -pxe-ip if given (a warning if it is not one of local), otherwise nil = per interface.
//
// -pxe-ip itself must be a usable IPv4 unicast address.
func pxeHTTPTarget(bound net.Addr, override net.IP, local []net.IP) (port int, adv net.IP, warning string, err error) {
	tcp, ok := bound.(*net.TCPAddr)
	if !ok || tcp.Port == 0 {
		return 0, nil, "", fmt.Errorf("pxe: cannot tell the HTTP port from %v", bound)
	}
	if override != nil {
		o4 := override.To4()
		if o4 == nil || o4.IsLoopback() || o4.IsUnspecified() || o4.Equal(net.IPv4bcast) || o4.IsMulticast() {
			return 0, nil, "", fmt.Errorf("pxe: -pxe-ip %s is not an IPv4 address PXE clients can reach", override)
		}
		override = o4
	}
	if tcp.IP != nil && !tcp.IP.IsUnspecified() {
		ip4 := tcp.IP.To4()
		switch {
		case ip4 == nil:
			return 0, nil, "", fmt.Errorf("pxe: HTTP is bound to %s, an IPv6 address PXE (IPv4) clients cannot use; bind -listen to an IPv4 address or to all addresses", tcp.IP)
		case ip4.IsLoopback():
			return 0, nil, "", fmt.Errorf("pxe: HTTP is bound to loopback %s, which PXE clients cannot reach; bind -listen to a LAN address or to all addresses (-listen defaults to 127.0.0.1:8080; for PXE, set M2W_TOKEN and use -listen :8080)", ip4)
		case override != nil && !override.Equal(ip4):
			return 0, nil, "", fmt.Errorf("pxe: -pxe-ip %s, but HTTP only listens on %s, so the boot URL would not work; make them the same or bind -listen to all addresses", override, ip4)
		}
		return tcp.Port, ip4, "", nil
	}
	if override != nil {
		for _, l := range local {
			if l.Equal(override) {
				return tcp.Port, override, "", nil
			}
		}
		return tcp.Port, override, fmt.Sprintf("pxe: warning: -pxe-ip %s is not an address of this host; clients will only reach the boot URL if it is forwarded here", override), nil
	}
	return tcp.Port, nil, "", nil
}

// listenIsLoopback reports whether -listen only accepts connections from this machine: a loopback address, or a name (localhost)
// that resolves only to loopback addresses. An empty host (":8080") means every interface.
func listenIsLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

// checkListen refuses a server that anyone on the network could use to arm a disk wipe: a non-loopback listener needs M2W_TOKEN,
// or an explicit -insecure-no-token. Machines being imaged never need the token (boot scripts, manifests and reports don't use it).
func checkListen(listen, token string, insecure bool) (warning string, err error) {
	switch {
	case listenIsLoopback(listen):
		if token == "" {
			return "listening on loopback only: the console works on this machine, but machines being imaged cannot reach it. " +
				"To deploy, set M2W_TOKEN and start with -listen :8080 (or a LAN address)", nil
		}
		return "", nil
	case token != "":
		return "", nil
	case insecure:
		return "WARNING: -insecure-no-token: M2W_TOKEN is not set and -listen " + listen + " is reachable from the network, so anyone " +
			"who can reach this port can arm a machine for a disk wipe", nil
	}
	return "", fmt.Errorf(`refusing to start: -listen %s accepts connections from other machines and M2W_TOKEN is not set, so anyone who can reach this port could arm a machine for a disk wipe. Choose one:
  - set M2W_TOKEN to a long random secret and start again (Linux: export M2W_TOKEN='...'; PowerShell: $env:M2W_TOKEN = '...').
    Only arming and disarming use it; machines being imaged never need it.
  - preview on this machine only: -listen 127.0.0.1:8080 (the default; machines being imaged cannot reach it).
  - on an isolated lab network where anyone who can reach this port may wipe machines: add -insecure-no-token`, listen)
}

// checkImageBase validates -image-base: the folder iPXE fetches wimboot, BCD, boot.sdi and boot.wim from.
func checkImageBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("-image-base %q must be the http:// or https:// URL of your folder holding wimboot, BCD, boot.sdi and boot.wim", base)
	}
	if placeholderHost(u.Hostname()) {
		return fmt.Errorf("-image-base %s uses the documentation's placeholder host %s; use your own boot-asset host", base, u.Hostname())
	}
	return nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "address to serve on; loopback by default. Machines being imaged must reach it, so for deployment use :8080 or a LAN address, with M2W_TOKEN set")
	manifests := flag.String("manifests", "", "folder of deployment manifests (*.json); default: ./manifests, next to the executable, or ../manifests")
	data := flag.String("data", "data", "folder for state.json (\"\" keeps state in memory)")
	base := flag.String("image-base", "", "URL of your folder holding wimboot, BCD, boot.sdi and boot.wim (required to arm; there is no default)")
	insecure := flag.Bool("insecure-no-token", false, "allow a network-reachable -listen without M2W_TOKEN: anyone who can reach the port can arm a disk wipe")
	ttl := flag.Duration("arm-ttl", 2*time.Hour, "an arm that no machine boots within this long expires")
	open := flag.Bool("open", runtime.GOOS == "windows", "open the web UI in a browser on start (default on Windows, so double-clicking works)")
	pxe := flag.Bool("pxe", false, "also answer PXE: proxyDHCP (UDP 67, 4011) and read-only TFTP (UDP 69), for armed MACs only")
	pxeDir := flag.String("pxe-dir", "", "with -pxe: folder holding the iPXE binaries to serve over TFTP (required)")
	pxeIP := flag.String("pxe-ip", "", "with -pxe: IPv4 address to advertise to PXE clients (default: the receiving interface's)")
	pxeX64 := flag.String("pxe-file-x64", "ipxe.efi", "with -pxe: boot file for x64 UEFI (arch 7, 9); BOOTX64.EFI for a signed shim chain")
	pxeIA32 := flag.String("pxe-file-ia32", "", "with -pxe: boot file for 32-bit x86 UEFI (arch 6); empty = such clients get no offer")
	pxeBIOS := flag.String("pxe-file-bios", "undionly.kpxe", "with -pxe: boot file for BIOS (arch 0)")
	pxeARM := flag.String("pxe-file-arm64", "ipxe-arm64.efi", "with -pxe: boot file for arm64 UEFI (arch 11)")
	pxeDHCP := flag.Int("pxe-dhcp-port", 67, "with -pxe: proxyDHCP broadcast port")
	pxeProxy := flag.Int("pxe-proxy-port", 4011, "with -pxe: proxyDHCP request port")
	pxeTFTP := flag.Int("pxe-tftp-port", 69, "with -pxe: TFTP port")
	flag.Parse()
	*manifests = findManifests(*manifests)

	token := os.Getenv("M2W_TOKEN")
	warning, err := checkListen(*listen, token, *insecure)
	if err != nil {
		log.Fatal(err)
	}
	if warning != "" {
		log.Print(warning)
	}
	if *base != "" {
		if err := checkImageBase(*base); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Print("no -image-base: the console works, but nothing can be armed until the server is restarted with " +
			"-image-base https://<your boot-asset host>/<folder> (the folder holding wimboot, BCD, boot.sdi and boot.wim)")
	}

	s := &Server{ManifestDir: *manifests, ImageBase: *base, Token: token, ArmTTL: *ttl, ReportTTL: 3 * time.Hour, Now: time.Now}
	if *data != "" {
		if err := os.MkdirAll(*data, 0o700); err != nil {
			log.Fatal(err)
		}
		s.StatePath = filepath.Join(*data, "state.json")
	}
	if err := s.Load(); err != nil {
		log.Fatal(err)
	}
	images := *base
	if images == "" {
		images = "(none: arming refused)"
	}
	log.Printf("minute2win-server on %s, manifests %s, images %s", *listen, *manifests, images)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	if *pxe {
		cfg := PXEConfig{DHCPPort: *pxeDHCP, ProxyPort: *pxeProxy, TFTPPort: *pxeTFTP, ClientPort: 68, Dir: *pxeDir,
			FileX64: *pxeX64, FileBIOS: *pxeBIOS, FileARM64: *pxeARM, FileIA32: *pxeIA32}
		var override net.IP
		if *pxeIP != "" {
			if override = net.ParseIP(*pxeIP).To4(); override == nil {
				log.Fatalf("pxe: -pxe-ip %q is not an IPv4 address", *pxeIP)
			}
		}
		// The HTTP listener is already bound, so the boot URL uses the port it really got (-listen :0 included) and, when it is
		// bound to one address, that address (the only one the URL can work on).
		_, local := localIPv4s()
		var warning string
		if cfg.HTTPPort, cfg.AdvertiseIP, warning, err = pxeHTTPTarget(ln.Addr(), override, local); err != nil {
			log.Fatal(err)
		}
		if warning != "" {
			log.Print(warning)
		}
		if _, err := s.StartPXE(cfg); err != nil {
			log.Fatal(err)
		}
	}
	for _, u := range lanURLs(ln.Addr()) {
		log.Printf("targets chain: %s/boot/${net0/mac}", u)
	}
	if *open {
		openBrowser(browserURL(*listen))
	}
	log.Fatal(http.Serve(ln, s.Handler()))
}
