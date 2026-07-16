---
ddx:
  id: FEAT-007
  depends_on:
    - helix.prd
    - FEAT-006
  review:
    self_hash: 20cf41ca595074feb1345729785859f504ce1fa570547ffc31ea38a264aa719b
    deps:
      FEAT-006: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Feature Specification: FEAT-007 — Self-Update and Installer

**Feature ID**: FEAT-007
**Status**: Approved
**Priority**: P1
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Distribution
**Covered PRD Requirements**: FR-7
**Cross-Subsystem Rationale**: Distributes the CLI proof surface while keeping
update networking outside embedded library execution.
**User Stories**: [US-007 — Install and Explicitly Update the Proof CLI](../user-stories/US-007-distribution.md)

## Overview

Fizeau provides a robust installation experience with automatic update checking,
safe binary replacement, and enhanced installer UX. This implements the ghostty
pattern — great library proven by a usable CLI app.

## Problem Statement

- **Current situation**: `install.sh` downloads latest release but has no update
  check capability. Users must manually re-run install script to upgrade.
- **Pain points**: No way to check if running version is outdated. No safe in-place
  binary replacement. Installer lacks colored output and PATH configuration.
- **Desired outcome**: `fiz update` command that checks for updates, prompts user,
  downloads new binary safely, and replaces old one atomically. Enhanced installer
  with better UX matching DDx patterns.

## Requirements

### Functional Requirements

#### Update Command (`fiz update`)

1. Check current version against latest GitHub release
2. Display version comparison: "Current: v0.0.8 | Latest: v0.0.9"
3. If outdated, show changelog summary (release notes from GitHub API)
4. Prompt user: "Update to v0.0.9? [y/N]"
5. Download new binary to temp location
6. Validate the download before replacement by rejecting empty or implausibly
   small artifacts. Checksum verification becomes required when the release
   workflow publishes a checksum manifest; the current release contract does
   not publish one.
7. Atomically replace old binary with new one
8. Preserve executable permissions
9. Show success message with new version

#### Update Check Only (`fiz update --check-only`)

10. Compare versions without downloading
11. Exit code 0 if up-to-date, exit code 1 if outdated
12. Print version comparison to stdout for scripting

#### Enhanced Installer (`install.sh`)

13. Colored output (blue info, green success, yellow warning, red error)
14. Prerequisites checking (curl/wget availability)
15. PATH configuration in shell rc files (.bashrc, .zshrc, .config/fish/config.fish)
16. Installation verification (`fiz --version` test)
17. Getting started guide with quick commands

#### Version Command Enhancement

18. `fiz version` shows current version and whether update is available
19. If outdated: "v0.0.8 (update available: v0.0.9)"
20. If up-to-date: "v0.0.8 (latest)"

### Non-Functional Requirements

- **Safety**: Binary replacement must be atomic — no partial writes or corrupted binaries
- **Reliability**: Network failures should not leave system in broken state
- **Performance**: Update check completes in < 3 seconds
- **UX**: Clear progress indicators during download
- **Portability**: Works on Linux and macOS (darwin)

## Edge Cases and Error Handling

### Network Errors

- **GitHub API unavailable**: Show error with retry suggestion, exit gracefully
- **Download interrupted**: Remove partial file, show error, don't replace binary
- **Rate limited by GitHub**: Cache version check for 1 hour to avoid repeated failures

### File System Errors

- **Binary not writable**: Error with clear message about permissions
- **No disk space**: Check before download, fail fast if insufficient space
- **Temp directory unavailable**: Use `$TMPDIR` or `/tmp` as fallback

### Version Comparison

- **Semantic versioning**: Compare v0.0.8 vs v0.0.9 correctly (not string comparison)
- **Pre-release versions**: Handle -rc1, -beta suffixes appropriately
- **Current version unknown**: If binary lacks version info, assume outdated

## Implementation Details

### Binary Location Detection

The update command must find where agent is installed:

```go
func findBinaryPath() (string, error) {
    // 1. Check if running from $GOPATH/bin or similar
    exe, err := os.Executable()
    if err != nil {
        return "", err
    }
    
    // 2. If in user's PATH, use that location
    dir := filepath.Dir(exe)
    if strings.Contains(dir, ".local/bin") || 
       strings.Contains(dir, "go/bin") ||
       strings.Contains(dir, "bin/fiz") {
        return exe, nil
    }
    
    // 3. Fall back to which command
    cmd := exec.Command("which", "fiz")
    output, err := cmd.Output()
    if err != nil {
        return "", fmt.Errorf("cannot locate agent binary: %w", err)
    }
    return strings.TrimSpace(string(output)), nil
}
```

### Atomic Replacement

