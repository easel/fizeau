package claudetui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

// claudettuiQuotaFreshness is the constant freshness window for claude-tui quota
// evidence. Matches claude's default of 15 minutes.
const claudettuiQuotaFreshness = 15 * time.Minute

// claudettuiAccountFreshness is the constant freshness window for claude-tui
// account evidence. Claude-tui embeds account info in its quota probe so the
// account refresh cadence matches quota.
const claudettuiAccountFreshness = claudettuiQuotaFreshness

// claudettuiQuotaProbeCeiling is the hard upper bound on how long a single
// live /usage PTY probe may run. It is distinct from claudettuiQuotaFreshness
// (a CACHE freshness window, not a probe timeout): a live `claude --usage`
// probe completes in ~15s, so 60s is a generous ceiling. Bounding the probe
// here keeps RefreshQuota/RefreshAccount terminating well under any test or
// caller deadline even when ctx carries no deadline of its own — without this
// ceiling a context.Background() caller would inherit the 15-minute freshness
// window and could outlive the go test binary timeout (root cause of F1).
const claudettuiQuotaProbeCeiling = 60 * time.Second

// modelDiscoveryTTL is the freshness window for model discovery cache per ADR-012.
const modelDiscoveryTTL = 24 * time.Hour

// modelDiscoveryRefreshDeadline is the maximum time a model discovery probe
// is expected to take, per ADR-012.
const modelDiscoveryRefreshDeadline = 60 * time.Second

// supportedLimitIDs is the stable public set of Windows[].LimitID values
// claude-tui's quota probe emits. Must match claude's set.
// Mirrored in doc.go for human readers.
var supportedLimitIDs = []string{
	"session",
	"weekly-all",
	"weekly-sonnet",
	"extra",
}

// supportedAliases is the stable public set of family aliases
// ResolveModelAlias recognizes. Mirrored in doc.go for human readers.
var supportedAliases = []string{"sonnet", "opus", "haiku", "fable"}

// modelDiscoveryCache is the per-process instance for model discovery caching
// per ADR-012. It is initialized once at package load time.
var modelDiscoveryCache *discoverycache.Cache

// modelDiscoveryCacheSource is the Source descriptor for the cache.
var modelDiscoveryCacheSource = discoverycache.Source{
	Tier:            "discovery",
	Name:            "claude-tui-v2",
	TTL:             modelDiscoveryTTL,
	RefreshDeadline: modelDiscoveryRefreshDeadline,
}

func init() {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = "/tmp"
	}
	modelDiscoveryCache = &discoverycache.Cache{
		Root: filepath.Join(cacheDir, "fizeau"),
	}
}

// refreshGroup serializes concurrent RefreshQuota / RefreshAccount calls
// across all *Harness instances. Per CONTRACT-004 invariant #6 Harnesses are
// stateless wrappers — sharing the singleflight group at package scope
// keeps single-flight semantics correct when callers construct fresh
// harness instances per call (which the dispatcher does today).
var refreshGroup singleflight.Group

// QuotaProbeFunc is the signature of the PTY-quota probe that
// Harness.RefreshQuota delegates to. Exposing it as a named type keeps
// the SetCaptureForTest seam decoupled from the internal ptyquota
// package; the test seam discards the ptyquota.Result that the live
// probe returns.
type QuotaProbeFunc func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error)

// captureQuotaProbe is the live PTY probe used by RefreshQuota. It is a
// package-level variable so tests can swap it via SetCaptureForTest
// without spawning the claude binary. The default delegates to
// captureClaudeTuiQuotaViaPTY and discards the ptyquota.Result.
var captureQuotaProbe QuotaProbeFunc = func(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
	windows, account, _, err := captureClaudeTuiQuotaViaPTY(ctx, timeout)
	return windows, account, err
}

// SetCaptureForTest swaps the package-level PTY probe used by
// Harness.RefreshQuota and returns a restore function. Production code
// MUST NOT call this — it exists so service-level tests can inject
// deterministic PTY responses while exercising the real cache I/O
// inside Harness.refreshQuotaLocked. The call also forgets any
// in-flight RefreshQuota cohort so the next caller runs a fresh
// single-flight execution against the new probe instead of piggybacking
// on a prior test's probe.
func SetCaptureForTest(fn QuotaProbeFunc) func() {
	prev := captureQuotaProbe
	captureQuotaProbe = fn
	refreshGroup.Forget("claudetui:refresh-quota")
	return func() {
		captureQuotaProbe = prev
		refreshGroup.Forget("claudetui:refresh-quota")
	}
}

