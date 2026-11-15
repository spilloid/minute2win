# Linux container host

The container packages the headless Go server and the public example manifests.
Operator profiles, boot assets, state and credentials stay outside the image.
The runtime uses UID/GID 65532 and a small distroless image with certificate roots;
there is no shell or package manager in the running image.

Build locally with Docker from the repository root. The Container image workflow
also publishes source-SHA tags for main and feature branches. These are deployment
artifacts, not signed Windows releases. Pin the registry digest in production.
The workflow selects current stable Go; the Dockerfile's default tracks the module
minimum. Publication never includes the local .git directory or operator files.

Supply the operator token through M2W_TOKEN, using the container platform's secret
facility. Mount profiles and Linux/iPXE assets read-only, and mount persistent state
read-write. The state directory must be writable by UID/GID 65532. Provide all the
usual server flags explicitly, including -open=false. The default container command
is a loopback-only, non-persistent preview and cannot arm the placeholder profile.

For native PXE/TFTP use Linux host networking and grant only NET_BIND_SERVICE for
UDP 69. TFTP negotiates a new UDP transfer port, so forwarding UDP 69 alone through
a normal Kubernetes Service does not carry the full transfer. Use a fixed LAN
worker address, a single replica, Recreate updates and ClusterFirstWithHostNet DNS.
Keep the web console on the trusted deployment LAN; do not publish boot capabilities
or the operator token through a public ingress. See the [server reference](SERVER.md)
and [Linux profiles](LINUX.md).

The amd64 executable carries only the NET_BIND_SERVICE file capability so a
runtime can retain that declared capability across non-root exec. Keep it in the
container capability bounding/permitted sets; no other capability is required.
CI tests UDP 69 with UID 65532, a read-only root, no-new-privileges, all other
capabilities dropped and the usual privileged-port threshold restored inside the
test container's own network namespace. The host sysctl is not changed.

If the router already supplies per-target boot rules, keep proxyDHCP on unused
ephemeral ports with -pxe-dhcp-port=0 and -pxe-proxy-port=0, while retaining TFTP 69.
Set unsupported architecture filenames empty. Never run a second installer host
against the same state directory. Keep the old host disarmed during migration,
verify HTTP, actual TFTP transfers, hashes and the arm/disarm lifecycle, then move
the router's target-only boot address. Restarting the server drops all arms and
unfinished Linux boot-delivery snapshots; move only when installations are idle.
