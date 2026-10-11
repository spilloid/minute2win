"""Executes release.yml's publish-job shell steps (extracted from the YAML, so this tests the real text) against a mock `gh` and
temporary asset sets. Covers: the asset allowlist (good set, extra file, missing file) and the draft-only upload (replaces a draft's
assets, stops when listing the old assets fails, refuses a published release). Run: python3 tests/release_workflow_test.py"""
import os, subprocess, sys, tempfile, yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
wf = yaml.safe_load(open(os.path.join(ROOT, ".github/workflows/release.yml")))
steps = {s.get("name"): s["run"] for s in wf["jobs"]["publish"]["steps"] if "run" in s}
failures = []


def check(name, got, want):
    ok = got == want
    print(f"{'ok  ' if ok else 'FAIL'} {name}: {got}")
    if not ok:
        failures.append(name)


V = "0.1.1"
GOOD = ["SIGNATURES.txt", f"minute2win-{V}-bootfiles.zip", f"minute2win-server-{V}-linux-amd64.tar.gz",
        f"minute2win-server-{V}-linux-arm64.tar.gz", f"minute2win-server-{V}-x86_64.AppImage",
        f"minute2win-server-{V}-windows-x64.zip"]


def allowlist(files):
    d = tempfile.mkdtemp()
    for n in files:
        open(os.path.join(d, n), "w").close()
    return subprocess.run(["bash", "-c", steps["Allowlist the asset set"]], cwd=d, capture_output=True, text=True,
                          env={**os.environ, "TAG": f"v{V}"}).returncode


check("allowlist: exact set passes", allowlist(GOOD), 0)
check("allowlist: extra file refused", allowlist(GOOD + ["evil.exe"]), 1)
check("allowlist: missing file refused", allowlist(GOOD[:-1]), 1)
check("allowlist: renamed file refused", allowlist(GOOD[:-1] + ["minute2win-server-0.1.1-windows-x64.zip.exe"]), 1)


def upload(mock):
    d = tempfile.mkdtemp()
    os.makedirs(d + "/bin")
    open(d + "/a.zip", "w").close()
    log = d + "/log"
    open(d + "/bin/gh", "w").write('#!/usr/bin/env bash\necho "gh $*" >> ' + log + "\n" + mock)
    os.chmod(d + "/bin/gh", 0o755)
    r = subprocess.run(["bash", "-c", steps["Upload to a draft release"]], cwd=d, capture_output=True, text=True,
                       env={**os.environ, "PATH": d + "/bin:" + os.environ["PATH"], "TAG": f"v{V}", "GITHUB_REPOSITORY": "o/r"})
    calls = open(log).read()
    return r.returncode, "release upload" in calls, "delete-asset" in calls


check("upload: draft, old assets replaced", upload('case "$*" in *"--json isDraft"*) echo true;; *"--json assets"*) printf "old1.zip\\nold2.zip\\n";; esac; exit 0'), (0, True, True))
check("upload: listing failure stops before any change", upload('case "$*" in *"--json isDraft"*) echo true;; *"--json assets"*) exit 1;; esac; exit 0'), (1, False, False))
check("upload: published release refused", upload('case "$*" in *"--json isDraft"*) echo false;; esac; exit 0'), (1, False, False))
check("upload: new release created as a draft", upload('case "$*" in "release view"*) exit 1;; esac; exit 0'), (0, True, False))
sys.exit(1 if failures else 0)