// QuotaStatus implements harnesses.QuotaHarness. It reads the cached
// snapshot owned by the shared anthropic cache and projects it onto QuotaStatus.
// A missing or undecodable cache is reported as State=QuotaUnavailable on
// a valid QuotaStatus value (no error) per CONTRACT-004 §"Errors are
// reserved for call failure."
func (h *Harness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	snap, ok := anthropic.ReadClaudeQuota()
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	return claudettuiQuotaStatusFromSnapshot(snap, now), nil
}

// RefreshQuota implements harnesses.QuotaHarness. It probes the claude
// CLI via PTY unconditionally and persists the result through the shared
// harness cache. Single-flight per harness: concurrent callers
// share one cohort and observe the just-written cached state. Probe
// failure (binary missing, PTY error, parse failure) is reported as
// State=QuotaUnavailable on a valid QuotaStatus value, not as an error.
func (h *Harness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	v, err, _ := refreshGroup.Do("claudetui:refresh-quota", func() (any, error) {
		return h.refreshQuotaLocked(ctx), nil
	})
	if err != nil {
		return harnesses.QuotaStatus{}, err
	}
	return v.(harnesses.QuotaStatus), nil
}

// refreshQuotaLocked is the single-flight critical section. It probes
// PTY, writes the cache on success, and returns the projected status.
// Probe failure is folded into State=QuotaUnavailable.
func (h *Harness) refreshQuotaLocked(ctx context.Context) harnesses.QuotaStatus {
	now := time.Now()
	// Start from the hard probe ceiling, NOT the cache freshness window.
	// A live /usage probe finishes in ~15s; the ceiling guarantees the
	// probe (and therefore RefreshQuota/RefreshAccount) returns well under
	// any test or caller timeout even when ctx has no deadline.
	timeout := claudettuiQuotaProbeCeiling
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	windows, account, err := captureQuotaProbe(ctx, timeout)
	if err != nil {
		state := harnesses.QuotaUnavailable
		reason := err.Error()
		if strings.Contains(strings.ToLower(reason), "not found in path") {
			state = harnesses.QuotaUnauthenticated
		}
		return harnesses.QuotaStatus{
			Source:            "pty",
			CapturedAt:        now.UTC(),
			State:             state,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            reason,
		}
	}
	snap := claudettuiQuotaSnapshotFromWindows(windows, account)
	if path, pathErr := anthropic.ClaudeQuotaCachePath(); pathErr == nil {
		_ = anthropic.WriteClaudeQuota(path, snap)
	}
	return claudettuiQuotaStatusFromSnapshot(&snap, now)
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (h *Harness) QuotaFreshness() time.Duration {
	return claudettuiQuotaFreshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (h *Harness) SupportedLimitIDs() []string {
	return append([]string(nil), supportedLimitIDs...)
}

// AccountStatus implements harnesses.AccountHarness. Claude-tui embeds
// account/plan info inside its quota probe, so this method projects the
// quota cache's Account field onto AccountSnapshot.
func (h *Harness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	snap, ok := anthropic.ReadClaudeQuota()
	if !ok || snap == nil {
		return harnesses.AccountSnapshot{Source: "cache"}, nil
	}
	return claudettuiAccountSnapshotFromQuotaSnapshot(snap, now), nil
}

// RefreshAccount implements harnesses.AccountHarness by driving the
// quota probe (which carries account evidence) and projecting the
// resulting cached snapshot onto AccountSnapshot.
func (h *Harness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if _, err := h.RefreshQuota(ctx); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return h.AccountStatus(ctx, time.Now())
}

// AccountFreshness implements harnesses.AccountHarness.
func (h *Harness) AccountFreshness() time.Duration {
	return claudettuiAccountFreshness
}

// claudettuiQuotaStatusFromSnapshot projects the shared snapshot onto
// harnesses.QuotaStatus using the same logic as claude.
func claudettuiQuotaStatusFromSnapshot(snap *anthropic.ClaudeQuotaSnapshot, now time.Time) harnesses.QuotaStatus {
	status := harnesses.QuotaStatus{
		Source:     snap.Source,
		CapturedAt: snap.CapturedAt,
		Fresh:      anthropic.IsClaudeQuotaFresh(snap, now, claudettuiQuotaFreshness),
		Age:        anthropic.ClaudeQuotaSnapshotAge(snap, now),
		Windows:    append([]harnesses.QuotaWindow(nil), snap.Windows...),
	}

	// Determine RoutingPreference and State based on snapshot freshness and content
	if status.Fresh {
		// Fresh snapshot: check if we have headroom
		status.State = harnesses.QuotaOK
		status.RoutingPreference = harnesses.RoutingPreferenceAvailable
	} else if snap != nil {
		// Stale snapshot: assume limited
		status.State = harnesses.QuotaStale
		status.RoutingPreference = harnesses.RoutingPreferenceBlocked
		status.Reason = "cached snapshot is stale; assume limited"
	} else {
		// No snapshot
		status.State = harnesses.QuotaUnavailable
		status.RoutingPreference = harnesses.RoutingPreferenceUnknown
		status.Reason = "no cached snapshot"
	}

	if snap.Account != nil {
		acct := claudettuiAccountSnapshotFromQuotaSnapshot(snap, time.Now())
		status.Account = &acct
	}
	return status
}

// claudettuiAccountSnapshotFromQuotaSnapshot derives an AccountSnapshot from
// the quota cache's embedded Account field.
func claudettuiAccountSnapshotFromQuotaSnapshot(snap *anthropic.ClaudeQuotaSnapshot, now time.Time) harnesses.AccountSnapshot {
	out := harnesses.AccountSnapshot{
		Source:     "cache",
		CapturedAt: snap.CapturedAt,
		Fresh:      anthropic.IsClaudeQuotaFresh(snap, now, claudettuiAccountFreshness),
	}
	if snap.Account == nil {
		return out
	}
	plan := strings.TrimSpace(snap.Account.PlanType)
	out.PlanType = plan
	out.Email = snap.Account.Email
	out.OrgName = snap.Account.OrgName
	switch {
	case plan == "" || strings.EqualFold(plan, "unknown"):
		out.Unauthenticated = true
	default:
		out.Authenticated = true
	}
	return out
}

// captureClaudeTuiQuotaViaPTY drives the /usage probe through ptyquota.Run
// and returns parsed windows and account info. This is the shared probe
// machinery that both claude and claude-tui use.
func captureClaudeTuiQuotaViaPTY(ctx context.Context, timeout time.Duration) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, ptyquota.Result, error) {
	var windows []harnesses.QuotaWindow
	var account *harnesses.AccountInfo
	// Claude Code >= 2.1.260 renders /usage as a full-screen dialog with no
	// plan line; capture the plan from the startup banner (visible on early
	// DoneWhen ticks) and fall back to it when the final screen has none.
	var bannerAccount *harnesses.AccountInfo

	result, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:  "claude-tui",
		Binary:       "claude",
		Args:         nil,
		Workdir:      "",
		Env:          nil,
		Command:      "/usage\r",
		ReadyMarkers: []string{"❯", "> "},
		DoneWhen: func(text string) bool {
			if bannerAccount == nil {
				bannerAccount = anthropic.ParseClaudePlanAccount(text)
			}
			return claudettuiUsageComplete(text)
		},
		Timeout: timeout,
		Size:    session.Size{Rows: 50, Cols: 220},
		Quota: func(text string) (cassette.QuotaRecord, error) {
			windows, account = parseClaudeTuiUsageOutput(text)
			if account == nil {
				account = bannerAccount
			}
			if len(windows) == 0 {
				return cassette.QuotaRecord{}, fmt.Errorf("no quota windows found in claude /usage output")
			}
			if err := validateClaudeTuiQuotaEvidence(windows, account); err != nil {
				return cassette.QuotaRecord{}, fmt.Errorf("incomplete claude /usage output: %w", err)
			}
			return quotaRecord(windows, map[string]any{"plan_type": accountPlan(account)}), nil
		},
	})
	if err != nil {
		return nil, nil, result, err
	}
	if len(windows) == 0 {
		windows, account = parseClaudeTuiUsageOutput(result.Text)
	}
	if account == nil {
		account = bannerAccount
	}
	if len(windows) == 0 {
		return nil, account, result, fmt.Errorf("no quota windows found in claude /usage output")
	}
	if err := validateClaudeTuiQuotaEvidence(windows, account); err != nil {
		return nil, account, result, fmt.Errorf("incomplete claude /usage output: %w", err)
	}
	return windows, account, result, nil
}

