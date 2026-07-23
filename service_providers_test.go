package fizeau

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
)

const defaultQuotaRefreshDebounce = 15 * time.Minute

// fakeServiceConfig implements ServiceConfig for tests.
type fakeServiceConfig struct {
	providers      map[string]ServiceProviderEntry
	names          []string
	defaultName    string
	healthCooldown time.Duration
	workDir        string
}

func (f *fakeServiceConfig) ProviderNames() []string     { return f.names }
func (f *fakeServiceConfig) DefaultProviderName() string { return f.defaultName }
func (f *fakeServiceConfig) Provider(name string) (ServiceProviderEntry, bool) {
	e, ok := f.providers[name]
	return e, ok
}
func (f *fakeServiceConfig) HealthCooldown() time.Duration { return f.healthCooldown }
func (f *fakeServiceConfig) WorkDir() string               { return f.workDir }
func (f *fakeServiceConfig) SessionLogDir() string {
	if f.workDir == "" {
		return ""
	}
	return filepath.Join(f.workDir, ".fizeau", "sessions")
}

// fakeClaudeQuotaHarness is a cache-backed harnesses.QuotaHarness /
// AccountHarness used by service tests. It reads and writes the same JSON
// fixture cache files the real Claude runner owns, but keeps the tests at the
// interface boundary instead of reaching into claude package exports.
type fakeClaudeQuotaHarness struct {
	refreshFn    func(ctx context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error)
	refreshCalls atomic.Int32
}

func newFakeClaudeQuotaHarness() *fakeClaudeQuotaHarness {
	return &fakeClaudeQuotaHarness{}
}

func (f *fakeClaudeQuotaHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "claude", Type: "subprocess"}
}

func (f *fakeClaudeQuotaHarness) HealthCheck(_ context.Context) error { return nil }

func (f *fakeClaudeQuotaHarness) Execute(_ context.Context, _ harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("fakeClaudeQuotaHarness: Execute not supported")
}

func (f *fakeClaudeQuotaHarness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	path := os.Getenv("FIZEAU_CLAUDE_QUOTA_CACHE")
	if path == "" {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	snap, ok := readClaudeQuotaCacheFileRaw(path)
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}

	fresh := !snap.CapturedAt.IsZero() && now.Sub(snap.CapturedAt) < defaultQuotaRefreshDebounce
	state := harnesses.QuotaOK
	routingPreference := harnesses.RoutingPreferenceAvailable
	if snap.FiveHourRemaining <= 0 || snap.WeeklyRemaining <= 0 {
		state = harnesses.QuotaBlocked
		routingPreference = harnesses.RoutingPreferenceBlocked
	} else if !fresh {
		state = harnesses.QuotaStale
		routingPreference = harnesses.RoutingPreferenceBlocked
	}

	status := harnesses.QuotaStatus{
		Source:            snap.Source,
		CapturedAt:        snap.CapturedAt,
		Fresh:             fresh,
		State:             state,
		Windows:           append([]harnesses.QuotaWindow(nil), snap.Windows...),
		RoutingPreference: routingPreference,
	}
	if snap.Account != nil {
		status.Account = &harnesses.AccountSnapshot{
			Authenticated: true,
			PlanType:      snap.Account.PlanType,
			Email:         snap.Account.Email,
			OrgName:       snap.Account.OrgName,
			Source:        snap.Source,
			CapturedAt:    snap.CapturedAt,
			Fresh:         fresh,
		}
	}
	return status, nil
}

func (f *fakeClaudeQuotaHarness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	f.refreshCalls.Add(1)

	windows := []harnesses.QuotaWindow{
		{LimitID: "session", UsedPercent: 20, State: "ok"},
		{LimitID: "weekly-all", UsedPercent: 10, State: "ok"},
	}
	account := &harnesses.AccountInfo{PlanType: "Claude Max"}
	if f.refreshFn != nil {
		var err error
		windows, account, err = f.refreshFn(ctx)
		if err != nil {
			return harnesses.QuotaStatus{
				Source:            "fixture",
				CapturedAt:        time.Now().UTC(),
				State:             harnesses.QuotaUnavailable,
				RoutingPreference: harnesses.RoutingPreferenceUnknown,
				Reason:            err.Error(),
			}, nil
		}
	}

	path := os.Getenv("FIZEAU_CLAUDE_QUOTA_CACHE")
	if path != "" {
		if err := writeClaudeQuotaCacheFileRaw(path, fakeClaudeQuotaSnapshotFromWindows(windows, account)); err != nil {
			return harnesses.QuotaStatus{}, err
		}
	}
	return f.QuotaStatus(ctx, time.Now())
}

