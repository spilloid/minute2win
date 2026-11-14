# Experimental Linux installer checks, 2026-11-13

Environment: Windows amd64, Go 1.27.0, Windows PowerShell and Git for Windows Bash. Go cache and temporary test files were placed in the writable workspace. No Linux installer image was booted and no disk was imaged during this work.

Passed:

- Native `go vet ./...` and the full `go test ./...` server suite.
- The full native `go test -race ./...` suite.
- Linux amd64 server cross-build; Linux arm64 vet and server cross-build, with CGO disabled. These validate the server binary, not target OS architecture support.
- `tests/site-version.sh` (source version 0.3.0) and `git diff --check`.
- New HTTP regressions cover Debian/Ubuntu/Proxmox boot routing without WinPE, arm consumption, invalid profiles/assets, configuration snapshots, changed hashes, range requests, capability expiry, closure after completion and closure after server restart. The Windows manifest route refuses Linux deployments, including after reload, so it cannot disclose the Linux capability.
- Generated guard, finish and first-boot shell scripts, Debian installer hooks and the Proxmox example pass shell syntax checks.
- The actual read-only disk-selection helper ran against synthetic sysfs/device/mount/swap fixtures, with ordinary files standing in for block-device nodes. Internal selection, removable/USB exclusion, capacity, mounted/swap disks, active holders, MMC fallback and missing mount-table refusal passed. This is policy coverage, not hardware discovery validation.
- The actual first-boot wrapper ran in temporary directories with reporting and `systemctl` stubbed: success, failure, a requested reboot followed by success, cleanup and no repeat after completion passed. No real reboot or package installation occurred.

One existing PXE diagnostic timing test (`TestRecheckRejectionSurvivesANoiseFlood`) failed once in an initial full run, then passed three isolated reruns and the final full suite. No unrelated PXE code was changed. The symlink-escape test was skipped on Windows because this sandbox lacks symlink-creation privilege; it remains available to Linux CI and does not substitute for a platform run here.

Not run locally: a Debian/Ubuntu unattended VM installation, Proxmox kernel/package conversion, physical disk discovery, Secure Boot, Linux runtime/race checks, the PXE LAN VM harness, Playwright or the unchanged PowerShell deployment checks. This host has no configured Linux VM harness/WSL. The PR CI runs the cross-platform server, PowerShell, UI, script and AppImage checks before merging.

## Scoped independent review

At the operator's request, Astra/high reviewed only `server/linux.go`, `server/linux/*.sh` and their integration in `server/server.go`, focusing on Debian live-test safety, boot/configuration generation, SSH setup and deployment lifecycle. It found one proven P1: a disk backing a mounted mapped volume could qualify when the active holder was on a partition instead of the whole disk. The orchestrator independently reproduced it, added a failing regression and fixed the helper to inspect both whole-disk and child-partition holders. Astra confirmed the original fixture now selects no disk, ran the disk-policy suite successfully and returned "SHIP to the controlled Debian live test; no remaining proven blocker in this scoped review." This is not evidence that an installer has booted, nor a review of unrelated workflows or UI.

The Linux feature is experimental. Follow [the Linux guide](../LINUX.md) to provide verified assets and a disposable target; collect installer logs, installed-system SSH access, first-boot status and an unarmed reboot before recording end-to-end support.
