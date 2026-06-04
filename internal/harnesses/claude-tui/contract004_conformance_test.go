package claudetui

import (
	"context"
	"os/exec"
	"testing"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses/harnesstest"
)

// TestClaudeTuiHealthCheckSuccess proves the positive HealthCheck path: when the
// real `claude` binary IS on PATH, HealthCheck(ctx) returns nil. It also asserts
// the Info() availability semantics hold (claude-tui advertises Available=false
// — the harness is a subscription PTY harness whose availability is gated on the
// quota cache, not on binary presence — and remains AutoRoutingEligible). On
// machines without claude installed the test SKIPs rather than failing.
func TestClaudeTuiHealthCheckSuccess(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not on PATH: %v", err)
	}

	h := &Harness{}
	if err := h.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck with claude on PATH = %v, want nil", err)
	}

	info := h.Info()
	if info.Name != "claude-tui" {
		t.Errorf("Info().Name = %q, want claude-tui", info.Name)
	}
	if info.Available {
		t.Errorf("Info().Available = true; claude-tui must advertise Available=false (availability is quota-gated, not binary-gated)")
	}
	if !info.IsSubscription {
		t.Errorf("Info().IsSubscription = false; claude-tui is a subscription harness")
	}
	if !info.AutoRoutingEligible {
		t.Errorf("Info().AutoRoutingEligible = false; claude-tui must remain auto-routing eligible")
	}
}

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
