#!/usr/bin/env bash
# Build on a native macOS runner; the app is ad-hoc signed, not notarized.
set -euo pipefail
version="${1:?version}"; arch="${2:?amd64 or arm64}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1
[[ "$arch" = amd64 || "$arch" = arm64 ]] || exit 1
cd "$(dirname "$0")/.."
dest="dist/minute2win-server-$version-darwin-$arch"
app="$dest/minute2win Tray.app"
mkdir -p "$app/Contents/MacOS" out
(cd server && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "../$dest/minute2win-server" .)
(cd desktop && CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "../$app/Contents/MacOS/minute2win-tray" .)
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>minute2win-tray</string>
<key>CFBundleIdentifier</key><string>io.github.spilloid.minute2win.tray</string>
<key>CFBundleName</key><string>minute2win Tray</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$version</string>
<key>CFBundleVersion</key><string>$version</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
cp -R manifests templates licenses LICENSE THIRD_PARTY_NOTICES.md "$dest/"
codesign --force --sign - "$app"
codesign --verify --deep --strict "$app"
tar -czf "out/minute2win-server-$version-darwin-$arch.tar.gz" -C dist "minute2win-server-$version-darwin-$arch"
