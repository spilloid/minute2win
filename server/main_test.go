package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserURL(t *testing.T) {
	for in, want := range map[string]string{":8080": "http://localhost:8080/", "0.0.0.0:9": "http://localhost:9/", "127.0.0.1:18080": "http://127.0.0.1:18080/", "[::]:80": "http://localhost:80/"} {
		if got := browserURL(in); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindManifests(t *testing.T) {
	if got := findManifests("/explicit"); got != "/explicit" {
		t.Fatalf("explicit flag ignored: %s", got)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := findManifests(""); got != "manifests" {
		t.Fatalf("./manifests not preferred: %s", got)
	}
}

func TestLanURLsRespectTheBoundAddress(t *testing.T) {
	specific := lanURLs(&net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 8080})
	if len(specific) != 1 || specific[0] != "http://192.168.1.50:8080" {
		t.Fatalf("a specific bind should advertise only that address: %v", specific)
	}
	if got := lanURLs(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}); len(got) != 0 {
		t.Fatalf("a loopback bind is unreachable from targets, advertise nothing: %v", got)
	}
	for _, u := range lanURLs(&net.TCPAddr{IP: net.IPv4zero, Port: 18080}) {
		if u == "" || u[len(u)-6:] != ":18080" {
			t.Fatalf("wildcard bind must use the bound port: %v", u)
		}
	}
	if got := lanURLs(&net.UnixAddr{Name: "x", Net: "unix"}); got != nil {
		t.Fatalf("non-TCP address: %v", got)
	}
}

// Review #3 and round 2 D: the iPXE URL uses the HTTP listener as bound, and whatever it advertises must be reachable there.
func TestPXEHTTPTarget(t *testing.T) {
	lan := net.IPv4(192, 168, 1, 50)
	local := []net.IP{lan.To4(), net.IPv4(10, 0, 0, 5).To4()}
	cases := []struct {
		name     string
		bound    net.Addr
		override net.IP
		port     int
		ip       string // "" = nil (per interface)
		err      string // substring; "" = no error
		warn     bool
	}{
		{"wildcard", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, nil, 8080, "", "", false},
		{"wildcard nil IP", &net.TCPAddr{Port: 8080}, nil, 8080, "", "", false},
		{"[::] (dual-stack) with :0's real port", &net.TCPAddr{IP: net.IPv6unspecified, Port: 41234}, nil, 41234, "", "", false},
		{"wildcard + local -pxe-ip", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, net.IPv4(10, 0, 0, 5), 8080, "10.0.0.5", "", false},
		{"wildcard + foreign -pxe-ip warns", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, net.IPv4(203, 0, 113, 9), 8080, "203.0.113.9", "", true},
		{"one LAN address", &net.TCPAddr{IP: lan, Port: 9000}, nil, 9000, "192.168.1.50", "", false},
		{"one LAN address + the same -pxe-ip", &net.TCPAddr{IP: lan, Port: 9000}, lan, 9000, "192.168.1.50", "", false},
		{"one LAN address + a different -pxe-ip", &net.TCPAddr{IP: lan, Port: 9000}, net.IPv4(10, 0, 0, 5), 0, "", "HTTP only listens on 192.168.1.50", false},
		{"loopback bind", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, nil, 0, "", "loopback", false},
		{"loopback bind + LAN -pxe-ip", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, lan, 0, "", "loopback", false},
		{"[::1] bind", &net.TCPAddr{IP: net.IPv6loopback, Port: 8080}, nil, 0, "", "IPv6", false},
		{"IPv6-specific bind", &net.TCPAddr{IP: net.ParseIP("fd00::1"), Port: 8080}, nil, 0, "", "IPv6", false},
		{"IPv6-specific bind + -pxe-ip", &net.TCPAddr{IP: net.ParseIP("fd00::1"), Port: 8080}, lan, 0, "", "IPv6", false},
		{"loopback -pxe-ip", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, net.IPv4(127, 0, 0, 1), 0, "", "-pxe-ip", false},
		{"IPv6 -pxe-ip", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, net.ParseIP("fd00::1"), 0, "", "-pxe-ip", false},
		{"port 0", &net.TCPAddr{IP: net.IPv4zero, Port: 0}, nil, 0, "", "port", false},
		{"not TCP", &net.UnixAddr{Name: "x", Net: "unix"}, nil, 0, "", "port", false},
	}
	for _, c := range cases {
		port, ip, warn, err := pxeHTTPTarget(c.bound, c.override, local)
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if (c.err == "") != (err == nil) || (c.err != "" && !strings.Contains(gotErr, c.err)) || port != c.port ||
			(c.ip == "" && ip != nil) || (c.ip != "" && ip.String() != c.ip) || (warn != "") != c.warn {
			t.Errorf("%s: got port %d ip %v warn %q err %v", c.name, port, ip, warn, err)
		}
	}
}

// Round 2 F: the same rules against real listeners.
func TestPXEHTTPTargetRealListeners(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, _, _, err := pxeHTTPTarget(ln.Addr(), net.IPv4(192, 168, 1, 50), nil); err == nil {
		t.Fatal("-pxe-ip with HTTP on loopback accepted: the advertised URL is unreachable")
	}
	any4, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer any4.Close()
	if port, _, _, err := pxeHTTPTarget(any4.Addr(), net.IPv4(10, 0, 0, 1), nil); err != nil || port != any4.Addr().(*net.TCPAddr).Port {
		t.Fatalf("wildcard :0 listener: port %d err %v", port, err)
	}
	v6, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	defer v6.Close()
	if _, _, _, err := pxeHTTPTarget(v6.Addr(), nil, nil); err == nil || !strings.Contains(err.Error(), "IPv6") {
		t.Fatalf("[::1] listener: %v", err)
	}
}
