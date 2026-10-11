#!/usr/bin/env bash
# Build the minute2win-server AppImage (x86_64): tools/appimage.sh <version> <linux-amd64 server binary> <output file>
# No appimagetool and no FUSE needed: an AppImage is the type 2 runtime followed by a squashfs of the AppDir. The runtime is
# the one prebuilt binary we fetch, and it is pinned by SHA-256 (a changed upstream file fails the build instead of shipping).
# To move to a newer runtime: download it, read it, and update RUNTIME_SHA256.
set -euo pipefail
VERSION="${1:?version}"; BIN="${2:?server binary}"; OUT="${3:?output file}"
RUNTIME_URL="https://github.com/AppImage/type2-runtime/releases/download/continuous/runtime-x86_64"
RUNTIME_SHA256="156f4bdbde9c52d01814600013e0a273f0118dc2de98975f3c8c63427ec79074"
cd "$(dirname "$0")/.."
BIN="$(realpath "$BIN")"; OUT="$(realpath -m "$OUT")"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
A="$W/minute2win.AppDir"
mkdir -p "$A/usr/bin"
install -m 0755 "$BIN" "$A/usr/bin/minute2win-server"
cp -r manifests LICENSE THIRD_PARTY_NOTICES.md "$A/usr/bin/"   # the server finds manifests/ next to its executable
cat > "$A/AppRun" <<'RUN'
#!/bin/sh
# Runs in the caller's directory, so -data defaults to ./data there (not inside the read-only image).
exec "${APPDIR:-$(dirname "$(readlink -f "$0")")}/usr/bin/minute2win-server" "$@"
RUN
chmod 0755 "$A/AppRun"
cat > "$A/minute2win.desktop" <<DESK
[Desktop Entry]
Type=Application
Name=minute2win-server
Comment=Zero-touch Windows deployment server ($VERSION)
Exec=minute2win-server
Icon=minute2win
Categories=System;Network;
Terminal=true
DESK
cat > "$A/minute2win.svg" <<'SVG'
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="12" fill="#0b57d0"/><circle cx="32" cy="34" r="19" fill="none" stroke="#fff" stroke-width="4"/><path d="M32 34V21M32 34l9 5M26 10h12" stroke="#fff" stroke-width="4" stroke-linecap="round" fill="none"/></svg>
SVG
cp "$A/minute2win.svg" "$A/.DirIcon"
RT="${APPIMAGE_RUNTIME:-$W/runtime}"
[ -f "$RT" ] || curl -fsSL --retry 3 -o "$RT" "$RUNTIME_URL"
echo "$RUNTIME_SHA256  $RT" | sha256sum --check --strict
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}"   # mksquashfs reads this itself
mksquashfs "$A" "$W/app.squashfs" -root-owned -noappend -quiet -comp zstd
mkdir -p "$(dirname "$OUT")"
cat "$RT" "$W/app.squashfs" > "$OUT"
chmod 0755 "$OUT"
echo "$OUT"
