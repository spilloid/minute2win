---
title: Server reference
---

# Server reference

`minute2win-server` is a single Go binary with the page built in. It runs next to dnsmasq (or answers PXE itself with `-pxe`, below) and is where the guard lives. The page uses
**htmx**: the server renders HTML fragments (`server/web/templates/`, escaped by `html/template`) and `hx-*` attributes handle polling,
arming and disarming, with no app JavaScript. htmx is vendored, not loaded from a CDN, so
it works offline. `/api/*` stays as JSON for scripts.

- **Arm** a MAC with a profile. The target's iPXE runs `chain http://<server>:8080/boot/${net0/mac}`. An armed MAC gets the
  minute2win boot script and is **disarmed in the same moment**: one arm is one wipe, so a machine that network-boots first does not
  wipe itself again after the deploy reboots it. An unarmed MAC gets `exit 1` and the firmware boots the local disk. Arms that nobody
  boots expire (`-arm-ttl`, default 2h), and **arms do not survive a restart of the server**: re-arm what you still want.
- The server serves the armed manifest with `report.url` set to itself. The engine then posts every phase and lap time, and the
  **Deployments** board shows a live stopwatch against the 60-second target.
- `M2W_TOKEN=...` makes arming and disarming require that token. Without it, anyone who can reach the server can arm a machine
  (the server warns about this at startup).

```
cd server && go build -o minute2win-server . && M2W_TOKEN=... ./minute2win-server -manifests ../manifests -data ./data
```

For existing DHCP/TFTP infrastructure, route the target to iPXE (for example, `ipxe.efi` for x64 UEFI). Give iPXE an embedded or chained script that
runs the `chain .../boot/${net0/mac}` line above.

## HTTP server flags

| Flag | Default | Purpose |
| --- | --- | --- |
| -listen | :8080 | HTTP address; use loopback for preview and a reachable LAN address for deployment |
| -manifests | Auto-detected | Profiles directory: current directory, executable directory, then ../manifests |
| -image-base | Maintainer host | URL serving wimboot, WinPE, BCD and boot.sdi; replace with your host |
| -data | data | State directory; an empty value keeps state in memory |
| -arm-ttl | 2h | Expiration of unused arms |
| -open | true on Windows | Open browser at startup |

`M2W_TOKEN` is an environment variable, not a command-line flag. Boot assets and the image are served separately from the console. Read [Getting started](GETTING_STARTED.md) for the complete setup.

## Built-in PXE (optional, `-pxe`)

`-pxe` makes minute2win-server its own PXE front end, so you don't need dnsmasq: a **proxyDHCP responder** and a **read-only TFTP
server**. It is off by default; without `-pxe` nothing PXE-related binds or runs.

```
sudo ./minute2win-server -manifests ../manifests -pxe -pxe-dir /srv/ipxe      # /srv/ipxe holds ipxe.efi, undionly.kpxe, ...
```

- **proxyDHCP only.** It never offers an IP address or lease options (`yiaddr` is 0.0.0.0; no subnet, router, DNS or lease time).
  Your network's normal DHCP server keeps handing out addresses; minute2win only adds "boot this file from that server". There is
  no full-DHCP mode.
- **Only armed MACs get an answer.** An unarmed or unknown machine gets no reply at all, so it boots its own disk and the network
  behaves as if minute2win were not there. The PXE path never consumes the arm; `GET /boot/{mac}` does. After that, the machine's
  next PXE request gets no offer, so firmware that always network-boots first boots Windows instead of wiping again. The armed check
  runs again immediately before each reply is sent, so an arm consumed or removed in the meantime gets no reply.
- Only DHCPDISCOVER/DHCPREQUEST carrying vendor class `PXEClient` and a client architecture (option 93) are considered. The reply
  echoes `PXEClient`, the client architecture (93), network interface identifier (94, if sent) and UUID (97), sets PXE discovery control to 8 (boot the file named here: no menu), and names
  this server in `siaddr` and the boot file (field and option 67). On UDP 67, a DHCPREQUEST is answered only if it names this server
  (option 54); anything else there belongs to the real DHCP server. UDP 4011 answers the PXE REQUEST/ACK step. DHCP option overload (52) is
  honoured: options carried in the `file`/`sname` fields are read too, and a malformed overloaded field drops the packet.
- **Boot file by architecture** (option 93): 0 (BIOS) `undionly.kpxe`; 7, 9 (x64 UEFI, EBC) `ipxe.efi`; 11 (arm64 UEFI)
  `ipxe-arm64.efi`; 6 (32-bit x86 UEFI) only `-pxe-file-ia32`, which is empty by default, so such clients get no offer (a 64-bit
  binary would not run on them); anything else is ignored. Change them with `-pxe-file-bios`, `-pxe-file-x64`, `-pxe-file-arm64`,
  `-pxe-file-ia32`.
- **No loop through iPXE.** When the client is already iPXE (user class `iPXE`, or option 175), it gets
  `http://<server-ip>:<port>/boot/<mac>` instead of the iPXE binary again. The port is the one the HTTP listener actually bound (so `-listen :0` works), the
  MAC comes from the packet.
- **Which address it advertises:** on Linux, the address of the interface the request arrived on (and the broadcast reply leaves on
  that interface). Elsewhere, the host's only IPv4 address; with several, it refuses to start until you set `-pxe-ip`. `-pxe-ip`
  sets siaddr and the iPXE URL, but it must be reachable on the HTTP listener, or startup is refused:
  - `-listen` on one IPv4 address: that address is advertised, and `-pxe-ip`, if given, must be the same address.
  - `-listen` on loopback, `[::1]` or any specific IPv6 address: refused with `-pxe` (PXE clients are IPv4 and remote).
  - `-listen` on all addresses (`:8080`, `0.0.0.0`, `[::]`): any `-pxe-ip` is accepted, with a warning if it isn't one of this host's
    addresses (fine behind forwarding, wrong otherwise). `[::]` is dual-stack on the supported platforms (Linux, Windows, macOS,
    FreeBSD, NetBSD); that is not true on OpenBSD, which is why the built-in PXE is not offered there.
