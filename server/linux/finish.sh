set -eu
umask 077
trap 'report setup failed "Installed-system setup failed"' EXIT
[ -d /target/etc ]
if [ "$M2W_INSTALLER" != ubuntu ]; then
    in-target useradd -m -U -s /bin/bash "$M2W_USERNAME"
    in-target usermod -aG sudo "$M2W_USERNAME"
fi
mkdir -p "/target/home/$M2W_USERNAME/.ssh" /target/etc/sudoers.d /target/etc/ssh/sshd_config.d
printf '%s' "$M2W_KEYS" > "/target/home/$M2W_USERNAME/.ssh/authorized_keys"
chmod 700 "/target/home/$M2W_USERNAME/.ssh"
chmod 600 "/target/home/$M2W_USERNAME/.ssh/authorized_keys"
chroot /target chown -R "$M2W_USERNAME:$M2W_USERNAME" "/home/$M2W_USERNAME/.ssh"
printf '%s ALL=(ALL) NOPASSWD: ALL\n' "$M2W_USERNAME" > "/target/etc/sudoers.d/90-minute2win"
chmod 440 /target/etc/sudoers.d/90-minute2win
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > /target/etc/ssh/sshd_config.d/00-minute2win.conf
chmod 644 /target/etc/ssh/sshd_config.d/00-minute2win.conf
mkdir -p /target/usr/local/sbin /target/var/lib/minute2win
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsS --max-time 120 "$M2W_BASE$1" -o "$2"
    else
        wget -q -T 120 -O "$2" "$M2W_BASE$1"
    fi
}
if [ "$M2W_FIRSTBOOT" = true ]; then
    fetch firstboot.sh /target/var/lib/minute2win/custom-firstboot.sh
fi
fetch firstboot-wrapper.sh /target/usr/local/sbin/minute2win-firstboot
chmod 700 /target/usr/local/sbin/minute2win-firstboot
cat > /target/etc/systemd/system/minute2win-firstboot.service <<'UNIT'
[Unit]
Description=minute2win installed-system setup and reporting
Wants=network-online.target
After=network-online.target
ConditionPathExists=!/var/lib/minute2win/complete

[Service]
Type=oneshot
ExecStart=/bin/sh /usr/local/sbin/minute2win-firstboot
TimeoutStartSec=infinity
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
chmod 644 /target/etc/systemd/system/minute2win-firstboot.service
mkdir -p /target/etc/systemd/system/multi-user.target.wants
ln -sf ../minute2win-firstboot.service /target/etc/systemd/system/multi-user.target.wants/minute2win-firstboot.service
report reboot running "Native installation complete; waiting for installed-system first boot"
trap - EXIT
