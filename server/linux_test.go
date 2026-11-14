package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const linuxTestKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKN8Yl2fA0x4h+3Gkr5kHd4QR4KqKO6D9JZkPXbuMx7S synthetic-test-key"

func writeLinuxAsset(t *testing.T, dir, name, body string) linuxAsset {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(body))
	return linuxAsset{File: name, SHA256: hex.EncodeToString(hash[:])}
}

func linuxFixture(t *testing.T, kind string) (*fixture, *linuxProfile) {
	t.Helper()
	f := newFixture(t, "")
	f.s.ImageBase = "" // Linux must not require WinPE or any Windows files.
	f.s.LinuxAssets = t.TempDir()
	p := &linuxProfile{Installer: kind, Suite: "trixie", AuthorizedKeys: []string{linuxTestKey},
		Kernel: writeLinuxAsset(t, f.s.LinuxAssets, "vmlinuz", "test kernel"),
		Initrd: writeLinuxAsset(t, f.s.LinuxAssets, "initrd", "test initrd")}
	if kind == "ubuntu" {
		a := writeLinuxAsset(t, f.s.LinuxAssets, "ubuntu.iso", "test ISO contents")
		p.ISO = &a
	}
	if kind == "proxmox" {
		a := writeLinuxAsset(t, f.s.LinuxAssets, "pve.sh", "#!/bin/sh\nexit 0\n")
		p.FirstBoot, p.Hostname = &a, "pve.lab.internal"
	}
	writeLinuxProfile(t, f, kind, p)
	return f, p
}

