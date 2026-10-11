# Security

Minute2Win authorizes unattended disk erasure. Treat the deployment host, its token and its boot/image hosting as privileged infrastructure.

## Report a vulnerability

Use GitHub's private vulnerability reporting option in this repository's Security tab if it is enabled. If it is unavailable, contact the maintainer through the contact information on their GitHub profile to arrange a private report. Do not post working exploit details or credentials in a public issue.

Only the current release line is actively maintained. There is no guaranteed response time or production-support commitment during this early release.

## Current operating assumptions

Use a trusted deployment network, set M2W_TOKEN, restrict access to the server and boot assets, and use HTTPS for image/script hosting where supported. The server's native HTTP listener does not encrypt bearer tokens. Arming/disarming require the token when set; state, boot and manifest delivery routes are not protected by that operator token.

The SHA-256 verifies image bytes against the manifest; it does not authenticate a maliciously replaced manifest or loader. Protect the whole boot chain. A deployment report key may be retrieved by another LAN host during delivery, allowing forged board status; confirm outcomes from the installed system and logs. See [Status](docs/STATUS.md) for accepted limitations.

For public repositories, maintainers must protect release tag creation, updates and deletion before relying on signing. See [Releasing](docs/RELEASING.md).
