#!/usr/bin/env bash
# Lay out the files to upload under your boot-asset folder: tools/stage.sh [outdir] [base] (default outdir dist/).
# base: the URL that folder will be served at (for example https://deploy.lab.internal/windows11). If given, it is written into the
# staged minute2win.ipxe as its m2w-base; if not, the staged script boots nothing until m2w-base is set before chaining it.
# Uploading is a separate, deliberate step; this script touches nothing remote.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="${1:-dist}/windows11"; BASE="${2:-}"
case "$BASE" in
  "" | http://* | https://*) ;;
  *) echo "base must be an http:// or https:// URL: $BASE" >&2; exit 2 ;;
esac
case "$BASE" in *[[:space:]\"\'\$\{\}\&#\\]*) echo "base must not contain spaces, quotes, \$, braces, &, # or backslashes: $BASE" >&2; exit 2 ;; esac
# Build and check the boot script before the output folder is touched, so a failure leaves any earlier staging as it was.
IPXE="$(mktemp)"; trap 'rm -f "$IPXE"' EXIT
cp boot/minute2win.ipxe "$IPXE"
if [ -n "$BASE" ]; then
  # Only the default on the "isset" line changes; the "iseq ... @M2W_BASE@" guard keeps the placeholder it compares against.
  sed -i.bak "s#^isset \${m2w-base} || set m2w-base @M2W_BASE@#isset \${m2w-base} || set m2w-base ${BASE%/}#" "$IPXE"
  rm -f "$IPXE.bak"
  # Literal match: a URL is not a regular expression ([fd00::1] would be a character class).
  grep -Fq -- "isset \${m2w-base} || set m2w-base ${BASE%/}" "$IPXE" || { echo "could not write the base into minute2win.ipxe" >&2; exit 1; }
fi
rm -rf "$OUT"; mkdir -p "$OUT/minute2win/manifests"
cp boot/install.ps1 "$OUT/"
cp "$IPXE" "$OUT/minute2win.ipxe"
# mktemp makes its file 0600 and cp keeps that mode; the web server serving this folder may run as another user, so the staged
# script must be world-readable like everything else here.
chmod 0644 "$OUT/minute2win.ipxe"
cp VERSION engine/Minute2Win.psm1 engine/Start-Minute2Win.ps1 bootstrap/Start-FirstBoot.ps1 templates/unattend-oobe.xml "$OUT/minute2win/"
cp manifests/win11-pro.json "$OUT/minute2win/manifests/"
find "$OUT" -type f | sort
