// minute2win-server: arm machines for a zero-touch wipe, hand iPXE their boot script, and show deployments live.
//
//	minute2win-server -listen :8080 -manifests ../manifests -data ./data
//
// Targets' iPXE (dnsmasq dhcp-boot, or a USB boot drive's autoexec) runs: chain http://<this>:8080/boot/${net0/mac}
// Set M2W_TOKEN to require a token for arming and disarming.
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

// pxeHTTPTarget derives what the iPXE boot URL advertises from the HTTP listener as bound: its real port, and its address when it
// is bound to one specific IPv4 address (HTTP answers nowhere else). override (-pxe-ip) wins for the address. nil = per interface.
func pxeHTTPTarget(bound net.Addr, override net.IP) (int, net.IP, error) {
	tcp, ok := bound.(*net.TCPAddr)
	if !ok || tcp.Port == 0 {
		return 0, nil, fmt.Errorf("pxe: cannot tell the HTTP port from %v", bound)
	}
	if override != nil {
		return tcp.Port, override.To4(), nil
	}
	if tcp.IP == nil || tcp.IP.IsUnspecified() {
		return tcp.Port, nil, nil
	}
	ip4 := tcp.IP.To4()
	switch {
	case ip4 == nil:
		return 0, nil, fmt.Errorf("pxe: HTTP is bound to %s, which PXE (IPv4) clients cannot use; bind -listen to an IPv4 address or all addresses, or set -pxe-ip", tcp.IP)
	case ip4.IsLoopback():
		return 0, nil, fmt.Errorf("pxe: HTTP is bound to loopback %s, which PXE clients cannot reach; bind -listen to a LAN address or all addresses", ip4)
	}
	return tcp.Port, ip4, nil
}

func main() {
	listen := flag.String("listen", ":8080", "address to serve on")
	manifests := flag.String("manifests", "", "folder of deployment manifests (*.json); default: ./manifests, next to the executable, or ../manifests")
	data := flag.String("data", "data", "folder for state.json (\"\" keeps state in memory)")
	base := flag.String("image-base", "https://boot.spillerstech.us/windows11", "URL holding wimboot, BCD, boot.sdi and boot.wim")
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

	s := &Server{ManifestDir: *manifests, ImageBase: *base, Token: os.Getenv("M2W_TOKEN"), ArmTTL: *ttl, ReportTTL: 3 * time.Hour, Now: time.Now}
	if *data != "" {
		if err := os.MkdirAll(*data, 0o700); err != nil {
			log.Fatal(err)
		}
		s.StatePath = filepath.Join(*data, "state.json")
	}
	if err := s.Load(); err != nil {
		log.Fatal(err)
	}
	if s.Token == "" {
		log.Print("warning: M2W_TOKEN is not set; anyone who can reach this server can arm a machine for a wipe")
	}
	log.Printf("minute2win-server on %s, manifests %s, images %s", *listen, *manifests, *base)
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
		if cfg.HTTPPort, cfg.AdvertiseIP, err = pxeHTTPTarget(ln.Addr(), override); err != nil {
			log.Fatal(err)
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