func writeLinuxProfile(t *testing.T, f *fixture, kind string, p *linuxProfile) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"schemaVersion": 2, "installer": kind, "linux": p})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.s.ManifestDir, "linux.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func armLinux(t *testing.T, f *fixture) {
	t.Helper()
	code, body := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"linux"}`, "")
	if code != 200 {
		t.Fatalf("Linux arm: %d %s", code, body)
	}
}

func linuxBootBase(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	_, script := f.boot(t, "52:54:00:12:34:56")
	f.s.mu.Lock()
	d := f.s.st.Deployments["52:54:00:12:34:56"]
	key := d.Key
	f.s.mu.Unlock()
	return "/linux/52:54:00:12:34:56/" + key + "/", script
}

func TestLinuxInstallersUseNativeBootAndConsumeOneArm(t *testing.T) {
	for _, kind := range []string{"debian", "ubuntu", "proxmox"} {
		t.Run(kind, func(t *testing.T) {
			f, _ := linuxFixture(t, kind)
			armLinux(t, f)
			base, script := linuxBootBase(t, f)
			for _, want := range []string{"imgfree\n", "kernel --name m2w-kernel " + f.ts.URL + base + "kernel", "initrd=m2w-initrd", "initrd --name m2w-initrd " + f.ts.URL + base + "initrd || goto failed", "boot || goto failed"} {
				if !strings.Contains(script, want) {
					t.Fatalf("missing %q in %s", want, script)
				}
			}
			if strings.Contains(script, "wimboot") || strings.Contains(script, "boot.wim") {
				t.Fatal("Linux boot still uses Windows assets")
			}
			if _, second := f.boot(t, "52:54:00:12:34:56"); !strings.Contains(second, "exit 1") || strings.Contains(second, "kernel --") {
				t.Fatalf("repeat boot deploys: %s", second)
			}
			if kind == "ubuntu" {
				if !strings.Contains(script, "autoinstall url="+f.ts.URL+base+"image.iso ds=nocloud-net;s="+f.ts.URL+base) {
					t.Fatal(script)
				}
				_, seed := f.do(t, "GET", base+"user-data", "", "")
				var doc map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(seed, "#cloud-config\n")), &doc); err != nil {
					t.Fatal(err, seed)
				}
				a := doc["autoinstall"].(map[string]any)
				if a["identity"].(map[string]any)["password"] != "!" || !strings.Contains(seed, "/dev/minute2win-guard-not-run") {
					t.Fatal("seed lacks locked password or fail-closed disk match")
				}
			} else {
				_, seed := f.do(t, "GET", base+"preseed.cfg", "", "")
				for _, want := range []string{"partman/early_command", "sh /tmp/m2w-guard &&", "&& debconf-set partman-auto/disk", "&& debconf-set grub-installer/bootdev", "while :; do sleep 60; done", "passwd/root-login boolean false", "preseed/late_command", "mirror/suite string trixie"} {
					if !strings.Contains(seed, want) {
						t.Fatalf("preseed missing %q", want)
					}
				}
			}
			for path, want := range map[string]string{"kernel": "test kernel", "initrd": "test initrd"} {
				if code, body := f.do(t, "GET", base+path, "", ""); code != 200 || body != want {
					t.Fatalf("asset %s: %d %q", path, code, body)
				}
			}
		})
	}
}

func TestLinuxRejectsBadProfilesAndAssetsBeforeArming(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fixture, *linuxProfile)
	}{
		{"no asset directory", func(f *fixture, p *linuxProfile) { f.s.LinuxAssets = "" }},
		{"missing kernel", func(f *fixture, p *linuxProfile) { p.Kernel.File = "missing" }},
		{"wrong kernel hash", func(f *fixture, p *linuxProfile) { p.Kernel.SHA256 = strings.Repeat("a", 64) }},
		{"zero hash", func(f *fixture, p *linuxProfile) { p.Initrd.SHA256 = strings.Repeat("0", 64) }},
		{"traversal", func(f *fixture, p *linuxProfile) { p.Kernel.File = "../outside" }},
		{"absolute path", func(f *fixture, p *linuxProfile) { p.Kernel.File = "C:\\outside" }},
		{"no keys", func(f *fixture, p *linuxProfile) { p.AuthorizedKeys = nil }},
		{"key newline", func(f *fixture, p *linuxProfile) { p.AuthorizedKeys = []string{linuxTestKey + "\ncommand=evil"} }},
		{"invalid username", func(f *fixture, p *linuxProfile) { p.Username = "root; reboot" }},
		{"root username", func(f *fixture, p *linuxProfile) { p.Username = "root" }},
		{"hostname injection", func(f *fixture, p *linuxProfile) { p.Hostname = "x\nboot" }},
		{"unsupported suite", func(f *fixture, p *linuxProfile) { p.Suite = "testing" }},
		{"small disk", func(f *fixture, p *linuxProfile) { p.MinSizeGB = -1 }},
		{"bad countdown", func(f *fixture, p *linuxProfile) { n := -1; p.CountdownSeconds = &n }},
		{"directory asset", func(f *fixture, p *linuxProfile) {
			os.Mkdir(filepath.Join(f.s.LinuxAssets, "adir"), 0o700)
			p.Kernel.File = "adir"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, p := linuxFixture(t, "debian")
			tc.mutate(f, p)
			writeLinuxProfile(t, f, "debian", p)
			if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"linux"}`, ""); code != 400 {
				t.Fatalf("invalid profile armed: %d", code)
			}
			if len(f.s.st.Armed) != 0 {
				t.Fatal("invalid profile changed arms")
			}
		})
	}
	for _, kind := range []string{"ubuntu", "proxmox", "typo"} {
		t.Run(kind+" required fields", func(t *testing.T) {
			f, p := linuxFixture(t, "debian")
			writeLinuxProfile(t, f, kind, p)
			if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"linux"}`, ""); code != 400 {
				t.Fatal("incomplete or unknown installer accepted")
			}
		})
	}
}

func TestLinuxDeliveryPinsProfileAndRechecksHashes(t *testing.T) {
	f, p := linuxFixture(t, "ubuntu")
	armLinux(t, f)
	// Changes after arming must not silently change disk, identity or installer.
	p.Hostname = "changed.lab.internal"
	writeLinuxProfile(t, f, "debian", p)
	base, script := linuxBootBase(t, f)
	if !strings.Contains(script, "autoinstall") {
		t.Fatal("profile drift changed the installer")
	}
	_, seed := f.do(t, "GET", base+"user-data", "", "")
	if strings.Contains(seed, "changed.lab.internal") {
		t.Fatal("profile drift changed seed")
	}
	for _, file := range []string{"vmlinuz", "initrd", "ubuntu.iso"} {
		if err := os.WriteFile(filepath.Join(f.s.LinuxAssets, file), []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := f.do(t, "GET", base+"kernel", "", ""); code != 503 || strings.Contains(body, "tampered") {
		t.Fatalf("changed kernel delivered: %d %s", code, body)
	}
	if f.s.st.Deployments["52:54:00:12:34:56"].Status != "failed" {
		t.Fatal("asset failure was not reported on the board")
	}
	for _, route := range []string{"initrd", "image.iso"} {
		if code, _ := f.do(t, "GET", base+route, "", ""); code != 404 {
			t.Fatal("failed deployment still serves content")
		}
	}
}

func TestLinuxDeliveryCapabilityAndLifecycle(t *testing.T) {
	f, _ := linuxFixture(t, "ubuntu")
	if code, _ := f.do(t, "GET", "/linux/52:54:00:12:34:56/fake/user-data", "", ""); code != 404 {
		t.Fatal(code)
	}
	armLinux(t, f)
	base, _ := linuxBootBase(t, f)
	key := strings.Split(base, "/")[3]
	if code, _ := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", ""); code != 404 {
		t.Fatal("Windows manifest route leaks the Linux capability")
	}
	for _, route := range []string{"/linux/52:54:00:12:34:56/wrong/user-data", base + "state.json", base + "preseed.cfg", base + "firstboot.sh"} {
		if code, _ := f.do(t, "GET", route, "", ""); code != 404 {
			t.Fatalf("%s: %d", route, code)
		}
	}
	_, state := f.do(t, "GET", "/api/state", "", "")
	if strings.Contains(state, key) || strings.Contains(state, linuxTestKey) {
		t.Fatal("state exposes Linux credentials/configuration")
	}
	if code, _ := f.do(t, "POST", "/api/report", linuxReportJSON("52:54:00:12:34:56", key, "guard", "running", "checking"), ""); code != 204 {
		t.Fatal(code)
	}
	for _, file := range []string{"user-data", "meta-data", "guard.sh", "finish.sh", "firstboot-wrapper.sh"} {
		if code, _ := f.do(t, "GET", base+file, "", ""); code != 200 {
			t.Fatalf("delivery closed after first report: %s %d", file, code)
		}
	}
	f.now = f.now.Add(3*time.Hour + time.Second)
	if code, _ := f.do(t, "GET", base+"kernel", "", ""); code != 404 {
		t.Fatal("expired delivery accepted")
	}
	f.now = f.now.Add(-3*time.Hour - time.Second)
	if code, _ := f.do(t, "POST", "/api/report", linuxReportJSON("52:54:00:12:34:56", key, "complete", "done", "booted"), ""); code != 204 {
		t.Fatal(code)
	}
	if code, _ := f.do(t, "GET", base+"user-data", "", ""); code != 404 {
		t.Fatal("finished deployment still serves boot credentials")
	}
}

func TestLinuxRangeDownloadsAndRestart(t *testing.T) {
	f, _ := linuxFixture(t, "ubuntu")
	armLinux(t, f)
	base, _ := linuxBootBase(t, f)
	r := httptest.NewRequest("GET", base+"image.iso", nil)
	r.Header.Set("Range", "bytes=5-7")
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "ISO" {
		t.Fatalf("range: %d %s", w.Code, w.Body.String())
	}
	if err := f.s.Load(); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.do(t, "GET", base+"user-data", "", ""); code != 404 {
		t.Fatal("restart restored boot credentials")
	}
	if code, _ := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", ""); code != 404 {
		t.Fatal("restart reopened Linux credential delivery through Windows manifest")
	}
}

func TestLinuxSymlinksCannotEscapeAssetRoot(t *testing.T) {
	f, p := linuxFixture(t, "debian")
	dir := t.TempDir()
	out := writeLinuxAsset(t, dir, "outside", "outside secret")
	link := filepath.Join(f.s.LinuxAssets, "escape")
	if err := os.Symlink(filepath.Join(dir, out.File), link); err != nil {
		t.Skip("symlink unavailable:", err)
	}
	p.Kernel = linuxAsset{File: "escape", SHA256: out.SHA256}
	writeLinuxProfile(t, f, "debian", p)
	if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"linux"}`, ""); code != 400 {
		t.Fatal("symlink escape armed")
	}
}

