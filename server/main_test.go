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
