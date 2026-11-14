// minute2win-tray is an unprivileged companion, independent of the host lifecycle.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"fyne.io/systray"
)

func main() {
	rawURL := flag.String("url", "http://127.0.0.1:8080", "Existing minute2win console origin")
	hostRoot := flag.String("host-root", "", "Optional host directory containing profiles and logs")
	install := flag.Bool("install-startup", false, "Enable this companion at user sign-in and exit")
	remove := flag.Bool("remove-startup", false, "Disable companion startup and exit")
	check := flag.Bool("check", false, "Print host status and exit without creating an icon")
	flag.Parse()
	origin, err := consoleURL(*rawURL)
	if err != nil {
		log.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	arguments := []string{"-url", origin}
	if *hostRoot != "" {
		*hostRoot, err = filepath.Abs(*hostRoot)
		if err != nil {
			log.Fatal(err)
		}
		arguments = append(arguments, "-host-root", *hostRoot)
	}
	// An AppImage executes from a transient mount/extraction directory.
	if persistent := os.Getenv("M2W_TRAY_APPIMAGE"); persistent != "" {
		if !filepath.IsAbs(persistent) {
			log.Fatal("AppImage startup path must be absolute")
		}
		exe = persistent
		arguments = append([]string{"--tray"}, arguments...)
	}
	if *install && *remove {
		log.Fatal("choose install-startup or remove-startup")
	}
	if *install || *remove {
		if err = setStartup(*install, exe, arguments); err != nil {
			log.Fatal(err)
		}
		return
	}
	client := statusClient()
	if *check {
		status, err := fetchStatus(context.Background(), client, origin)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(status.Label)
		return
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Fatal(err)
	}
	cache = filepath.Join(cache, "minute2win")
	if err = os.MkdirAll(cache, 0700); err != nil {
		log.Fatal(err)
	}
	logPath := filepath.Join(cache, "tray.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	log.SetOutput(f)
	release, err := acquireInstance(cache, origin)
	if err != nil {
		log.Print(err)
		return
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	systray.Run(func() {
		icons := make(map[string][]byte)
		for _, kind := range []string{"offline", "idle", "armed", "deploying", "degraded"} {
			icons[kind] = statusIcon(kind)
		}
		systray.SetIcon(icons["offline"])
		systray.SetTooltip("minute2win | Checking host")
		openConsole := systray.AddMenuItem("Open console", origin)
		stateItem := systray.AddMenuItem("Checking host...", "Server-reported status; not independent installation verification")
		stateItem.Disable()
		address := systray.AddMenuItem(origin, "Configured console address")
		address.Disable()
		systray.AddSeparator()
		profiles := systray.AddMenuItem("Open host profiles", "Open the configured host's profiles directory")
		hostLogs := systray.AddMenuItem("Open host logs", "Open the configured host's logs directory")
		if *hostRoot == "" {
			profiles.Disable()
			hostLogs.Disable()
		}
		trayLog := systray.AddMenuItem("Open tray log", "Companion errors and connection changes")
		startup := systray.AddMenuItemCheckbox("Start at sign-in", "Starts only this icon in your login session", startupEnabled(exe, arguments))
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit tray icon (host keeps running)", "Leaves the server, arms and deployments alone")
		go func() {
			lastKind := ""
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				status, pollErr := fetchStatus(ctx, client, origin)
				if ctx.Err() != nil {
					return
				}
				if pollErr != nil {
					status = trayStatus{"Host unreachable | last state is unknown", "offline"}
				}
				stateItem.SetTitle(status.Label)
				systray.SetTooltip("minute2win | " + status.Label)
				if status.Kind != lastKind {
					systray.SetIcon(icons[status.Kind])
					log.Print(status.Label)
					if pollErr != nil {
						log.Print(pollErr)
					}
					lastKind = status.Kind
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
		go func() {
			launch := func(target string) {
				if err := openNative(target); err != nil {
					log.Print(err)
				}
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-openConsole.ClickedCh:
					launch(origin)
				case <-profiles.ClickedCh:
					launch(filepath.Join(*hostRoot, "profiles"))
				case <-hostLogs.ClickedCh:
					launch(filepath.Join(*hostRoot, "logs"))
				case <-trayLog.ClickedCh:
					launch(logPath)
				case <-startup.ClickedCh:
					err := setStartup(!startupEnabled(exe, arguments), exe, arguments)
					if err != nil {
						log.Print(err)
						startup.SetTitle("Start at sign-in failed (see tray log)")
					} else {
						startup.SetTitle("Start at sign-in")
					}
					if startupEnabled(exe, arguments) {
						startup.Check()
					} else {
						startup.Uncheck()
					}
				case <-quit.ClickedCh:
					cancel()
					systray.Quit()
					return
				}
			}
		}()
	}, cancel)
}