func (f *fakeClaudeQuotaHarness) QuotaFreshness() time.Duration { return defaultQuotaRefreshDebounce }

func (f *fakeClaudeQuotaHarness) SupportedLimitIDs() []string {
	return []string{"session", "weekly-all", "weekly-sonnet", "extra"}
}

func (f *fakeClaudeQuotaHarness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	status, err := f.QuotaStatus(ctx, now)
	if err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	if status.Account == nil {
		return harnesses.AccountSnapshot{Source: status.Source}, nil
	}
	return *status.Account, nil
}

func (f *fakeClaudeQuotaHarness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if _, err := f.RefreshQuota(ctx); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return f.AccountStatus(ctx, time.Now())
}

func (f *fakeClaudeQuotaHarness) AccountFreshness() time.Duration { return defaultQuotaRefreshDebounce }

func fakeClaudeQuotaSnapshotFromWindows(windows []harnesses.QuotaWindow, account *harnesses.AccountInfo) claudeTestQuotaSnapshot {
	weeklyUsed := usedPercentForLimitID(windows, "weekly-all")
	if weeklyUsed < 0 {
		weeklyUsed = usedPercentForLimitID(windows, "weekly-sonnet")
	}
	return claudeTestQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: remainingPercentFromUsed(usedPercentForLimitID(windows, "session")),
		FiveHourLimit:     100,
		WeeklyRemaining:   remainingPercentFromUsed(weeklyUsed),
		WeeklyLimit:       100,
		Windows:           append([]harnesses.QuotaWindow(nil), windows...),
		Source:            "fixture",
		Account:           account,
	}
}

func usedPercentForLimitID(windows []harnesses.QuotaWindow, limitID string) float64 {
	for _, window := range windows {
		if window.LimitID == limitID {
			return window.UsedPercent
		}
	}
	return -1
}

