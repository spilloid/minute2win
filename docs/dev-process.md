# Dev process log

Orchestrator (Claude) writes and verifies; a different model (astra, Codex `gpt-6-astra`) reviews adversarially.
Every finding is reproduced against source before it is accepted (corporate-strategy STD-001).

## 2026-10-10 — 0.1.0 first version

- Asked: a separate repo for Windows deployment that kvm-it can launch, then: zero-touch (wipes with no disk prompt), the
  minute2win name and the one-minute target, and a rudimentary GUI (web chosen: the arm list and the board belong on the deploy server).
- Seeded from: the 2026-10-05 R2 scripts (`install.ps1`, `build-winpe.ps1`, `unattend-oobe.xml`) and imaging-server-winpe's
  driver injection and dnsmasq pattern. kvm-it's VM harness is reused by path.
- Verified by running: Pester 32/32 and PSScriptAnalyzer (5.1 syntax, 0 warnings/errors) in podman; `go vet` and `go test`;
  a Playwright UX check against the real server. Screenshots were inspected, and that inspection found four defects the passing
  suite had missed (the token field shown when no token is required, laps listed in alphabetical order, low button contrast in
  dark mode, an empty table header). All four are fixed, and the first two now have assertions.
- Web UI moved to htmx (operator request) after the first Playwright pass. The same Playwright check, unchanged, passes against the
  htmx version. Go tests were added for the fragments: escaping of target-supplied text, the token on the htmx routes, and the MAC
  field cleared only on success.
- Adversarial review round 1: astra (`codex exec -m gpt-6-astra`, high, read-only, 96k tokens; the first dispatch died with its
  parent shell and was re-run). Ran while the UI moved to htmx; findings were checked against the current tree. 12 findings, all
  confirmed by reading source; #8 was also reproduced as a failing test first.

| # | Sev | Finding | Verdict |
|---|---|---|---|
| 1 | High | Drive-letter conflict found by diskpart only after `clean` | **Confirmed.** Fixed: free S/W/Y from other disks before diskpart (`Get-M2WLetterConflicts`, tested) |
| 2 | High | Build-WinPE ignores `/Add-Package` failures | **Confirmed.** Fixed: every DISM call is checked; the mount is discarded on failure |
| 3 | Med | SetupComplete line appended after a possible `exit`; OEM-key skip | **Confirmed.** Fixed: prepend. The OEM-key skip stays a dated item in CLAUDE.md (needs a machine with a firmware key) |
| 4 | Med | `-Base` not passed from startnet to the loader | **Confirmed.** Fixed |
| 5 | High | UI defaulted to `example` (placeholder URL, zero hash, no countdown): it would wipe, then fail to download | **Confirmed.** Fixed: moved to `manifests/examples/` (not offered). Also added: a HEAD check on the image **before** the wipe, so a dead URL changes nothing |
| 6 | High | Static `minute2win.ipxe` deploys on every boot, so a network-first loop is possible | **Confirmed; by design for the kvm-it path.** Documented in the script and README: use it only behind a per-boot opt-in, and use server arms for PXE |
| 7 | High | Ignored persistence errors could bring back a consumed arm after a restart | **Confirmed.** Fixed: `save` returns errors; boot fails closed (no deploy) if consumption can't be saved; arm/disarm roll back. Tested |
| 8 | High | iPXE command injection through a rejected MAC in `/boot/` | **Reproduced** (a test emitted `chain https://evil...` before `exit 1`). Fixed: constant script, request never echoed |
| 9 | Med | Anyone could forge reports | **Confirmed.** Fixed: a per-deployment key, delivered once in the manifest, hidden from `/api/state`; finished deployments can't be overwritten. Tested |
| 10 | Med | "Disarmed" shown when the machine had already booted | **Confirmed.** Fixed: the API returns 404 and the UI says "was not armed ... not cancelled". Tested in Go and Playwright |
| 11 | Med | Bookkeeping in `finally` could throw and stop the reboot | **Confirmed.** Fixed: each step isolated |
| 12 | Med | First-boot marker written even on failure | **Confirmed.** Fixed: marker only on success |

  Test grading (D for deploy assurance, C for server) accepted: an ordering test was added for the diskpart script, plus server
  restart/failure/injection/race tests. Still open and true: nothing exercises the engine on Windows, and `tests/vm/deploy-vm.sh`
  takes screenshots but asserts nothing and uses the static route.