func testShell(t *testing.T) string {
	t.Helper()
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if runtime.GOOS == "windows" {
		sh := filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	t.Skip("POSIX shell not installed")
	return ""
}

func TestGeneratedLinuxShellSyntax(t *testing.T) {
	sh := testShell(t)
	check := func(name, body string) {
		t.Helper()
		cmd := exec.Command(sh, "-n")
		cmd.Stdin = strings.NewReader(body)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", name, err, b)
		}
	}
	for _, kind := range []string{"debian", "ubuntu", "proxmox"} {
		f, _ := linuxFixture(t, kind)
		armLinux(t, f)
		base, _ := linuxBootBase(t, f)
		for _, file := range []string{"guard.sh", "finish.sh", "firstboot-wrapper.sh"} {
			_, body := f.do(t, "GET", base+file, "", "")
			check(kind+"/"+file, body)
		}
		if kind != "ubuntu" {
			_, seed := f.do(t, "GET", base+"preseed.cfg", "", "")
			for _, line := range strings.Split(seed, "\n") {
				for _, prefix := range []string{"d-i partman/early_command string ", "d-i preseed/late_command string "} {
					if command, ok := strings.CutPrefix(line, prefix); ok {
						check(kind+" hook", command)
					}
				}
			}
		}
	}
	template, err := os.ReadFile(filepath.Join("..", "templates", "linux", "proxmox-firstboot.sh"))
	if err != nil {
		t.Fatal(err)
	}
	check("Proxmox template syntax", string(template))
}