func remainingPercentFromUsed(used float64) int {
	if used < 0 {
		return 0
	}
	remaining := int(math.Round(100 - used))
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func readClaudeQuotaCacheFileRaw(path string) (*claudeTestQuotaSnapshot, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var snap claudeTestQuotaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

func writeClaudeQuotaCacheFileRaw(path string, snap claudeTestQuotaSnapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

type fakeCodexQuotaHarness struct {
	refreshFn    func(ctx context.Context) (harnesses.QuotaStatus, error)
	refreshCalls atomic.Int32
}

func newFakeCodexQuotaHarness() *fakeCodexQuotaHarness {
	return &fakeCodexQuotaHarness{}
}

func (f *fakeCodexQuotaHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "codex", Type: "subprocess"}
}
func (f *fakeCodexQuotaHarness) HealthCheck(_ context.Context) error { return nil }
func (f *fakeCodexQuotaHarness) Execute(_ context.Context, _ harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("fakeCodexQuotaHarness: Execute not supported")
}
func (f *fakeCodexQuotaHarness) QuotaStatus(_ context.Context, _ time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{State: harnesses.QuotaUnavailable}, nil
}
func (f *fakeCodexQuotaHarness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	f.refreshCalls.Add(1)
	if f.refreshFn != nil {
		return f.refreshFn(ctx)
	}
	return harnesses.QuotaStatus{
		Fresh:             true,
		State:             harnesses.QuotaOK,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	}, nil
}
func (f *fakeCodexQuotaHarness) QuotaFreshness() time.Duration { return 15 * time.Minute }
func (f *fakeCodexQuotaHarness) SupportedLimitIDs() []string   { return []string{"codex"} }

// fakeGrokQuotaHarness is a QuotaHarness stub for the "grok" primary
// harness so startup/foreground refresh paths never launch a real grok PTY
// probe from tests (grok may be installed on developer machines).
type fakeGrokQuotaHarness struct {
	refreshCalls atomic.Int32
	refreshFn    func(ctx context.Context) (harnesses.QuotaStatus, error)
}

func newFakeGrokQuotaHarness() *fakeGrokQuotaHarness {
	return &fakeGrokQuotaHarness{}
}

func (f *fakeGrokQuotaHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "grok", Type: "subprocess"}
}
func (f *fakeGrokQuotaHarness) HealthCheck(_ context.Context) error { return nil }
func (f *fakeGrokQuotaHarness) Execute(_ context.Context, _ harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("fakeGrokQuotaHarness: Execute not supported")
}
func (f *fakeGrokQuotaHarness) QuotaStatus(_ context.Context, _ time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{State: harnesses.QuotaUnavailable}, nil
}
func (f *fakeGrokQuotaHarness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	f.refreshCalls.Add(1)
	if f.refreshFn != nil {
		return f.refreshFn(ctx)
	}
	return harnesses.QuotaStatus{
		Fresh:             true,
		State:             harnesses.QuotaOK,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
	}, nil
}
func (f *fakeGrokQuotaHarness) QuotaFreshness() time.Duration { return 15 * time.Minute }
func (f *fakeGrokQuotaHarness) SupportedLimitIDs() []string   { return []string{"grok", "grok-weekly"} }

// setFakeGrokHarness installs h as the "grok" entry in harnessInstanceHook
// so both New() and newTestService() pick it up. Restores the previous hook
// via t.Cleanup.
func setFakeGrokHarness(t *testing.T, h *fakeGrokQuotaHarness) {
	t.Helper()
	prev := harnessInstanceHook
	harnessInstanceHook = func(instances map[string]harnesses.Harness) map[string]harnesses.Harness {
		if prev != nil {
			instances = prev(instances)
		}
		instances["grok"] = h
		return instances
	}
	t.Cleanup(func() { harnessInstanceHook = prev })
}

// setFakeCodexHarness installs h as the "codex" entry in harnessInstanceHook
// so both New() and newTestService() pick it up. Restores the previous hook
// via t.Cleanup.
func setFakeCodexHarness(t *testing.T, h *fakeCodexQuotaHarness) {
	t.Helper()
	prev := harnessInstanceHook
	harnessInstanceHook = func(instances map[string]harnesses.Harness) map[string]harnesses.Harness {
		if prev != nil {
			instances = prev(instances)
		}
		instances["codex"] = h
		return instances
	}
	t.Cleanup(func() { harnessInstanceHook = prev })
}

func setFakeClaudeHarness(t *testing.T, h *fakeClaudeQuotaHarness) {
	t.Helper()
	prev := harnessInstanceHook
	harnessInstanceHook = func(instances map[string]harnesses.Harness) map[string]harnesses.Harness {
		if prev != nil {
			instances = prev(instances)
		}
		instances["claude"] = h
		return instances
	}
	t.Cleanup(func() { harnessInstanceHook = prev })
}

func TestListProviders_NoServiceConfig(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	_, err := svc.ListProviders(context.Background())
	if err == nil {
		t.Fatal("expected error when ServiceConfig is nil")
	}
}

func TestHealthCheck_NoServiceConfig(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	err := svc.HealthCheck(context.Background(), HealthTarget{Type: "provider", Name: "x"})
	if err == nil {
		t.Fatal("expected error when ServiceConfig is nil")
	}
}

// TestHealthCheck_ClaudeRefreshesQuotaWhenStale verifies that HealthCheck
// triggers a quota cache refresh when the cached snapshot is older than
// default quota refresh debounce (15m).
func TestHealthCheck_ClaudeRefreshesQuotaWhenStale(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cachePath)

	// Write a snapshot older than the 15m debounce.
	staleSnap := claudeTestQuotaSnapshot{
		CapturedAt:        time.Now().UTC().Add(-20 * time.Minute),
		FiveHourRemaining: 80,
		FiveHourLimit:     100,
		WeeklyRemaining:   90,
		WeeklyLimit:       100,
		Source:            "pty",
	}
	writeClaudeQuotaCacheFile(t, cachePath, staleSnap)

	// Inject a fake refresher so no real PTY probe is invoked.
	fakeClaude := newFakeClaudeQuotaHarness()
	fakeClaude.refreshFn = func(_ context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		return []harnesses.QuotaWindow{
			{LimitID: "session", UsedPercent: 20},
			{LimitID: "weekly-all", UsedPercent: 10},
		}, nil, nil
	}
	setFakeClaudeHarness(t, fakeClaude)

	svc := newTestService(t, ServiceOptions{})
	installPrimaryQuotaRefreshSchedulerForTest(t, svc)
	// HealthCheck for "claude" requires the binary to be discoverable.
	// If claude is not in PATH, the harness is unavailable → the quota refresh
	// is never reached. To keep the test self-contained we call the helper
	// directly rather than going through HealthCheck's availability gate.
	svc.healthCheckRefreshClaudeQuota(context.Background())

	if got := fakeClaude.refreshCalls.Load(); got != 1 {
		t.Fatalf("expected one Claude quota refresh, got %d", got)
	}

	// Verify the cache was rewritten with a newer timestamp.
	loaded, ok := readClaudeQuotaCacheFile(t, cachePath)
	if !ok {
		t.Fatal("expected cache file to exist after refresh")
	}
	if !loaded.CapturedAt.After(staleSnap.CapturedAt) {
		t.Errorf("expected cache CapturedAt to be newer than stale snapshot: got %v, stale was %v",
			loaded.CapturedAt, staleSnap.CapturedAt)
	}
}

