# Changelog

## [0.2.0] - 2026-10-24

### Breaking: safe defaults for arming

These close three gaps a live evaluation confirmed in 0.1.2: any web page the operator visited could arm a known MAC for a wipe; with no token, anyone on the LAN could; and a missing `-image-base` silently booted the maintainer's WinPE, which then ran the maintainer's hosted engine with wipe rights.

- **Cross-site requests are refused.** Every state-changing route (`POST /api/armed`, `DELETE /api/armed/{mac}`, `/ui/arm`, `/ui/disarm`, `POST /api/report`) refuses browser requests from another origin with 403 (Go's `http.CrossOriginProtection`: `Sec-Fetch-Site`, or `Origin` against `Host`). **Booting takes a one-time ticket.** That protection lets every GET through, and a browser loading `/boot/<armed MAC>` from another site's `<img>` over plain HTTP sends no `Origin` or `Sec-Fetch-Site`, exactly like iPXE, so a page could use up an arm. `GET /boot/{mac}` now changes nothing: for an armed MAC it answers with an iPXE script that chains `/boot/{mac}/{ticket}`, and only that URL consumes the arm. The ticket is 128 random bits per arm, single-use, gone with the arm, and never shown in `/api/state`, the console or the state file; a page on another site cannot read the response holding it. iPXE follows the chain with no configuration change (built-in PXE, dnsmasq and USB autoexec routes alike); the static `minute2win.ipxe` never used `/boot`. `/boot` also refuses requests whose `Sec-Fetch-Site` or `Origin` shows another site. Remaining: a DNS-rebinding page or someone reading the plain-HTTP traffic can learn a ticket and use up an arm; a ticket alone cannot create one, but anyone reading the operator's plain-HTTP traffic can also capture `M2W_TOKEN` and then arm machines (use a trusted segment or a TLS reverse proxy). `POST /api/armed` and `POST /api/report` require `Content-Type: application/json` (415 otherwise). With no token, arming and disarming are refused when the request is addressed to a host name other than `localhost`, so a DNS-rebinding page cannot reach them. curl, scripts and the WinPE engine (which sends `application/json` and no browser headers) are unaffected.
- **`-listen` defaults to `127.0.0.1:8080`** (was `:8080`, every interface). On a network-reachable address the server **refuses to start without `M2W_TOKEN`** and prints the three ways out; `-insecure-no-token` is the explicit opt-out for an isolated lab. The decision is checked again on the address actually bound, before anything is served or PXE starts, so a host name that resolves to loopback for the check and to a LAN address for the bind is refused and the listener closed. The token is still only for arming and disarming: the routes booting machines use (`/boot`, `/manifest`, `/api/report`) never needed it and still don't.
- **No default image or boot-asset host.** `-image-base` has no default (was the maintainer's host): without it the server starts but refuses every arm, saying why; a placeholder host is refused at startup. Arming also refuses a profile whose image URL is missing, not `https://` or a documentation placeholder, whose SHA-256 is missing or all zeros, or which is not valid JSON (including a BOM), instead of consuming the arm and failing in WinPE. The shipped `manifests/win11-pro.json` is now a template (`your-image-host.invalid`, zero hash) that cannot be armed. `Build-WinPE.ps1 -Base` is required and checked before the work folder is deleted; `install.ps1` stops without `-Base`; the static `minute2win.ipxe` boots nothing until `m2w-base` is set (`tools/stage.sh <outdir> <base>` can write it in, IPv6 literal bases such as `http://[fd00::1]:8080/windows11` included; it builds and checks the script before replacing the output folder, so a refused or failed base leaves an earlier staging as it was, and sets the staged script to 0644 so a web server running as another user can read it; `tests/stage.test.sh` in CI); `tests/vm/deploy-vm.sh` needs `BASE=`.

**Upgrading from 0.1.2:**

1. Deployment server: set `M2W_TOKEN` and pass `-listen :8080` (or your LAN address) and `-image-base https://<your host>/<folder>` explicitly. A server started with only `-manifests` now listens on loopback, and machines being imaged cannot reach it.
2. If you really run without a token on an isolated network, add `-insecure-no-token`, and open the console by IP address (or `localhost`), not by host name.
3. Scripts calling `POST /api/armed` must send `-H 'Content-Type: application/json'`. Anything that sent an `Origin` header from another site will get 403.
4. Profiles: if you armed the shipped `win11-pro` profile against the maintainer's host, make your own profile with your image URL and SHA-256.
5. Boot assets: rebuild WinPE with an explicit `-Base` if you relied on the default (images built by 0.1.2's builder with an explicit `-Base` keep working), and either stage with `tools/stage.sh <outdir> <base>` or `set m2w-base` before chaining the static `minute2win.ipxe`. Anything that runs `install.ps1` by hand must pass `-Base`.
6. iPXE needs no change for the one-time boot ticket. A tool other than iPXE that fetched `/boot/{mac}` to start a deployment must now follow the `chain` line in the response.

- Fix `scripts/verify-signature.ps1` on Windows PowerShell 5.1 (the shell a stock Windows 11 has): em dashes inside its strings, in a file without a BOM, were read as ANSI and became closing quotes, so the script did not parse there. CI runs it under pwsh 7, which reads UTF-8, so it had not shown. The separators are now ASCII, and a Pester test requires every PowerShell file to be ASCII or carry a UTF-8 BOM.
- Product page: the Pages home (https://spilloid.github.io/minute2win/) is now a product page (`docs/index.html`, `docs/assets/site.css`) covering what minute2win does, the console, how a deployment runs, the safety guards, verification status, a loopback quick start and the guides. The disk-erase warning sits in the hero; the console screenshots are labelled as UI-test demo data, and the one-minute name is stated as a goal next to the one measured VM run (359.4 s). The documentation home moved from `docs/index.md` to `docs/guide.md` (`/guide.html`); the README, docs navigation and contributor notes link to it. The page says exactly what is checked before the wipe (manifest, image reachability and size, capacity, disk) and that the image's SHA-256 can only be checked after the download, which comes after partitioning, so a wrong hash or a corrupted download leaves an erased disk (0.1.0's "a bad setup changes nothing" overstated this). `tests/site-version.sh`, run by the Pages workflow, fails if the page names any three-part version number other than VERSION, bare mentions such as "Release 0.1.1" included (IPv4 addresses are skipped). `docs/SERVER.md`: the built-in PXE example had a literal `\n` instead of a line continuation, so a pasted command dropped `-image-base`, `-pxe` and `-pxe-dir`.
- Product page relaunch: a dark, gold landing page whose hero is a canvas boot sequence (`docs/assets/hero.js`): firmware PXE and the armed iPXE chain using the server's own strings, a stopwatch boot with the engine's phases at the one-minute target pace, then a gold finish. It is labelled a simulation on screen, honours reduced motion, has pause and replay controls, and stops drawing when off screen. New sections: the one recorded run as a lap chart against the 60 s target, and line-rate arithmetic (clearly not a measurement) showing why the network is the throttle. The disk-erase warning stays in the hero; the screenshots stay labelled as demo data.
- Walkthrough (`docs/WALKTHROUGH.md`): a complete first deployment on one Windows PC: Hyper-V external switch, release download and hash check, WinPE build, single-edition image export and HTTPS hosting, a BOM-free profile, a file server, the built-in PXE, a Generation 2 VM with a fixed MAC, arming, what each screen shows (quoted from the source), result checks, re-imaging, FAQ, advice and clean-up. The Hyper-V target is stated as not yet in the verification record. Two new console screenshots (`walkthrough-console.png`, `walkthrough-armed.png`) were captured from the 0.1.2 Windows release on loopback. The walkthrough is linked from the docs navigation, the docs home, Getting started and the README. The docs status bar now says the chain is verified on one VM, not that it is unverified.
- Documentation: console screenshots (desktop and phone) on the README, the docs home page and Getting started, captioned as UI-test demo data. `tests/ui/run.sh docs/assets/screenshots` regenerates them.

## [0.1.2] - 2026-10-11

- Fix Windows state persistence: directory sync returned Access is denied, causing the server to become degraded and reject every arm. The Windows replacement now uses MoveFileExW with replacement and write-through flags after flushing the temporary file; Unix keeps rename plus directory sync. Arms still never survive a restart.
- Name the injected manifest explicitly in iPXE for UEFI wimboot. The loader now refuses to run without injection unless a static profile is explicitly selected with `-Manifest`, preventing a missing armed profile from silently choosing the default.
- Validate Windows-hosted UEFI PXE through real OVMF firmware, automatic WinPE imaging, Windows disk boot, normal OOBE and automatic first-boot bootstrap on a disposable Proxmox VM. Record measured timing and remaining hardware/Secure Boot limits.
- Run server tests natively on Windows in CI, with a regression covering repeated state replacement under a Unicode path, preserved deployment history and dropped arms on restart.

- Linux x86_64 AppImage (`minute2win-server-X-x86_64.AppImage`): the server and `manifests/` in one runnable file, built in CI on every PR (artifact `appimage`, smoke-tested) and attached to each release. Built by `tools/appimage.sh` with no appimagetool; the one prebuilt piece, the AppImage type 2 runtime, is pinned by SHA-256. Like the Linux archives it is not code-signed (covered by `SHA256SUMS`). Smoke-tested extracted (no FUSE here); not yet run on a desktop with FUSE. x86_64 only.
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
