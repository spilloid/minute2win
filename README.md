# minute2win

Zero-touch Windows deployment from WinPE. A machine network-boots (dnsmasq/iPXE, or a USB boot drive such as kvm-it's), WinPE
starts, and minute2win **wipes the first internal disk** and installs Windows from a verified image, with no input. Then it reboots
into Windows and runs a first-boot bootstrap.

Version **0.1.0** ([releases](https://github.com/spilloid/minute2win/releases), [releasing](docs/RELEASING.md)). **Status: unit-tested, never booted.** Nothing here has deployed a machine yet; see "Verification" below.

## It is a power tool

minute2win does not ask which disk or whether to wipe. That is the point: it is for machines you have already decided to
reimage. The guards are:

1. **Reaching it is the opt-in.** For PXE, use minute2win-server: arm a MAC, and the arm is consumed when that machine boots,
   so one arm wipes once. The static `boot/minute2win.ipxe` deploys on *every* boot that reaches it, so use it only behind a
   per-boot opt-in (kvm-it's "press n" prompt). On firmware that always network-boots first, it would loop.
2. **WinPE only.** The engine refuses to run unless it is inside WinPE (`HKLM\SYSTEM\...\Control\MiniNT`), so a copy run on a
   working Windows install does nothing.
3. **Abort countdown.** It shows the disk it is about to wipe and counts down (`guard.countdownSeconds`, default 10); any key
   aborts. Set it to 0 for no pause. It never waits for input.
4. **Never the boot stick.** USB and file-backed virtual disks are never targets, and neither is anything under
   `disk.minSizeGB` (default 64).

Target disk: the lowest-numbered eligible disk, with SD/MMC (card readers, eMMC) after real fixed disks. `-Disk N` overrides it.
It is never required.

## How it fits together

```
dnsmasq / USB -> iPXE -> minute2win.ipxe -> wimboot -> WinPE (boot.wim, startnet.cmd auto-runs)
  -> <base>/install.ps1 (loader)  -> engine: manifest -> hardware log -> pick disk -> countdown
     -> diskpart GPT -> download image, SHA-256 -> DISM apply -> bcdboot -> SetupComplete bootstrap -> logs -> reboot
  -> Windows Setup -> SetupComplete.cmd -> Start-FirstBoot.ps1 (idempotent) -> OOBE / first logon
```

kvm-it doesn't need to know how Windows gets installed, and minute2win doesn't need to know what booted it.

| Path | What |
|---|---|
| `boot/minute2win.ipxe` | Loads wimboot, BCD, boot.sdi, boot.wim and the chosen manifest (`m2w-manifest`, default `win11-pro`) |
| `boot/install.ps1` | Loader at the URL the WinPE fetches; downloads the engine and runs it. Behaviour changes by uploading files, not by rebuilding WinPE |
| `engine/Start-Minute2Win.ps1` | The deployment, in order |
| `engine/Minute2Win.psm1` | Its logic, kept pure where possible so it is unit-testable |
| `bootstrap/Start-FirstBoot.ps1` | Runs once as SYSTEM from `SetupComplete.cmd`; records results |
| `build/Build-WinPE.ps1` | Builds `boot.wim` (ADK + WinPE add-on, PowerShell components, optional `-Drivers`) |
| `manifests/*.json` | Deployable profiles (`manifests/examples/` holds templates the server does not offer) |
| `templates/unattend-oobe.xml` | OOBE answer file, used only when a manifest names `oobe.adminUser` |
| `tools/stage.sh` | Lays out the upload under `windows11/` |

## Server and web UI (`server/`)

`minute2win-server` is a single Go binary with the page built in. It runs next to dnsmasq and is where the guard lives. The page uses
**htmx**: the server renders HTML fragments (`server/web/templates/`, escaped by `html/template`) and `hx-*` attributes handle polling,
arming and disarming, with no app JavaScript. To add a feature, add a fragment and an attribute. htmx is vendored, not loaded from a CDN, so
it works offline. `/api/*` stays as JSON for scripts.

- **Arm** a MAC with a profile. The target's iPXE runs `chain http://<server>:8080/boot/${net0/mac}`. An armed MAC gets the
  minute2win boot script and is **disarmed in the same moment**: one arm is one wipe, so a machine that network-boots first does not
  wipe itself again after the deploy reboots it. An unarmed MAC gets `exit 1` and the firmware boots the local disk. Arms that nobody
  boots expire (`-arm-ttl`, default 2h), and **arms do not survive a restart of the server**: re-arm what you still want.
- The server serves the armed manifest with `report.url` set to itself. The engine then posts every phase and lap time, and the
  **Deployments** board shows a live stopwatch against the 60-second target.
- `M2W_TOKEN=...` makes arming and disarming require that token. Without it, anyone who can reach the server can arm a machine
  (the server warns about this at startup).

```
cd server && go build -o minute2win-server . && M2W_TOKEN=... ./minute2win-server -manifests ../manifests -data ./data
```

dnsmasq's side is the `imaging-server-winpe` pattern (UEFI clients get `ipxe.efi`). Give iPXE an embedded or chained script that
runs the `chain .../boot/${net0/mac}` line above.

## Manifest

```json
{
  "schemaVersion": 1,
  "deployment": { "name": "Windows 11 Pro" },
  "image": { "source": "https", "uri": "https://.../install.wim", "sha256": "<64 hex>", "edition": "Windows 11 Pro" },
  "disk": { "minSizeGB": 64, "stagingGB": 12 },
  "oobe": { "adminUser": null },
  "tenant": { "id": "none", "joinMethod": "none" },
  "guard": { "countdownSeconds": 10 },
  "report": { "url": "http://deploy-server:8080/api/report" }
}
```

- `image.sizeBytes`: optional. The size is normally learned from the image server before the wipe; if the server reports none and
  this is unset, the deploy stops before touching the disk.
- `image.source`: only `https` is implemented (`smb` goes behind the same function later). The hash is required and checked
  before anything is applied. The edition is matched by name, since indices differ between ISOs; `image.index` is the fallback.
- `oobe.adminUser`: when it is set, OOBE is skipped and that local admin is created with the public baseline password
  `Password1!`, which Windows forces to change at the next logon. Manifests never hold secrets.
- `report.url`: optional. minute2win-server fills it in. Reporting is best-effort and never stops a deploy.
- `tenant.joinMethod`: `none` works. `ad`, `entra` and `hybrid` are accepted but only logged as "not implemented yet".

Logs: `X:\minute2win\logs` during the deploy, then copied to `C:\Windows\Logs\minute2win\` (the deploy transcript,
`hardware.json`, `setupcomplete.log`, `firstboot.log` and `firstboot-result.json`). On failure the engine stops at the console
and does not reboot.

## Build, test, publish

- **Tests** (Linux, no pwsh needed): `tests/run.sh` runs Pester and PSScriptAnalyzer (including PowerShell 5.1 syntax
  compatibility, because WinPE runs 5.1) in podman. CI runs the same `tests/ci.ps1`.
- **WinPE**: on Windows with the ADK 10.1.26100.x and the WinPE add-on, run `build/Build-WinPE.ps1 [-Drivers C:\drivers]` from the Deployment and
  Imaging Tools Environment. Install both from Microsoft's links (winget's ADK manifest is stale).
- **Publish**: run `tools/stage.sh`, then upload `dist/windows11/` next to `wimboot`, `boot.wim`, `BCD`, `boot.sdi` and `install.wim`.
- **Server**: `cd server && go test ./...`. **UI**: `tests/ui/run.sh` runs a Playwright check (arm, boot, live report,
  disarm, no horizontal scroll at phone width, light and dark) and saves screenshots.
- **End to end**: `tests/vm/deploy-vm.sh` boots a VM with a blank 80 GB disk through the real chain, using kvm-it's
  `boot-vm.sh`, and saves screenshots.

## Verification

| Layer | State |
|---|---|
| Engine logic (manifest, disk choice, layout, edition, unattend) | Pester (`tests/run.sh`) |
| PowerShell 5.1 syntax compatibility | PSScriptAnalyzer `PSUseCompatibleSyntax` |
| Server arm/boot/report rules | `go test` |
| Web UI flow, phone width, light/dark | Playwright (`tests/ui/run.sh`) |
| WinPE boot over iPXE/wimboot | **not yet run** |
| Partition, download, apply, bcdboot, reboot | **not yet run** (VM first, `tests/vm/deploy-vm.sh`) |
| SetupComplete bootstrap, OOBE answer file on 26300 | **not yet run** |
| Real hardware, Secure Boot | **not yet run** |
