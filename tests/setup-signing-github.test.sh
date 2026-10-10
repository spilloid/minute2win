#!/usr/bin/env bash
# Tests scripts/setup-signing-github.sh against a fake `gh` (no network, no GitHub): exact-match reconciliation of the allowed refs
# (a literal "v*", not a regex), idempotent reruns, and every failed write failing the script.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir "$W/bin"; : > "$W/calls.log"
cat > "$W/bin/gh" <<'FAKE'
#!/usr/bin/env bash
echo "gh $*" >> "$CALLS"
case "$*" in
  *"deployment-branch-policies"*"--method POST"*|*"--method POST"*"deployment-branch-policies"*) cat >/dev/null; [ -z "${FAIL_POST:-}" ] || exit 1; exit 0 ;;
  *"--method DELETE"*) exit 0 ;;
  *"--method PUT"*) cat >/dev/null; exit 0 ;;
  "api repos/"*"/deployment-branch-policies") printf '%s' "${POLICIES:-{\"branch_policies\":[]\}}"; exit 0 ;;
  "variable set"*) [ -z "${FAIL_VAR:-}" ] || exit 42; exit 0 ;;
  "secret set"*) cat >/dev/null; [ -z "${FAIL_SECRET:-}" ] || exit 42; exit 0 ;;
esac
exit 0
FAKE
chmod +x "$W/bin/gh"
export PATH="$W/bin:$PATH" CALLS="$W/calls.log" REPO=spilloid/minute2win
RES='{"repo":"spilloid/minute2win","environment":"release","variables":{"SIGNING_ACCOUNT":"a"},"secrets":{"AZURE_CLIENT_ID":"c"},"deploymentBranches":["main"],"deploymentTags":["v*"]}'
fail() { echo "FAIL: $*" >&2; cat "$W/calls.log" >&2; exit 1; }

# 1. fresh environment: both rules created
: > "$W/calls.log"; echo "$RES" | "$ROOT/scripts/setup-signing-github.sh" apply >/dev/null
[ "$(grep -c -- '--method POST' "$W/calls.log")" = 2 ] || fail "expected two rule creations"

# 2. rerun with the rules already present (a literal v*): nothing created, nothing deleted
: > "$W/calls.log"
POLICIES='{"branch_policies":[{"id":1,"type":"branch","name":"main"},{"id":2,"type":"tag","name":"v*"}]}' \
  bash -c 'echo "$0" | "$1/scripts/setup-signing-github.sh" apply >/dev/null' "$RES" "$ROOT"
grep -q -- '--method POST' "$W/calls.log" && fail "rerun recreated an existing rule"
grep -q -- '--method DELETE' "$W/calls.log" && fail "rerun deleted a wanted rule"

# 3. a stale rule is removed, a wanted one kept
: > "$W/calls.log"
POLICIES='{"branch_policies":[{"id":1,"type":"branch","name":"main"},{"id":2,"type":"tag","name":"v*"},{"id":9,"type":"branch","name":"scratch"}]}' \
  bash -c 'echo "$0" | "$1/scripts/setup-signing-github.sh" apply >/dev/null' "$RES" "$ROOT"
[ "$(grep -c -- '--method DELETE' "$W/calls.log")" = 1 ] && grep -q 'policies/9 ' "$W/calls.log" || grep -q 'policies/9$' "$W/calls.log" || fail "stale rule 9 not removed"

# 4. every failed write fails the script (no "Done" on failure)
for v in FAIL_VAR FAIL_SECRET FAIL_POST; do
  if env "$v=1" bash -c 'echo "$0" | "$1/scripts/setup-signing-github.sh" apply >"$2" 2>&1' "$RES" "$ROOT" "$W/out.txt"; then fail "$v: script exited 0"; fi
  grep -q '^Done' "$W/out.txt" && fail "$v: printed Done after a failure"
done
echo "setup-signing-github: all checks passed"