- Round 2 (astra, 68k tokens), verifying the fixes: 7 of 11 closed. Not closed, or new:

| # | Sev | Finding | Verdict |
|---|---|---|---|
| r1#1 | High | An optical or other non-partition volume on S/W/Y still breaks `assign` after `clean` | **Confirmed.** Better fix: letters are now **chosen at runtime** from every letter in use (`Get-M2WUsedLetters`: logical disks, volumes, PS drives) and passed through diskpart and every path. Its test then caught a bug in my own picker (the system role took W when S was busy); fixed with two passes |
| new | High | State file not fsynced: a power cut could bring back a consumed arm | **Confirmed.** Fixed: fsync the file, rename, fsync the directory, fail closed |
| new | High | Too-small staging (or disk) fails only after the wipe | **Confirmed.** Fixed: `Test-M2WCapacity` checks the image size (from the probe) against staging, and the disk against staging plus Windows, before partitioning |
| r1#9 | Med | The first requester of the manifest gets the report key | **Partly fixed:** empty keys rejected; the manifest is served only while booting, for 10 minutes at most. **Residual risk, tracked:** someone on the LAN who knows the MAC can still race the target in that window and forge the board's outcome. This cannot arm or wipe anything |
| new | Med | The serve-once manifest broke iPXE retries | **Confirmed.** Fixed: served idempotently until the first report or 10 minutes |
| new | Med | HEAD fails on presigned GET URLs | **Confirmed** (not the shipped public URL). Fixed: a 1-byte ranged GET, which also yields the size |
| new | Med | The SetupComplete prepend re-encoded the existing script as ASCII | **Confirmed.** Fixed: a byte-level prepend, tested byte for byte |
| r1#3 | Med | SetupComplete is skipped on OEM-key installs | **Open, tracked:** needs a machine with a firmware key to verify any fallback |

- Round 3 (astra, 36k tokens), sign-off check: 5 of 7 round-2 items closed; **SIGN-OFF: no**, two High findings:

| # | Sev | Finding | Verdict |
|---|---|---|---|
| 1 | High | An unknown image size bypassed the capacity check, so a wipe could still precede a failed download | **Confirmed.** Fixed: the capacity check fails closed on an unknown size; the optional manifest `image.sizeBytes` supplies it when the server reports none. Tested |
| 2 | High | A failed directory fsync after a successful rename left the new arm on disk while the API said "nothing changed", so a restart could wipe | **Confirmed.** Fixed: on any save failure the mutation is undone and saved again; if that also fails the server is **degraded** (refuses to deploy or arm, says so in `/api/state`) until someone checks it. Tested |

- Round 4 (astra), final sign-off check: #1 (unknown image size) **CLOSED**. #2 **NOT CLOSED, confirmed**: a double fault (rename
  succeeds, directory sync fails, the restoring save also fails) leaves a stale arm on disk, and a restart loads it with `degraded`
  reset. Fixed more simply than persisting a flag: **arms no longer survive a restart** (dropped at load, logged; deployment history
  still persists), so a restart can only mean "re-arm what you still want". Test reproduces the exact sequence. Round 5 checks only this.
- Round 5 (astra): **CLOSED, SIGN-OFF: yes.** No new High/Critical. astra could not run the tests (read-only sandbox); the orchestrator ran them (below).
- Not verified: anything booted (WinPE, the engine on a real disk, SetupComplete, OOBE), and real hardware.

## 2026-10-10 — Windows build and signed-release pipeline (PR #3)

- Asked: a Windows client like PartnerCenterBridge's; strong CI/CD; signing set up through kubert.
- Found on inspection: signing here is Azure Artifact Signing over OIDC (D-0047), not keys on kubert; STD-006 forbids keys in CI. kubert
  holds an `az` login (its cached token has expired, so the Azure half is **not run yet**) but its `gh` token is invalid. So the
  setup script was split into an Azure half and a GitHub half and no GitHub token is copied to kubert.
