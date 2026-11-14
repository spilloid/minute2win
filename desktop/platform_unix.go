//go:build linux || darwin

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

func startupContent(exe string, args []string) []byte {
	if runtime.GOOS == "darwin" {
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>io.github.spilloid.minute2win.tray</string><key>ProgramArguments</key><array>`)
		for _, arg := range append([]string{exe}, args...) {
			b.WriteString("<string>")
			_ = xml.EscapeText(&b, []byte(arg))
			b.WriteString("</string>")
		}
		b.WriteString("</array><key>RunAtLoad</key><true/></dict></plist>\n")
		return b.Bytes()
	}
	quote := func(s string) string {
		s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`, "%", "%%").Replace(s)
		// Desktop-entry string escaping is decoded before Exec argument quoting.
		s = strings.ReplaceAll(s, `\`, `\\`)
		return `"` + s + `"`
	}
	parts := []string{quote(exe)}
	for _, arg := range args {
		parts = append(parts, quote(arg))
	}
	return []byte("[Desktop Entry]\nType=Application\nName=minute2win tray\nComment=Status icon for an existing minute2win host\nExec=" + strings.Join(parts, " ") + "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n")
}

func startupPath() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		return filepath.Join(home, "Library", "LaunchAgents", "io.github.spilloid.minute2win.tray.plist"), err
	}
	config, err := os.UserConfigDir()
	return filepath.Join(config, "autostart", "minute2win-tray.desktop"), err
}

func startupEnabled(exe string, args []string) bool {
	path, err := startupPath()
	if err != nil {
		return false
	}
	content, err := os.ReadFile(path)
	return err == nil && bytes.Equal(content, startupContent(exe, args))
}

func setStartup(enabled bool, exe string, args []string) error {
	if enabled && runtime.GOOS == "darwin" && strings.Contains(exe, "/AppTranslocation/") {
		return fmt.Errorf("move minute2win Tray.app to a permanent folder and relaunch it before enabling sign-in startup")
	}
	path, err := startupPath()
	if err != nil {
		return err
	}
	if !enabled {
		err = os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if strings.ContainsAny(exe+strings.Join(args, ""), "\r\n\x00") {
		return fmt.Errorf("startup arguments contain control characters")
	}
	if runtime.GOOS == "linux" && strings.Contains(exe, "=") {
		return fmt.Errorf("desktop startup executable path cannot contain =")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, startupContent(exe, args), 0600)
}

func openNative(target string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, target).Run()
}

func acquireInstance(cache, origin string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(cache, fmt.Sprintf("tray-%x.lock", sha256.Sum256([]byte(origin)))), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("tray already running: %w", err)
	}
	return func() { f.Close() }, nil
}
