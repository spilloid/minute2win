package main

// Linux uses the distro's installer rather than the WinPE imaging engine. Only
// explicitly configured local files are served; this is not a general file host
// or an HTTP proxy. Every asset is pinned and checked before arming and delivery.
import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
)

//go:embed linux/*.sh
var linuxScripts embed.FS

type linuxAsset struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type linuxProfile struct {
	Installer        string      `json:"-"`
	Kernel           linuxAsset  `json:"kernel"`
	Initrd           linuxAsset  `json:"initrd"`
	ISO              *linuxAsset `json:"iso,omitempty"`
	Stage2           *linuxAsset `json:"stage2,omitempty"`
	ContainerImage   string      `json:"containerImage,omitempty"`
	DesktopAutologin bool        `json:"desktopAutologin,omitempty"`
	FirstBoot        *linuxAsset `json:"firstBoot,omitempty"`
	Suite            string      `json:"suite,omitempty"`
	Hostname         string      `json:"hostname,omitempty"`
	Username         string      `json:"username,omitempty"`
	AuthorizedKeys   []string    `json:"authorizedKeys"`
	MinSizeGB        int         `json:"minSizeGB,omitempty"`
	CountdownSeconds *int        `json:"countdownSeconds,omitempty"`
}

var (
	linuxFileRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	linuxUserRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	linuxHostRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	linuxOriginRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.:[\]-]+$`)
	bazziteImageRe = regexp.MustCompile(`^ghcr\.io/ublue-os/bazzite@sha256:[a-f0-9]{64}$`)
)

func (p *linuxProfile) validate() error {
	if p.MinSizeGB == 0 {
		p.MinSizeGB = 64
	}
	if p.MinSizeGB < 8 || p.MinSizeGB > 1048576 {
		return fmt.Errorf("linux.minSizeGB must be between 8 and 1048576")
	}
	if p.CountdownSeconds == nil {
		n := 10
		p.CountdownSeconds = &n
	}
	if *p.CountdownSeconds < 0 || *p.CountdownSeconds > 300 {
		return fmt.Errorf("linux.countdownSeconds must be between 0 and 300")
	}
	if p.Username == "" {
		p.Username = "m2w"
	}
	if !linuxUserRe.MatchString(p.Username) || p.Username == "root" {
		return fmt.Errorf("linux.username must be a non-root Linux account name")
	}
	if p.Hostname != "" && (len(p.Hostname) > 63 || !linuxHostRe.MatchString(p.Hostname)) {
		return fmt.Errorf("linux.hostname must be a lowercase DNS hostname of at most 63 characters")
	}
	if len(p.AuthorizedKeys) == 0 || len(p.AuthorizedKeys) > 32 {
		return fmt.Errorf("linux.authorizedKeys requires 1 to 32 public SSH keys; password login is disabled")
	}
	for _, key := range p.AuthorizedKeys {
		fields := strings.Fields(key)
		if len(key) > 16384 || strings.ContainsAny(key, "\r\n\x00") || len(fields) < 2 {
			return fmt.Errorf("linux.authorizedKeys contains an invalid public SSH key")
		}
		if fields[0] != "ssh-ed25519" && fields[0] != "ssh-rsa" && !strings.HasPrefix(fields[0], "ecdsa-sha2-nistp") {
			return fmt.Errorf("linux.authorizedKeys requires plain public keys without authorized_keys options")
		}
		if b, err := base64.StdEncoding.DecodeString(fields[1]); err != nil || len(b) < 32 {
			return fmt.Errorf("linux.authorizedKeys contains invalid public key data")
		}
	}
	if p.Installer != "bazzite" && (p.Stage2 != nil || p.ContainerImage != "" || p.DesktopAutologin) {
		return fmt.Errorf("linux.stage2, containerImage and desktopAutologin are only used by Bazzite")
	}
	if p.Installer == "bazzite" {
		if p.Stage2 == nil || !bazziteImageRe.MatchString(p.ContainerImage) || strings.HasSuffix(p.ContainerImage, strings.Repeat("0", 64)) {
			return fmt.Errorf("Bazzite requires linux.stage2 and the AMD/Intel KDE image ghcr.io/ublue-os/bazzite pinned by @sha256 digest")
		}
		if p.ISO != nil || p.Suite != "" {
			return fmt.Errorf("Bazzite uses Fedora Anaconda stage2, not linux.iso or Debian linux.suite")
		}
	} else if p.Installer == "ubuntu" {
		if p.ISO == nil {
			return fmt.Errorf("Ubuntu requires linux.iso, matching its kernel and initrd")
		}
	} else {
		if p.ISO != nil {
			return fmt.Errorf("linux.iso is only used by Ubuntu")
		}
		if p.Suite != "bookworm" && p.Suite != "trixie" {
			return fmt.Errorf("linux.suite must be bookworm or trixie for Debian netboot")
		}
	}
	if p.Installer == "proxmox" && (p.Suite != "trixie" || p.FirstBoot == nil || !strings.Contains(p.Hostname, ".")) {
		return fmt.Errorf("Proxmox requires Debian trixie, an FQDN in linux.hostname, and a pinned linux.firstBoot script")
	}
	for _, a := range p.assets() {
		if !linuxFileRe.MatchString(a.File) || strings.Contains(a.File, "..") || strings.HasSuffix(a.File, ".") {
			return fmt.Errorf("Linux asset file must be a plain filename inside -linux-assets")
		}
		if !sha256Re.MatchString(a.SHA256) || strings.Trim(a.SHA256, "0") == "" {
			return fmt.Errorf("Linux asset %s needs its real SHA-256 (64 hex digits, not zeros)", a.File)
		}
	}
	return nil
}

func (p *linuxProfile) assets() []linuxAsset {
	a := []linuxAsset{p.Kernel, p.Initrd}
	if p.ISO != nil {
		a = append(a, *p.ISO)
	}
	if p.Stage2 != nil {
		a = append(a, *p.Stage2)
	}
	if p.FirstBoot != nil {
		a = append(a, *p.FirstBoot)
	}
	return a
}

func (s *Server) verifiedLinuxAsset(a linuxAsset) (*os.File, error) {
	if s.LinuxAssets == "" {
		return nil, fmt.Errorf("Linux profiles require -linux-assets pointing to your local boot files")
	}
	root, err := os.OpenRoot(s.LinuxAssets)
	if err != nil {
		return nil, fmt.Errorf("open Linux assets: %w", err)
	}
	defer root.Close()
	info, err := root.Stat(a.File)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Linux asset %s must be an accessible regular file", a.File)
	}
	f, err := root.Open(a.File)
	if err != nil {
		return nil, fmt.Errorf("open Linux asset %s: %w", a.File, err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("Linux asset %s must be a regular file", a.File)
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	if err != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		f.Close()
		return nil, fmt.Errorf("Linux asset %s failed SHA-256 verification", a.File)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (s *Server) checkLinuxAssets(p *linuxProfile) error {
	for _, a := range p.assets() {
		f, err := s.verifiedLinuxAsset(a)
		if err != nil {
			return err
		}
		f.Close()
	}
	return nil
}

func (p *linuxProfile) hostname(mac string) string {
	if p.Hostname != "" {
		return p.Hostname
	}
	return "m2w-" + strings.ReplaceAll(mac, ":", "")
}

func linuxBase(origin, mac, key string) string {
	return origin + "/linux/" + mac + "/" + key + "/"
}

func linuxBootScript(origin, mac, key string, p *linuxProfile) string {
	base := linuxBase(origin, mac, key)
	args := "auto=true priority=critical netcfg/choose_interface=auto hostname=" + p.hostname(mac) + " url=" + base + "preseed.cfg"
	if p.Installer == "ubuntu" {
		args = "ip=dhcp boot=casper autoinstall url=" + base + "image.iso ds=nocloud-net;s=" + base
	} else if p.Installer == "bazzite" {
		args = "ip=dhcp rd.neednet=1 inst.cmdline inst.stage2=" + base + " inst.ks=" + base + "kickstart.ks"
	}
	// Every command fails closed: iPXE must never fall through to booting a
	// stale kernel/initrd left by an earlier script when a transfer fails.
	return fmt.Sprintf("#!ipxe\nimgfree\nkernel --name m2w-kernel %skernel initrd=m2w-initrd %s --- || goto failed\ninitrd --name m2w-initrd %sinitrd || goto failed\nboot || goto failed\n:failed\necho minute2win: Linux boot failed; re-arm to retry\nexit 1\n", base, args, base)
}

// A per-deployment capability gates all Linux content. Unlike the WinPE
// manifest, seeds remain available after the first progress report, for the
// lifetime of an active deployment. No unlisted file can be fetched.
func (s *Server) linuxFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mac, ok := normMAC(r.PathValue("mac"))
	s.mu.Lock()
	d := s.st.Deployments[mac]
	if !ok || d == nil || d.Linux == nil || s.degraded ||
		(d.Status != "booting" && d.Status != "running") || s.Now().Sub(d.BootedAt) > s.ReportTTL ||
		!tokenEqual(r.PathValue("key"), d.Key) {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	p, key := d.Linux, d.Key
	s.mu.Unlock()
	if !linuxOriginRe.MatchString(origin(r)) {
		http.Error(w, "invalid boot origin", http.StatusBadRequest)
		return
	}
	var asset *linuxAsset
	switch r.PathValue("file") {
	case "kernel":
		asset = &p.Kernel
	case "initrd":
		asset = &p.Initrd
	case "image.iso":
		asset = p.ISO
	case "stage2.img":
		asset = p.Stage2
	case "firstboot.sh":
		asset = p.FirstBoot
	default:
		if body, found := linuxConfig(origin(r), mac, key, p, r.PathValue("file")); found {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, body)
			return
		}
	}
	if asset == nil {
		http.NotFound(w, r)
		return
	}
	f, err := s.verifiedLinuxAsset(*asset)
	if err != nil {
		log.Printf("Linux asset for %s: %v", mac, err)
		s.mu.Lock()
		current := s.st.Deployments[mac]
		if current != nil && tokenEqual(current.Key, key) && (current.Status == "booting" || current.Status == "running") {
			current.UpdatedAt, current.Status, current.Phase = s.Now(), "failed", "boot-assets"
			current.Message = "Linux asset unavailable or SHA-256 mismatch; re-arm after fixing assets"
			_ = s.save()
		}
		s.mu.Unlock()
		http.Error(w, "Linux asset unavailable or SHA-256 mismatch; deployment stopped", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	// The same verified descriptor serves full and ranged downloads. Never
	// publish a partial response before hashing the whole asset.
	http.ServeContent(w, r, asset.File, s.Now(), f)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func linuxConfig(origin, mac, key string, p *linuxProfile, file string) (string, bool) {
	base := linuxBase(origin, mac, key)
	switch file {
	case ".treeinfo":
		if p.Installer == "bazzite" {
			return "[stage2]\nmainimage=stage2.img\n", true
		}
	case "preseed.cfg":
		if p.Installer == "debian" || p.Installer == "proxmox" {
			return debianPreseed(base, mac, p), true
		}
	case "kickstart.ks":
		if p.Installer == "bazzite" {
			return bazziteKickstart(base, origin, mac, key, p), true
		}
	case "user-data":
		if p.Installer == "ubuntu" {
			return ubuntuSeed(base, origin, mac, key, p), true
		}
	case "meta-data":
		if p.Installer == "ubuntu" {
			return "instance-id: m2w-" + key + "\nlocal-hostname: " + p.hostname(mac) + "\n", true
		}
	case "guard.sh", "finish.sh", "firstboot-wrapper.sh", "bazzite-finish.sh":
		if file == "bazzite-finish.sh" && p.Installer != "bazzite" {
			return "", false
		}
		if file == "finish.sh" && p.Installer == "bazzite" {
			return "", false
		}
		body, _ := linuxScripts.ReadFile("linux/" + file)
		common, _ := linuxScripts.ReadFile("linux/common.sh")
		selector, _ := linuxScripts.ReadFile("linux/select-disk.sh")
		vars := "#!/bin/sh\nM2W_BASE=" + shellQuote(base) + "\nM2W_REPORT=" + shellQuote(origin+"/api/report") +
			"\nM2W_MAC=" + shellQuote(mac) + "\nM2W_KEY=" + shellQuote(key) +
			"\nM2W_USERNAME=" + shellQuote(p.Username) +
			"\nM2W_KEYS=" + shellQuote(strings.Join(p.AuthorizedKeys, "\n")+"\n") +
			"\nM2W_INSTALLER=" + shellQuote(p.Installer) +
			"\nM2W_STATE=/var/lib/minute2win\nM2W_LOG=/var/log/minute2win-firstboot.log\nM2W_WRAPPER=/usr/local/sbin/minute2win-firstboot" +
			fmt.Sprintf("\nM2W_MIN_GB=%d\nM2W_COUNTDOWN=%d\nM2W_FIRSTBOOT=%t\n", p.MinSizeGB, *p.CountdownSeconds, p.FirstBoot != nil)
		if p.Installer == "bazzite" {
			vars += "M2W_WRAPPER=/var/usrlocal/sbin/minute2win-firstboot\nM2W_BAZZITE_TRACKING_IMAGE=ghcr.io/ublue-os/bazzite:stable\n" +
				fmt.Sprintf("M2W_DESKTOP_AUTOLOGIN=%t\n", p.DesktopAutologin)
		}
		return vars + string(common) + string(selector) + string(body), true
	}
	return "", false
}

func debianPreseed(base, mac string, p *linuxProfile) string {
	// A failed guard must STOP partman, not merely return a failing early
	// command which some d-i versions could ignore. Late-command failure is
	// also held rather than rebooting a partially configured installation.
	guard := "if wget -q -O /tmp/m2w-guard " + shellQuote(base+"guard.sh") + " && sh /tmp/m2w-guard && " +
		"disk=$(cat /tmp/m2w-disk) && [ -b \"$disk\" ] && debconf-set partman-auto/disk \"$disk\" && debconf-set grub-installer/bootdev \"$disk\"; then :; " +
		"else echo 'minute2win: disk guard failed; installation stopped' >/dev/console; while :; do sleep 60; done; fi"
	finish := "if wget -q -O /tmp/m2w-finish " + shellQuote(base+"finish.sh") + " && sh /tmp/m2w-finish; then :; " +
		"else echo 'minute2win: setup failed; installation stopped' >/dev/console; while :; do sleep 60; done; fi"
	return fmt.Sprintf(`#_preseed_V1
