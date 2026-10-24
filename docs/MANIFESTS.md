---
title: Manifest reference
---

# Manifest reference

```json
{
  "schemaVersion": 1,
  "deployment": { "name": "Windows 11 Pro" },
  "image": { "source": "https", "uri": "https://.../install.wim", "sha256": "<64 hex>", "edition": "Windows 11 Pro" },
  "disk": { "minSizeGB": 64, "stagingGB": 12 },
  "oobe": { "adminUser": null },
  "tenant": { "id": "none", "joinMethod": "none" },
  "guard": { "countdownSeconds": 10 },
  "report": { "url": "http://deploy-server:8080/api/report" }
}
```

- `image.sizeBytes`: optional. The size is normally learned from the image server before the wipe; if the server reports none and
  this is unset, the deploy stops before touching the disk.
- `image.source`: only `https` is implemented (`smb` goes behind the same function later). The hash is required and checked
  before anything is applied. The edition is matched by name, since indices differ between ISOs; `image.index` is the fallback.
- `oobe.adminUser`: when it is set, OOBE is skipped and that local admin is created with the public baseline password
  `Password1!`, which Windows forces to change at the next logon. Manifests never hold secrets.
- `report.url`: optional. minute2win-server fills it in. Reporting is best-effort and never stops a deploy.
- `tenant.joinMethod`: `none` works. `ad`, `entra` and `hybrid` are accepted but only logged as "not implemented yet".

Logs: `X:\minute2win\logs` during the deploy, then copied to `C:\Windows\Logs\minute2win\` (the deploy transcript,
`hardware.json`, `setupcomplete.log`, `firstboot.log` and `firstboot-result.json`). On failure the engine stops at the console
and does not reboot.


Profiles are the filename without `.json`. The server offers only JSON files directly inside its manifest directory. Templates in `manifests/examples/` are not offered. Copy a template to your own manifest directory and replace its image URL and SHA-256 before use.

The shipped `win11-pro` profile is a template: its image URL is the placeholder `https://your-image-host.invalid/windows11/install.wim` and its hash is all zeros. Supply your own licensed image, set its URL and SHA-256, and the server will arm it. (Releases up to 0.1.2 shipped it pointing at the maintainer's restricted host instead.)

When arming, the server refuses a profile that is not valid JSON (a byte-order mark included), whose `image.uri` is missing, not `https://`, or on a documentation placeholder host (`example.com`, `example.net`, `example.org`, `*.example`, `*.invalid`), or whose `image.sha256` is not 64 hex digits or is all zeros. The refusal names the profile and the problem. This is not the engine's full validation, which still runs in WinPE before the disk is touched.

ARM64 PXE file selection does not establish ARM64 Windows deployment support: the supplied WinPE builder builds amd64.

Save server profile JSON as UTF-8 **without a byte-order mark**. Windows PowerShell 5.1 `Set-Content -Encoding UTF8` adds a BOM; use `[IO.File]::WriteAllText($path, $json, (New-Object Text.UTF8Encoding($false)))` or an editor configured for UTF-8 without BOM. A malformed profile is still listed in the console, but arming it is refused with the JSON error.
