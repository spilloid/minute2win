# Native companion

This separate Go module keeps native tray dependencies out of the headless server, which builds without cgo. `fyne.io/systray` supplies the small Win32/AppKit/DBus integration: maintaining three native event loops and menu implementations would add substantially more platform code. The full Fyne toolkit and a webview are not included. Pinned dependencies are systray, godbus and Go's x/sys; licenses ship in `../licenses/`.

Build with current stable Go: `go build .`; Windows GUI build: `go build -ldflags="-H=windowsgui" .`. Windows/Linux support `CGO_ENABLED=0`; macOS needs cgo and Xcode command-line tools. `../tools/macos-bundle.sh` creates the app on a native Mac. Checks: `go vet ./...` and `go test -race ./...`.

The companion performs bounded, timeout-controlled GETs only. It never owns/stops the server or accesses deployment credentials. See [operator instructions](../docs/DESKTOP.md).
