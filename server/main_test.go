package main

import (
	"net"
	"os"
	"path/filepath"
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

// Review #3: the iPXE URL uses the HTTP listener as actually bound.
func TestPXEHTTPTarget(t *testing.T) {
	lan := net.IPv4(192, 168, 1, 50)
	cases := []struct {
		name     string
		bound    net.Addr
		override net.IP
		port     int
		ip       string // "" = nil (per interface)
		err      bool
	}{
		{"wildcard", &net.TCPAddr{IP: net.IPv4zero, Port: 8080}, nil, 8080, "", false},
		{"wildcard nil IP", &net.TCPAddr{Port: 8080}, nil, 8080, "", false},
		{"listen :0 got a real port", &net.TCPAddr{IP: net.IPv6unspecified, Port: 41234}, nil, 41234, "", false},
		{"one LAN address is the only one HTTP answers on", &net.TCPAddr{IP: lan, Port: 9000}, nil, 9000, "192.168.1.50", false},
		{"-pxe-ip wins", &net.TCPAddr{IP: lan, Port: 9000}, net.IPv4(10, 0, 0, 5), 9000, "10.0.0.5", false},
		{"loopback is unreachable", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, nil, 0, "", true},
		{"IPv6-only bind", &net.TCPAddr{IP: net.ParseIP("fd00::1"), Port: 8080}, nil, 0, "", true},
		{"port 0", &net.TCPAddr{IP: net.IPv4zero, Port: 0}, nil, 0, "", true},
		{"not TCP", &net.UnixAddr{Name: "x", Net: "unix"}, nil, 0, "", true},
	}
	for _, c := range cases {
		port, ip, err := pxeHTTPTarget(c.bound, c.override)
		if (err != nil) != c.err || port != c.port || (c.ip == "" && ip != nil) || (c.ip != "" && ip.String() != c.ip) {
			t.Errorf("%s: got %d %v %v", c.name, port, ip, err)
		}
	}
	// And a real listener on :0.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if port, _, _ := pxeHTTPTarget(ln.Addr(), net.IPv4(10, 0, 0, 1)); port == 0 || port != ln.Addr().(*net.TCPAddr).Port {
		t.Fatalf("port from a :0 listener: %d", port)
	}
}
