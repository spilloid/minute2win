package main

import (
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func startupCommand(exe string, args []string) string {
	parts := []string{syscall.EscapeArg(exe)}
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func startupEnabled(exe string, args []string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	value, _, err := k.GetStringValue("minute2win-tray")
	return err == nil && value == startupCommand(exe, args)
}

func setStartup(enabled bool, exe string, args []string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if enabled {
		return k.SetStringValue("minute2win-tray", startupCommand(exe, args))
	}
	err = k.DeleteValue("minute2win-tray")
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func openNative(target string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func acquireInstance(_ string, origin string) (func(), error) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\minute2win-tray-%x`, sha256.Sum256([]byte(origin))))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil, fmt.Errorf("tray already running or mutex unavailable: %w", err)
	}
	return func() { windows.CloseHandle(h) }, nil
}