- Verified by running: `go vet`, `go test -race`, Windows/arm64 cross-builds, CI green, actionlint clean, a mocked-`gh` test of
  the GitHub setup script (which fails on the pre-fix script, as it should), the asset allowlist step run against good and bad sets.
- Adversarial review round 1 (astra, 65k tokens), 6 findings, all confirmed:

| # | Sev | Finding | Verdict |
|---|---|---|---|
| 1 | High | Shell injection through the manual `tag` input (`v0.1.0$(...)` executed while producing a valid-looking tag) | **Confirmed** (astra reproduced it). Fixed: values enter through `env`, a strict `^v[0-9]+\.[0-9]+\.[0-9]+$` check, no expression interpolation into `run:` source |
| 2 | High | `actions/checkout` prefers a branch over a tag of the same name: a pushed branch `v0.1.0` could run its own scripts inside the signing job | **Confirmed.** Fixed: checkout `refs/tags/<tag>`, require the commit to be an ancestor of `main`, pin the SHA for the sign job |
| 3 | Med | Reruns could rewrite a published release (`--clobber`) and leave a mixed asset set | **Confirmed.** Fixed: per-tag concurrency, refuse a published release, replace a draft's whole asset set, allowlist the exact files |
| 4 | Med | Setup script compared rule names as regexes (`v*`): reruns recreated rules and deleted wanted ones | **Confirmed** (reproduced). Fixed: exact `(type, name)` matching in jq; test added |
| 5 | Med | A failed `gh variable/secret set` still printed "Done" | **Confirmed.** Fixed: every write checked; test added |
| 6 | Low | Advertised LAN URLs ignored the bound address | **Confirmed.** Fixed: derived from the bound address; test added |

- Not verified: the Azure setup and the `signing check` workflow have **not been run**; no signed binary exists yet; the Windows
  exe has never run on Windows.
- Round 2 (astra, run with the vendored codex 0.162.1 binary: the `codex` shim vanished mid-session in an upgrade and the first two attempts
  never read the repo, so they are not counted as reviews): #1, #2, #4, #5, #6 CLOSED. **#3 NOT CLOSED, confirmed**: the old-asset listing
  sat inside `for a in $(gh ...)`, where a failure does not trip `set -e`, so a failed listing went on to upload. Fixed with a checked
  assignment; `tests/release_workflow_test.py` now executes the real allowlist and upload steps (extracted from the YAML) against a mock `gh`
  and runs in CI.
