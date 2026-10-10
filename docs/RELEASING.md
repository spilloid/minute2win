# Releasing minute2win

A checklist (STD-003 rule 3): every step is a step.

1. **Before.** Changes go in through a PR using the template (STD-002). Non-trivial diffs get an astra review, adjudicated and
   logged in `docs/dev-process.md`, with a re-review after the fix round (STD-001). CI is green (`ci.yml`: Pester + lint, Go,
   Playwright).
2. **Docs and version.** `VERSION`, the README version line and the top CHANGELOG entry agree. The README verification table,
   the CHANGELOG and CLAUDE.md's dated re-verify list all use the same claim levels: *unit-tested*, *VM-verified*,
   *hardware-verified*. Nothing is called booted unless it booted.
3. **Merge, then tag the merge commit:** `git tag -a vX.Y.Z -m "minute2win X.Y.Z"` and push the tag. `release.yml` builds the
   assets (Linux server for amd64/arm64, the boot-files zip, `SHA256SUMS`) and attaches them to a draft release.
4. **Verify the assets** from the release page: download them, check `sha256sum -c SHA256SUMS`, check the server
   runs (`--help`, serves `/`), and check the zip holds the `tools/stage.sh` tree.
5. **Publish as a normal (Latest) release.** Anything not yet verified or known to be unsafe goes in the notes' "Known issues"
   **and** a linked tracking issue, written concretely: what, where, impact, workaround.
6. **Deploying the boot files** (uploading to R2 or a LAN web server) is a separate, deliberate step, not part of a release.

## CI signing (one-time setup)

The Windows executable is signed in CI with [Azure Artifact Signing](https://learn.microsoft.com/azure/artifact-signing/), the same
setup as SpoolSmith and kvm-it (D-0047). There is no key, PFX or password anywhere (STD-006): the `sign` job gets a short-lived
token through a GitHub OIDC federated credential that trusts exactly `<repo>:environment:release`.

Prerequisite (already true for the studio): a signing account with an **Active** public-trust certificate profile
(`jdspille` / `primary-profile`, East US). Then, once per repo:

1. **GitHub half, step 1** (wherever `gh` is signed in as a repo admin):
   `scripts/setup-signing-github.sh prefix` turns on immutable OIDC subject claims and prints the subject prefix.
2. **Azure half** (wherever `az login` works for someone who can create app registrations and assign roles; it needs no GitHub
   access): `pwsh scripts/setup-signing-azure.ps1 -AccountName jdspille -ResourceGroup RG0 -ProfileName primary-profile -SubjectPrefix <prefix>`
   creates the `minute2win-release-signing` app registration and service principal, the federated credential, and the **Certificate
   Profile Signer** role on that profile only. It prints JSON.
3. **GitHub half, step 2:** `scripts/setup-signing-github.sh apply < that.json` creates the `release` environment, restricts it to
   `main` and `v*` tags, and sets the variables and the three Azure identifiers (IDs, not key material).
4. Run the **signing check** workflow (Actions, run manually). It builds, signs and verifies a throwaway executable and publishes
   nothing. Do this before the first tag and after any change here.

Why two scripts: so no GitHub token has to be copied onto the machine that holds the `az` login. Both are idempotent.
If signing is not configured, `release.yml` fails before signing and attaches **nothing**: a release is never published unsigned.

## Who can start a signing run (tag protection)

A `v*` tag push starts `release.yml`, and the `release` environment allows `main` and `v*` tags, so whoever can push a tag can start a
signing run. Controls in place: the build job refuses any tag whose commit is not on `main` and pins that commit for the signing job
(a tag on an unreviewed commit cannot be built or signed); the tag is resolved under `refs/tags/` so a branch of the same name cannot
stand in for it; and only the signing job holds the OIDC identity. **Known gap:** GitHub tag rulesets (restricting who may create or
move `v*` tags) are not available on a private repo on the free plan (HTTP 403 when tried 2026-10-10). Because the workflow file
itself comes from the tagged commit, an attacker with write access could push a tag on a commit that edits the workflow. Today
the only writer is the owner. **Before anyone else gets write access, or if the repo goes public, enable a ruleset** for
`refs/tags/v*` (restrict creation, update and deletion to admins). The release also only ever writes a **draft** and refuses to touch a
published release.
