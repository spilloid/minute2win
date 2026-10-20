# Security

Minute2Win authorizes unattended disk erasure. Treat the deployment host, its token and its boot/image hosting as privileged infrastructure.

## Report a vulnerability

Use GitHub's private vulnerability reporting option in this repository's Security tab if it is enabled. If it is unavailable, contact the maintainer through the contact information on their GitHub profile to arrange a private report. Do not post working exploit details or credentials in a public issue.

Only the current release line is actively maintained. There is no guaranteed response time or production-support commitment during this early release.

## Current operating assumptions

Use a trusted deployment network, set M2W_TOKEN, restrict access to the server and boot assets, and use HTTPS for image/script hosting where supported. The server's native HTTP listener does not encrypt bearer tokens. Arming/disarming require the token when set; state, boot and manifest delivery routes are not protected by that operator token.

The server listens on loopback by default and refuses to listen on a network-reachable address without M2W_TOKEN unless started with `-insecure-no-token` (judged on the address actually bound, not on a separate DNS lookup). It refuses cross-origin browser requests to arm, disarm and report. Using up an arm needs a one-time ticket that `GET /boot/{mac}` hands to iPXE; that GET itself changes nothing, and a page on another site cannot read its response, so a blind cross-site request (for example an `<img>` over plain HTTP, which carries no `Origin` or `Sec-Fetch-Site`) cannot consume an arm. A DNS-rebinding page, or anyone who can read the plain-HTTP traffic, can still learn the ticket and use up (not create) an arm. It requires a JSON content type on JSON mutations, and, with no token, accepts arming only at an IP address or `localhost` (a DNS-rebinding guard). There is no default image or boot-asset host. Release 0.1.2 and earlier had none of these protections: with those builds, always set M2W_TOKEN, even on loopback.

The SHA-256 verifies image bytes against the manifest; it does not authenticate a maliciously replaced manifest or loader. Protect the whole boot chain. A deployment report key may be retrieved by another LAN host during delivery, allowing forged board status; confirm outcomes from the installed system and logs. See [Status](docs/STATUS.md) for accepted limitations.

For public repositories, maintainers must protect release tag creation, updates and deletion before relying on signing. See [Releasing](docs/RELEASING.md).
