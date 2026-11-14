set -eu
umask 077
[ -d "$M2W_STATE" ] || mkdir -p "$M2W_STATE"
[ ! -e "$M2W_STATE/complete" ] || exit 0
exec >> "$M2W_LOG" 2>&1
report firstboot running "Installed OS booted; running first-boot setup"
if [ "$M2W_FIRSTBOOT" = true ]; then
    result=0
    /bin/sh -e "$M2W_STATE/custom-firstboot.sh" || result=$?
    if [ "$result" = 75 ]; then
        # A custom script can request another boot (e.g. a newly installed
        # Proxmox kernel). It must persist its own stage before returning 75.
        report reboot running "First-boot setup requested a reboot; deployment remains active"
        sync
        systemctl reboot
        exit 0
    fi
    if [ "$result" != 0 ]; then
        report firstboot failed "First-boot setup failed; inspect /var/log/minute2win-firstboot.log"
        exit "$result"
    fi
fi
touch "$M2W_STATE/complete"
report complete done "Installed OS booted and first-boot setup completed"
# Drop boot delivery credentials and custom scripts after success.
rm -f "$M2W_STATE/custom-firstboot.sh" "$M2W_WRAPPER"