// TestHealthCheck_ClaudeSkipsRefreshWhenFresh verifies that HealthCheck does
// NOT invoke the PTY quota refresher when the cached snapshot is younger than
// default quota refresh debounce (15m).
func TestHealthCheck_ClaudeSkipsRefreshWhenFresh(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", cachePath)

	// Write a snapshot that is only 30s old (fresh).
	freshSnap := claudeTestQuotaSnapshot{
		CapturedAt:        time.Now().UTC().Add(-30 * time.Second),
		FiveHourRemaining: 80,
		FiveHourLimit:     100,
		WeeklyRemaining:   90,
		WeeklyLimit:       100,
		Source:            "pty",
		Account:           &harnesses.AccountInfo{PlanType: "Claude Max"},
	}
	writeClaudeQuotaCacheFile(t, cachePath, freshSnap)

	// Inject a fake refresher that must NOT be called.
	fakeClaude := newFakeClaudeQuotaHarness()
	fakeClaude.refreshFn = func(_ context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		return nil, nil, nil
	}
	setFakeClaudeHarness(t, fakeClaude)

	svc := newTestService(t, ServiceOptions{})
	installPrimaryQuotaRefreshSchedulerForTest(t, svc)
	svc.healthCheckRefreshClaudeQuota(context.Background())

	if got := fakeClaude.refreshCalls.Load(); got != 0 {
		t.Fatalf("expected no Claude quota refreshes for fresh cache, got %d", got)
	}

	// Verify the cache timestamp is unchanged (still matches freshSnap).
	loaded, ok := readClaudeQuotaCacheFile(t, cachePath)
	if !ok {
		t.Fatal("expected cache file to still exist")
	}
	if !loaded.CapturedAt.Equal(freshSnap.CapturedAt) {
		t.Errorf("cache was unexpectedly rewritten: got CapturedAt %v, want %v",
			loaded.CapturedAt, freshSnap.CapturedAt)
	}
}