// Helper functions for parsing and validation

func claudettuiQuotaSnapshotFromWindows(windows []harnesses.QuotaWindow, account *harnesses.AccountInfo) anthropic.ClaudeQuotaSnapshot {
	fiveHourUsed := usedPercentFor(windows, "session")
	weeklyUsed := usedPercentFor(windows, "weekly-all")
	if weeklyUsed < 0 {
		weeklyUsed = usedPercentFor(windows, "weekly-sonnet")
	}
	return anthropic.ClaudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourLimit:     100,
		FiveHourRemaining: remainingPercent(fiveHourUsed),
		WeeklyLimit:       100,
		WeeklyRemaining:   remainingPercent(weeklyUsed),
		Windows:           append([]harnesses.QuotaWindow(nil), windows...),
		Source:            "pty",
		Account:           account,
	}
}

func usedPercentFor(windows []harnesses.QuotaWindow, limitID string) float64 {
	for _, window := range windows {
		if window.LimitID == limitID {
			return window.UsedPercent
		}
	}
	return -1
}

func hasQuotaWindow(windows []harnesses.QuotaWindow, limitID string) bool {
	return usedPercentFor(windows, limitID) >= 0
}

func claudettuiUsageComplete(text string) bool {
	windows, _ := parseClaudeTuiUsageOutput(text)
	return hasQuotaWindow(windows, "session") && (hasQuotaWindow(windows, "weekly-all") || hasQuotaWindow(windows, "weekly-sonnet"))
}

