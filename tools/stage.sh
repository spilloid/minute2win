#!/usr/bin/env bash
# Lay out the files to upload under the bucket's windows11/ prefix: tools/stage.sh [outdir] (default dist/).
# Uploading is a separate, deliberate step (rclone to R2); this script touches nothing remote.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="${1:-dist}/windows11"
rm -rf "$OUT"; mkdir -p "$OUT/minute2win/manifests"
cp boot/install.ps1 boot/minute2win.ipxe "$OUT/"
cp VERSION engine/Minute2Win.psm1 engine/Start-Minute2Win.ps1 bootstrap/Start-FirstBoot.ps1 templates/unattend-oobe.xml "$OUT/minute2win/"
cp manifests/win11-pro.json "$OUT/minute2win/manifests/"
find "$OUT" -type f | sort