// TestHealthCheck_GeminiDoesNotInvokeQuotaProbe verifies that HealthCheck for
// Gemini does not call Claude/Codex PTY quota refreshers. Gemini quota status
// is auth/account-gated until the CLI exposes a stable numeric quota counter.
func TestHealthCheck_GeminiDoesNotInvokeQuotaProbe(t *testing.T) {
	// Inject a counter to detect unexpected calls.
	fakeClaude := newFakeClaudeQuotaHarness()
	setFakeClaudeHarness(t, fakeClaude)

	svc := newTestService(t, ServiceOptions{})
	// "gemini" is registered but unavailable in CI (binary not found).
	// HealthCheck returns an error but must not invoke the quota refresher.
	_ = svc.HealthCheck(context.Background(), HealthTarget{Type: "harness", Name: "gemini"})

	if got := fakeClaude.refreshCalls.Load(); got != 0 {
		t.Fatalf("healthCheck must not refresh Claude quota for gemini; got %d calls", got)
	}
}

// TestHealthCheck_CodexCallsRefreshQuota verifies that the scheduler's explicit
// refresh for "codex" delegates to QuotaHarness.RefreshQuota rather than calling
// codex-package PTY helpers directly (CONTRACT-004 migration).
func TestHealthCheck_CodexCallsRefreshQuota(t *testing.T) {
	resetPrimaryQuotaRefreshForTest(t)

	fake := newFakeCodexQuotaHarness()
	refreshCalled := false
	fake.refreshFn = func(_ context.Context) (harnesses.QuotaStatus, error) {
		refreshCalled = true
		return harnesses.QuotaStatus{
			Fresh:             true,
			State:             harnesses.QuotaOK,
			RoutingPreference: harnesses.RoutingPreferenceAvailable,
		}, nil
	}
	setFakeCodexHarness(t, fake)

	svc := newTestService(t, ServiceOptions{})
	installPrimaryQuotaRefreshSchedulerForTest(t, svc)
	svc.refreshScheduler.RefreshPrimaryQuotaForHealthCheck(context.Background(), "codex")
	if !refreshCalled {
		t.Error("expected QuotaHarness.RefreshQuota to be called for codex")
	}
}

func TestNewWaitsBrieflyForInvalidQuotaRefresh(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudePath)
	resetPrimaryQuotaRefreshForTest(t)

	fakeClaude := newFakeClaudeQuotaHarness()
	fakeClaude.refreshFn = func(_ context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		return []harnesses.QuotaWindow{
			{LimitID: "session", UsedPercent: 20},
			{LimitID: "weekly-all", UsedPercent: 10},
		}, &harnesses.AccountInfo{PlanType: "Claude Max"}, nil
	}
	setFakeClaudeHarness(t, fakeClaude)

	fake := newFakeCodexQuotaHarness()
	setFakeCodexHarness(t, fake)
	setFakeGrokHarness(t, newFakeGrokQuotaHarness())

	if _, err := New(ServiceOptions{
		ServiceConfig:           &fakeServiceConfig{},
		QuotaRefreshStartupWait: time.Second,
	}); err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := readClaudeQuotaCacheFile(t, claudePath); !ok {
		t.Fatal("expected startup wait to allow Claude quota cache write")
	}
	if got := fake.refreshCalls.Load(); got == 0 {
		t.Fatal("expected startup wait to trigger Codex QuotaHarness.RefreshQuota")
	}
}

func TestNewStartupQuotaRefreshContinuesAfterTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "claude-quota.json"))
	resetPrimaryQuotaRefreshForTest(t)

	release := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})

	fakeClaude := newFakeClaudeQuotaHarness()
	fakeClaude.refreshFn = func(ctx context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		return []harnesses.QuotaWindow{
			{LimitID: "session", UsedPercent: 20},
			{LimitID: "weekly-all", UsedPercent: 10},
		}, &harnesses.AccountInfo{PlanType: "Claude Max"}, nil
	}
	setFakeClaudeHarness(t, fakeClaude)

	fake := newFakeCodexQuotaHarness()
	fake.refreshFn = func(_ context.Context) (harnesses.QuotaStatus, error) {
		<-release
		return harnesses.QuotaStatus{Fresh: true, State: harnesses.QuotaOK, RoutingPreference: harnesses.RoutingPreferenceAvailable}, nil
	}
	setFakeCodexHarness(t, fake)
	setFakeGrokHarness(t, newFakeGrokQuotaHarness())

	start := time.Now()
	svc, err := New(ServiceOptions{
		ServiceConfig:           &fakeServiceConfig{},
		QuotaRefreshStartupWait: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	concreteSvc := svc.(*service)
	t.Cleanup(concreteSvc.refreshScheduler.Stop)
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("New blocked too long: %v", elapsed)
	}
	close(release)
	released = true
	waitForQuotaRefreshFiles(t, filepath.Join(dir, "claude-quota.json"))
	deadline := time.After(time.Second)
	for fake.refreshCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for Codex QuotaHarness.RefreshQuota to complete after release")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	concreteSvc.refreshScheduler.Stop()
}

