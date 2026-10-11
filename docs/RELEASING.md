---
title: Releasing Minute2Win
---

# Releasing Minute2Win

Releases package the server and deployment scripts. They do not publish Windows images or deploy boot files to an image host.

## Public repository preparation

Before changing repository visibility:

1. Review tracked files and Git history for credentials, customer data, private logs and artifacts. A public checkout exposes history as well as current files. This documentation revision is not a full history audit.
2. Create an active tag ruleset for `v*` that restricts creation, update and deletion to authorized release administrators. The private/free-plan limitation recorded on 2026-10-10 must not be carried forward as an exception for a public repository. Check workflow permissions and the `release` environment's permitted refs/reviewers.
3. Enable private vulnerability reporting in Settings → Security where available.
4. In Settings → Pages, select **GitHub Actions** as the build/deployment source. The `pages.yml` workflow publishes the docs on main and can be run manually. It builds PRs without publishing them.
5. Check the first Pages build and deployment. The expected project URL is `https://spilloid.github.io/minute2win/`; repository visibility and Pages settings are managed separately.
6. Confirm that release notes and linked issues disclose the current verification level. Default image URLs are reference configuration, not a public asset service.

Tag protection matters because a pushed tag selects the release workflow itself. The workflow also requires the tagged commit to be on main and pins that SHA before signing; those checks complement repository rules rather than replace them.

## Release checklist

1. Merge changes through a PR. CI must pass. Independently review non-trivial behavior/signing changes and record findings in `docs/dev-process.md`.
2. Update VERSION, the README version and CHANGELOG together. Keep `docs/STATUS.md` and implementation notes aligned with actual evidence. Documentation-only changes do not change the release version.
3. Tag the merged commit with an annotated `vX.Y.Z` tag and push it. `release.yml` also supports manual rebuilds of an existing tag. It refuses tags outside the strict version format or commits not on main.
4. Wait for build, Windows signing and signature/timestamp verification. The workflow creates or replaces assets only on a **draft** release and refuses to modify a published release.
5. Download all assets from that draft. Run `sha256sum -c SHA256SUMS`. Confirm the expected Linux amd64/arm64 archives, the x86_64 AppImage, Windows x64 zip, boot-files zip and SIGNATURES.txt are present.
6. Extract the Linux archive, check `-help`, run the AppImage (`--appimage-extract-and-run` style if the machine has no FUSE), and serve `/` on a loopback listener with a token. Inspect the Windows signature evidence and, on Windows, verify the extracted executable with Get-AuthenticodeSignature. Check the boot zip matches the `tools/stage.sh` tree. Record what actually ran, including any platform not exercised.
7. Write useful release notes: changes, setup requirements, verification evidence, and concrete known issues with tracking links. Do not claim an end-to-end Windows boot based on unit tests or signing success.
8. Publish as a normal release and mark it Latest. If a published artifact needs replacement, cut a patch release.
9. Deploying boot files to an operator image host is a separate deliberate action. Update between deployments and keep scripts and manifests consistent.

## Azure Artifact Signing setup

Windows signing uses Azure Artifact Signing with GitHub OIDC. No signing private key or PFX is stored in GitHub. The `release` environment supplies the configuration and Azure identifiers; only the signing job receives `id-token: write`.

Prerequisites: an active public-trust signing certificate profile and an Azure account authorized to create application registrations/service principals and assign the Certificate Profile Signer role. Use your own account/profile names and identifiers.

1. On a host where gh is authenticated as a repository administrator, run `scripts/setup-signing-github.sh prefix`. It configures immutable OIDC subject claims and prints the subject prefix.
2. On a host with a suitable Azure login, run:

   ```powershell
   .\scripts\setup-signing-azure.ps1 -AccountName <account> -ResourceGroup <resource-group> -ProfileName <profile> -SubjectPrefix <prefix>
   ```

   This creates the signing identity, federated credential and narrowly scoped role assignment, then prints JSON.
3. Feed the output to `scripts/setup-signing-github.sh apply < signing-config.json` on the GitHub-authenticated host. It configures the release environment, allowed refs, variables and Azure identifiers. Keep the temporary file private and delete it afterward.
4. Run the manual **signing check** workflow before tagging. It builds, signs and verifies a throwaway executable without publishing.

The split setup avoids moving a GitHub credential onto an Azure-authenticated host. The signing job fails if any required configuration is missing and does not fall back to unsigned Windows artifacts. The verifier checks the expected certificate subject and RFC 3161 timestamp.

## Documentation deployment

`pages.yml` builds the public Markdown guides with Jekyll, uploads a Pages artifact, and deploys from main to the `github-pages` environment. PR jobs build only and have no deployment permission. The site excludes the historical dev-process log and is independent of the release/signing environment.

GitHub's [custom Pages workflow guide](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages) describes the repository setting and deployment actions.
