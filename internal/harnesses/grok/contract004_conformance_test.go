package grok

import (
	"path/filepath"
	"testing"

	"github.com/easel/fizeau/internal/harnesses/harnesstest"
)

// TestGrokRunnerHarnessConformance asserts the bare Harness contract. Run in
// an isolated env: non-existent cache and auth paths plus an empty PATH so
// neither a stale snapshot nor a real grok binary can sneak in.
func TestGrokRunnerHarnessConformance(t *testing.T) {
	isolateGrokRunnerEnv(t)
	harnesstest.RunHarnessConformance(t, &Runner{})
}

// TestGrokRunnerQuotaHarnessConformance asserts the QuotaHarness contract:
// QuotaStatus returns a valid value with no error for a cold cache;
// RefreshQuota's probe failure surfaces as State=QuotaUnavailable on a valid
// status value, not as an error.
func TestGrokRunnerQuotaHarnessConformance(t *testing.T) {
	isolateGrokRunnerEnv(t)
	harnesstest.RunQuotaHarnessConformance(t, &Runner{})
}

// TestGrokRunnerAccountHarnessConformance asserts the AccountHarness
// contract against the cold-cache path (no auth.json).
func TestGrokRunnerAccountHarnessConformance(t *testing.T) {
	isolateGrokRunnerEnv(t)
	harnesstest.RunAccountHarnessConformance(t, &Runner{})
}

// TestGrokRunnerModelDiscoveryHarnessConformance asserts ResolveModelAlias
// covers each documented family and rejects out-of-set families with
// ErrAliasNotResolvable.
func TestGrokRunnerModelDiscoveryHarnessConformance(t *testing.T) {
	isolateGrokRunnerEnv(t)
	harnesstest.RunModelDiscoveryHarnessConformance(t, &Runner{})
}

// isolateGrokRunnerEnv points every grok evidence source at a temp location
// that does not exist and clears PATH so the PTY probe cannot find a real
// grok binary. This is the cold-cache + binary-absent path the CONTRACT-004
// conformance suite expects on every harness.
func isolateGrokRunnerEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("FIZEAU_GROK_QUOTA_CACHE", filepath.Join(dir, "grok-quota.json"))
	t.Setenv("FIZEAU_GROK_AUTH", filepath.Join(dir, "auth.json"))
	t.Setenv("FIZEAU_GROK_MODELS_CACHE", filepath.Join(dir, "models_cache.json"))
	t.Setenv("GROK_HOME", filepath.Join(dir, "grok-home"))
	t.Setenv("PATH", "")
}
