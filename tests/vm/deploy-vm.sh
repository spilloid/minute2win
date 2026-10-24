#!/usr/bin/env bash
# End-to-end: boot a UEFI VM with a blank 80 GB NVMe disk through iPXE -> minute2win WinPE -> zero-touch deploy -> reboot into
# Windows, screenshotting along the way (the evidence). Reuses kvm-it's harness rather than copying it:
#   kvm-it/scripts/build-ipxe-image.sh   (boot drive image with our autoexec)
#   kvm-it/tools/ipxe-test/boot-vm.sh    (QEMU/OVMF in podman, QMP keys and screendumps)
#   tests/vm/deploy-vm.sh [seconds] [out.png]      default 3600 s, /tmp/m2w-vm.png
# env: BASE=<your boot-asset URL> (required)  KVMIT=../kvm-it  MANIFEST=win11-pro  TYPE_AT=300 (seconds; type the deploy command, for a WinPE built with -Manual or
#      the pre-0.1.0 boot.wim that stops at a prompt; empty = rely on startnet auto-run)  SB=off|sb
# Needs: the files from tools/stage.sh uploaded under <base>, podman with the localhost/win11-qemu image, /dev/kvm, ~40 GB free
# (the sparse disk grows as Windows is applied; it is deleted when the run ends). Downloads ~8.5 GB from the image host.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
KVMIT="${KVMIT:-$HERE/../../../kvm-it}"
SECS="${1:-3600}"; OUT="${2:-/tmp/m2w-vm.png}"
MANIFEST="${MANIFEST:-win11-pro}"; TYPE_AT="${TYPE_AT:-}"; SB="${SB:-off}"
BASE="${BASE:-}"
[ -n "$BASE" ] || { echo "set BASE= to your boot-asset URL (the staged windows11/ folder), for example BASE=https://deploy.lab.internal/windows11; there is no default host" >&2; exit 2; }
[ -x "$KVMIT/tools/ipxe-test/boot-vm.sh" ] || { echo "kvm-it checkout not found at $KVMIT (set KVMIT=)" >&2; exit 2; }
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
cat > "$W/autoexec.ipxe" <<IPXE
#!ipxe
dhcp || exit 1
set m2w-manifest $MANIFEST
set m2w-base $BASE
chain $BASE/minute2win.ipxe || exit 1
IPXE
AUTOEXEC="$W/autoexec.ipxe" OUT="$W/boot.img" "$KVMIT/scripts/build-ipxe-image.sh"
TYPE=""
if [ -n "$TYPE_AT" ]; then
  printf '%s\tpowershell -NoProfile -ExecutionPolicy Bypass -File X:\\install.ps1 -Base %s -Manifest %s\n' "$TYPE_AT" "$BASE" "$MANIFEST" > "$W/type.txt"
  TYPE="$W/type.txt"
fi
BOOT_IMAGE="$W/boot.img" RAM=4G NIC=e1000e DISK=80G TYPE_FILE="$TYPE" \
  SHOTS="${SHOTS:-60 180 300 600 900 1200 1800 2400 3000}" "$KVMIT/tools/ipxe-test/boot-vm.sh" "$SB" "$SECS" "$OUT"
