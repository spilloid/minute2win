# minute2win

**Arm a machine. Network boot it. Install Windows.**

Minute2Win is a self-hosted Windows deployment toolkit: a small Go server with a web console, an iPXE boot chain, and a PowerShell engine that installs Windows from a verified image. Arm a MAC address with a deployment profile, watch each phase on the live board, and keep deployment logs after reboot.

[Download releases](https://github.com/spilloid/minute2win/releases) · [Walkthrough: first deployment on one PC](docs/WALKTHROUGH.md) · [Getting started](docs/GETTING_STARTED.md) · [Documentation](docs/guide.md) · [Product page](https://spilloid.github.io/minute2win/) · [Known issues](docs/STATUS.md)

**Current code version: 0.2.0. Early release.** Server logic and deployment helpers are unit-tested; the web console is tested with Playwright. A disposable Proxmox x64 UEFI VM has booted through Windows-hosted PXE, completed imaging and booted its installed Windows disk. It reached normal OOBE, and logs confirm automatic first-boot bootstrap success. The optional local-account profile reached the desktop automatically. Physical hardware and remaining setup checks are covered in Status. The name describes the one-minute goal, not a measured performance claim.

> **Deployment erases the selected internal disk without a confirmation prompt.** Arming a machine is authorization to reimage it. Start with a disposable VM, use your own boot assets and Windows image, and set `M2W_TOKEN` before allowing network access (since 0.2.0 the server refuses to listen on the network without it).

![The console after a machine booted and reported: nothing armed, one deployment with each phase's lap time.](docs/assets/screenshots/deployed-desktop.png)

*Demo data from the automated UI test: the MAC, the QEMU machine and the 58.1 s lap times are fixtures, not a measured deployment.*

## What you get

- **One deployment console:** a single server binary with a built-in web UI and JSON API. Linux amd64/arm64 and signed Windows x64 release builds.
- **One arm, one deployment:** an arm is consumed when iPXE requests its boot script. Unarmed clients receive no deployment script; arms expire and are dropped on server restart.
- **Optional built-in PXE:** proxyDHCP and read-only TFTP for armed machines, alongside your normal DHCP server. Existing dnsmasq/iPXE and USB boot setups work too.
- **Image verification and logs:** a manifest specifies the image URL, SHA-256, edition, disk requirements, and bootstrap options. Deployment phases and timings appear on the board.
- **A replaceable engine:** WinPE downloads the deployment scripts at boot, so updating them does not require rebuilding WinPE unless its boot configuration changes.

## Try the console

Download and extract the server archive for your platform from [Releases](https://github.com/spilloid/minute2win/releases). Verify `SHA256SUMS` first. Run from the extracted folder containing the binary and `manifests/`.

On Linux x86_64 you can instead download `minute2win-server-X-x86_64.AppImage`, `chmod +x` it and run it with the same flags (`-data` defaults to `./data` in the directory you start it from). It is not code-signed; rely on `SHA256SUMS`.

Linux:

```bash
export M2W_TOKEN='replace-with-a-long-random-token'
./minute2win-server -listen 127.0.0.1:8080 -open=false
```

Windows PowerShell:

```powershell
$env:M2W_TOKEN = 'replace-with-a-long-random-token'
.\minute2win-server.exe -listen 127.0.0.1:8080
```

Open <http://localhost:8080>. A loopback listener lets you inspect the console without serving boot scripts to other machines. To deploy, follow [Getting started](docs/GETTING_STARTED.md) for your own manifest, boot files, reachable LAN address, and boot route.

**Changed in 0.2.0 (breaking defaults):** the server listens on `127.0.0.1:8080` by default and refuses to listen on the network without `M2W_TOKEN` (`-insecure-no-token` overrides that for an isolated lab); it refuses arming and disarming requests from web pages on other origins; and there is no default image or boot-asset host, so nothing can be armed until `-image-base` points at your own boot files and the profile at your own image. The included profile is a template the server will not arm. With 0.1.2 and earlier, keep the token set even on loopback and pass `-image-base` explicitly: those builds default to every interface and to a restricted maintainer host. See the [changelog](CHANGELOG.md) for upgrade steps.

## How deployment works

```text
Arm MAC + profile → iPXE requests /boot/<mac> → arm consumed
  → wimboot loads WinPE + manifest → loader downloads engine
  → validate image and capacity → select disk → abort countdown
  → partition → download and verify SHA-256 → apply Windows → bcdboot
  → copy logs → reboot → Windows Setup → first-boot bootstrap
```

The engine runs only in WinPE. It chooses the lowest-numbered eligible internal disk, excludes USB and file-backed virtual disks, and places SD/MMC after fixed disks. A ten-second abort countdown is the default. These guards do not replace checking which machine you arm. The static `boot/minute2win.ipxe` route deploys on every boot that reaches it; use it only behind a per-boot opt-in.

## Guides and reference

| Task | Guide |
| --- | --- |
| Set up a full lab on one Windows PC, step by step | [Walkthrough](docs/WALKTHROUGH.md) |
| Set up a deployment host | [Getting started](docs/GETTING_STARTED.md) |
| Build WinPE and stage boot files | [Boot assets](docs/BOOT_ASSETS.md) |
| Configure images and deployment profiles | [Manifest reference](docs/MANIFESTS.md) |
| Configure the server, PXE, TFTP and Windows host | [Server reference](docs/SERVER.md) |
| Automate arming and inspect state | [API reference](docs/API.md) |
| Diagnose a failed deployment | [Troubleshooting](docs/TROUBLESHOOTING.md) |
| Understand verified behavior and limitations | [Status](docs/STATUS.md) |
| Build, contribute and release | [Contributing](CONTRIBUTING.md) · [Releasing](docs/RELEASING.md) |

## Scope

Minute2Win is for operators who have already decided to reimage a machine. It is not yet a validated production deployment system. Windows images, WinPE, wimboot and iPXE binaries are not included. Domain/Entra/hybrid joins are not implemented. Secure Boot and physical-machine PXE remain unverified. See [Status](docs/STATUS.md) before a deployment.

The project uses the [MIT license](LICENSE); bundled third-party notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Windows licensing and the licenses of boot components you supply remain your responsibility.
