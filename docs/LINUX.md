---
title: Linux network installation
---

# Linux network installation

The source tree adds experimental Debian, Ubuntu Server, Debian-based Proxmox and Bazzite profiles. They use the same console, MAC arming, proxyDHCP/TFTP and one-time iPXE ticket as Windows. Linux does not require WinPE, the ADK, wimboot or a Windows image host.

**Verification limit:** server tests and simulated disk-policy tests are available; no Linux installer has completed a VM or physical-machine deployment in this project's verification record. Start with a disposable x86_64 VM, Secure Boot disabled, an empty internal disk and a DHCP-enabled NIC. The native Proxmox ISO installer, ZFS/RAID layouts, disk encryption, ARM and Secure Boot are outside this implementation. The existing Windows VM evidence does not validate Linux.

Arming authorizes erasing one eligible disk, without another prompt. Debian and Ubuntu perform a fresh installation using their own installer; these are not persistent diskless desktops or arbitrary ISO chainloading.

## Gather and verify the files

Create a dedicated `linux-assets` directory. All referenced files must be directly inside it; the server confines access with `os.OpenRoot` and exposes only the files selected by an active deployment. Do not put unrelated files or secrets there.

| Installer | Required files |
| --- | --- |
| Debian | Matching `linux` and `initrd.gz` from one amd64 Debian **bookworm** or **trixie** netboot build, renamed `debian-linux` and `debian-initrd.gz` |
| Ubuntu Server | A verified live-server amd64 ISO, plus `casper/vmlinuz` and `casper/initrd` extracted from that exact ISO, renamed `ubuntu-server.iso`, `ubuntu-vmlinuz` and `ubuntu-initrd` |
| Proxmox | Debian trixie netboot files plus a reviewed, host-specific `proxmox-firstboot.sh`, described below |
| Bazzite AMD/Intel KDE | `images/pxeboot/vmlinuz`, `images/pxeboot/initrd.img` and `images/install.img` from one verified Fedora 45 Everything netinstall ISO, renamed `bazzite-linux`, `bazzite-initrd.img` and `bazzite-stage2.img`, plus a pinned Bazzite amd64 container digest |

