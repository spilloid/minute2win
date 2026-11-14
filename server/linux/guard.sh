set -eu
umask 077
trap 'report guard failed "Disk guard interrupted"; exit 1' HUP INT TERM
# Re-entering an installer hook must never select a different disk after work
# has started. Reuse the original successful decision within this live boot.
if [ -f /tmp/m2w-guard-complete ]; then
    [ -s /tmp/m2w-disk ] || exit 1
    exit 0
fi
report guard running "Checking internal disks"
rm -f /tmp/m2w-disk
if ! selected=$(select_disk /sys/block /dev /proc "$M2W_MIN_GB"); then
    report guard failed "No eligible unmounted internal disk meets the minimum size"
    exit 1
fi
echo "minute2win: erasing $selected in $M2W_COUNTDOWN seconds; Ctrl-C stops this guard"
sleep "$M2W_COUNTDOWN"
printf '%s\n' "$selected" > /tmp/m2w-disk
if [ "$M2W_INSTALLER" = ubuntu ]; then
    # Subiquity re-reads this file after early-commands, before storage work.
    python3 - <<'PY'
import yaml
path = "/autoinstall.yaml"
with open(path) as stream:
    config = yaml.safe_load(stream)
with open("/tmp/m2w-disk") as stream:
    disk = stream.read().strip()
settings = config.get("autoinstall", config)
settings["storage"]["layout"]["match"] = {"path": disk}
with open(path, "w") as stream:
    yaml.safe_dump(config, stream)
PY
fi
touch /tmp/m2w-guard-complete
report install running "Disk guard passed; native installer is partitioning and installing"