```bash
# Download to temp file with same prefix
curl -L "$URL" -o "${BINARY}.new.${$}"

# Verify it's executable and has content
if [ ! -s "${BINARY}.new.${$}" ]; then
    rm -f "${BINARY}.new.${$}"
    error "Download failed or empty file"
fi

# Make executable
chmod +x "${BINARY}.new.${$}"

# Atomic move (preserves permissions on most systems)
mv -f "${BINARY}.new.${$}" "$BINARY"
```

### Version Comparison

Use semantic version parsing:

```go
type SemVer struct {
    Major, Minor, Patch int
    PreRelease string  // e.g., "rc1", "beta2"
}

func parseSemVer(v string) (SemVer, error) {
    // Strip 'v' prefix if present
    v = strings.TrimPrefix(v, "v")
    
    // Split by '-' for pre-release
    parts := strings.SplitN(v, "-", 2)
    version := parts[0]
    prerelease := ""
    if len(parts) > 1 {
        prerelease = parts[1]
    }
    
    // Parse major.minor.patch
    nums := strings.Split(version, ".")
    if len(nums) != 3 {
        return SemVer{}, fmt.Errorf("invalid version: %s", v)
    }
    
    var major, minor, patch int
    fmt.Sscanf(nums[0], "%d", &major)
    fmt.Sscanf(nums[1], "%d", &minor)
    fmt.Sscanf(nums[2], "%d", &patch)
    
    return SemVer{Major: major, Minor: minor, Patch: patch, PreRelease: prerelease}, nil
}

func (v SemVer) Less(other SemVer) bool {
    if v.Major != other.Major {
        return v.Major < other.Major
    }
    if v.Minor != other.Minor {
        return v.Minor < other.Minor
    }
    // Pre-release versions are less than release versions
    if v.PreRelease != "" && other.PreRelease == "" {
        return true
    }
    if v.PreRelease == "" && other.PreRelease != "" {
        return false
    }
    return v.Patch < other.Patch || (v.Patch == other.Patch && v.PreRelease < other.PreRelease)
}
```

### GitHub API Integration

```go
type GitHubRelease struct {
    TagName     string `json:"tag_name"`
    Name        string `json:"name"`
    Body        string `json:"body"`           // Release notes (markdown)
    PublishedAt time.Time `json:"published_at"`
}

func getLatestRelease(repo string) (*GitHubRelease, error) {
    url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var release GitHubRelease
    if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
        return nil, err
    }
    
    return &release, nil
}
```

## Success Metrics

- User can run `fiz update` and upgrade from v0.0.8 to v0.0.9 in one command
- Update check completes in < 3 seconds on typical network
- Binary replacement is atomic — no corrupted binaries after failed updates
- Installer successfully configures PATH for bash, zsh, and fish shells

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-007-01 | `fiz version` and `fiz update --check-only` perform semantic-version comparison against cached/latest release data, print scripting-friendly output, and return exit `0` when current and exit `1` when outdated. | `go test ./cmd/fiz ./...` |
| AC-FEAT-007-02 | `fiz update` downloads the correct platform binary, verifies the download, atomically replaces the current binary, preserves executable permissions, and leaves no partial replacement on success. | `go test ./cmd/fiz ./...` |
| AC-FEAT-007-03 | Network, API, permission, temp-dir, and disk/write failures leave the existing binary intact, clean up partial downloads, and emit actionable operator-facing errors. | `go test ./cmd/fiz ./...` |
| AC-FEAT-007-04 | `install.sh` verifies prerequisites, selects the correct release artifact for Linux/macOS amd64/arm64, installs into the requested target directory, and verifies the resulting binary. | shell-based acceptance test in CI/local temp directories |
| AC-FEAT-007-05 | `install.sh` updates bash, zsh, and fish PATH configuration idempotently and prints correct immediate-use guidance when the binary is not yet on the current shell `PATH`. | shell-based acceptance test in CI/local temp directories |
| AC-FEAT-007-06 | Install/update acceptance tests run without live GitHub dependencies by using mocked HTTP responses, temp files, and disposable shell rc files. | `go test ./cmd/fiz ./...`; shell-based acceptance test in CI |

## Constraints and Assumptions

- GitHub releases are the source of truth for versioning
- Binaries are built for linux-amd64, linux-arm64, darwin-amd64, darwin-arm64
- User has write permission to binary location (or will use sudo)
- Network access to api.github.com and github.com is available

## Dependencies

- **Other features**: FEAT-006 (the distributed proof CLI)
- **External services**: GitHub API, GitHub releases CDN
- **PRD requirements**: FR-7 (Distribution)

## Out of Scope

- Automatic background updates (user must explicitly run `fiz update`)
- Rollback to previous version (would require keeping old binary)
- Update notifications on startup (could be added later as opt-in feature)
- Custom release channels (beta, nightly — use GitHub tags directly if needed)