Obtain files from the distributions' official sites. Verify upstream signatures and checksums before computing the hashes for your profile. A locally calculated checksum alone does not prove a download's authenticity. See Debian's [network installation guide](https://www.debian.org/distrib/netinst.en.html) and [installation-image verification](https://www.debian.org/CD/verify), and Canonical's [server netboot guide](https://ubuntu.com/server/docs/how-to/installation/how-to-netboot-the-server-installer-on-amd64/) and [ISO verification guide](https://ubuntu.com/tutorials/how-to-verify-ubuntu).

For Debian, the release-specific amd64 installer directories are under `https://deb.debian.org/debian/dists/<suite>/main/installer-amd64/current/images/`. Take the kernel and initrd from `netboot/debian-installer/amd64/`, using the same build and its published checksum/signature files. The configured suite must match the boot assets. Ubuntu's kernel, initrd and ISO must match each other; mixing releases does not work. Ubuntu downloads its ISO into the live environment, so provision enough VM RAM for the ISO and installer (8 GiB is a starting point, not a validated minimum).

Calculate SHA-256 after verification. In PowerShell:

```powershell
Get-FileHash .\linux-assets\debian-linux -Algorithm SHA256
Get-FileHash .\linux-assets\debian-initrd.gz -Algorithm SHA256
```

On Linux, use `sha256sum linux-assets/*`.

## Create a profile

Copy the appropriate template from `manifests/examples/debian.json`, `ubuntu.json` or `proxmox.json` directly into your own profiles directory, for example as `profiles/lab-debian.json`. Examples remain hidden from the profile selector until copied. Replace every checksum placeholder and set `authorizedKeys` to your public SSH key. Never include a private key or password. Save JSON as UTF-8 without a BOM.

The top-level `installer` is `debian`, `ubuntu`, `proxmox` or `bazzite`. Omitting it, or setting `windows`, retains the Windows manifest format. Unknown installer names are refused.

| `linux` setting | Behavior |
| --- | --- |
| `kernel`, `initrd` | Required `{ "file": "plain-filename", "sha256": "64 hex digits" }` objects |
| `iso` | Required for Ubuntu only; the same file/hash object format |
| `stage2` | Required for Bazzite only; the Fedora installer runtime, using the same file/hash object format |
| `containerImage` | Bazzite only: `ghcr.io/ublue-os/bazzite@sha256:<amd64 digest>`. Mutable tags and other image variants are refused |
| `desktopAutologin` | Bazzite KDE only, default false. Automatically opens the configured user's Plasma session; SSH still requires its key |
| `firstBoot` | Optional pinned shell script; required for Proxmox. Runs as root in the installed OS |
| `suite` | Debian `bookworm` or `trixie`; Proxmox requires `trixie`. Not used by Ubuntu |
| `hostname` | Optional lowercase DNS hostname. Default `m2w-<MAC without colons>`. Proxmox requires an explicit FQDN |
| `username` | Non-root local administrator, default `m2w` |
| `authorizedKeys` | 1–32 plain public SSH keys, without authorized_keys options. Password SSH login is disabled |
| `minSizeGB` | Minimum disk capacity in GiB, default 64 (range 8–1048576) |
| `countdownSeconds` | Abort countdown before partitioning, default 10 (range 0–300). Ctrl-C interrupts the guard |

The generated account has passwordless sudo. Root and user passwords are locked; use your SSH key for access. This is deliberate for unattended installs. For console login or Proxmox `root@pam` web login, set a password after installation over SSH, for example `sudo passwd root`. There is no shared default password.

The server checks the complete SHA-256 of every selected file before accepting an arm, including Ubuntu's ISO and any first-boot script. It captures the validated Linux profile in memory when arming: edits affect subsequent arms only. Each download rechecks its full file before sending any bytes, including range requests. Keep assets immutable during active deployments. Hashing a large ISO adds disk I/O and delays both arming and download startup; this implementation has no asset cache or one-minute Linux performance claim. Errors in the profile, missing assets or incorrect hashes refuse arming. A later asset change stops delivery; depending on when it happens, the native installer may already have partitioned the target.

## Start the server and deploy

Build the server from this source tree. On Windows, from the repository root:

```powershell
Push-Location server
go build -o minute2win-server.exe .
Pop-Location
$env:M2W_TOKEN = 'replace-with-a-long-random-token'
.\server\minute2win-server.exe -listen :8080 -manifests .\profiles -linux-assets .\linux-assets -pxe -pxe-dir .\ipxe -open=false
```

On Linux:

```bash
(cd server && go build -o minute2win-server .)
export M2W_TOKEN='replace-with-a-long-random-token'
./server/minute2win-server -listen :8080 -manifests ./profiles -linux-assets ./linux-assets -pxe -pxe-dir ./ipxe -open=false
```

Provide your own iPXE binaries in `ipxe/` as explained in the [server reference](SERVER.md). Built-in PXE needs access to UDP 67/4011/69 and HTTP TCP 8080, and appropriate OS privileges. If you already run a PXE service, omit `-pxe -pxe-dir ...` and have iPXE chain `http://<server>:8080/boot/${net0/mac}`. Use a trusted deployment LAN. The server-to-target HTTP traffic, including boot capabilities and scripts, is not protected against a hostile LAN; file hashes protect the server's local assets, not the transport. DHCP and distribution package mirrors must remain reachable.

Open the console, choose your Linux profile, arm the checked disposable VM's MAC and network boot it. One ticket is consumed, exactly as for Windows. After install, further network boots are unarmed and fall back to the local disk. Keep the server running until the installed system reports completion. For a mixed Windows/Linux server, supply both `-image-base` and `-linux-assets`.

## Disk guard and progress

Before the native installer partitions, the Linux guard chooses the first qualifying disk in the shell's filename order, preferring ordinary disks over MMC. It excludes removable media, USB ancestry, detected iSCSI/FC ancestry, optical/loop/device-mapper/md/network block-device names, disks with active holders, mounted partitions and active swap. A qualifying disk must meet `minSizeGB`. Virtio disks are eligible, so disposable VMs work. There is no mandatory disk-selection prompt. Remove any other disk you cannot afford to lose.

The guard runs a countdown, then passes the selected path to Debian's partman, Ubuntu's storage layout or Bazzite's Kickstart disk include. Ubuntu begins with an impossible disk match so a missing guard cannot silently select a default disk. Debian blocks the installer if its guard fails. Bazzite's pre-install hook stops on failure and generates disk commands only after the guard succeeds. Each installs a single-disk layout; they do not clone Windows partitioning rules. Existing active LVM/RAID holders can cause all disks to be refused; deactivate those manually in a disposable environment before retrying.

The board receives coarse `guard`, `install`, `reboot`, `firstboot`, `complete` or failure reports. Individual package installation steps and per-phase lap timing are not instrumented. Completion means the installed OS actually ran its first-boot service and any configured custom script returned success. Reporting is best effort; if the server is unavailable, inspect `/var/log/minute2win-firstboot.log` and `journalctl -u minute2win-firstboot` on the target. Debian failures outside the provided hooks may leave the board at the last phase; inspect the installer console and logs.

Linux boot files and generated configuration are gated by the deployment's random capability, work only while the deployment is booting/running, and expire three hours after boot. The first report does not close seed delivery. Restarting the server intentionally drops the Linux boot-delivery snapshot, so an unfinished install may fail and need re-arming. An already installed first-boot service has all scripts it needs locally. Old capabilities stop working after completion, failure, expiry or re-arming followed by another boot.

## Bazzite AMD/Intel KDE

This profile uses Fedora 45 Anaconda's native `ostreecontainer` payload to install the official Bazzite desktop image. It supports the AMD/Intel KDE variant, including Radeon RX 6600 hardware. NVIDIA and GNOME variants are outside the current profile. A VM boot verifies the installer path; it does not verify a physical GPU or its gaming performance.

1. Download the Fedora 45 Everything x86_64 netinstall ISO from [Fedora](https://fedoraproject.org/server/download). Verify its signed checksum file using Fedora's official signing keys. Extract the three files listed above from that same ISO and calculate their SHA-256 hashes.
2. Resolve the official `ghcr.io/ublue-os/bazzite:stable` multi-platform manifest to its **linux/amd64** manifest digest. Record the image version and digest. Use that immutable reference in `containerImage`; arming validates its syntax, while Anaconda fetches and verifies the container content during installation. The server does not pre-download or verify its signature at arm time. Keep GHCR reachable from the target. The tested image has about 4.45 GB of compressed layers, separate from the installer downloads.
3. Copy `manifests/examples/bazzite.json` into your profiles directory. Set the three asset hashes, digest and your public SSH key. Use a disposable UEFI VM with Secure Boot disabled, 8 GiB RAM and an empty 80 GiB internal disk for initial testing. These are test settings, not measured minimum requirements.
4. Arm and network boot as usual. Anaconda downloads its runtime through a capability-gated `.treeinfo`, partitions only the guarded disk with its Btrfs default layout, installs the pinned container and reboots. The post-install hook configures signed stable updates using the same `bootc switch --mutate-in-place --enforce-container-sigpolicy` operation as the [upstream Bazzite installer](https://github.com/ublue-os/bazzite/blob/main/installer/titanoboa_hook_postrootfs.sh).

With `desktopAutologin: true`, KDE opens automatically as `m2w` (or your configured username). Passwords remain locked and the account has passwordless sudo. Set a personal password over SSH with `sudo passwd m2w` if you want console authentication or to disable automatic desktop login. There is no shared starter password. Keep other disks disconnected when reimaging a gaming PC.

Installer failures are reported best effort. Inspect `/tmp/minute2win-pre.log`, Anaconda's `/tmp/*log` files and the installed system's `/root/minute2win-install.log` when diagnosing a failed install. The physical machine remains unarmed until you explicitly arm its MAC.

## Proxmox through Debian

This path installs Debian trixie and converts that fresh system to Proxmox VE using your pinned first-boot script. Proxmox documents [installation on top of Debian](https://pve.proxmox.com/wiki/Install_Proxmox_VE_on_Debian_13_Trixie) as an advanced alternative to its recommended ISO installer. It does not provide the native Proxmox installer's ZFS or RAID layouts.

1. Copy `templates/linux/proxmox-firstboot.sh` into `linux-assets/`. Edit **every** `CHANGE_ME`: FQDN, static IPv4 CIDR, gateway, DNS server, physical NIC name and the current official keyring SHA-256. Match the FQDN in the profile. Use a unique static IP outside the DHCP pool or reserve it appropriately.
2. Review the script for that host. It configures a single uplink bridged as `vmbr0`, uses the no-subscription repository and overwrites the fresh system's hosts/network files. Adapt it before pinning if your network or subscription needs differ. The first boot needs working DHCP to fetch packages before switching to the configured static bridge on reboot.
3. Pin the edited script's SHA-256 as `linux.firstBoot.sha256`, plus the Debian kernel/initrd hashes, then deploy the Proxmox profile.
4. The script installs the Proxmox kernel, returns **75** to request a reboot, checks that the running kernel ends in `-pve`, installs the remaining packages and checks core services. The wrapper keeps the deployment running across that reboot. Once done, log in using the SSH key and configure credentials, subscriptions and cluster/storage settings. The Debian fallback kernel is retained until you validate the host and follow upstream cleanup instructions.

Any `linux.firstBoot` script follows the same contract: run noninteractively under `/bin/sh -e` as root, return 0 for success, 75 for a requested reboot after saving its own stage, or another status for failure. The script and report credentials are stored root-only and removed after success. A failed custom script can retry on a later boot, so make it safe to resume. A successful deployment does not rerun it.

## Validate on a VM

For each installer, record firmware mode, exact asset versions/hashes, RAM and virtual storage/network devices. Verify the guarded disk choice, unattended installation, installed-system SSH access, status reports, custom-script behavior and an unarmed reboot. Test a wrong hash, an undersized disk and a USB/removable disk as negative controls. For Proxmox, also verify the kernel reboot, static bridge, web login, virtualization and package repositories. Record evidence in [Status](STATUS.md) before treating any of these as boot-verified.