- Round 3 (astra, vendored codex 0.162.1): finding #3 **CLOSED**, no new High/Critical, **SIGN-OFF: yes.** The Azure setup and the signing-check
  workflow still have not been run (kubert's `az` login must be refreshed), so the signed-release path is reviewed and fails closed, but unproven.

## 2026-10-10 — built-in PXE (`-pxe`), unreleased

- Asked: an opt-in proxyDHCP + read-only TFTP front end in minute2win-server so a deploy needs no dnsmasq, answering ARMED MACs only,
  never offering an address, no embedded iPXE. Then (operator decision mid-task): no third-party dependencies, standard library only.
- Built: `server/dhcp.go` (BOOTP/DHCP parse and proxy reply), `server/pxe.go` (policy, listeners, reply routing, rate-limited
  logging, bind-error advice), `server/tftp.go` (RRQ only, octet, blksize/tsize/timeout via OACK, bounded retransmit, one goroutine
  and ephemeral port per transfer, directory confinement), `server/sock_*.go` (SO_REUSEADDR/REUSEPORT on 67; Linux IP_PKTINFO to learn
  and answer on the receiving interface), flags in `main.go`, and `tests/pxe/pxe-lan.sh`.
- Decisions not in the brief: REQUESTs on UDP 67 are answered only when option 54 names us (others belong to the real DHCP server);
  option 66 (TFTP server) is added for TFTP file names as a hint for firmware that prefers it; option 54 is sent (needed for the 4011
  step); replies are padded to 300 bytes; a degraded server offers nothing (it would not deploy anyway); non-Linux hosts with several
  IPv4 addresses must set `-pxe-ip`; TFTP blksize is capped at 1468 and only octet mode is served; bind failures stop startup.
- Verified by running: `gofmt -l`, `go vet`, `go test -count=1 -race` (decision table per arch/armed/unarmed/expired/consumed/degraded,
  reply contents and destinations, garbage and truncated packets for both parsers, loopback end to end: arm, DISCOVER, 4011 ACK, TFTP
  byte-exact, iPXE URL, `/boot`, ignored afterwards; TFTP refusals, exact-512 and multi-block files, retry bound); 20 s and 10 s fuzz runs
  of the two parsers; cross builds for windows/amd64, linux/arm64, darwin. Mutation spot checks: ten deliberate bugs (armed check,
  expiry, iPXE detection, yiaddr, option 43, path confinement, end-of-transfer) each made a test fail. `tests/pxe/pxe-lan.sh`
  (MODE=bios) passed: QEMU's iPXE ROM on a bridge, with dnsmasq as a separate plain DHCP server, got the `/boot` URL advertising the
  bridge address on a two-interface host, the arm was consumed, and the second boot was ignored.
- Not verified: any real PXE firmware; UEFI firmware PXE and our TFTP serving a real client (OVMF in the harness never attempted a
  network boot); BIOS firmware PXE with `undionly.kpxe`; the Secure Boot shim chain; anything on Windows beyond compiling. No astra
  review has been run on this change yet (STD-001 still owed).

### PXE adversarial review round 1

astra (Codex `gpt-6-astra`) reviewed feat/pxe after the rebase onto main. 11 findings; the orchestrator adjudicated them (#1
narrowed, all others accepted). Each fix has a test that was checked by planting the old behaviour back and watching it fail.

| # | Sev | Finding | Adjudication | Change |
|---|---|---|---|---|
| 1 | High | Arm checked under `s.mu`, then released before the reply is built and sent | **Accepted, narrowed**: don't hold `s.mu` across the write | Re-check armed and not degraded immediately before the send, with a 250 ms write deadline. The remaining window is microseconds and harmless: an offer only names iPXE or the `/boot` URL, and `/boot` (decided under `s.mu`) is the only thing that deploys. Test: a hook consumes, disarms or degrades between the decision and the send; no reply |
| 2 | Med | Option overload (52) ignored, so options in `file`/`sname` were missed | Accepted | Parsed in RFC 3396 order (options, file, sname); a malformed overloaded field drops the packet. Tests: PXE options and iPXE user class found in file/sname, concatenation order, 7 malformed cases |
| 3 | Med | `-listen :0` advertised HTTP port 0 | Accepted | HTTP binds first; `pxeHTTPTarget` takes the port from the bound listener and advertises the bound IP when there is one (loopback or IPv6-only refused). Table test plus a real `:0` listener |
| 4 | High | Log rate-limit maps unbounded; attacker bytes logged raw | Accepted | Hard 4096-key cap with an O(1) reset, a global 20 lines/s (burst 50) budget with a "suppressed" count, `%q` for file names. Tests: 100k distinct keys stay under the cap and within budget; a file name with a newline is logged quoted (served and refused) |
| 5 | High | `timeout=255` let 64 RRQs hold every slot for ~25 minutes | Accepted | `timeout` accepted only for 1..10 s (echoed exactly, otherwise left out of the OACK); 60 s lifetime and 20 s idle limit regardless; at most 4 transfers per client IP as well as 64 in total; shutdown closes transfer sockets. Tests: negotiation table, per-IP cap, 64-slot saturation then recovery, idle limit, lifetime, close returns at once |
| 6 | Med | Check-then-open symlink race | Accepted | `os.OpenRoot` + `root.Open`; only the regular file at that name (Lstat + SameFile on the opened file), so symlinks are now refused even inside the directory. Test: a name flipped between a file and a symlink to an outside file 300 times never serves the outside file (the old-style open served it 3 of 3 runs) |
| 7 | Med | A duplicate RRQ started a second transfer | Accepted | Same client address + file while a transfer runs is ignored. Tests: duplicate RRQs give one TID; duplicate ACKs don't trigger a resend |
| 8 | Med | Arch 6 (IA32 UEFI) got the x64 file | Accepted | New `-pxe-file-ia32` (default empty: no offer); 7 and 9 stay x64 |
| 9 | Low | Options 93/94 not echoed (RFC 4578 section 2) | Accepted | Echoed; 94 only if sent |
| 10 | Low | SO_REUSEPORT hard-coded 0xf on all Linux (MIPS is 0x200); fallback compiled for Solaris etc. | Accepted | Linux file limited to amd64/arm64/386/arm/riscv64/ppc64le/s390x/loong64 (all 0xf, checked in the Go 1.27.1 syscall sources); fallback limited to darwin/freebsd/netbsd/openbsd (syscall defines SO_REUSEPORT there); a stub everywhere else makes `-pxe` fail at startup with a clear error. No runtime test is possible for the stub on this host; evidence is `go vet` + `go build` for linux/mips, solaris/amd64, freebsd/amd64, darwin/arm64, plan9/amd64, windows/amd64 and linux/arm64, plus builds for 17 more GOOS/GOARCH pairs |
| 11 | Med | `pxe-lan.sh` could certify "second boot ignored" with no evidence | Accepted | Fails on any setup/container/qemu failure; tcpdump on the bridge per boot; boot 2 must show the VM's PXE DHCPDISCOVER and zero replies from minute2win on UDP 67/4011. Run: PASS. With an always-armed server planted: FAIL ("minute2win replied on boot 2"), a case the old log-based check would have passed because the offer line was rate-limited |

Also added: a loopback end-to-end test that reads replies concurrently while arms flip (every reply well-formed, yiaddr 0).
Two planted bugs did not fail a test, and both were redundant checks rather than gaps: removing only the first duplicate-RRQ check
(the second, under the lock, still catches it), and opening with `os.Open` while the Lstat/SameFile check remains (that check alone still
refuses a swapped name). With both halves removed, each is caught.
Still not verified: everything listed in CLAUDE.md for real PXE firmware (UEFI and BIOS firmware PXE, TFTP to a real client, Secure
Boot, Windows).

### PXE adversarial review round 2

astra re-checked round 1: findings 1-3 and 5-11 **closed**, #4 not fully. It also confirmed new gaps in the fixes. The orchestrator
accepted all of them (A-F). As before, each fix has a test that fails when the old behaviour is planted back.

| # | Finding | Change | Planted-back check |
|---|---|---|---|
| A | TFTP writes had no deadline: a blocked UDP write bypassed the lifetime/idle limits, including the listener's refusals | Every write (DATA, OACK, ERROR, refusals) goes through a 2 s write deadline (capped by the lifetime); a failed or timed-out write ends the transfer and frees its slot. Transfer and listener sockets are injectable for tests | A connection whose writes block until their deadline: the slot is freed within the lifetime, and a blocked refusal doesn't stall the listener. Without the deadlines: both fail |
| B | Log injection via error text: `%q` on the name, but the `*PathError` (which holds the raw name) printed with `%v`. The round 1 test used a name with `:`, which is refused before the filesystem lookup | `errText` (strconv.Quote) for every logged error; every log call in pxe/tftp/dhcp re-checked: the rest are Go-formatted addresses, MACs formatted from bytes, or messages built from quoted parts | A nonexistent `"x\nFORGED ..."` reaches the lookup; no raw newline in any line. Old `%v`: fails. Round 1 test kept |
| C | One shared log budget: noise could starve offers; a budget-dropped line still stamped its key; repeats not counted; the notice was free | Two classes: policy (offers/ACKs, send failures, local errors) 20/s burst 50; noise (malformed, unarmed MACs, which a spoofer can invent at will, and every TFTP line) 5/s burst 20. Stamp the key only when a line is printed; count repeats too; the notice costs a token. Hard key cap kept | Starvation, retry-after-drop, exact suppressed counts with refill, output bound under alternating repeats and new keys. One shared bucket, early stamping, uncounted repeats, a free notice: each fails |
| D | `-pxe-ip` returned early and skipped reachability, so `-listen 127.0.0.1:8080 -pxe-ip <LAN>` advertised an unreachable URL | `pxeHTTPTarget` validates: a specific IPv4 bind must equal `-pxe-ip`; loopback, `[::1]` and IPv6-specific binds are refused with `-pxe`; on a wildcard bind `-pxe-ip` must be a unicast IPv4 address, with a warning if it isn't local. `[::]` is treated as dual-stack; OpenBSD (where it isn't) moved to the platforms where `-pxe` refuses to start | 17-row table plus real listeners (loopback with a LAN `-pxe-ip`, wildcard `:0`, `[::1]`). The old early return, a missing mismatch check and a missing warning: each fails |
| E | `pxe-lan.sh` could still falsely pass: process existence isn't responder health, and tcpdump's stderr (drops, errors) was discarded | Boot 3 after re-arming must be answered and must consume the new arm (positive control). Each capture must say it was listening and exit with "0 packets dropped by kernel" and no other messages | Run: PASS (`boot1 replies=2`, `boot2 discover=2 replies=0`, `boot3 discover=1 replies=1`). A responder planted to wedge after its first offer passes boots 1-2 and fails at boot 3. The drop check has only seen 0 drops; its failure path has not run |
| F | Tests too weak: the symlink race allowed zero successful fetches; no expiry between the two arm checks; no listener tests for a mismatched `-pxe-ip` or an IPv6-only bind | Symlink race must both serve and refuse (it ran up to 3000 fetches); the recheck test gains an arm that expires between the checks; real-listener tests in D | A recheck that skips expiry fails; with every open refused, the symlink test now fails instead of passing vacuously |