- **Where replies go:** to the relay (`giaddr`) if relayed; to the client's address if it already has one; otherwise broadcast to
  port 68, since there is no offered address to unicast to. Port 4011 replies go back to the sender.

| Flag | Default | |
|---|---|---|
| `-pxe` | off | Turn the PXE front end on |
| `-pxe-dir` | (required) | Folder served over TFTP. Only regular files directly inside it; no subfolders, `..`, absolute paths, or symlinks (not even ones pointing inside: copy the file instead) |
| `-pxe-ip` | receiving interface | IPv4 address to advertise |
| `-pxe-file-x64`, `-pxe-file-bios`, `-pxe-file-arm64` | `ipxe.efi`, `undionly.kpxe`, `ipxe-arm64.efi` | Boot file per architecture |
| `-pxe-file-ia32` | (empty: no offer) | Boot file for 32-bit x86 UEFI (arch 6) |
| `-pxe-dhcp-port`, `-pxe-proxy-port`, `-pxe-tftp-port` | 67, 4011, 69 | UDP ports |

**Privileges.** Ports 67, 69 and 4011 need root on Linux, or `sudo setcap cap_net_bind_service=+ep ./minute2win-server`. On Windows,
run it as Administrator and allow UDP 67, 69 and 4011 inbound in the firewall. If a port can't be bound, startup fails and says which
port and why. If another process holds UDP 67 (dnsmasq or another DHCP server on the same host), it says so: stop that process or
run minute2win on another host. UDP 67 is opened with `SO_REUSEADDR`/`SO_REUSEPORT` so it can share the port with a listener that
allows sharing, but don't run two PXE answerers for the same clients.

**Platforms.** The PXE sockets are written for Windows, macOS, FreeBSD, NetBSD and Linux on amd64, arm64, 386, arm,
riscv64, ppc64le, s390x and loong64. The server builds everywhere else too, but `-pxe` refuses to start there with an error saying so.

**Built-in or dnsmasq?** Use `-pxe` when minute2win-server is the only boot service on the segment and you want one binary. Keep dnsmasq
if you already run it, need PXE for machines that aren't armed (menus, other OSes), or want its own DHCP. Don't enable both on the same
host.

**Boot files are yours to provide.** This repo does not include or download any iPXE binary (iPXE is GPL; shipping it would bring
source obligations). Put your own copies in `-pxe-dir`: build them from https://ipxe.org, or use the iPXE project's published signed
pair that kvm-it pins (in the separate [kvm-it repository](https://github.com/spilloid/kvm-it), under `firmware/ipxe/signed/`; its `scripts/fetch-ipxe-signed.sh` re-downloads and checks them).
For **Secure Boot**, put the shim and iPXE side by side in `-pxe-dir` (`BOOTX64.EFI` and `IPXE.EFI`) and start
with `-pxe-file-x64 BOOTX64.EFI`. TFTP passes the requested spelling to the filesystem. Names must match case on case-sensitive filesystems; Windows may accept different case. If a client asks for a name you don't
have, the log shows `tftp: ... refused "<name>"`.

**Security.** TFTP has no authentication and can't know which MAC is asking, so anyone who can reach UDP 69 can download anything in
`-pxe-dir`. Keep only the public iPXE binaries there. Files are opened through `os.Root`, so the directory confines the open itself.
TFTP use is bounded: at most 64 transfers at once and 4 per client IP, each ended after 60 s or 20 s without progress, and a client's
`timeout` option is honoured only from 1 to 10 s. Every TFTP send has a 2 s write deadline, and a send that fails or times out ends
that transfer, so a stuck socket can't hold a slot. Logging is bounded as well: one line per client/file per minute, and two separate
budgets so noise can't drown out what matters: offers and local errors get about 20 lines a second, while things any host can trigger
(malformed packets, unarmed MACs, every TFTP line) get about 5. Dropped lines are counted and reported as "N ... log line(s)
suppressed". Anything from the wire, including error text, is logged quoted. The guard is still the arm: a machine that isn't armed gets no proxyDHCP answer,
and the boot script still comes from `/boot/{mac}`.

**Status: unit-tested; seen working only in a QEMU VM on a simulated LAN (BIOS iPXE stage); never run on real PXE firmware.**
`tests/pxe/pxe-lan.sh` checks, from packet captures with no drops, that boot 1's PXE DISCOVER is answered and the arm consumed,
that boot 2's PXE DISCOVER gets no reply, and (positive control) that after re-arming, boot 3 is answered and consumes the new arm. See [verification and known issues](STATUS.md) for the remaining checks.

## Windows

The Windows build (`minute2win-server-X-windows-x64.zip`, Authenticode-signed) is the same server: extract it, run
`minute2win-server.exe`, and the UI opens in your browser. It prints the line a target should chain, for example
`http://192.168.1.50:8080/boot/${net0/mac}`. Windows Firewall will ask to allow it on private networks; that is what lets the target
reach it. Set `M2W_TOKEN` first: a laptop that can arm disk wipes should not leave the port open without one.

The natural pairing is kvm-it's USB boot drive: the target only needs ordinary DHCP from your network, so no DHCP or TFTP server is
needed. For plain PXE, enable the built-in front end with `-pxe`, or use an existing DHCP/TFTP service such as dnsmasq. The ordinary DHCP server still provides addresses in either case.

