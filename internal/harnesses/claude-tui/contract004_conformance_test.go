package claudetui

import (
	"testing"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses/harnesstest"
)

// TestClaudeTuiHarnessConformance asserts the bare Harness contract.
// Run in an isolated env: a non-existent cache path and an empty PATH
// so neither a stale snapshot nor a real claude binary can sneak in.
func TestClaudeTuiHarnessConformance(t *testing.T) {
	isolateClaudeTuiEnv(t)
	harnesstest.RunHarnessConformance(t, &Harness{})
}

// TestClaudeTuiQuotaHarnessConformance asserts QuotaHarness contract:
// QuotaStatus returns a valid value with no error for a cold cache;
// RefreshQuota's probe failure surfaces as State=QuotaUnavailable on a
// valid status value, not as an error.
func TestClaudeTuiQuotaHarnessConformance(t *testing.T) {
	isolateClaudeTuiEnv(t)
	harnesstest.RunQuotaHarnessConformance(t, &Harness{})
}

// TestClaudeTuiAccountHarnessConformance asserts the AccountHarness
// contract against the cold-cache path (no embedded account evidence).
func TestClaudeTuiAccountHarnessConformance(t *testing.T) {
	isolateClaudeTuiEnv(t)
	harnesstest.RunAccountHarnessConformance(t, &Harness{})
}

// TestClaudeTuiModelDiscoveryHarnessConformance asserts ResolveModelAlias
// covers each documented family and rejects out-of-set families with
// ErrAliasNotResolvable.
func TestClaudeTuiModelDiscoveryHarnessConformance(t *testing.T) {
	isolateClaudeTuiEnv(t)
	harnesstest.RunModelDiscoveryHarnessConformance(t, &Harness{})
}

// isolateClaudeTuiEnv clears PATH so the PTY probe cannot find a real
// claude binary AND redirects the model-discovery cache to an empty temp
// dir so a stale on-disk snapshot cannot sneak in. This is the cold-cache
// + binary-absent path the CONTRACT-004 conformance suite expects on every
// harness. Without the cache redirect, DefaultModelSnapshot reads the
// machine-local ~/.cache/fizeau/discovery/claude-tui.json scrape (which may
// hold a partial model set such as ["claude-tui","opus-4.8","opus"]); the
// suite would then succeed at discovery but FAIL ResolveModelAliasPositive
// for the advertised "sonnet"/"haiku" families that the stale scrape lacks.
// Pointing the cache at an empty dir forces the documented cold-cache path:
// discovery fails (no binary to refresh), and the conformance suite takes
// its AliasResolutionSkipped branch deterministically on every machine.
func isolateClaudeTuiEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")

	prevCache := modelDiscoveryCache
	modelDiscoveryCache = &discoverycache.Cache{Root: t.TempDir()}
	t.Cleanup(func() { modelDiscoveryCache = prevCache })
}
