# Contributing

Start with [Status](docs/STATUS.md) and the existing issues. For a substantial behavior change, describe the operator problem and proposed approach in an issue before implementation.

## Build and checks

Build from source with the current stable Go toolchain:

```bash
cd server
go build -o minute2win-server .
go vet ./...
go test -race ./...
```

From the repository root:

| Command | Coverage |
| --- | --- |
| tests/run.sh | Pester and PSScriptAnalyzer in Podman, including PowerShell 5.1 syntax |
| tests/ui/run.sh | Playwright console flow and screenshots; inspect captures |
| tests/site-version.sh | The product page names only the VERSION release |
| tests/pxe/pxe-lan.sh | QEMU BIOS iPXE simulation on a bridged LAN |
| tests/vm/deploy-vm.sh | Intended full VM chain; requires external assets, kvm-it harness and substantial free disk space |
| tests/setup-signing-github.test.sh | GitHub signing setup script with a fake gh |
| python3 tests/release_workflow_test.py | Release shell steps with mock gh; requires PyYAML |

Read each harness before running it. The VM deployment harness uses a disposable disk and screenshots; it does not independently assert a successful Windows installation. CI runs PowerShell checks, Go tests/cross-builds, Playwright, workflow lint and script checks.

## Change requirements

- Keep WinPE code compatible with Windows PowerShell 5.1. Put testable logic in `engine/Minute2Win.psm1` and orchestration in `Start-Minute2Win.ps1`.
- Preserve zero-touch deployment and existing guards. Do not add a required confirmation prompt or mandatory disk selection.
- Add meaningful regression coverage for behavior changes. State whether a deployment change was boot-verified and describe the environment.
- Update relevant guides and CHANGELOG in the same change. Behavior releases update VERSION, README and changelog together; documentation-only changes need no version bump.
- Submit a PR with the provided template. Maintainers arrange independent adversarial review for non-trivial behavior or release/security changes, reproduce findings and log outcomes in `docs/dev-process.md`.
- Use synthetic data in examples, fixtures, screenshots and logs. Do not submit secrets or customer data.

## Documentation

Public guides live in `docs/`. The Pages home is the static product page `docs/index.html` (styles in `docs/assets/site.css`); the documentation home is `docs/guide.md`. Keep the product page within the evidence in `docs/STATUS.md`, keep its disk-erase warning visible, and label demo screenshots as such. The Pages workflow builds them with Jekyll and deploys only from main. Markdown guides remain readable on GitHub. Keep local links relative and follow the existing front matter/layout; use .md links in source, which the Pages build converts to .html destinations.

`docs/dev-process.md` is historical engineering evidence and is excluded from the Pages build. CLAUDE.md contains implementation guidance; the public source of verification claims is `docs/STATUS.md`. Never promote a claim from unit-tested to VM/hardware-verified without actual boot evidence.