func TestLinuxFirstBootRebootFailureAndCompletion(t *testing.T) {
	sh := testShell(t)
	body, _ := linuxScripts.ReadFile("linux/firstboot-wrapper.sh")
	for _, scenario := range []string{"success", "failure", "reboot then success"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			root := shellPath(dir)
			custom := "exit 0\n"
			if scenario == "failure" {
				custom = "exit 1\n"
			}
			if scenario == "reboot then success" {
				custom = "if [ ! -f " + shellQuote(root+"/stage") + " ]; then touch " + shellQuote(root+"/stage") + "; exit 75; fi\nexit 0\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "custom-firstboot.sh"), []byte(custom), 0o600); err != nil {
				t.Fatal(err)
			}
			prefix := "M2W_STATE=" + shellQuote(root) + "\nM2W_LOG=" + shellQuote(root+"/log") + "\nM2W_WRAPPER=" + shellQuote(root+"/wrapper") + "\nM2W_FIRSTBOOT=true\n" +
				"report() { printf '%s %s\\n' \"$1\" \"$2\" >> " + shellQuote(root+"/reports") + "; }\n" +
				"systemctl() { printf '%s\\n' \"$1\" >> " + shellQuote(root+"/systemctl") + "; }\nsync() { :; }\n"
			run := func() error {
				cmd := exec.Command(sh, "-s")
				cmd.Stdin = strings.NewReader(prefix + string(body))
				b, err := cmd.CombinedOutput()
				if len(b) != 0 {
					t.Logf("wrapper: %s", b)
				}
				return err
			}
			err := run()
			reports, _ := os.ReadFile(filepath.Join(dir, "reports"))
			_, completeErr := os.Stat(filepath.Join(dir, "complete"))
			if scenario == "failure" {
				if err == nil || completeErr == nil || !strings.Contains(string(reports), "firstboot failed") {
					t.Fatal("failure marked complete", err, string(reports))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "reboot then success" {
				if completeErr == nil || strings.Contains(string(reports), "complete done") || !strings.Contains(string(reports), "reboot running") {
					t.Fatal("premature completion on reboot", string(reports))
				}
				calls, _ := os.ReadFile(filepath.Join(dir, "systemctl"))
				if string(calls) != "reboot\n" {
					t.Fatalf("reboot request: %q", calls)
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
			}
			reports, _ = os.ReadFile(filepath.Join(dir, "reports"))
			if strings.Count(string(reports), "complete done") != 1 {
				t.Fatal("missing completion", string(reports))
			}
			if _, err := os.Stat(filepath.Join(dir, "custom-firstboot.sh")); !os.IsNotExist(err) {
				t.Fatal("custom script not removed", err)
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "reports"))
			if string(after) != string(reports) {
				t.Fatal("completed setup ran again")
			}
		})
	}
}

