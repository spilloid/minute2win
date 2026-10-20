#!/usr/bin/env bash
# Tests tools/stage.sh: every base form an operator may use (IPv6 literals included) stages the complete tree with the base written
# literally, and a refused or failed base leaves an earlier staging untouched.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
want_files="install.ps1
minute2win.ipxe
minute2win/Minute2Win.psm1
minute2win/Start-FirstBoot.ps1
minute2win/Start-Minute2Win.ps1
minute2win/VERSION
minute2win/manifests/win11-pro.json
minute2win/unattend-oobe.xml"
tree() { (cd "$1/windows11" && find . -type f | sed 's#^\./##' | LC_ALL=C sort); }

# 1. each base stages the whole tree; the "isset" default becomes the base (trailing slash dropped), the guard keeps the placeholder
for base in 'http://[fd00::1]:8080/windows11' 'https://deploy.lab.internal/windows11/' 'http://192.168.1.10:8000/w11' 'http://[fe80::1]/a.b+c'; do
  out="$W/out"; rm -rf "$out"
  "$ROOT/tools/stage.sh" "$out" "$base" >/dev/null || fail "$base: stage.sh exited $?"
  [ "$(tree "$out")" = "$want_files" ] || fail "$base: incomplete tree: $(tree "$out")"
  ipxe="$out/windows11/minute2win.ipxe"
  grep -Fqx -- "isset \${m2w-base} || set m2w-base ${base%/}" <(tr -d '\r' < "$ipxe") || fail "$base: base not written literally"
  grep -Fq -- 'iseq ${m2w-base} @M2W_BASE@ && goto nobase' "$ipxe" || fail "$base: the no-base guard lost its placeholder"
  [ "$(grep -c '@M2W_BASE@' "$ipxe")" = 1 ] || fail "$base: placeholder count changed"
done

# 2. no base: the script is staged unchanged (boots nothing until m2w-base is set)
rm -rf "$W/out"; "$ROOT/tools/stage.sh" "$W/out" >/dev/null
cmp -s "$ROOT/boot/minute2win.ipxe" "$W/out/windows11/minute2win.ipxe" || fail "no base: script changed"

# 3. a refused base, or a base that could not be written, leaves the earlier staging exactly as it was
rm -rf "$W/out"; "$ROOT/tools/stage.sh" "$W/out" 'https://deploy.lab.internal/windows11' >/dev/null
before="$(cd "$W/out" && find . -type f -exec cksum {} + | LC_ALL=C sort)"
for base in 'ftp://deploy.lab.internal/w' 'https://deploy.lab.internal/w x' 'https://h/#frag' 'https://h/a&b' 'https://h/a\b' 'https://h/${x}' "https://h/a'b"; do
  if "$ROOT/tools/stage.sh" "$W/out" "$base" >/dev/null 2>&1; then fail "$base: accepted"; fi
done
mkdir "$W/bin"; printf '#!/usr/bin/env bash\nexit 0\n' > "$W/bin/sed"; chmod +x "$W/bin/sed"   # a sed that writes nothing
if PATH="$W/bin:$PATH" "$ROOT/tools/stage.sh" "$W/out" 'https://other.lab.internal/w' >/dev/null 2>&1; then fail "unwritten base accepted"; fi
[ "$(cd "$W/out" && find . -type f -exec cksum {} + | LC_ALL=C sort)" = "$before" ] || fail "a failed staging changed the earlier output"
echo "stage.sh: all cases pass"
