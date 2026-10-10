#!/usr/bin/env bash
# Built-in PXE on a simulated LAN: one privileged podman container holds a bridge, "the network's DHCP server" (dnsmasq, plain DHCP,
# no PXE options, in its own network namespace like a router), minute2win-server -pxe on the bridge, and a QEMU VM on a tap.
# It arms the VM's MAC, boots it twice and checks: boot 1 gets a proxyDHCP offer and fetches /boot (arm consumed), boot 2 is ignored.
#
#   tests/pxe/pxe-lan.sh [ipxe-dir]     ipxe-dir: a folder with your ipxe.efi (default: kvm-it's firmware/ipxe/signed, IPXE.EFI)
#
# MODE=bios (default): SeaBIOS + QEMU's own iPXE NIC ROM. That client is already iPXE, so this exercises the iPXE stage (HTTP boot
# URL), interface-address selection, the broadcast reply, and arm consumption. It does NOT exercise firmware PXE + TFTP.
# MODE=uefi: OVMF from the host (/usr/share/edk2/ovmf). On 2026-10-10 OVMF never attempted a network boot in this harness (no DHCP
# seen), so the firmware-PXE + TFTP stage is still unrun here.
# Needs podman, /dev/kvm; builds localhost/m2w-pxe-test (Debian + qemu + dnsmasq) once. Nothing leaves the container's network.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"; ROOT="$HERE/../.."
IPXE_DIR="${1:-$ROOT/../kvm-it/firmware/ipxe/signed}"; MODE="${MODE:-bios}"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/tftp" "$W/out"
if [ -f "$IPXE_DIR/ipxe.efi" ]; then cp "$IPXE_DIR/ipxe.efi" "$W/tftp/"; elif [ -f "$IPXE_DIR/IPXE.EFI" ]; then cp "$IPXE_DIR/IPXE.EFI" "$W/tftp/ipxe.efi"; fi
(cd "$ROOT/server" && CGO_ENABLED=0 go build -o "$W/minute2win-server" .)
podman image exists localhost/m2w-pxe-test || podman build -q -t localhost/m2w-pxe-test - >/dev/null <<'EOF'
FROM docker.io/library/debian:stable-slim
RUN apt-get update && apt-get install -y --no-install-recommends qemu-system-x86 ipxe-qemu dnsmasq-base iproute2 curl ca-certificates && rm -rf /var/lib/apt/lists/*
EOF
cat > "$W/run.sh" <<'EOF'
#!/bin/bash
set -u
MAC=52:54:00:12:34:56
ip link add br0 type bridge && ip addr add 10.77.0.1/24 dev br0 && ip link set br0 up
ip tuntap add tap0 mode tap && ip link set tap0 master br0 && ip link set tap0 up
unshare -n sleep 100000 & NP=$!; sleep 0.5
ip link add vh type veth peer name vd && ip link set vd netns $NP && ip link set vh master br0 && ip link set vh up
N="nsenter -t $NP -n"
$N ip addr add 10.77.0.2/24 dev vd; $N ip link set vd up
$N dnsmasq --no-daemon --port=0 --interface=vd --dhcp-range=10.77.0.100,10.77.0.150,1h --log-dhcp --dhcp-leasefile=/tmp/l > /out/dnsmasq.log 2>&1 &
mkdir -p /tmp/m && cp /m/win11-pro.json /tmp/m/
/w/minute2win-server -listen :8080 -manifests /tmp/m -data "" -image-base http://10.77.0.1:8080/no-images -pxe -pxe-dir /w/tftp >> /out/server.log 2>&1 &
sleep 1
curl -s -X POST -d "{\"mac\":\"$MAC\",\"manifest\":\"win11-pro\"}" http://127.0.0.1:8080/api/armed > /dev/null
FW=""
if [ "$MODE" = uefi ]; then cp /fw/OVMF_VARS.fd /tmp/vars.fd; FW="-drive if=pflash,format=raw,readonly=on,file=/fw/OVMF_CODE.fd -drive if=pflash,format=raw,file=/tmp/vars.fd"; fi
boot() { echo "== boot $1" >> /out/server.log; timeout 45 qemu-system-x86_64 -enable-kvm -m 1024 -machine q35 $FW \
  -netdev tap,id=n0,ifname=tap0,script=no,downscript=no -device virtio-net-pci,netdev=n0,mac=$MAC \
  -display none -serial file:/out/serial-$1.log > /dev/null 2>&1; }
boot 1; curl -s http://127.0.0.1:8080/api/state > /out/state-1.json
boot 2
EOF
chmod +x "$W/run.sh"
FW=(); [ -d /usr/share/edk2/ovmf ] && FW=(-v /usr/share/edk2/ovmf:/fw:ro)
podman run --rm --privileged --device /dev/kvm --security-opt label=disable -e MODE="$MODE" -v "$W":/w -v "$W/out":/out "${FW[@]}" \
  -v "$ROOT/manifests":/m:ro localhost/m2w-pxe-test /w/run.sh 2>/dev/null || true
grep -a 'pxe: \|tftp: \|== boot' "$W/out/server.log" || true
fail=0
grep -aq 'pxe: offered 52:54:00:12:34:56' "$W/out/server.log" || { echo "FAIL: no offer on boot 1"; fail=1; }
grep -q '"status":"booting"' "$W/out/state-1.json" || { echo "FAIL: /boot was not fetched (arm not consumed)"; fail=1; }
sed -n '/== boot 2/,$p' "$W/out/server.log" | grep -aq 'pxe: offered' && { echo "FAIL: boot 2 got an offer"; fail=1; }
[ $fail = 0 ] && echo "PASS ($MODE): offered on boot 1, /boot consumed the arm, boot 2 ignored"
exit $fail
