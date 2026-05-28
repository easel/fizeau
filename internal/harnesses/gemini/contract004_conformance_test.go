package gemini_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/gemini"
	"github.com/easel/fizeau/internal/harnesses/harnesstest"
)

// TestGeminiRunnerHarnessConformance asserts the bare Harness contract.
// Run in an isolated env: a non-existent quota cache, an empty HOME so
// ~/.gemini reads find nothing, an empty PATH so the PTY probe cannot
// find a real gemini binary, and gemini-relevant env vars cleared so
// authTypeFromEnv returns "".
func TestGeminiRunnerHarnessConformance(t *testing.T) {
	isolateGeminiRunnerEnv(t)
	harnesstest.RunHarnessConformance(t, &gemini.Runner{})
}

// TestGeminiRunnerQuotaHarnessConformance asserts QuotaHarness contract:
// QuotaStatus returns a valid value with no error for a cold cache;
// RefreshQuota's probe failure surfaces as State=QuotaUnavailable on a
// valid status value, not as an error.
func TestGeminiRunnerQuotaHarnessConformance(t *testing.T) {
	isolateGeminiRunnerEnv(t)
	harnesstest.RunQuotaHarnessConformance(t, &gemini.Runner{})
}

// TestGeminiRunnerAccountHarnessConformance asserts the AccountHarness
// contract against the cold-evidence path (no ~/.gemini config).
func TestGeminiRunnerAccountHarnessConformance(t *testing.T) {
	isolateGeminiRunnerEnv(t)
	harnesstest.RunAccountHarnessConformance(t, &gemini.Runner{})
}

// TestGeminiRunnerModelDiscoveryHarnessConformance asserts ResolveModelAlias
// covers each documented family and rejects out-of-set families with
// ErrAliasNotResolvable.
func TestGeminiRunnerModelDiscoveryHarnessConformance(t *testing.T) {
	isolateGeminiRunnerEnv(t)
	harnesstest.RunModelDiscoveryHarnessConformance(t, &gemini.Runner{})
}

// TestRefreshQuotaSkipsProbeWhenUnauthenticated verifies that RefreshQuota
// returns QuotaUnavailable without spawning a PTY probe when gemini is not
// authenticated (GEMINI_API_KEY unset, no ~/.gemini config).
// AC #1: With GEMINI_API_KEY unset, no gemini process spawned during discovery.
func TestRefreshQuotaSkipsProbeWhenUnauthenticated(t *testing.T) {
	isolateGeminiRunnerEnv(t)
	runner := &gemini.Runner{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := runner.RefreshQuota(ctx)
	if err != nil {
		t.Errorf("RefreshQuota: %v", err)
	}
	if status.State != harnesses.QuotaUnavailable {
		t.Errorf("expected QuotaUnavailable, got %s", status.State)
	}
	if status.Reason != "gemini not configured or authenticated" {
		t.Errorf("expected 'gemini not configured or authenticated' reason, got %q", status.Reason)
	}
}

// isolateGeminiRunnerEnv pins HOME to a temp dir, points the quota cache
// at a non-existent file, clears PATH so the PTY probe cannot find a
// real gemini binary, and clears the env vars authTypeFromEnv consults
// so the auth path falls through to the (empty) ~/.gemini directory.
// This is the cold-cache + binary-absent path the CONTRACT-004
// conformance suite expects on every harness.
func isolateGeminiRunnerEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", filepath.Join(dir, "gemini-quota.json"))
	t.Setenv("PATH", "")
	t.Setenv("GOOGLE_GENAI_USE_GCA", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLOUD_SHELL", "")
	t.Setenv("GEMINI_CLI_USE_COMPUTE_ADC", "")
}
