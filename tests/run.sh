#!/usr/bin/env bash
# Unit tests + lint in a PowerShell container (the Linux host has no pwsh): tests/run.sh
# Lint includes PSUseCompatibleSyntax for Windows PowerShell 5.1, which is what WinPE runs.
set -euo pipefail
cd "$(dirname "$0")/.."
exec podman run --rm -v "$PWD":/src:Z,ro -w /src "${PWSH_IMAGE:-mcr.microsoft.com/powershell:lts-alpine-3.17}" pwsh -NoProfile -File tests/ci.ps1
