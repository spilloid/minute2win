# Windows server validation — 2026-10-11

Tested from an isolated lab source copy on a Windows 11 QEMU/Q35 guest, using Go 1.27.1 windows/amd64. No existing OS disk was reimaged.

## Failure reproduced on 0.1.1

Native `go test ./...` failed across arming, persistence, PXE and UI tests. The shared cause was directory `File.Sync` returning `Access is denied` after the state-file rename. Rollback hit the same failure, so the server entered degraded mode and refused further arms.

## Fix and evidence

- Flush the temporary state file as before. On Windows, replace it using MoveFileExW with REPLACE_EXISTING and WRITE_THROUGH flags. On other platforms, retain rename followed by parent-directory sync.
- All native Windows server tests and `go vet ./...` passed after the fix, including the new test for repeated replacements under a Unicode directory/filename, retained deployment history and dropped arms on restart.
- Linux `go vet ./...` and `go test -race ./...` passed. PowerShell unit tests passed 47/47; compatibility lint passed. Workflow lint and mocked signing/release-script tests passed.
- Playwright console flow passed against the fixed Linux cross-build (arm, boot, report, disarm, desktop/mobile and light/dark). The documentation site also builds with the exact GitHub Pages action image.
- In-session Windows runtime check: console GET returned 200, state showed tokenRequired=true and degraded=false, an unauthenticated arm returned 401, and a synthetic MAC could be armed and disarmed with the token.
- A disposable Proxmox x64 UEFI/Q35 VM subsequently booted through the Windows-hosted proxy DHCP and TFTP service into iPXE, wimboot and automatically started WinPE. Details follow below.

Windows MoveFileEx behavior is documented by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw). No power-loss durability test was run; arms are discarded on Load regardless of persisted contents.

## Firmware and WinPE run

Test target: newly created VM 100 on the Proxmox host at 192.168.68.3, 4 GB RAM, two host CPU cores, an 80 GB SATA disk, e1000e NIC with synthetic MAC 02:00:00:00:02:11, OVMF/Q35, Secure Boot disabled. Existing host disks and other VMs were not modified. Kubert (192.168.68.227) served the isolated lab on TCP 18080/18081 and standard PXE/TFTP UDP ports; its existing Windows disk was not reimaged.

- Unarmed firmware DHCP capture: four client discoveries and four ordinary router offers, zero lab proxy offers, zero capture drops. This is a real UEFI firmware check, distinct from the earlier simulated BIOS iPXE LAN test.
- Armed firmware capture/logs: x64 arch 7 proxy offer, TFTP delivery of BOOTX64.EFI and shim's ipxe.efi, then an iPXE HTTP boot-script offer. Capture had zero drops. No port 4011 request occurred, so that path remains unverified. Windows accepted lower-case ipxe.efi for an upper-case staged filename; case-sensitive hosts still require matching filenames. Secure Boot was disabled, so the signed-chain behavior was not verified.
- Rebuilt WinPE with this repository's ADK builder and the isolated lab URL. It automatically fetched and ran the loader.
- The first automatic run exposed missing UEFI manifest injection. The loader used its implicit fallback, leaving report.url/key absent. Fixed both iPXE scripts to explicitly name the manifest (`initrd --name manifest.json`) and made missing injection stop the loader unless a static profile is explicitly supplied with `-Manifest`. After this fix, the VM reported authenticated progress against the selected arm.
- The first image download failed its checksum. Independent Windows HTTP streaming and the local source image both matched the pinned hash. A fresh corrected boot downloaded and verified the image successfully; the hash check was retained. The cause of the first bad transfer is unresolved.
- The corrected default-profile run completed partitioning, download/hash verification, Windows 11 Pro selection (index 6), DISM apply, bcdboot, bootstrap staging, staging partition removal and Windows partition expansion, then reported done and rebooted. The arm remained consumed; the Windows PXE log ignored the reboot's unarmed request. OVMF tried PXE and HTTP IPv4/IPv6 entries with long timeouts; after observing the ignored request, the lab VM was changed to disk-first for installed-OS verification.

Engine cumulative timing from the default-profile board report: ready 17.3 s; partitioned 23.9 s; downloaded 176.9 s; applied 353.1 s; done 359.4 s. This VM and remote image host do **not** establish a one-minute deployment claim. The installed disk booted Windows 11 build 26300.9457 (observed in the setup diagnostic console). The default profile showed a long OOBE spinner, then reached the normal country-selection screen. Installed-OS log collection confirmed firstboot.done exists and firstboot-result.json has status ok for Microsoft Windows 11 Pro 10.0.26300, with join: none. This was automatic SetupComplete execution, not a manual bootstrap invocation. Completed user setup was not attempted for the default profile. The VM was stopped and a snapshot named default-profile-oobe preserved before testing the optional account profile. Setup restarted the first diagnostic console; log collection was retried at the stable country-selection screen.

The iPXE project documents [explicit image names](https://ipxe.org/cmd/imgfetch) and [wimboot file injection](https://ipxe.org/wimboot#injected_files).

## Test environment limits

Lab services ran only within a bounded live SSH session, with process and temporary firewall cleanup in finally. No persistent scheduled task was created. Installed Windows boot, normal OOBE and automatic bootstrap were observed on the default profile; the optional-account test is still underway; Secure Boot, OEM-key behavior, physical hardware and concurrent socket pressure remain unverified.

Installed-OS evidence also confirms three final GPT partitions: 272,629,760-byte system, 16,777,216-byte reserved and 85,608,873,472-byte Windows; the staging partition was removed. timing.json records sinceWinPEBoot 375.3 s. Plain deployment transcript, hardware inventory, timing, firstboot log and firstboot result were extracted to the lab evidence directory.

Machine-readable observed results: [VM evidence](2026-10-11-vm-results.json).

## Optional-account profile

A separate isolated lab profile selected oobe.adminUser=m2wlab, leaving the image/hash unchanged. The first staged file had a UTF-8 BOM from Windows PowerShell 5.1 Set-Content; the server rejected manifest delivery before WinPE installation and firmware returned to the previous Windows disk. It was rewritten as UTF-8 without BOM and deliberately re-armed. The corrected run completed imaging with ready 14.4 s, partitioned 21.0 s, downloaded 265.5 s, applied 452.9 s and done 456.6 s. Installed-OS account, cleanup and next-logon checks remain pending.
