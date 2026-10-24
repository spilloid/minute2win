---
title: Troubleshooting
---

# Troubleshooting

Begin with the server console output and [Status](STATUS.md). A consumed arm means the boot script was requested, not that Windows installed successfully. Avoid re-arming until you understand the failure.

| Symptom | Check |
| --- | --- |
| UI works locally, target cannot reach server | The server listens on 127.0.0.1 by default. Set `M2W_TOKEN` and use `-listen :8080` (or a LAN address); check TCP 8080, advertised address and network routing |
| Server says "refusing to start: ... M2W_TOKEN is not set" | `-listen` is reachable from the network. Set `M2W_TOKEN` (machines being imaged never need it), listen on loopback for a preview, or pass `-insecure-no-token` on an isolated lab network |
| Arm refused: "this server has no -image-base" | Restart with `-image-base` set to the URL of your folder holding wimboot, BCD, boot.sdi and boot.wim. There is no default host |
| Arm refused: "profile ...: image.uri ..." or "image.sha256 ..." | The profile still has the template's placeholder URL or zero hash, or is not `https://`. Set your own image URL and its SHA-256 |
| Arm refused: "without M2W_TOKEN, arming and disarming are only accepted at an IP address or localhost" | You opened the console through a host name on a server with no token. Use `http://127.0.0.1:8080/` (or the server's IP address), or set a token |
| 403 "cross-origin request refused" | The request came from a page on another origin. Use the console served by this server; behind a reverse proxy, forward the original `Host` header. Scripts should not send `Origin` or `Sec-Fetch-Site` |
| 415 "Content-Type must be application/json" | Add `-H 'Content-Type: application/json'` to scripted `POST /api/armed` calls |
| HTTP 403 for boot/image files | Check that `-image-base`, the WinPE builder's `-Base` and the profile's image URL all point at your host; check host ACLs and target network access |
| No built-in PXE offer | MAC must be armed and unexpired; normal DHCP must supply an address; check UDP 67/4011, privileges and architecture |
| Startup cannot bind a PXE port | Check for dnsmasq/another PXE service or missing privileges; do not run competing answerers for the same clients |
| TFTP file refused | File must exist directly in pxe-dir, with matching case on case-sensitive filesystems, as a regular file without symlinks |
| Wrong address advertised | Match a specific listen address and pxe-ip; for multiple IPv4 addresses on non-Linux hosts set pxe-ip explicitly |
| WinPE leaves a prompt | Confirm startnet came from this builder and was not built with -Manual; inspect its loader command |
| Arm refused: "profile ... is not valid JSON" | Save profiles as UTF-8 without BOM; check JSON syntax. Arming checks the JSON, the image URL and the hash, not every engine setting |
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
