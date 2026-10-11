---
title: Troubleshooting
---

# Troubleshooting

Begin with the server console output and [Status](STATUS.md). A consumed arm means the boot script was requested, not that Windows installed successfully. Avoid re-arming until you understand the failure.

| Symptom | Check |
| --- | --- |
| UI works locally, target cannot reach server | Replace loopback with a reachable LAN listener; check TCP 8080, advertised address and network routing |
| HTTP 403 for boot/image files | Replace the restricted maintainer URL with your host; check host ACLs and target network access |
| No built-in PXE offer | MAC must be armed and unexpired; normal DHCP must supply an address; check UDP 67/4011, privileges and architecture |
| Startup cannot bind a PXE port | Check for dnsmasq/another PXE service or missing privileges; do not run competing answerers for the same clients |
| TFTP file refused | File must exist directly in pxe-dir, with matching case on case-sensitive filesystems, as a regular file without symlinks |
| Wrong address advertised | Match a specific listen address and pxe-ip; for multiple IPv4 addresses on non-Linux hosts set pxe-ip explicitly |
| WinPE leaves a prompt | Confirm startnet came from this builder and was not built with -Manual; inspect its loader command |
| Boot manifest was not injected | Use the updated iPXE script with `initrd --name manifest.json`; confirm the server manifest request succeeds. The loader stops before installation rather than selecting a default profile |
| Loader cannot download scripts | Builder Base and hosted tree must agree; check WinPE NIC drivers, DNS and certificate trust |
| Image checks fail before wipe | Check HTTPS URI, image size, staging space, disk capacity and manifest values |
| Download hash fails after wipe | Recheck the exact hosted bytes/hash; stabilize image hosting before another attempt |
| No eligible disk | Confirm internal disk type and minimum size; inject storage drivers into WinPE if needed |
| Server reports degraded | Investigate data directory permissions/storage and persistence errors; restarting drops arms but does not fix the underlying storage problem |
| No bootstrap after Windows Setup | Check SetupComplete logs; OEM-key installs may skip SetupComplete |
| Target attempts to deploy after reboot | Use server arms; static boot scripts require a per-boot opt-in |

## Collect evidence

During deployment inspect `X:\minute2win\logs`. After log copying, inspect `C:\Windows\Logs\minute2win\` for the transcript, hardware inventory, first-boot results and setup logs. On engine failure, the intended behavior is to remain at the console without rebooting.

For a report, include the release/commit, server platform, firmware mode, boot route, anonymized manifest, expected behavior, actual behavior and relevant log excerpts. Use synthetic MACs and redact tokens, report keys, image access URLs, serial numbers and personal/customer data. See [Contributing](https://github.com/spilloid/minute2win/blob/main/CONTRIBUTING.md).
