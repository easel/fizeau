# Review: Self-Update & Installer (FEAT-007)

**Bead**: fizeau-5b2a8950  
**Date**: 2026-05-31  
**Scope**: `agentcli/update.go`, `agentcli/update_test.go`, `agentcli/run.go` (cmdVersion/cmdUpdate), `install.sh`  
**Governing spec**: `docs/helix/01-frame/features/FEAT-007-self-update-and-installer.md`  
**Prior AR entry**: AR-2026-05-17-repo.md §7 — ALIGNED with gap  

---

## Overall Status: ALIGNED with gaps

Core FEAT-007 functionality is implemented and tested. Three code-level issues found (one correctness bug, two behavioral inconsistencies), plus the known shell-test gap from AR-2026-05-17-repo.

---

## AC Coverage

| AC | Status | Evidence |
|----|--------|----------|
| AC-FEAT-007-01 — semver comparison, scripting output, exit codes | SATISFIED | `TestCmdVersion_ShowsUpdateAvailability`, `TestCmdUpdate_CheckOnly_OutdatedReturnsOne`, `TestCmdUpdate_CheckOnly_UpToDateReturnsZero` pass |
| AC-FEAT-007-02 — download, verify, atomic replace, permission preservation | SATISFIED with bug | `TestReplaceBinary_PreservesOriginalPermissions`, `TestDownloadBinary_RemovesTempFileOnSmallDownload` pass; last-chunk drop bug (see below) undermines full correctness |
| AC-FEAT-007-03 — network/permission/temp failures leave binary intact | SATISFIED | `TestDownloadBinary_RemovesTempFileOnSmallDownload` demonstrates cleanup; error paths verified by test |
| AC-FEAT-007-04 — install.sh prereqs, platform selection, verify | GAP | No shell integration test; manual read-through confirms logic correct for bash/zsh/fish but idempotency is untested |
| AC-FEAT-007-05 — idempotent PATH configuration | GAP | `configure_path()` checks PATH and rc-file content before writing, but no automated test asserts re-run safety |
| AC-FEAT-007-06 — tests without live GitHub deps | PARTIAL — Go side satisfied; shell side GAP | `githubAPIBase` injection + httptest server used throughout Go tests; no equivalent for shell |

---

## Findings

### FINDING-1: Download loop silently drops last chunk (BUG — CORRECTNESS)

**File**: `agentcli/update.go:244–265`  
**Severity**: Medium-high — data corruption under normal HTTP conditions

The download loop reads into `buf`, updates the progress counter when `n > 0`, then checks `err` before writing to the file:

```go
for {
    n, err := resp.Body.Read(buf)
    if n > 0 {
        written += int64(n)
        // update progress...
    }
    if err != nil {
        if err == io.EOF {
            break       // <-- breaks before tmpFile.Write below
        }
        // cleanup and return error
    }
    if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil { ... }
}
```

Go's `io.Reader` contract explicitly permits returning `(n > 0, io.EOF)` simultaneously on the last read. When this happens, the loop breaks on `io.EOF` without writing the final `n` bytes. The downloaded binary will be silently truncated.

**Fix**: write before the error check, or restructure to always write when `n > 0`:

```go
n, err := resp.Body.Read(buf)
if n > 0 {
    written += int64(n)
    // progress...
    if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil { ... }
}
if err != nil { ... }
```

The existing 10KB size check (`info.Size() < 10*1024`) provides a partial backstop for small files, but a real binary download will easily exceed 10KB even with the last chunk dropped, so the guard will not catch this in practice.

---

### FINDING-2: Dead version constant in `update.go` (ISSUE — CORRECTNESS)

**File**: `agentcli/update.go:23`  
**Severity**: Low — no runtime impact; confusion for release tooling

```go
const (
    defaultGitHubRepo = "easel/fizeau"
    defaultGitHubAPI  = "https://api.github.com"
    version           = "v0.0.8"  // Updated by release script
    updateCheckTTL    = time.Hour
)
```

The constant `version` (lowercase) is never referenced anywhere in the file or package. The live version string is `Version` (var, `agentcli/run.go:76`). If the release script patches `version` in `update.go`, it patches dead code and the actual version reported by `fiz version` remains unchanged.

**Fix**: remove the constant, or if the intent was to keep a "local fallback" version, add a reference and document the relationship with `run.go:Version`.

---

### FINDING-3: `cmdUpdate` returns error exit code on dev builds (ISSUE — UX)

**File**: `agentcli/run.go:2319–2323`  
**Severity**: Low — affects only dev-build users

`cmdVersion` (run.go:2249) checks `Version == "dev"` and silently returns exit 0 with no update check. `cmdUpdate` does not have the same guard:

```go
currentVer, err := ParseSemVer(Version)  // "dev" → error
if err != nil {
    fmt.Fprintf(os.Stderr, "error: invalid current version format: %s\n", Version)
    return 1
}
```

A developer running `fiz update` from a locally-built binary sees an error exit 1. Expected behavior matches `cmdVersion`: detect "dev", print an informational message, exit 0.

---

### FINDING-4: `install.sh` binary download has no size guard (MINOR)

**File**: `install.sh:install_binary()`

The shell installer downloads directly to `${INSTALL_DIR}/${BINARY_NAME}` and then `chmod +x`s it without any file-size verification. A failed or partial download leaves an invalid binary that `verify_installation()` will try to run, producing a confusing failure message. The Go updater applies a 10KB minimum check (`update.go:280–283`) — the shell installer should do the same:

```bash
local size
size=$(wc -c < "${INSTALL_DIR}/${BINARY_NAME}" 2>/dev/null || echo 0)
if [ "$size" -lt 10240 ]; then
    rm -f "${INSTALL_DIR}/${BINARY_NAME}"
    error "Downloaded file too small (${size} bytes). Download may have failed."
fi
```

---

### FINDING-5: Shell integration test gap (KNOWN — follow-on filed)

**Ref**: AR-2026-05-17-repo.md:125,142,174; follow-on bead fizeau-e0dabfc9

FEAT-007 AC-04, AC-05, and AC-06 require shell-based acceptance tests that verify install.sh in a clean-PATH environment and assert idempotency across shells. No such test exists. This is tracked by fizeau-e0dabfc9.

---

## Tests Run

```
go test ./agentcli/ -run "TestParseSemVer|TestSemVer|TestGetLatestRelease|TestCachedVersion|TestReplaceBinary|TestDownloadBinary|TestFindBinaryPath|TestCmdVersion|TestCmdUpdate" -v
```

All 18 tests pass (3 skipped: integration/network tests correctly guarded).

---

## Summary

| Finding | Severity | Disposition |
|---------|----------|-------------|
| FINDING-1: last-chunk drop in download loop | Medium | File corrective bead or fix here |
| FINDING-2: dead `version` constant | Low | Remove in cleanup pass |
| FINDING-3: `cmdUpdate` error on dev builds | Low | Add dev-version guard matching cmdVersion |
| FINDING-4: install.sh no size guard | Minor | Simple shell addition |
| FINDING-5: shell integration test gap | Known | fizeau-e0dabfc9 tracks |
