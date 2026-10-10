#!/usr/bin/env bash
# GITHUB HALF of the signing setup (the Azure half is scripts/setup-signing-azure.ps1). Idempotent. Needs `gh` signed in as a
# repo admin. No Azure access and no key material here; the Azure identifiers are IDs, not secrets that can sign anything.
#
#   scripts/setup-signing-github.sh prefix                 # print the OIDC subject prefix to give the Azure half
#   scripts/setup-signing-github.sh apply < result.json    # JSON printed by setup-signing-azure.ps1
#
# `apply` creates the `release` environment, restricts it to main and v* tags (so a branch anyone with write access can push
# cannot sign as this identity), and sets the variables and the three Azure identifiers as environment-scoped values.
set -euo pipefail
REPO="${REPO:-spilloid/minute2win}"
cmd="${1:-}"
case "$cmd" in
  prefix)
    # Turn on immutable subject claims first (numeric IDs in the subject: a renamed or re-created repo cannot inherit the trust).
    cur="$(gh api "repos/$REPO/actions/oidc/customization/sub")"
    if [ "$(jq -r .use_immutable_subject <<<"$cur")" != "true" ]; then
      echo '{"use_default":true,"use_immutable_subject":true}' | gh api --method PUT "repos/$REPO/actions/oidc/customization/sub" --input - >/dev/null
      cur="$(gh api "repos/$REPO/actions/oidc/customization/sub")"
    fi
    jq -r .sub_claim_prefix <<<"$cur"
    ;;
  apply)
    in="$(cat)"
    env="$(jq -r .environment <<<"$in")"
    [ "$(jq -r .repo <<<"$in")" = "$REPO" ] || { echo "result is for $(jq -r .repo <<<"$in"), not $REPO" >&2; exit 1; }
    gh api --method PUT "repos/$REPO/environments/$env" --input - >/dev/null <<<'{"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}'
    have="$(gh api "repos/$REPO/environments/$env/deployment-branch-policies" -q '.branch_policies[] | "\(.type) \(.name) \(.id)"')"
    want=$( { jq -r '.deploymentBranches[] | "branch " + .' <<<"$in"; jq -r '.deploymentTags[] | "tag " + .' <<<"$in"; } )
    while read -r t n; do
      grep -qx "$t $n [0-9]*" <<<"$have" && { echo "already allowed: $t $n"; continue; }
      jq -nc --arg n "$n" --arg t "$t" '{name:$n,type:$t}' | gh api --method POST "repos/$REPO/environments/$env/deployment-branch-policies" --input - >/dev/null
      echo "allowed: $t $n"
    done <<<"$want"
    while read -r t n id; do
      [ -z "$t" ] && continue
      grep -qx "$t $n" <<<"$want" || { gh api --method DELETE "repos/$REPO/environments/$env/deployment-branch-policies/$id" >/dev/null; echo "removed stale rule: $t $n"; }
    done <<<"$have"
    for k in $(jq -r '.variables | keys[]' <<<"$in"); do
      v="$(jq -r --arg k "$k" '.variables[$k]' <<<"$in")"
      [ -n "$v" ] && gh variable set "$k" --env "$env" --repo "$REPO" --body "$v" >/dev/null && echo "variable $k"
    done
    for k in $(jq -r '.secrets | keys[]' <<<"$in"); do
      jq -r --arg k "$k" '.secrets[$k]' <<<"$in" | tr -d '\n' | gh secret set "$k" --env "$env" --repo "$REPO" >/dev/null && echo "secret   $k"
    done
    echo "Done. Nothing has been signed yet; prove it with the signing-check workflow before tagging."
    ;;
  *) echo "usage: $0 prefix | apply < result.json" >&2; exit 2 ;;
esac
