package main

import (
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
