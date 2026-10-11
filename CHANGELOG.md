# Changelog

## [Unreleased]

## [0.1.2] - 2026-10-11

- Fix Windows state persistence: directory sync returned Access is denied, causing the server to become degraded and reject every arm. The Windows replacement now uses MoveFileExW with replacement and write-through flags after flushing the temporary file; Unix keeps rename plus directory sync. Arms still never survive a restart.
- Name the injected manifest explicitly in iPXE for UEFI wimboot. The loader now refuses to run without injection unless a static profile is explicitly selected with `-Manifest`, preventing a missing armed profile from silently choosing the default.
- Run server tests natively on Windows in CI, with a regression covering repeated state replacement under a Unicode path, preserved deployment history and dropped arms on restart.

- Public documentation: focused README, setup and boot-asset guides, server/manifest/API references, troubleshooting, and explicit verification status.
- GitHub Pages documentation workflow with a responsive light/dark layout; PRs build without deployment privileges.
- Public contribution/security guidance and release preparation notes for tag protection and operator-owned image hosting.

## [0.1.1] - 2026-10-10 (Windows server build, signed releases, built-in PXE; still never booted a Windows deploy)

Everything below was adversarially reviewed (docs/dev-process.md). Signing is proven end to end with the manual `signing check`
workflow (a throwaway exe signed by `CN=Joseph Spillers`, timestamped by Microsoft, `Valid`); this is the first release to go through the
signing pipeline. Known issues: #2.

- Windows: the server builds for Windows (`minute2win-server-X-windows-x64.zip`), opens the UI in your browser on start, finds
  `manifests/` next to the executable, and prints the line targets should chain (`http://<this PC>:8080/boot/${net0/mac}`).
  With kvm-it's USB boot drive a laptop running it is the whole deploy console: the target only needs ordinary DHCP.
- Release pipeline: the Windows executable is **Authenticode-signed in CI** with Azure Artifact Signing (GitHub OIDC, no key
  or secret that can sign), the signature and timestamp are verified, and nothing is attached to a release unless every file passed;
  an unconfigured or failed signing step stops the release rather than publishing unsigned. A manual `signing check` workflow proves
  the setup without publishing. `scripts/setup-signing-azure.ps1` and `scripts/setup-signing-github.sh` wire it up (split so
  that no GitHub token has to be on the machine that holds the `az` login).
- CI cross-compiles for Windows and arm64 on every PR and runs the Go tests with the race detector.

- Built-in PXE front end for `minute2win-server`, opt-in with `-pxe` (off by default): a proxyDHCP responder (UDP 67 and 4011) and a
  read-only TFTP server (UDP 69, `-pxe-dir`), so the deploy server needs no dnsmasq. It never offers an address or lease options, and
  it answers only armed MACs: unarmed machines get silence and boot their disk, and once `/boot` consumes the arm the machine gets no
  more offers. Boot file by client architecture (`-pxe-file-x64`, `-pxe-file-bios`, `-pxe-file-arm64`); a client that is already iPXE
  gets the `/boot/<mac>` URL instead of iPXE again. Clear startup errors for privileged ports and for a port another DHCP server holds.
  Standard library only (no new dependencies). No iPXE binary is included: point `-pxe-dir` at your own.
- Built-in PXE hardened after adversarial review: the armed check is repeated just before each reply; DHCP option overload is read;
  the iPXE URL uses the HTTP listener's real port and its bound address; 32-bit x86 UEFI (arch 6) gets only the new `-pxe-file-ia32`
  (none by default) instead of the x64 file; options 93/94 are echoed; TFTP opens through `os.Root`, refuses all symlinks, caps
  transfers per client IP and their lifetime/idle time, accepts `timeout` only from 1 to 10 s, ignores duplicate requests, and stops
  promptly on shutdown; logging has a hard key cap and a global rate budget; unsupported platforms build but refuse `-pxe`.
- Built-in PXE, second review round: every TFTP send has a write deadline and a failed send frees the slot; error text is logged
  quoted; logging has separate budgets for policy lines and network noise, and suppressed lines are counted; `-pxe-ip` must be
  reachable on the HTTP listener (a specific bind must match it; loopback and IPv6-specific binds are refused with `-pxe`); OpenBSD
  joins the platforms where `-pxe` refuses to start. The LAN harness gained a re-armed third boot as a positive control.
  Status: unit-tested and run once in a QEMU VM on a simulated LAN (BIOS iPXE stage, `tests/pxe/pxe-lan.sh`); never run on real PXE
  firmware, UEFI firmware PXE + TFTP, or Secure Boot.

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
