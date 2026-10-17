#!/usr/bin/env bash
# The product page names a release in its download links and text; every one must be VERSION (STD-003 rule 1).
#   tests/site-version.sh [page]   (default: docs/index.html)
set -euo pipefail
cd "$(dirname "$0")/.."
page="${1:-docs/index.html}"
want="$(tr -d '[:space:]' < VERSION)"
found="$(grep -oE '(\bv|[Vv]ersion )[0-9]+\.[0-9]+\.[0-9]+' "$page" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | sort -u || true)"
if [ -z "$found" ]; then echo "$page names no version; expected $want" >&2; exit 1; fi
if [ "$found" != "$want" ]; then echo "$page names version(s) $(echo "$found" | tr '\n' ' ')but VERSION is $want" >&2; exit 1; fi
echo "$page: every named version is $want ($(grep -cE "(\bv|[Vv]ersion )$want" "$page") line(s))"
