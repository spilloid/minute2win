---
title: Troubleshooting
---

# Troubleshooting

Something didn't happen the way you expected? Start with two things: what the server printed in its console window, and [Status](STATUS.md), in case it's a known limitation.

**One rule before anything else: don't re-arm until you know what went wrong.** A used-up arm only means the machine asked for its boot script. It doesn't mean Windows installed.

The tables below are grouped by when the problem shows up. Find the message or symptom on the left; the right says what to check.

## Starting the server

| Symptom | Check |
| --- | --- |
| Server says "refusing to start: ... M2W_TOKEN is not set" | `-listen` is reachable from the network, so a token is required. Set `M2W_TOKEN` (machines being imaged never need it), listen on loopback for a preview, or pass `-insecure-no-token` on an isolated lab network |
| Startup cannot bind a PXE port | Another DHCP or PXE service (dnsmasq, for example) is running, or the server lacks privileges. Don't run two PXE answerers for the same clients |
| Server reports degraded | Look at the data directory's permissions and storage, and the persistence errors. A restart drops arms but doesn't fix the storage problem |

## Arming a machine

| Symptom | Check |
| --- | --- |
| Arm refused: "this server has no -image-base" | Restart with `-image-base` set to the URL of your folder holding wimboot, BCD, boot.sdi and boot.wim. There's no default host |
| Arm refused: "profile ...: image.uri ..." or "image.sha256 ..." | The profile still has the template's placeholder URL or zero hash, or the URL isn't `https://`. Put in your own image URL and its SHA-256 |
| Arm refused: "profile ... is not valid JSON" | Save profiles as UTF-8 without a BOM, and check the JSON syntax. Arming checks the JSON, the image URL and the hash, not every engine setting |
| Arm refused: "without M2W_TOKEN, arming and disarming are only accepted at an IP address or localhost" | You opened the console through a host name on a server with no token. Use `http://127.0.0.1:8080/` (or the server's IP address), or set a token |
| 403 "cross-origin request refused" | The request came from a page on another origin. Use the console this server serves. Behind a reverse proxy, forward the original `Host` header. Scripts shouldn't send `Origin` or `Sec-Fetch-Site` |
| 415 "Content-Type must be application/json" | Add `-H 'Content-Type: application/json'` to scripted `POST /api/armed` calls |

## Network boot

| Symptom | Check |
| --- | --- |
| The console works on the server, but the target can't reach it | The server listens on 127.0.0.1 by default. Set `M2W_TOKEN` and use `-listen :8080` (or a LAN address). Check TCP 8080, the advertised address and network routing |
| No built-in PXE offer | The MAC must be armed and not expired, normal DHCP must hand out an address, and UDP 67/4011 must be open. Check privileges and the machine's architecture |
| TFTP file refused | The file must sit directly in `pxe-dir`, as a regular file (no symlinks), with matching case on case-sensitive filesystems |
| Wrong address advertised | Match a specific listen address and `-pxe-ip`. On non-Linux hosts with several IPv4 addresses, set `-pxe-ip` explicitly |
| HTTP 403 for boot or image files | Check that `-image-base`, the WinPE builder's `-Base` and the profile's image URL all point at your host. Check the host's access rules and the target's network access |
| Boot manifest was not injected | Use the updated iPXE script with `initrd --name manifest.json`, and confirm the server's manifest request succeeds. The loader stops before installing rather than guessing a profile |

## In WinPE

| Symptom | Check |
| --- | --- |
| WinPE stops at a prompt | Confirm `startnet` came from this repo's builder and wasn't built with `-Manual`. Look at its loader command |
| Loader can't download the scripts | The builder's `-Base` and the hosted folder must match. Check WinPE's network drivers, DNS and certificate trust |
| Image checks fail before the wipe | Check the HTTPS URL, image size, staging space, disk capacity and manifest values. The disk hasn't been touched yet |
| No eligible disk | Confirm the internal disk's type and minimum size. Add storage drivers to WinPE if needed |
| Download hash fails after the wipe | Re-check that the hosted bytes match the hash in the profile, and make the image hosting reliable before trying again |

## After Windows installs

| Symptom | Check |
| --- | --- |
| No first-boot bootstrap after Windows Setup | Check the SetupComplete logs. Installs with an OEM product key may skip SetupComplete |
| The machine tries to deploy again after a reboot | Use server arms. Static boot scripts need a per-boot opt-in |

## Gathering evidence

**While it's running**, logs are in `X:\minute2win\logs` inside WinPE. If the engine fails, it's meant to stay at the console without rebooting, so you can read them.

**After it's installed**, look in `C:\Windows\Logs\minute2win\` for the transcript, hardware inventory, first-boot results and setup logs.

**Reporting a problem?** Thank you, it helps. Please include the release or commit, the server platform, the firmware mode, the boot route, your profile (anonymized), what you expected, what happened, and the relevant log lines. Use made-up MACs, and redact tokens, report keys, image access URLs, serial numbers and any personal or customer data. See [Contributing](https://github.com/spilloid/minute2win/blob/main/CONTRIBUTING.md).
