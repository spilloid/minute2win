#!/usr/bin/env bash
# The product page names a release in its download links and text; every one must be VERSION (STD-003 rule 1).
#   tests/site-version.sh [page]   (default: docs/index.html)
# Every three-part version number on the page counts, whatever precedes it ("v0.1.2", "Version 0.1.2", "Release 0.1.2", a bare
# "0.1.2 and earlier"); a four-part dotted number (an IPv4 address such as 127.0.0.1) is not a version and is skipped.
set -euo pipefail
cd "$(dirname "$0")/.."
page="${1:-docs/index.html}"
want="$(tr -d '[:space:]' < VERSION)"
found="$(grep -oE '(^|[^0-9.])[0-9]+[.][0-9]+[.][0-9]+([.][0-9]+)?' "$page" | sed -E 's/^[^0-9]//' | grep -vE '^[0-9]+([.][0-9]+){3}$' | sort -u || true)"
if [ -z "$found" ]; then echo "$page names no version; expected $want" >&2; exit 1; fi
if [ "$found" != "$want" ]; then echo "$page names version(s) $(echo "$found" | tr '\n' ' ')but VERSION is $want" >&2; exit 1; fi
echo "$page: every version number is $want ($(grep -c -F "$want" "$page") line(s))"
