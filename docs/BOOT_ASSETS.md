---
title: Boot assets
---

# Boot assets

The deployment server generates boot scripts and manifests. A separate web host serves WinPE, wimboot, the loader and engine. GitHub Pages serves documentation only.

## Build WinPE

On Windows install the Windows ADK and its matching WinPE add-on from [Microsoft's ADK download page](https://learn.microsoft.com/windows-hardware/get-started/adk-install). The repository's documented build baseline is ADK 10.1.26100.x; other versions have not been verified here.

Open an elevated Deployment and Imaging Tools Environment, then run the builder from the source checkout in PowerShell:

```powershell
.\build\Build-WinPE.ps1 -Work C:\WinPE_minute2win -Base https://deploy.example.net/windows11
```

Replace the URL with your own base. **The builder deletes the directory passed as `-Work` before recreating it.** Use a dedicated scratch directory. `-Drivers C:\drivers` injects NIC/storage INF drivers. `-Manual` creates a debugging image that prints the deploy command and leaves a prompt.

Output is `boot.wim`, `BCD`, and `boot.sdi` under `<Work>\out`. The builder includes PowerShell components and an auto-running startnet. It builds amd64 WinPE. Its network wait probes 1.1.1.1 for up to about 60 seconds; networks without Internet ICMP may incur this delay before the loader fetch.

## Stage and host

On a host with Bash, from the source checkout:

```bash
tools/stage.sh
```

This generates `dist/windows11/`; it uploads nothing. Supplying a different output directory is supported, but the script replaces its `windows11/` subdirectory. Add your separately supplied boot files to produce:

```text
windows11/
  wimboot                 # supplied separately
  BCD                     # WinPE builder output
  boot.sdi                # WinPE builder output
  boot.wim                # WinPE builder output
  install.wim             # your licensed image, if hosted here
  install.ps1             # staged loader
  minute2win.ipxe          # staged static boot script
  minute2win/
    VERSION
    Minute2Win.psm1
    Start-Minute2Win.ps1
    Start-FirstBoot.ps1
    unattend-oobe.xml
    manifests/
      win11-pro.json
```

The release boot-files zip contains the staged scripts, not Windows images or boot binaries. Replace staged `win11-pro.json` with your own profile for static boots; add other profiles as needed. Both the static iPXE script and server boot script inject the selected profile as `manifest.json` using iPXE's explicit `--name` option. The loader stops if injection is missing. A deliberate manual fallback requires `install.ps1 -Base <your-base> -Manifest <profile-name>`.

Host the directory at your configured base URL. The builder's `-Base`, server's `-image-base`, and hosted loader/engine tree must agree. The Windows image URI in the manifest can point elsewhere. WinPE must trust any HTTPS certificate; browser access alone does not prove WinPE certificate trust.

## Updates and the static route

To update the engine, stage and upload a consistent version of the scripts. Rebuild WinPE when changing its base URL, startnet behavior or drivers. Active deployments download scripts, so update between deployments.

The static `minute2win.ipxe` has its own hard-coded base URL. Edit it to your host before using it, and set `m2w-manifest` to select a staged profile (default `win11-pro`). It does not use server arms and deploys every time it is reached. Put it behind a deliberate per-boot opt-in. Prefer the server route for firmware configured to network-boot first.

No iPXE binaries are bundled. Supply builds from the [iPXE project](https://ipxe.org/) under their license. Secure Boot requires an appropriate signed chain and remains unverified for Minute2Win. See [Server reference](SERVER.md) for shim filenames and TFTP case sensitivity.