func TestResolveRouteDoesNotTriggerAsyncQuotaRefresh(t *testing.T) {
	dir := tempDiscoveryCacheDir(t)
	t.Setenv("HOME", dir)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("GOOGLE_GENAI_USE_GCA", "")
	t.Setenv("GEMINI_CLI_USE_COMPUTE_ADC", "")
	t.Setenv("CLOUD_SHELL", "")
	claudeQuotaPath := filepath.Join(dir, "missing-claude-quota.json")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", claudeQuotaPath)
	resetPrimaryQuotaRefreshForTest(t)
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("local", "local", "http://127.0.0.1:9999/v1", ""), time.Now().UTC(), []string{"model-a"})

	var claudeCalls atomic.Int32

	fakeClaude := newFakeClaudeQuotaHarness()
	fakeClaude.refreshFn = func(_ context.Context) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
		claudeCalls.Add(1)
		return []harnesses.QuotaWindow{
			{LimitID: "session", UsedPercent: 20},
			{LimitID: "weekly-all", UsedPercent: 10},
		}, &harnesses.AccountInfo{PlanType: "Claude Max"}, nil
	}
	setFakeClaudeHarness(t, fakeClaude)

	fake := newFakeCodexQuotaHarness()
	setFakeCodexHarness(t, fake)
	fakeGrok := newFakeGrokQuotaHarness()
	setFakeGrokHarness(t, fakeGrok)

	svc := newTestService(t, ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"local": {Type: "lmstudio", BaseURL: "http://127.0.0.1:9999/v1", Model: "model-a"},
			},
			names:       []string{"local"},
			defaultName: "local",
		},
	})
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "model-a"})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil || dec.Model != "model-a" {
		t.Fatalf("decision=%#v, want local model-a route", dec)
	}
	time.Sleep(50 * time.Millisecond)
	if got := claudeCalls.Load(); got != 0 {
		t.Fatalf("unexpected Claude quota refresh calls = %d", got)
	}
	if got := fake.refreshCalls.Load(); got != 0 {
		t.Fatalf("unexpected Codex quota refresh calls = %d", got)
	}
}

func resetPrimaryQuotaRefreshForTest(t *testing.T) {
	t.Helper()
	t.Cleanup(routehealth.ResetPrimaryQuotaRefreshForTest())
}

func installPrimaryQuotaRefreshSchedulerForTest(t *testing.T, svc *service) {
	t.Helper()
	// These tests exercise the primary compatibility path in isolation. The
	// generic freshness-derived cadence has its own routehealth tests.
	svc.refreshScheduler = routehealth.NewRefreshScheduler(svc.harnessByName, nil)
	policy := routehealth.DefaultPrimaryQuotaRefreshPolicy()
	if svc.opts.QuotaRefreshDebounce > 0 {
		policy.Debounce = svc.opts.QuotaRefreshDebounce
	}
	if svc.opts.QuotaRefreshStartupWait > 0 {
		policy.StartupWait = svc.opts.QuotaRefreshStartupWait
	}
	policy.Interval = svc.opts.QuotaRefreshInterval
	svc.refreshScheduler.ConfigurePrimaryQuotaRefresh(policy)
}

func waitForQuotaRefreshFiles(t *testing.T, paths ...string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		allPresent := true
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				allPresent = false
				break
			}
		}
		if allPresent {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for quota refresh files: %v", paths)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
