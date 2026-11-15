package main

import "strings"

// Bazzite uses Fedora's native Anaconda OSTree-container payload. Disk commands
// exist only in the include written after the common guard succeeds. Anaconda
// must stop on a failed pre/post hook; it must never ask for another disk.
func bazziteKickstart(base, origin, mac, key string, p *linuxProfile) string {
	const seed = `#version=F45
cmdline
lang en_US.UTF-8
keyboard us
timezone UTC --utc
network --bootproto=dhcp --device=link --activate --hostname=@@HOST@@
rootpw --lock
firstboot --disable
xconfig --startxonboot
services --enabled=sshd
ostreecontainer --url=@@IMAGE@@ --transport=registry --no-signature-verification
reboot

%pre --interpreter=/bin/sh --erroronfail --log=/tmp/minute2win-pre.log
set -eu
curl -fsS --retry 3 --connect-timeout 10 --max-time 120 @@GUARD@@ -o /tmp/minute2win-guard
/bin/sh /tmp/minute2win-guard
selected=$(cat /tmp/m2w-disk)
[ -b "$selected" ]
disk=${selected##*/}
case "$disk" in ''|*[!a-zA-Z0-9]*) exit 1 ;; esac
printf 'ignoredisk --only-use=%s\nclearpart --all --initlabel --drives=%s\nbootloader --boot-drive=%s\nautopart --type=btrfs --nohome --noswap\n' "$disk" "$disk" "$disk" > /tmp/minute2win-disk.ks
%end
%include /tmp/minute2win-disk.ks

%post --interpreter=/bin/sh --erroronfail --log=/root/minute2win-install.log
set -eu
mkdir -p /var/tmp
curl -fsS --retry 3 --connect-timeout 10 --max-time 120 @@FINISH@@ -o /var/tmp/minute2win-finish
/bin/sh /var/tmp/minute2win-finish
rm -f /var/tmp/minute2win-finish
%end

%onerror --interpreter=/bin/sh
curl -fsS --max-time 10 -H 'Content-Type: application/json' --data @@ERROR@@ @@REPORT@@ >/dev/null 2>&1 || true
%end
`
	return strings.NewReplacer(
		"@@HOST@@", p.hostname(mac),
		"@@IMAGE@@", p.ContainerImage,
		"@@GUARD@@", shellQuote(base+"guard.sh"),
		"@@FINISH@@", shellQuote(base+"bazzite-finish.sh"),
		"@@ERROR@@", shellQuote(linuxReportJSON(mac, key, "install", "failed", "Bazzite installer failed; inspect the Anaconda logs")),
		"@@REPORT@@", shellQuote(origin+"/api/report"),
	).Replace(seed)
}