d-i debian-installer/locale string en_US.UTF-8
d-i keyboard-configuration/xkb-keymap select us
d-i netcfg/choose_interface select auto
d-i netcfg/get_hostname string %s
d-i netcfg/hostname string %s
d-i netcfg/get_domain string local
d-i hw-detect/load_firmware boolean true
d-i mirror/country string manual
d-i mirror/protocol string http
d-i mirror/http/hostname string deb.debian.org
d-i mirror/http/directory string /debian
d-i mirror/http/proxy string
d-i mirror/suite string %s
d-i passwd/root-login boolean false
d-i passwd/make-user boolean false
d-i clock-setup/utc boolean true
d-i time/zone string UTC
d-i partman/early_command string %s
d-i partman-auto/method string regular
d-i partman-auto/choose_recipe select atomic
d-i partman-lvm/device_remove_lvm boolean true
d-i partman-md/device_remove_md boolean true
d-i partman-partitioning/confirm_write_new_label boolean true
d-i partman/choose_partition select finish
d-i partman/confirm boolean true
d-i partman/confirm_nooverwrite boolean true
d-i apt-setup/cdrom/set-first boolean false
d-i apt-setup/non-free-firmware boolean true
tasksel tasksel/first multiselect standard, ssh-server
d-i pkgsel/include string openssh-server sudo curl ca-certificates
popularity-contest popularity-contest/participate boolean false
d-i grub-installer/only_debian boolean true
d-i grub-installer/with_other_os boolean false
d-i preseed/late_command string %s
d-i finish-install/reboot_in_progress note
`, p.hostname(mac), p.hostname(mac), p.Suite, guard, finish)
}

func ubuntuSeed(base, origin, mac, key string, p *linuxProfile) string {
	a := map[string]any{
		"version":           1,
		"refresh-installer": map[string]any{"update": false},
		"locale":            "en_US.UTF-8", "keyboard": map[string]any{"layout": "us"},
		"identity": map[string]any{"hostname": p.hostname(mac), "username": p.Username, "password": "!"},
		"ssh":      map[string]any{"install-server": true, "allow-pw": false, "authorized-keys": p.AuthorizedKeys},
		// The guard replaces this deliberately impossible disk match before
		// storage discovery. Even a skipped guard cannot select a default disk.
		"storage":        map[string]any{"layout": map[string]any{"name": "direct", "match": map[string]any{"path": "/dev/minute2win-guard-not-run"}}},
		"early-commands": []string{"curl -fsS " + shellQuote(base+"guard.sh") + " -o /run/m2w-guard && sh /run/m2w-guard"},
		"late-commands":  []string{"curl -fsS " + shellQuote(base+"finish.sh") + " -o /run/m2w-finish && sh /run/m2w-finish"},
		"error-commands": []string{"curl -fsS --max-time 10 -H 'Content-Type: application/json' --data " + shellQuote(linuxReportJSON(mac, key, "install", "failed", "Ubuntu installer failed; inspect /var/log/installer")) + " " + shellQuote(origin+"/api/report") + " || true"},
		"shutdown":       "reboot",
	}
	data, _ := json.MarshalIndent(map[string]any{"autoinstall": a}, "", "  ")
	// JSON is a YAML subset; the cloud-config marker makes NoCloud consume it.
	return "#cloud-config\n" + string(data) + "\n"
}

func linuxReportJSON(mac, key, phase, status, message string) string {
	b, _ := json.Marshal(map[string]any{"macs": []string{mac}, "key": key, "phase": phase, "status": status, "message": message})
	return string(b)
}
