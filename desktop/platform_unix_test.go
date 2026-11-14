//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/xml"
	"io"
	"runtime"
	"strings"
	"testing"
)

func TestStartupContent(t *testing.T) {
	content := startupContent(`/home/a b/$cash%/tray`, []string{"-url", "https://example.com", "-host-root", `/tmp/a"b`})
	if runtime.GOOS == "darwin" {
		dec := xml.NewDecoder(bytes.NewReader(content))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Contains(content, []byte("<key>RunAtLoad</key><true/>")) {
			t.Fatal(string(content))
		}
	} else {
		if !strings.Contains(string(content), `Exec="/home/a b/\\$cash%%/tray" "-url" "https://example.com" "-host-root" "/tmp/a\\"b"`) {
			t.Fatal(string(content))
		}
	}
}

func TestInstanceLock(t *testing.T) {
	dir := t.TempDir()
	release, err := acquireInstance(dir, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	if release2, err := acquireInstance(dir, "http://127.0.0.1:8080"); err == nil {
		release2()
		t.Error("duplicate accepted")
	}
	release()
	release, err = acquireInstance(dir, "http://127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestMacTranslocationCannotRegisterStartup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific startup guard")
	}
	err := setStartup(true, "/private/var/folders/test/AppTranslocation/uuid/d/minute2win Tray.app/Contents/MacOS/minute2win-tray", nil)
	if err == nil || !strings.Contains(err.Error(), "permanent folder and relaunch") {
		t.Fatalf("unexpected translocation result: %v", err)
	}
}
