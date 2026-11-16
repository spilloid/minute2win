---
title: MCP control
---

# Drive minute2win through MCP

Version 0.6.0 adds MCP for setup, profile and media management, PXE,
arming/disarming and deployment monitoring. It uses the running host's existing
state and deployment guards. An LLM can work through missing prerequisites and
recoverable errors without consuming a boot ticket during inspection.

**Arming authorizes a disk wipe on the next network boot.** It does not power on
or reboot a target. Use an authorized console or power tool for that step.

## Start and connect

Enable the endpoint explicitly, with a token even on loopback:

```powershell
$env:M2W_TOKEN = 'replace-with-a-long-random-bootstrap-token'
.\minute2win-server.exe -mcp -open=false -listen 127.0.0.1:8080 -data .\data
```

For actual targets, use a reachable LAN listener and the normal boot flags.
MCP is at `/mcp`, using stateless Streamable HTTP with JSON responses. Pass
`Authorization: Bearer <token>` on every request. Protocol revisions
2025-03-26, 2025-06-18 and 2025-11-25 are supported. GET and DELETE return 405;
there are no server-pushed SSE streams or HTTP sessions. Cross-origin browser
requests are refused. Put remote management behind HTTPS.

For clients that launch a stdio process, use the same executable as a bridge:

```json
{
  "mcpServers": {
    "minute2win": {
      "command": "C:/tools/minute2win/minute2win-server.exe",
      "args": ["-mcp-stdio", "-mcp-url", "http://127.0.0.1:8080/mcp"],
      "env": {"M2W_TOKEN": "YOUR_PERSONAL_MCP_TOKEN"}
    }
  }
}
```

The bridge connects to an already running host. It never starts a second writer,
opens its state file or binds PXE ports. HTTP bridge URLs are limited to loopback;
remote URLs must use HTTPS. Stdout contains protocol messages only.

The server token has administrator rights. For routine imaging, create an
operator account with an explicit image list and issue a personal token using
`account_token_create`. The bridge's `M2W_TOKEN` can contain that personal token.
See [Accounts and SSO](AUTH.md) for local sign-in and native Authentik OIDC.

## Tools and permissions

| Tools | Role | Purpose |
| --- | --- | --- |
| `whoami`, `setup_status`, `setup_guide` | Viewer+ | Identity, readiness, official media sources and setup steps |
| `profiles_list`, `profile_get`, `profile_template`, `profile_validate` | Viewer+ | Inspect permitted images, templates, pins and media readiness |
| `deployments_list`, `deployment_get` | Viewer+ | Inspect permitted targets, phases, timing, stale reports and recovery |
| `machine_arm`, `machine_disarm` | Operator+ | Arm/revoke within the account's image scope |
| `profile_put`, `profile_delete` | Admin | Validate and edit profiles with optimistic concurrency |
| `media_list`, `media_inspect`, `media_fetch`, `media_import` | Admin | Inventory, hash, verify and acquire boot media |
| `jobs_list`, `job_get`, `job_cancel` | Admin | Recover IDs and poll/cancel bounded media operations |
| `deployment_abandon` | Admin | Close tracking after externally stopping a stuck target; records outcome unknown |
| `server_configure`, `pxe_start`, `pxe_stop` | Admin | Configure the host while idle and manage PXE listeners |
| `accounts_list`, `account_put`, `account_delete`, `account_token_create` | Admin | Manage local/SSO identities and scoped credentials |

The server also exposes four guide resources under `minute2win://guide/` and a
`guided_deployment` prompt. `tools/list` supplies the full argument schemas and
destructive/read-only annotations. Read the descriptions before calling tools.

## Walk an installation through completion

1. Call `whoami`, then `setup_status`. Choose an installer and use `setup_guide`.
   It names official sources and explains Windows ADK/WinPE, WIM extraction,
   iPXE/wimboot, Linux assets, SSH keys, hosting and diagnostic steps.
2. Acquire matching media. Supply a publisher-verified SHA-256 to `media_fetch`
   along with a direct HTTPS URL and `max_bytes`. `media_import` can copy from a
   startup-approved import root. Both return a `job_id`; poll `job_get` until
   terminal. Keep the same ID after a client timeout.
   A terminal job may be failed/cancelled: require `status=done` (or `success=true`)
   before using its output. `pin_verified` means its bytes match the supplied
   pin; the server does not authenticate the publisher or its checksum metadata.