Remaining gaps: write deadlines and log budgets were tested with fakes and a simulated clock, not under real socket pressure; the
capture-drop failure path is untested; and everything listed in CLAUDE.md for real PXE firmware (UEFI and BIOS firmware PXE, TFTP to a
real client, Secure Boot, Windows) is still UNVERIFIED.

### PXE adversarial review round 3 (astra, vendored codex 0.162.1)

A, B, D, E, F CLOSED. **C NOT CLOSED, Medium, confirmed:** two events that should be protected diagnostics still sat in the noise class, so an
attacker flooding unauthenticated requests could hide them: the final authorization-recheck rejection of a previously armed MAC (which
also shared its suppression key with the ordinary "not armed" noise for that MAC) and TFTP listener read failures. Ordinary first-check
"not armed" lines stay in the noise class (astra agreed: a spoofer can invent unlimited MACs). Fixed by the orchestrator (a small, mechanical
change, so not delegated): the recheck rejection is a policy line with its own `revoked:<mac>` key (it can only fire for a MAC that WAS
armed a moment earlier); the listener read failure is a policy line via `readFailed`, and the listener now backs off 50 ms after a read
error so a persistent socket failure can't spin the CPU. Two tests (`pxe_review3_test.go`) flood the noise budget with 5000 distinct keys and
require both lines to still appear; the recheck test failed on the unchanged code, and planting the old class back fails the TFTP test.
astra's note that coverage of the *call sites'* classification and of writes where `WriteTimeout > Lifetime` would be nice is accepted as a
follow-up; the first is now covered for these two sites.

Round 4 (astra): finding C **CLOSED**, no regression, **SIGN-OFF: yes** for the PXE front end (after four rounds: 11 findings, then 5 gaps in the fixes, then 1 more, then closed). Two coverage caveats accepted
and noted rather than fixed: the "burn the ordinary key" step in the recheck test runs after the noise budget is already exhausted so it does not itself stamp that key (the separate
`revoked:` key is covered by source inspection), and the TFTP test checks the backoff constant, not the actual sleep or listener dispatch. Still UNVERIFIED: any real PXE firmware (BIOS and UEFI), TFTP to
a real client, the Secure Boot shim chain, and anything PXE on Windows beyond compiling; the write deadlines and log budgets were tested with fakes and a simulated clock, not under real socket pressure.
