package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bazziteFixture(t *testing.T) (*fixture, *linuxProfile) {
	t.Helper()
	f, p := linuxFixture(t, "debian")
	p.Installer, p.Suite = "bazzite", ""
	p.ContainerImage = "ghcr.io/ublue-os/bazzite@sha256:" + strings.Repeat("a", 64)
	a := writeLinuxAsset(t, f.s.LinuxAssets, "fedora-stage2.img", "test Anaconda stage2")
	p.Stage2, p.DesktopAutologin = &a, true
	writeLinuxProfile(t, f, "bazzite", p)
	return f, p
}

func TestBazziteNativeBootAndGuardedKickstart(t *testing.T) {
	f, p := bazziteFixture(t)
	armLinux(t, f)
	base, boot := linuxBootBase(t, f)
	for _, want := range []string{"inst.cmdline", "inst.stage2=" + f.ts.URL + base + " inst.ks=", "inst.ks=" + f.ts.URL + base + "kickstart.ks", "rd.neednet=1"} {
		if !strings.Contains(boot, want) {
			t.Fatalf("missing %q in %s", want, boot)
		}
	}
	if code, tree := f.do(t, "GET", base+".treeinfo", "", ""); code != 200 || tree != "[stage2]\nmainimage=stage2.img\n" {
		t.Fatalf("Anaconda stage2 tree metadata: %d %q", code, tree)
	}
	if code, seed := f.do(t, "GET", base+"kickstart.ks", "", ""); code != 200 {
		t.Fatalf("kickstart: %d %s", code, seed)
	} else {
		for _, want := range []string{"%pre --interpreter=/bin/sh --erroronfail", "%post --interpreter=/bin/sh --erroronfail", "%include /tmp/minute2win-disk.ks", "sh /tmp/minute2win-guard", "[ -b \"$selected\" ]", "ignoredisk --only-use=%s", "clearpart --all --initlabel --drives=%s", "ostreecontainer --url=" + p.ContainerImage, "%onerror", "rootpw --lock", "xconfig --startxonboot"} {
			if !strings.Contains(seed, want) {
				t.Fatalf("missing %q in %s", want, seed)
			}
		}
	}
	for _, name := range []string{"preseed.cfg", "user-data", "finish.sh", "image.iso"} {
		if code, _ := f.do(t, "GET", base+name, "", ""); code != 404 {
			t.Fatalf("Bazzite exposes incompatible %s: %d", name, code)
		}
	}
	if _, second := f.boot(t, "52:54:00:12:34:56"); strings.Contains(second, "kernel --") {
		t.Fatal("Bazzite's arm was not consumed")
	}
	_, finish := f.do(t, "GET", base+"bazzite-finish.sh", "", "")
	for _, want := range []string{"bootc switch --mutate-in-place --enforce-container-sigpolicy", "usermod -aG wheel", "M2W_DESKTOP_AUTOLOGIN=true", "M2W_WRAPPER=/var/usrlocal/sbin/minute2win-firstboot", "PasswordAuthentication no"} {
		if !strings.Contains(finish, want) {
			t.Fatalf("finish missing %q", want)
		}
	}
}

func TestBazziteRefusesIncompleteOrUnpinnedImagesBeforeArming(t *testing.T) {
	cases := []struct {
		name string
		edit func(*linuxProfile)
	}{
		{"no-stage2", func(p *linuxProfile) { p.Stage2 = nil }},
		{"mutable-tag", func(p *linuxProfile) { p.ContainerImage = "ghcr.io/ublue-os/bazzite:stable" }},
		{"wrong-publisher", func(p *linuxProfile) {
			p.ContainerImage = strings.Replace(p.ContainerImage, "ublue-os", "someone-else", 1)
		}},
		{"shell-injection", func(p *linuxProfile) { p.ContainerImage += "\n%post\nreboot" }},
		{"zero-digest", func(p *linuxProfile) { p.ContainerImage = "ghcr.io/ublue-os/bazzite@sha256:" + strings.Repeat("0", 64) }},
		{"debian-suite", func(p *linuxProfile) { p.Suite = "trixie" }},
		{"bad-stage2-pin", func(p *linuxProfile) { p.Stage2.SHA256 = strings.Repeat("b", 64) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, p := bazziteFixture(t)
			tc.edit(p)
			writeLinuxProfile(t, f, "bazzite", p)
			if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"linux"}`, ""); code == 200 {
				t.Fatal("unsafe Bazzite profile was armed")
			}
			if len(f.s.st.Armed) != 0 {
				t.Fatal("failed validation left an arm")
			}
		})
	}
}

func TestBazziteStage2IsCapabilityGatedAndTamperStopsDelivery(t *testing.T) {
	f, p := bazziteFixture(t)
	armLinux(t, f)
	base, _ := linuxBootBase(t, f)
	if code, _ := f.do(t, "GET", "/linux/52:54:00:12:34:56/not-the-key/stage2.img", "", ""); code != 404 {
		t.Fatalf("unauthenticated stage2: %d", code)
	}
	if code, body := f.do(t, "GET", base+"stage2.img", "", ""); code != 200 || body != "test Anaconda stage2" {
		t.Fatalf("valid stage2: %d %s", code, body)
	}
	if err := os.WriteFile(filepath.Join(f.s.LinuxAssets, p.Stage2.File), []byte("changed stage2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.do(t, "GET", base+"stage2.img", "", ""); code != 503 {
		t.Fatalf("changed stage2 was delivered: %d", code)
	}
	if code, _ := f.do(t, "GET", base+"kickstart.ks", "", ""); code != 404 {
		t.Fatalf("failed deployment still serves installation config: %d", code)
	}
}
