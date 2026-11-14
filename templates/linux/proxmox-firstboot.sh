#!/bin/sh
# Example for a FRESH Debian trixie installation only. Edit these values for
# one host, review the upstream guide, then pin this file's SHA-256 in its
# minute2win profile. Do not share a static-address profile between machines.
# https://pve.proxmox.com/wiki/Install_Proxmox_VE_on_Debian_13_Trixie
set -eu
umask 022
PVE_FQDN='CHANGE_ME'
PVE_CIDR='CHANGE_ME'
PVE_GATEWAY='CHANGE_ME'
PVE_DNS='CHANGE_ME'
PVE_IFACE='CHANGE_ME'
# Copy the CURRENT SHA-256 published by Proxmox for this keyring. Never obtain
# the expected checksum from the same untrusted download you are checking.
KEY_SHA256='CHANGE_ME'

case "$PVE_FQDN $PVE_CIDR $PVE_GATEWAY $PVE_DNS $PVE_IFACE $KEY_SHA256" in
    *CHANGE_ME*) echo 'Edit every CHANGE_ME value before deploying Proxmox' >&2; exit 1 ;;
esac
[ "$(id -u)" = 0 ]
[ "$(dpkg --print-architecture)" = amd64 ]
. /etc/os-release
[ "$ID:$VERSION_CODENAME" = debian:trixie ]
printf '%s\n' "$KEY_SHA256" | grep -Eq '^[0-9a-fA-F]{64}$'
[ -d "/sys/class/net/$PVE_IFACE" ]
export DEBIAN_FRONTEND=noninteractive
stage=/var/lib/minute2win/pve-kernel-installed
if [ ! -f "$stage" ]; then
    curl -fsS --retry 3 https://enterprise.proxmox.com/debian/proxmox-archive-keyring-trixie.gpg \
        -o /usr/share/keyrings/proxmox-archive-keyring.gpg
    printf '%s  %s\n' "$KEY_SHA256" /usr/share/keyrings/proxmox-archive-keyring.gpg | sha256sum -c -
    chmod 644 /usr/share/keyrings/proxmox-archive-keyring.gpg
    cat > /etc/apt/sources.list.d/minute2win-pve.sources <<'REPO'
Types: deb
URIs: http://download.proxmox.com/debian/pve
Suites: trixie
Components: pve-no-subscription
Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg
REPO
    address=${PVE_CIDR%/*}
    cat > /etc/hosts <<HOSTS
127.0.0.1 localhost
::1 localhost ip6-localhost ip6-loopback
$address $PVE_FQDN ${PVE_FQDN%%.*}
HOSTS
    printf '%s\n' "${PVE_FQDN%%.*}" > /etc/hostname
    hostname "${PVE_FQDN%%.*}"
    apt-get update
    apt-get -y full-upgrade
    apt-get install -y proxmox-default-kernel ifupdown2
    # Stage networking for the reboot; do not reload a live management link.
    cat > /etc/network/interfaces <<NET
auto lo
iface lo inet loopback

iface $PVE_IFACE inet manual

auto vmbr0
iface vmbr0 inet static
    address $PVE_CIDR
    gateway $PVE_GATEWAY
    bridge-ports $PVE_IFACE
    bridge-stp off
    bridge-fd 0
NET
    printf 'nameserver %s\n' "$PVE_DNS" > /etc/resolv.conf
    touch "$stage"
    # The minute2win wrapper reports a pending reboot and reruns this script
    # next boot. It must not mark the deployment done at this point.
    exit 75
fi
case "$(uname -r)" in
    *-pve) ;;
    *) echo 'The new Proxmox kernel did not boot; inspect the bootloader' >&2; exit 1 ;;
esac
printf 'postfix postfix/main_mailer_type select Local only\n' | debconf-set-selections
printf 'postfix postfix/mailname string %s\n' "$PVE_FQDN" | debconf-set-selections
apt-get install -y proxmox-ve postfix open-iscsi chrony
# This example selects the no-subscription repository. Operators with a
# subscription should adapt the repository policy before pinning the script.
for repo in /etc/apt/sources.list.d/pve-enterprise.sources /etc/apt/sources.list.d/ceph.sources; do
    if [ -f "$repo" ] && grep -q enterprise.proxmox.com "$repo"; then
        awk 'BEGIN { RS=""; ORS="\n\n" } { gsub(/(^|\n)Enabled:[^\n]*/, ""); print $0 "\nEnabled: no" }' "$repo" > "$repo.m2w"
        mv "$repo.m2w" "$repo"
    fi
done
systemctl is-active --quiet pve-cluster pvedaemon pveproxy
echo 'Proxmox services are running. SSH with your profile key; use sudo passwd root to enable root@pam web login.'
# Keep the Debian fallback kernel until this host is validated. Follow the
# upstream guide for kernel cleanup, subscription policy and cluster setup.