func shellPath(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		path = "/" + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func TestLinuxDiskSelectionPolicy(t *testing.T) {
	sh := testShell(t)
	helper, _ := linuxScripts.ReadFile("linux/select-disk.sh")
	for _, scenario := range []string{"internal", "removable", "usb", "too small", "mounted", "swap", "holders", "partition holders", "mmc fallback", "missing mount table"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range []string{"sys", "dev", "proc"} {
				os.Mkdir(filepath.Join(dir, d), 0o700)
			}
			for _, table := range []string{"mounts", "swaps"} {
				os.WriteFile(filepath.Join(dir, "proc", table), nil, 0o600)
			}
			add := func(name string, removable, gb int) {
				path := filepath.Join(dir, "sys", name)
				os.MkdirAll(filepath.Join(path, "holders"), 0o700)
				os.WriteFile(filepath.Join(path, "removable"), []byte(fmt.Sprint(removable)), 0o600)
				os.WriteFile(filepath.Join(path, "size"), []byte(fmt.Sprint(int64(gb)*2097152)), 0o600)
				os.WriteFile(filepath.Join(dir, "dev", name), nil, 0o600)
			}
			add("sda", 0, 128)
			want := shellPath(filepath.Join(dir, "dev", "sda"))
			switch scenario {
			case "removable":
				add("sda", 1, 128)
				want = ""
			case "too small":
				add("sda", 0, 32)
				want = ""
			case "mounted", "swap":
				table := "mounts"
				if scenario == "swap" {
					table = "swaps"
				}
				os.WriteFile(filepath.Join(dir, "proc", table), []byte(want+"1 /target ext4 rw 0 0\n"), 0o600)
				want = ""
			case "holders":
				os.WriteFile(filepath.Join(dir, "sys", "sda", "holders", "dm-0"), nil, 0o600)
				want = ""
			case "partition holders":
				os.MkdirAll(filepath.Join(dir, "sys", "sda", "sda1", "holders"), 0o700)
				os.WriteFile(filepath.Join(dir, "sys", "sda", "sda1", "holders", "dm-0"), nil, 0o600)
				os.MkdirAll(filepath.Join(dir, "dev", "mapper"), 0o700)
				os.WriteFile(filepath.Join(dir, "dev", "mapper", "data"), nil, 0o600)
				os.WriteFile(filepath.Join(dir, "proc", "mounts"), []byte(shellPath(filepath.Join(dir, "dev", "mapper", "data"))+" /target ext4 rw 0 0\n"), 0o600)
				want = ""
			case "usb":
				// Mock only readlink's sysfs ancestry, avoiding Windows symlink privileges.
				want = ""
			case "mmc fallback":
				add("sda", 1, 128)
				add("mmcblk0", 0, 128)
				want = shellPath(filepath.Join(dir, "dev", "mmcblk0"))
			case "missing mount table":
				os.Remove(filepath.Join(dir, "proc", "mounts"))
				want = ""
			}
			// Fixture files stand in for block devices; all selection logic is the
			// actual read-only helper. No host disk can be selected or modified.
			script := string(helper) + "\nis_block_disk() { [ -f \"$1\" ]; }\n"
			if scenario == "usb" {
				script += "readlink() { printf '/sys/devices/usb1/1-1/block/sda\\n'; }\n"
			}
			script += "select_disk " + shellQuote(shellPath(filepath.Join(dir, "sys"))) + " " + shellQuote(shellPath(filepath.Join(dir, "dev"))) + " " + shellQuote(shellPath(filepath.Join(dir, "proc"))) + " 64\n"
			cmd := exec.Command(sh, "-s")
			cmd.Stdin = strings.NewReader(script)
			b, err := cmd.CombinedOutput()
			if (want != "" && err != nil) || (want == "" && err == nil) || strings.TrimSpace(string(b)) != want {
				t.Fatalf("selection=%q err=%v want=%q", b, err, want)
			}
		})
	}
}
