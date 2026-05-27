package claudetui

import (
	"testing"

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
// claude binary. This is the cold-cache + binary-absent path the
// CONTRACT-004 conformance suite expects on every harness.
func isolateClaudeTuiEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", "")
}
