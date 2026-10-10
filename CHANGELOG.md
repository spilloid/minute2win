# Changelog

## [Unreleased]

## [0.1.0] - 2026-10-10 (unit-tested, never booted)

First version. It grew from the Windows 11 network-install scripts staged in R2 on 2026-10-05 (`install.ps1`,
`build-winpe.ps1`, the OOBE answer file), which never ran.

- Zero-touch: the deploy runs without prompting. The disk choice is deterministic (lowest-numbered eligible internal disk,
  never USB), with an any-key abort countdown and a WinPE-only check. This replaces the WIPE prompt and the `-Yes` flag, which is
  still accepted so kvm-it's existing scripts keep working.
- Manifest-driven (`manifests/*.json`): image URI and required SHA-256, edition by name, disk sizes, optional OOBE admin,
  join method, and the guard. iPXE can choose the manifest per machine.
- The engine is downloaded at boot by a loader at the old `install.ps1` URL, so the WinPE already in R2 can run it.
- Drive letters for the new layout are chosen at runtime from every letter in use (the old script hard-coded D:, which the boot
  USB stick or an optical drive often holds; diskpart would fail right after the wipe).
- Checks run before the wipe, so a bad setup changes nothing: the image is reachable (a 1-byte ranged GET), it fits the staging
  partition, and the disk has room for Windows.
- Logs survive the reboot (`C:\Windows\Logs\minute2win`). A first-boot bootstrap runs once via `SetupComplete.cmd`.
- WinPE build: TLS 1.2 forced, startnet auto-runs the deploy (`-Manual` for a prompt instead), optional driver injection.
- Every phase is timed (`timing.json`), for the one-minute target.
- `minute2win-server` (Go, one binary, web UI built in). Arm a MAC with a profile; the boot script consumes the arm (one arm is one
  wipe, so there's no reboot loop) and fails closed if that can't be saved durably; unarmed machines boot their own disk; arms expire.
  Reports need a per-deployment key. If saving state fails and can't be undone, the server degrades (no deploys or arms) rather than guess. Arms do not survive a server restart (re-arm what you still want); deployment history does. A live board shows phases and a stopwatch
  against 60 s. An optional `M2W_TOKEN` guards arming.
- The web UI is htmx (vendored 2.0.11, 0BSD) over server-rendered `html/template` fragments, with no hand-written app JavaScript.
- Pester and PSScriptAnalyzer (including PowerShell 5.1 syntax) in podman and CI, Go tests, a Playwright UX check, and a VM end-to-end harness built on kvm-it's.