func validateClaudeTuiQuotaEvidence(windows []harnesses.QuotaWindow, account *harnesses.AccountInfo) error {
	if accountPlan(account) == "" {
		return fmt.Errorf("missing account plan")
	}
	if !hasQuotaWindow(windows, "session") {
		return fmt.Errorf("missing session window")
	}
	if !hasQuotaWindow(windows, "weekly-all") && !hasQuotaWindow(windows, "weekly-sonnet") {
		return fmt.Errorf("missing weekly window")
	}
	return nil
}

func remainingPercent(used float64) int {
	if used < 0 {
		return 0
	}
	remaining := int(100 - used)
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func quotaRecord(windows []harnesses.QuotaWindow, metadata map[string]any) cassette.QuotaRecord {
	records := make([]map[string]any, 0, len(windows))
	for _, window := range windows {
		records = append(records, map[string]any{
			"name":           window.Name,
			"limit_id":       window.LimitID,
			"window_minutes": window.WindowMinutes,
			"used_percent":   window.UsedPercent,
			"resets_at":      window.ResetsAt,
			"state":          window.State,
		})
	}
	accountClass, _ := metadata["plan_type"].(string)
	return cassette.QuotaRecord{
		Source:            "pty",
		Status:            string(ptyquota.StatusOK),
		CapturedAt:        time.Now().UTC().Format(time.RFC3339),
		FreshnessWindow:   claudettuiQuotaFreshness.String(),
		StalenessBehavior: "stale quota evidence keeps Claude out of automatic routing and is treated as limited",
		AccountClass:      accountClass,
		Windows:           records,
		Metadata:          metadata,
	}
}

func accountPlan(account *harnesses.AccountInfo) string {
	if account == nil {
		return ""
	}
	return account.PlanType
}

// parseClaudeTuiUsageOutput delegates to the shared parsing from the anthropic package.
// Both claude and claude-tui use the same /usage output format, so they share the same parser.
func parseClaudeTuiUsageOutput(text string) ([]harnesses.QuotaWindow, *harnesses.AccountInfo) {
	return anthropic.ParseClaudeUsageOutput(text)
}

// modelDiscoveryRefresher is the live PTY probe used by DefaultModelSnapshot.
// It is a package-level variable so tests can swap it via
// SetModelDiscoveryRefresherForTest without spawning the claude binary.
// The default delegates to readClaudeTuiModelDiscoveryViaPTY and encodes the result.
var modelDiscoveryRefresher discoverycache.Refresher = func(ctx context.Context) ([]byte, error) {
	snapshot, err := readClaudeTuiModelDiscoveryViaPTY(ctx, modelDiscoveryRefreshDeadline)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Models) == 0 {
		return nil, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal model discovery snapshot: %w", err)
	}
	return data, nil
}

// SetModelDiscoveryRefresherForTest swaps the package-level PTY probe used by
// DefaultModelSnapshot and returns a restore function. Production code MUST NOT
// call this — it exists so tests can inject deterministic PTY responses while
// exercising the real cache I/O inside DefaultModelSnapshot.
func SetModelDiscoveryRefresherForTest(fn discoverycache.Refresher) func() {
	prev := modelDiscoveryRefresher
	modelDiscoveryRefresher = fn
	return func() {
		modelDiscoveryRefresher = prev
	}
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
