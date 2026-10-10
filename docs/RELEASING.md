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
