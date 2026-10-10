## What changed

<!-- What problem does this solve? What's the actual change? -->

## How it was checked

- [ ] `tests/run.sh` passes (Pester + PSScriptAnalyzer incl. PowerShell 5.1 syntax)
- [ ] Docs, CHANGELOG and VERSION updated in the same change if behaviour changed
- [ ] Non-trivial diff adversarially reviewed by a different model; findings reproduced; logged in `docs/dev-process.md`
- [ ] Deployment behaviour: either run end to end (`tests/vm/deploy-vm.sh`, or a named physical machine) or explicitly marked "not boot-verified"

## Evidence

<!-- Logs, screenshots, or a short reproduction. -->

## Data safety

- [ ] All examples, logs, screenshots and fixtures in this PR are synthetic: no real credentials, tenant IDs, serial numbers, customer or personal data.
