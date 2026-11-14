package main

import (
	"strings"
	"testing"
)

func TestStartupCommand(t *testing.T) {
	got := startupCommand(`C:\Program Files\minute2win\minute2win-tray.exe`, []string{"-url", "http://127.0.0.1:8080", "-host-root", `C:\a b\`})
	if !strings.HasPrefix(got, `"C:\Program Files\minute2win\minute2win-tray.exe" -url http://127.0.0.1:8080 -host-root "C:\a b\\"`) {
		t.Fatal(got)
	}
}

func TestInstanceMutex(t *testing.T) {
	release, err := acquireInstance("", "http://127.0.0.1:18769")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if release2, err := acquireInstance("", "http://127.0.0.1:18769"); err == nil {
		release2()
		t.Fatal("duplicate accepted")
	}
}
