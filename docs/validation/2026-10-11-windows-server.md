# Windows server validation — 2026-10-11

Tested from an isolated lab source copy on a Windows 11 QEMU/Q35 guest, using Go 1.27.1 windows/amd64. No existing OS disk was reimaged.

## Failure reproduced on 0.1.1

Native `go test ./...` failed across arming, persistence, PXE and UI tests. The shared cause was directory `File.Sync` returning `Access is denied` after the state-file rename. Rollback hit the same failure, so the server entered degraded mode and refused further arms.

## Fix and evidence

- Flush the temporary state file as before. On Windows, replace it using MoveFileExW with REPLACE_EXISTING and WRITE_THROUGH flags. On other platforms, retain rename followed by parent-directory sync.
- All native Windows server tests and `go vet ./...` passed after the fix, including the new test for repeated replacements under a Unicode directory/filename, retained deployment history and dropped arms on restart.
- Linux `go vet ./...` and `go test -race ./...` passed. PowerShell unit tests passed 46/46; compatibility lint passed. Workflow lint and mocked signing/release-script tests passed.
- Playwright console flow passed against the fixed Linux cross-build (arm, boot, report, disarm, desktop/mobile and light/dark). The documentation site also builds with the exact GitHub Pages action image.
- In-session Windows runtime check: console GET returned 200, state showed tokenRequired=true and degraded=false, an unauthenticated arm returned 401, and a synthetic MAC could be armed and disarmed with the token.
- The Windows PXE listener started on its standard ports with the x64 boot pair present. This establishes startup only, not firmware PXE or a Windows deployment.

Windows MoveFileEx behavior is documented by [Microsoft](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw). No power-loss durability test was run; arms are discarded on Load regardless of persisted contents.

## Test environment limits

The long-lived WinPE builder and server did not survive the SSH session ending. A new disposable 80 GB UEFI VM was prepared on Proxmox, but its network boot has not yet been attempted. Temporary background execution requires approval. No end-to-end installation, hardware deployment, Secure Boot test or performance claim follows from these results.