3. Fill `profile_template`, save with `profile_put`, then run `profile_validate`.
   Windows `probe=true` checks hosted files with bounded HEAD requests. Linux
   checks the actual pinned files. Neither proves the target's reachability or
   WinPE certificate trust. A host HEAD check does not verify the Windows image
   bytes; the engine's full hash check occurs after partitioning.
4. After the user authorizes the exact target and image, call:

   ```json
   {"name":"machine_arm","arguments":{"mac":"52:54:00:12:34:56","manifest":"lab-win11","request_id":"lab-vm-install-001"}}
   ```

   **Reuse `request_id` for every retry of this operation.** The arm and receipt
   are saved in one state commit. A repeated request returns the original
   `run_id`; it cannot create another arm after boot, disarm or server restart.
   A conflicting target/image is refused. Receipts are retained at least seven
   days, with a bounded capacity of 4096. Memory-only hosts lose receipts at
   restart; use persistent `-data` for normal operation.
5. Network-boot the target using its separate console/power tool. Poll
   `deployment_get` with the MAC and returned `run_id`. It distinguishes pending
   arms, booting/running, terminal failure/done and reports older than 120 seconds.
   Missing, mismatched or stale reports mean the outcome is unknown. Linux first
   boot and large downloads can be quiet: inspect the target before acting.
6. Diagnose a failure using the returned message and `next_steps`. Do not
   automatically re-arm. Disarming a consumed arm cannot stop its installer.
   Target-reported completion still needs whatever installed-system checks the
   user asked for; it is not independent hardware verification.

## Media and configuration boundaries

`-mcp-media-dir` defaults to `<data>/media`. The `media` area uses that root,
`linux` uses the configured Linux asset root, and `pxe` uses `-pxe-dir` (or a
dedicated `pxe` subdirectory under the media root). Optional `-mcp-import-dir`
approves a separate read-only source root. Paths are confined with `os.OpenRoot`;
destinations are plain filenames. No tool provides arbitrary shell execution.

Downloads require HTTPS, refuse downgrade redirects, run for at most 30 minutes
and are capped at 32 GiB. At most four media jobs run concurrently. Hash/size
failures and cancellation remove unpublished partials. Existing destination
files are never overwritten; use a new filename and update profile pins.
Computed hashes alone do not establish publisher authenticity. Jobs do not
survive a host restart: inspect inventory before retrying an interrupted job.

Profile writes require the current `expected_sha256` when replacing/deleting.
Edits are blocked while an arm or running deployment references the profile.
Host configuration changes and PXE shutdown require no pending arms or active
installers. A stale report is still active for these checks. After independently
stopping the target, an administrator can call `deployment_abandon` with
`target_stopped=true` and a reason. It revokes delivery/report capabilities and
closes tracking with outcome unknown; it cannot stop or undo the installer.

`server_configure` saves image base, Linux root and arm TTL in
`<data>/mcp-settings.json` when persistence is configured. Explicit startup flags
override saved values. PXE start/stop is process-local. Listener address,
credentials, approved roots and service/container lifecycle remain startup
configuration. The tool returns concrete restart flags where relevant.

Windows license/consent choices, installing ADK, building WinPE, extracting ISO
contents, external storage uploads, driver acquisition and firmware/power actions
are guided tasks on their owning host/service. The MCP does not claim to perform
those operating-system or third-party actions itself.

## Errors an LLM can act on

Protocol problems use JSON-RPC errors (`-32700`, `-32600`, `-32601`, `-32602`).
Execution failures use `isError: true` with matching text and structured content:

```json
{"ok":false,"error":{"code":"missing_image_base","message":"...","next_steps":["Host the Windows boot files, then configure image_base."],"retryable":false}}
```

Common codes include `forbidden`, `invalid_mac`, `unknown_profile`,
`validation_failed`, `profile_conflict`, `profile_in_use`, `host_busy`,
`media_invalid`, `media_unreachable`, `media_exists`, `job_not_found`,
`request_conflict`, `run_not_current`, `not_armed`, `state_save_failed` and
`server_degraded`. An authentication failure is HTTP 401; check the personal
token, role changes and host endpoint. An HTTP 404 at `/mcp` usually means `-mcp`
is disabled or the proxy route is wrong. No error or timeout establishes that a
previous arm did not happen: reuse its request ID and inspect its run.

The transport follows the [MCP transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
and [tool-result conventions](https://modelcontextprotocol.io/specification/2025-11-25/server/tools).
