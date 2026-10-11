---
title: Status and known issues
---

# Status and known issues

Current code version: **0.1.2**. This is an early release for evaluation. The server and helpers have automated checks; a disposable x64 UEFI VM has completed imaging and booted the installed Windows disk. The default profile reached the normal country-selection OOBE screen after a long setup delay; logs confirm automatic first-boot bootstrap success. The optional local-account profile reached the desktop automatically with verified admin membership and main-answer-file deletion. Next-logon password-change behavior and complete cached-XML cleanup remain unverified. No measured result supports the one-minute goal yet.

## Verification

| Component | Evidence | Remaining work |
| --- | --- | --- |
| Deployment logic | Pester tests and a real WinPE engine run on a disposable 80 GB VM disk | Physical hardware validation |
| PowerShell compatibility | PowerShell 5.1 syntax lint and runtime in ADK WinPE | Other ADK versions and hardware |
| Server arm, boot, report and persistence rules | Go tests; race detector in CI | Operational validation on deployment hosts |
| Console | Playwright flow, phone width, light/dark | Operator feedback |
| PXE policy and TFTP | Go tests plus real OVMF UEFI proxy DHCP and shim/iPXE TFTP from Windows | Port 4011 path, physical firmware and socket pressure |
| Simulated LAN | QEMU BIOS iPXE ROM, separate DHCP service; consume, ignore, re-arm checks | Physical BIOS/UEFI firmware and Secure Boot |
| Windows server | Native Go tests, console/API checks and UEFI VM PXE/deployment from Windows; signing verified for 0.1.1 | Concurrent clients and multi-interface validation |
| WinPE and Windows install | Automatic wimboot/WinPE, injected manifest, partition, verified download, DISM apply, bcdboot, reclaim and disk boot on one VM | Remaining setup checks and physical hardware |
| First boot and OOBE | Automatic bootstrap, normal OOBE and optional local-admin desktop logon on Windows 26300.9457 VM; main answer file deleted | Next-logon password-change behavior, cached XML and OEM-key behavior |
| Speed and hardware | One 4 GB/two-core VM: engine done at 359.4 s with remote 8.16 GB image | Physical machines and a local single-edition image; no one-minute claim |

Board reports are telemetry, not independent deployment verification. The simulated BIOS iPXE result covers the network front end. The separate OVMF UEFI run demonstrates imaging and Windows disk boot, with OOBE/first-boot limits stated above.

[Windows validation record](https://github.com/spilloid/minute2win/blob/main/docs/validation/2026-10-11-windows-server.md) documents the persistence fix, UEFI/WinPE run, timing and remaining setup limits.

## Known limitations

The original deployment issues are tracked in [#2](https://github.com/spilloid/minute2win/issues/2). Additional PXE validation items are listed here and in the development history.

| Issue | Impact | Workaround or next step |
| --- | --- | --- |
| Default-profile setup takes additional time | Board done precedes Windows setup and OOBE | Wait for installed-OS setup; validate OOBE and first-boot logs separately |
| Limited install coverage | One VM is insufficient to establish production readiness | Capture OOBE/first-boot evidence and validate physical machines |
| Windows 0.1.1 persistent arming ([#7](https://github.com/spilloid/minute2win/issues/7)) | Directory sync fails and degrades the server | Fixed in 0.1.2 source; for an older lab build, `-data ""` disables persistence |
| Restricted default image host/profile | Public users can receive HTTP 403 and cannot rely on those assets | Set your own base URLs and image URI/hash; rebuild WinPE |
| Older maintainer WinPE lacks auto-run | Boot can stop at a prompt | Build with this repository's builder, or manually launch the loader |
| OEM product keys may skip SetupComplete | First-boot bootstrap and logs may not run | No verified fallback; test a firmware-key machine |
| Report key available during manifest delivery window | A LAN host knowing the MAC can forge a board outcome | Isolate deployment traffic; verify the installed OS and logs |
| Static iPXE route repeats deployment | A forced network/USB-first boot can wipe again | Use server arms or a deliberate per-boot opt-in |
| Token is optional | Any reachable host can arm a wipe when it is unset | Always set M2W_TOKEN on a network-accessible server |
| Arms are dropped on restart | Pending work must be re-armed | Deliberate fail-closed behavior; review the target before re-arming |
| Physical firmware/Secure Boot unverified | OVMF UEFI worked; target firmware may differ | Test your firmware and signed chain; existing boot infrastructure remains an option |
| AD/Entra/hybrid joins not implemented | Tenant configuration only logs that work is unimplemented | Use joinMethod none; manage enrollment separately |
| Optional-account follow-up checks ([#8](https://github.com/spilloid/minute2win/issues/8)) | Auto-admin desktop and main answer-file deletion passed; next-logon password-change flow and remaining setup XML were not verified | Keep adminUser null until the optional route is validated for your image |
| Public OOBE administrator password | Creating an admin uses Password1!; forced change at next logon is intended and still needs verification | Leave adminUser null unless this behavior is acceptable |

## Next validation milestones

1. Repeat the VM run with your own images, profiles and firmware settings.
2. Complete next-logon password-change and cached-XML checks; repeat the observed VM setup on additional images.
3. Verify bootstrap on OEM-key hardware and establish any fallback.
4. Exercise physical firmware, the port 4011 path, Secure Boot and multi-interface behavior.
5. Measure each phase with a single-edition image on a local network before publishing performance claims.

These are validation goals, not shipped or scheduled features. Contributors should include exact environment details and evidence when changing a verification claim.
