# Runs in Anaconda's installed-system chroot, not the live installer root.
set -eu
umask 077
trap 'report setup failed "Bazzite installed-system setup failed"' EXIT

# Match the upstream Bazzite installer: retain signed stable updates after the
# reproducible digest-pinned installation. No custom image is built or rebased.
bootc switch --mutate-in-place --enforce-container-sigpolicy --transport registry "$M2W_BAZZITE_TRACKING_IMAGE"

if ! id "$M2W_USERNAME" >/dev/null 2>&1; then
    useradd -m -U -s /bin/bash "$M2W_USERNAME"
fi
usermod -aG wheel "$M2W_USERNAME"
passwd -l "$M2W_USERNAME"
home=$(getent passwd "$M2W_USERNAME" | cut -d: -f6)
[ -n "$home" ]
mkdir -p "$home/.ssh" /etc/sudoers.d /etc/ssh/sshd_config.d
printf '%s' "$M2W_KEYS" > "$home/.ssh/authorized_keys"
chmod 700 "$home/.ssh"
chmod 600 "$home/.ssh/authorized_keys"
chown -R "$M2W_USERNAME:$M2W_USERNAME" "$home/.ssh"
printf '%s ALL=(ALL) NOPASSWD: ALL\n' "$M2W_USERNAME" > /etc/sudoers.d/90-minute2win
chmod 440 /etc/sudoers.d/90-minute2win
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/00-minute2win.conf
chmod 644 /etc/ssh/sshd_config.d/00-minute2win.conf
systemctl enable sshd.service
systemctl set-default graphical.target
if command -v firewall-offline-cmd >/dev/null 2>&1; then
    firewall-offline-cmd --add-service=ssh
fi

if [ "$M2W_DESKTOP_AUTOLOGIN" = true ]; then
    [ -f /usr/share/wayland-sessions/plasma.desktop ]
    if [ -f /usr/lib/systemd/system/plasmalogin.service ]; then
        login_dir=/etc/plasmalogin.conf.d
    elif [ -f /usr/lib/systemd/system/sddm.service ]; then
        login_dir=/etc/sddm.conf.d
    else
        echo 'minute2win: no supported KDE display manager installed' >&2
        exit 1
    fi
    mkdir -p "$login_dir"
    printf '[Autologin]\nUser=%s\nSession=plasma.desktop\nRelogin=false\n' "$M2W_USERNAME" > "$login_dir/90-minute2win.conf"
    chmod 644 "$login_dir/90-minute2win.conf"
    restorecon -RF "$login_dir"
fi

mkdir -p /var/usrlocal/sbin "$M2W_STATE"
fetch() { curl -fsS --retry 3 --connect-timeout 10 --max-time 120 "$M2W_BASE$1" -o "$2"; }
if [ "$M2W_FIRSTBOOT" = true ]; then
    fetch firstboot.sh "$M2W_STATE/custom-firstboot.sh"
fi
fetch firstboot-wrapper.sh "$M2W_WRAPPER"
chmod 700 "$M2W_WRAPPER"
cat > /etc/systemd/system/minute2win-firstboot.service <<'UNIT'
[Unit]
Description=minute2win installed-system setup and reporting
Wants=network-online.target
After=network-online.target
ConditionPathExists=!/var/lib/minute2win/complete

[Service]
Type=oneshot
ExecStart=/bin/sh /var/usrlocal/sbin/minute2win-firstboot
TimeoutStartSec=infinity
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
chmod 644 /etc/systemd/system/minute2win-firstboot.service
systemctl enable minute2win-firstboot.service
restorecon -RF "$home/.ssh" /etc/sudoers.d/90-minute2win /etc/ssh/sshd_config.d/00-minute2win.conf /var/usrlocal/sbin /var/lib/minute2win /etc/systemd/system/minute2win-firstboot.service
report reboot running "Bazzite installation complete; waiting for installed-system first boot"
trap - EXIT
