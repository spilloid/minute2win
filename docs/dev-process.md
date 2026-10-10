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
