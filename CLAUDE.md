# CLAUDE.md

minute2win: zero-touch Windows deployment from WinPE, plus `minute2win-server` (arm by MAC, iPXE boot scripts, live board).
Branding and goal: an NVMe machine on 1 GbE from network boot to flashed Windows in about a minute. Every phase is timed
(`timing.json`, the board) so that claim is measured, never asserted.

## Rules

- **Zero-touch is the design.** The engine wipes without asking. Don't add prompts or make disk selection mandatory. Guards
  must not need anyone at the machine: the opt-in boot entry (a server arm, consumed on boot), WinPE-only, the abort countdown,
  never USB.
- **WinPE runs Windows PowerShell 5.1.** No ternary, `??`, `-AsHashtable` or `&&`. The lint gate (`PSUseCompatibleSyntax` 5.1)
  catches syntax but not behaviour.
- Keep logic in `engine/Minute2Win.psm1`, pure where possible, with tests; `Start-Minute2Win.ps1` only orchestrates.
- Never claim a boot, deploy or hardware behaviour as verified without a VM run (`tests/vm/deploy-vm.sh`) or a named machine.
- Manifests never hold secrets. The OOBE baseline password is public by design and forced to change.
- kvm-it stays unaware of how Windows is installed; minute2win stays unaware of what booted it. kvm-it's harness is
  reused by path (`tests/vm/deploy-vm.sh`), not copied.

## Go

Stable latest, always (operator preference): CI and releases build with `go-version: stable`, and `server/go.mod`'s `go` directive tracks the current stable language version. Standard library first; a third-party dependency needs a reason, because the operator prefers rolling our own when it isn't too complex and avoiding transitive dependencies.

## Checks

| Command | What |
|---|---|
| `tests/run.sh` | Pester + PSScriptAnalyzer in podman (no pwsh on the Linux host) |
| `cd server && go vet ./... && go test ./...` | Server rules |
| `tests/ui/run.sh [dir]` | Playwright: arm, boot, report, disarm, phone width, light/dark; look at the screenshots |
| `tests/pxe/pxe-lan.sh` | Built-in PXE on a simulated LAN in podman: separate DHCP server, QEMU (BIOS iPXE ROM) three times; from tcpdump captures (no drops): boot 1 answered and arm consumed, boot 2's PXE DISCOVER seen and not answered, boot 3 re-armed and answered again (positive control) |
| `tests/vm/deploy-vm.sh` | Full chain in a VM (needs staged files uploaded, `localhost/win11-qemu`, ~40 GB free) |

Process: corporate-strategy standards. STD-001 (an astra review of non-trivial diffs, logged in `docs/dev-process.md`),
STD-002 (PR template), STD-003 (docs and version in the same change: `VERSION`, README, CHANGELOG), STD-006, STD-007, and
STD-008 (UI capture and overflow).

## Things to re-verify before trusting them

- 2026-10-10: **Nothing has booted.** WinPE over wimboot, the engine, SetupComplete and the OOBE answer file on build 26300
  have only been unit-tested.
- 2026-10-10: `wimboot` puts an initrd named `manifest.json` in `X:\Windows\System32`. That's from wimboot's documented behaviour,
  not observed here.
- 2026-10-10: `SetupComplete.cmd` is documented as not running when Windows was installed with an OEM product key (except on
  Enterprise). Check on a machine with a firmware OEM key.
- 2026-10-10: The WinPE `boot.wim` currently in R2 (built 2026-10-04) does not auto-run; its startnet leaves a prompt. Rebuild with
  `build/Build-WinPE.ps1` for zero-touch, or type the loader command (`tests/vm/deploy-vm.sh TYPE_AT=`).
- 2026-10-10: R2 `windows11/` is WAF-locked to the home IP. A deploy from elsewhere gets 403.
- 2026-10-10: **Built-in PXE (`-pxe`) is UNVERIFIED on a real PXE client.** Unit tests (crafted packets, loopback TFTP) pass, and
  `tests/pxe/pxe-lan.sh` saw QEMU's BIOS iPXE ROM on a bridged LAN with a separate DHCP server get the `/boot` URL, consume the arm, and
  be ignored on the next boot. Still to check, on a real network with a normal DHCP server: (1) UEFI firmware PXE (not iPXE): offer
  accepted with our option 43, the 4011 REQUEST/ACK, TFTP of `ipxe.efi` with blksize/tsize negotiation, then iPXE chaining to `/boot`;
  (2) BIOS firmware PXE with `undionly.kpxe`; (3) Secure Boot: `-pxe-file-x64 BOOTX64.EFI` with the signed shim, and which name the
  shim asks TFTP for its second stage (case matters); (4) that the real DHCP server and our proxy coexist (both offers seen, client
  takes the address from one and the boot file from the other); (5) a real 32-bit UEFI client is left alone without
  `-pxe-file-ia32`, and a client using option overload, if one can be found; (6) broadcast replies on a multi-homed Linux host leave on the right
  interface (seen once in the harness) and the Windows build's address choice and firewall advice. OVMF in the harness never tried a
  network boot, so (1) has not run anywhere.
- 2026-10-10: Built-in PXE on Windows only builds; nothing PXE has run on Windows. It can't learn the receiving interface there, so it
  needs one IPv4 address or `-pxe-ip`.
- 2026-10-10: The built-in PXE's write deadlines and log budgets were tested only with fakes and a simulated clock (a blocking
  connection, an injected writer); the capture-drop check in `tests/pxe/pxe-lan.sh` has only ever seen 0 drops, so its failure path
  has not run.
