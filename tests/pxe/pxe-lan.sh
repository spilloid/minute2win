#!/usr/bin/env bash
# Built-in PXE on a simulated LAN: one privileged podman container holds a bridge, "the network's DHCP server" (dnsmasq, plain DHCP,
# no PXE options, in its own network namespace like a router), minute2win-server -pxe on the bridge, and a QEMU VM on a tap.
# It arms the VM's MAC and boots it twice, capturing each boot's DHCP traffic on the bridge (tcpdump). It passes only on positive
# evidence from the captures:
#   boot 1: the VM sent a PXE DHCPDISCOVER, minute2win sent a proxy reply, and /boot consumed the arm (state shows "booting");
#   boot 2: the VM DID send a PXE DHCPDISCOVER again, and minute2win sent NO reply (nothing from 10.77.0.1 on UDP 67/4011);
#   boot 3 (positive control): after re-arming, the PXE DHCPDISCOVER is answered again and /boot consumes the new arm. This proves
#           the responder was alive and boot 2's silence was policy, not a dead or wedged process.
# Each capture must be healthy: tcpdump must report it was listening, and its exit statistics must show 0 packets dropped by the
# kernel and no errors. Any failed setup step, container failure, unhealthy capture or missing evidence fails the run.
#
#   tests/pxe/pxe-lan.sh [ipxe-dir]     ipxe-dir: a folder with your ipxe.efi (default: kvm-it's firmware/ipxe/signed, IPXE.EFI)
#
# MODE=bios (default): SeaBIOS + QEMU's own iPXE NIC ROM. That client is already iPXE, so this exercises the iPXE stage (HTTP boot
# URL), interface-address selection, the broadcast reply, and arm consumption. It does NOT exercise firmware PXE + TFTP.
# MODE=uefi: OVMF from the host (/usr/share/edk2/ovmf). On 2026-10-10 OVMF never attempted a network boot in this harness (no DHCP
# seen), so this mode fails at the first check; the firmware-PXE + TFTP stage is still unrun here.
# Needs podman, /dev/kvm; builds localhost/m2w-pxe-test:2 (Debian + qemu + dnsmasq + tcpdump) once. Nothing leaves the container.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; ROOT="$HERE/../.."
IPXE_DIR="${1:-$ROOT/../kvm-it/firmware/ipxe/signed}"; MODE="${MODE:-bios}"
IMG=localhost/m2w-pxe-test:2
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/tftp" "$W/out"
if [ -f "$IPXE_DIR/ipxe.efi" ]; then cp "$IPXE_DIR/ipxe.efi" "$W/tftp/"; elif [ -f "$IPXE_DIR/IPXE.EFI" ]; then cp "$IPXE_DIR/IPXE.EFI" "$W/tftp/ipxe.efi"; fi
(cd "$ROOT/server" && CGO_ENABLED=0 go build -o "$W/minute2win-server" .)
podman image exists "$IMG" || podman build -q -t "$IMG" - >/dev/null <<'EOF'
FROM docker.io/library/debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends qemu-system-x86 ipxe-qemu dnsmasq-base iproute2 curl ca-certificates tcpdump && rm -rf /var/lib/apt/lists/*
EOF
cat > "$W/run.sh" <<'EOF'
#!/bin/bash
set -euo pipefail
MAC=52:54:00:12:34:56
SRV=10.77.0.1
ip link add br0 type bridge; ip addr add $SRV/24 dev br0; ip link set br0 up
ip tuntap add tap0 mode tap; ip link set tap0 master br0; ip link set tap0 up
unshare -n sleep 100000 & NP=$!; sleep 0.5
ip link add vh type veth peer name vd; ip link set vd netns $NP; ip link set vh master br0; ip link set vh up
N="nsenter -t $NP -n"
$N ip addr add 10.77.0.2/24 dev vd; $N ip link set vd up
$N dnsmasq --no-daemon --port=0 --interface=vd --dhcp-range=10.77.0.100,10.77.0.150,1h --log-dhcp --dhcp-leasefile=/tmp/l > /out/dnsmasq.log 2>&1 &
# A deployable-looking profile: arming refuses the shipped placeholder one. Nothing in this run ever fetches the image.
mkdir -p /tmp/m
printf '{"schemaVersion":1,"image":{"source":"https","uri":"https://images.lab.internal/w11/install.wim","sha256":"%s","edition":"Windows 11 Pro"}}' \
  "$(printf 'a%.0s' $(seq 64))" > /tmp/m/win11-pro.json
# -listen :8080 is reachable from the bridge, so the server requires a token. Machines being imaged never use it; only arming does.
export M2W_TOKEN=pxe-lan-test
ARM=(curl -sf -X POST -H "Authorization: Bearer $M2W_TOKEN" -H 'Content-Type: application/json' -d "{\"mac\":\"$MAC\",\"manifest\":\"win11-pro\"}" http://127.0.0.1:8080/api/armed)
/w/minute2win-server -listen :8080 -manifests /tmp/m -data "" -image-base http://$SRV:8080/no-images -pxe -pxe-dir /w/tftp >> /out/server.log 2>&1 &
SP=$!
for i in $(seq 50); do curl -sf http://127.0.0.1:8080/api/state >/dev/null && break; sleep 0.1; done
kill -0 $SP || { echo "minute2win-server exited at startup" >&2; exit 1; }
"${ARM[@]}" > /dev/null
FW=""
if [ "$MODE" = uefi ]; then cp /fw/OVMF_VARS.fd /tmp/vars.fd; FW="-drive if=pflash,format=raw,readonly=on,file=/fw/OVMF_CODE.fd -drive if=pflash,format=raw,file=/tmp/vars.fd"; fi
boot() {
  echo "== boot $1" >> /out/server.log
  tcpdump -i br0 -U -w /out/boot$1.pcap 'udp port 67 or udp port 68 or udp port 4011' 2>/out/tcpdump-$1.err & TP=$!
  for i in $(seq 50); do grep -q 'listening on br0' /out/tcpdump-$1.err && break; sleep 0.1; done
  grep -q 'listening on br0' /out/tcpdump-$1.err || { echo "tcpdump did not start: $(cat /out/tcpdump-$1.err)" >&2; exit 1; }
  rc=0; timeout 45 qemu-system-x86_64 -enable-kvm -m 1024 -machine q35 $FW \
    -netdev tap,id=n0,ifname=tap0,script=no,downscript=no -device virtio-net-pci,netdev=n0,mac=$MAC \
    -display none -serial file:/out/serial-$1.log > /out/qemu-$1.log 2>&1 || rc=$?
  [ $rc = 124 ] || { echo "qemu exited with $rc (expected the 45 s timeout): $(cat /out/qemu-$1.log)" >&2; exit 1; }
  sleep 1; kill -INT $TP; wait $TP || true
  # tcpdump prints its statistics on exit; anything dropped, or any line that isn't startup/statistics, is an unhealthy capture.
  grep -q '^0 packets dropped by kernel' /out/tcpdump-$1.err || { echo "boot $1 capture dropped packets or gave no statistics: $(cat /out/tcpdump-$1.err)" >&2; exit 1; }
  if grep -v -e 'listening on br0' -e 'packets captured' -e 'packets received by filter' -e 'packets dropped by kernel' /out/tcpdump-$1.err | grep -q .; then
    echo "boot $1 capture reported: $(cat /out/tcpdump-$1.err)" >&2; exit 1
  fi
  # Client PXE DISCOVERs (from the VM's MAC to port 67, vendor class PXEClient) and replies from minute2win (UDP 67 or 4011).
  disc=$(tcpdump -r /out/boot$1.pcap -nn -v "ether src $MAC and udp dst port 67" 2>/dev/null | grep -c 'Discover' || true)
  pxe=$(tcpdump -r /out/boot$1.pcap -nn -v "ether src $MAC and udp dst port 67" 2>/dev/null | grep -c 'PXEClient' || true)
  ours=$(tcpdump -r /out/boot$1.pcap -nn "src host $SRV and (udp src port 67 or udp src port 4011)" 2>/dev/null | wc -l)
  echo "boot$1 discover=$disc pxeclient=$pxe minute2win_replies=$ours" >> /out/result.txt
}
boot 1; curl -sf http://127.0.0.1:8080/api/state > /out/state-1.json
boot 2
"${ARM[@]}" > /dev/null
boot 3; curl -sf http://127.0.0.1:8080/api/state > /out/state-3.json
kill -0 $SP || { echo "minute2win-server died during the run" >&2; exit 1; }
EOF
chmod +x "$W/run.sh"
FW=(); [ -d /usr/share/edk2/ovmf ] && FW=(-v /usr/share/edk2/ovmf:/fw:ro)
if ! podman run --rm --privileged --device /dev/kvm --security-opt label=disable -e MODE="$MODE" -v "$W":/w -v "$W/out":/out "${FW[@]}" "$IMG" /w/run.sh; then
  echo "FAIL: the container run failed"; cat "$W/out/server.log" 2>/dev/null || true; exit 1
fi
grep -a 'pxe: \|tftp: \|== boot' "$W/out/server.log" || true
cat "$W/out/result.txt"
get() { sed -n "s/^boot$1 .*$2=\([0-9]*\).*/\1/p" "$W/out/result.txt"; }
fail=0
[ "$(get 1 discover)" -gt 0 ] && [ "$(get 1 pxeclient)" -gt 0 ] || { echo "FAIL: boot 1 sent no PXE DHCPDISCOVER"; fail=1; }
[ "$(get 1 minute2win_replies)" -gt 0 ] || { echo "FAIL: boot 1 got no proxy reply from minute2win"; fail=1; }
grep -q '"status":"booting"' "$W/out/state-1.json" || { echo "FAIL: /boot was not fetched (arm not consumed)"; fail=1; }
[ "$(get 2 discover)" -gt 0 ] && [ "$(get 2 pxeclient)" -gt 0 ] || { echo "FAIL: boot 2 sent no PXE DHCPDISCOVER, so 'ignored' cannot be shown"; fail=1; }
[ "$(get 2 minute2win_replies)" = 0 ] || { echo "FAIL: minute2win replied on boot 2"; fail=1; }
[ "$(get 3 discover)" -gt 0 ] && [ "$(get 3 pxeclient)" -gt 0 ] || { echo "FAIL: boot 3 sent no PXE DHCPDISCOVER"; fail=1; }
[ "$(get 3 minute2win_replies)" -gt 0 ] || { echo "FAIL: positive control: re-armed boot 3 got no reply (responder dead?)"; fail=1; }
b1=$(grep -o '"bootedAt":"[^"]*"' "$W/out/state-1.json" || true); b3=$(grep -o '"bootedAt":"[^"]*"' "$W/out/state-3.json" || true)
[ -n "$b3" ] && [ "$b1" != "$b3" ] && grep -q '"armed":\[\]' "$W/out/state-3.json" || { echo "FAIL: boot 3 did not consume the new arm via /boot"; fail=1; }
[ $fail = 0 ] && echo "PASS ($MODE): boot 1 answered and arm consumed; boot 2 PXE DISCOVER seen and not answered; boot 3 (re-armed) answered and consumed; captures healthy"
exit $fail
