---
title: Status and known issues
---

# Status and known issues

Current code version: **0.1.2**. This is an early release for evaluation. The server and helpers have automated checks; a complete Windows deployment has not run. No measured result supports the one-minute goal yet.

## Verification

| Component | Evidence | Remaining work |
| --- | --- | --- |
| Deployment logic | Pester unit tests | Run engine inside WinPE against a disposable disk |
| PowerShell compatibility | PSScriptAnalyzer checks PowerShell 5.1 syntax | Verify runtime behavior in WinPE |
| Server arm, boot, report and persistence rules | Go tests; race detector in CI | Operational validation on deployment hosts |
| Console | Playwright flow, phone width, light/dark | Operator feedback |
| PXE policy and TFTP | Crafted packet and loopback Go tests | Real firmware and real socket pressure |
| Simulated LAN | QEMU BIOS iPXE ROM, separate DHCP service; consume, ignore, re-arm checks | UEFI/BIOS firmware PXE, TFTP boot files, Secure Boot |
| Windows server | Native Go tests and authenticated console/API smoke test on Windows; release signing verified for 0.1.1 | VM deployment and real PXE validation on Windows |
| WinPE and Windows install | Not boot-verified | wimboot, injected manifest, partition, download, apply, bcdboot, reboot |
| First boot and OOBE | Unit-tested helpers | SetupComplete, answer file, OEM-key behavior |
| Speed and hardware | No measurement | Full VM run, then named physical machines |

Board reports are telemetry, not independent deployment verification. The simulated BIOS iPXE result covers the network front end; the engine's GPT/UEFI install path has not been demonstrated by it.

[Windows validation record](https://github.com/spilloid/minute2win/blob/main/docs/validation/2026-10-11-windows-server.md) documents the native runtime check and persistence fix.

## Known limitations

The original deployment issues are tracked in [#2](https://github.com/spilloid/minute2win/issues/2). Additional PXE validation items are listed here and in the development history.

| Issue | Impact | Workaround or next step |
| --- | --- | --- |
| No end-to-end Windows install | A successful unit test does not establish a working installation | Validate on a disposable VM; capture logs and Windows boot evidence |
| Restricted default image host/profile | Public users can receive HTTP 403 and cannot rely on those assets | Set your own base URLs and image URI/hash; rebuild WinPE |
| Older maintainer WinPE lacks auto-run | Boot can stop at a prompt | Build with this repository's builder, or manually launch the loader |
| OEM product keys may skip SetupComplete | First-boot bootstrap and logs may not run | No verified fallback; test a firmware-key machine |
| Report key available during manifest delivery window | A LAN host knowing the MAC can forge a board outcome | Isolate deployment traffic; verify the installed OS and logs |
| Static iPXE route repeats deployment | A forced network/USB-first boot can wipe again | Use server arms or a deliberate per-boot opt-in |
| Token is optional | Any reachable host can arm a wipe when it is unset | Always set M2W_TOKEN on a network-accessible server |
| Arms are dropped on restart | Pending work must be re-armed | Deliberate fail-closed behavior; review the target before re-arming |
| Real firmware/Secure Boot unverified | Built-in PXE may fail on target firmware | Test your firmware and signed chain; existing boot infrastructure remains an option |
| AD/Entra/hybrid joins not implemented | Tenant configuration only logs that work is unimplemented | Use joinMethod none; manage enrollment separately |
| Public OOBE administrator password | Creating an admin uses Password1! with forced change at next logon | Leave adminUser null unless this behavior is acceptable |

## Next validation milestones

1. Rebuild WinPE using an operator-owned image host and verify loader startup.
2. Complete a disposable x64 UEFI VM deployment, including logs, OOBE and a second boot with no arm.
3. Verify bootstrap on OEM-key hardware and establish any fallback.
4. Exercise real UEFI PXE/TFTP, BIOS firmware boot routing, Secure Boot, Windows-host PXE and multi-interface behavior.
5. Measure each phase with a single-edition image on a local network before publishing performance claims.

These are validation goals, not shipped or scheduled features. Contributors should include exact environment details and evidence when changing a verification claim.
