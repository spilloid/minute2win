# minute2win

**Arm a machine. Network boot it. Install Windows.**

Minute2Win is a self-hosted Windows deployment toolkit: a small Go server with a web console, an iPXE boot chain, and a PowerShell engine that installs Windows from a verified image. Arm a MAC address with a deployment profile, watch each phase on the live board, and keep deployment logs after reboot.

[Download releases](https://github.com/spilloid/minute2win/releases) · [Getting started](docs/GETTING_STARTED.md) · [Documentation](docs/index.md) · [Known issues](docs/STATUS.md)

**Current code version: 0.1.2. Early release.** Server logic and deployment helpers are unit-tested; the web console is tested with Playwright. The PXE front end has been observed in a QEMU BIOS iPXE simulation. **A Windows installation has not yet been run end to end.** The name describes the one-minute goal, not a measured performance claim.

> **Deployment erases the selected internal disk without a confirmation prompt.** Arming a machine is authorization to reimage it. Start with a disposable VM, use your own boot assets and Windows image, and set `M2W_TOKEN` before allowing network access.

## What you get

- **One deployment console:** a single server binary with a built-in web UI and JSON API. Linux amd64/arm64 and signed Windows x64 release builds.
- **One arm, one deployment:** an arm is consumed when iPXE requests its boot script. Unarmed clients receive no deployment script; arms expire and are dropped on server restart.
- **Optional built-in PXE:** proxyDHCP and read-only TFTP for armed machines, alongside your normal DHCP server. Existing dnsmasq/iPXE and USB boot setups work too.
- **Image verification and logs:** a manifest specifies the image URL, SHA-256, edition, disk requirements, and bootstrap options. Deployment phases and timings appear on the board.
- **A replaceable engine:** WinPE downloads the deployment scripts at boot, so updating them does not require rebuilding WinPE unless its boot configuration changes.

## Try the console

Download and extract the server archive for your platform from [Releases](https://github.com/spilloid/minute2win/releases). Verify `SHA256SUMS` first. Run from the extracted folder containing the binary and `manifests/`.

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

Open <http://localhost:8080>. A loopback listener lets you inspect the console without serving boot scripts to other machines. To deploy, follow [Getting started](docs/GETTING_STARTED.md) for your own manifest, boot files, reachable LAN address, and boot route. The included profile and default URLs reference a restricted maintainer host; public users must replace them.

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
| Set up a deployment host | [Getting started](docs/GETTING_STARTED.md) |
| Build WinPE and stage boot files | [Boot assets](docs/BOOT_ASSETS.md) |
| Configure images and deployment profiles | [Manifest reference](docs/MANIFESTS.md) |
| Configure the server, PXE, TFTP and Windows host | [Server reference](docs/SERVER.md) |
| Automate arming and inspect state | [API reference](docs/API.md) |
| Diagnose a failed deployment | [Troubleshooting](docs/TROUBLESHOOTING.md) |
| Understand verified behavior and limitations | [Status](docs/STATUS.md) |
| Build, contribute and release | [Contributing](CONTRIBUTING.md) · [Releasing](docs/RELEASING.md) |

## Scope

Minute2Win is for operators who have already decided to reimage a machine. It is not yet a validated production deployment system. Windows images, WinPE, wimboot and iPXE binaries are not included. Domain/Entra/hybrid joins are not implemented. Secure Boot and real firmware PXE remain unverified. See [Status](docs/STATUS.md) before a deployment.

The project uses the [MIT license](LICENSE); bundled third-party notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Windows licensing and the licenses of boot components you supply remain your responsibility.
