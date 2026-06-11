package claude

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

// claudeQuotaFreshness is the constant freshness window for claude quota
// evidence. Mirrors defaultClaudeQuotaStaleAfter (kept as a separate name
// so the CONTRACT-004 method has a stable, contract-named constant).
const claudeQuotaFreshness = defaultClaudeQuotaStaleAfter

// claudeAccountFreshness is the constant freshness window for claude
// account evidence. Claude embeds account info in its quota probe so the
// account refresh cadence matches quota.
const claudeAccountFreshness = defaultClaudeQuotaStaleAfter

// supportedLimitIDs is the stable public set of Windows[].LimitID values
// claude's quota probe emits. Mirrored in doc.go for human readers.
var supportedLimitIDs = []string{
	"session",
	"weekly-all",
	"weekly-sonnet",
	"extra",
}

// supportedAliases is the stable public set of family aliases
// ResolveModelAlias recognizes. Mirrored in doc.go for human readers.
var supportedAliases = []string{"sonnet", "opus", "haiku", "fable"}

// refreshGroup serializes concurrent RefreshQuota / RefreshAccount calls
// across all *Runner instances. Per CONTRACT-004 invariant #6 Runners are
// stateless wrappers — sharing the singleflight group at package scope
// keeps single-flight semantics correct when callers construct fresh
// runner instances per call (which the dispatcher does today).
var refreshGroup singleflight.Group

// QuotaProbeFunc is the signature of the PTY-quota probe that
// Runner.RefreshQuota delegates to. Exposing it as a named type keeps
// the SetCaptureForTest seam decoupled from the internal ptyquota
// package; the test seam discards the ptyquota.Result that the live
// probe returns.
type QuotaProbeFunc func(ctx context.Context, timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error)

// captureQuotaProbe is the live PTY probe used by RefreshQuota. It is a
// package-level variable so tests can swap it via SetCaptureForTest
// without spawning the claude binary. The default delegates to
// captureClaudeQuotaViaPTY and discards the ptyquota.Result.
var captureQuotaProbe QuotaProbeFunc = func(ctx context.Context, timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
	windows, account, _, err := captureClaudeQuotaViaPTY(ctx, timeout, opts...)
	return windows, account, err
}

// SetCaptureForTest swaps the package-level PTY probe used by
// Runner.RefreshQuota and returns a restore function. Production code
// MUST NOT call this — it exists so service-level tests can inject
// deterministic PTY responses while exercising the real cache I/O
// inside Runner.refreshQuotaLocked. The call also forgets any
// in-flight RefreshQuota cohort so the next caller runs a fresh
// single-flight execution against the new probe instead of piggybacking
// on a prior test's probe.
func SetCaptureForTest(fn QuotaProbeFunc) func() {
	prev := captureQuotaProbe
	captureQuotaProbe = fn
	refreshGroup.Forget("claude:refresh-quota")
	return func() {
		captureQuotaProbe = prev
		refreshGroup.Forget("claude:refresh-quota")
	}
}

// QuotaStatus implements harnesses.QuotaHarness. It reads the cached
// snapshot owned by this harness and projects it onto QuotaStatus. A
// missing or undecodable cache is reported as State=QuotaUnavailable on
// a valid QuotaStatus value (no error) per CONTRACT-004 §"Errors are
// reserved for call failure."
func (r *Runner) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	snap, ok := readClaudeQuota()
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	return claudeQuotaStatusFromSnapshot(snap, now), nil
}

// RefreshQuota implements harnesses.QuotaHarness. It probes the claude
// CLI via PTY unconditionally and persists the result through the
// harness's owned cache. Single-flight per harness: concurrent callers
// share one cohort and observe the just-written cached state. Probe
// failure (binary missing, PTY error, parse failure) is reported as
// State=QuotaUnavailable on a valid QuotaStatus value, not as an error.
func (r *Runner) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	v, err, _ := refreshGroup.Do("claude:refresh-quota", func() (any, error) {
		return r.refreshQuotaLocked(ctx), nil
	})
	if err != nil {
		return harnesses.QuotaStatus{}, err
	}
	return v.(harnesses.QuotaStatus), nil
}

// refreshQuotaLocked is the single-flight critical section. It probes
// PTY, writes the cache on success, and returns the projected status.
// Probe failure is folded into State=QuotaUnavailable.
func (r *Runner) refreshQuotaLocked(ctx context.Context) harnesses.QuotaStatus {
	now := time.Now()
	timeout := claudeQuotaFreshness
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	var opts []QuotaPTYOption
	if r.Binary != "" {
		opts = append(opts, WithQuotaPTYCommand(r.Binary))
	}
	windows, account, err := captureQuotaProbe(ctx, timeout, opts...)
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
	snap := claudeQuotaSnapshotFromWindows(windows, account)
	if path, pathErr := claudeQuotaCachePath(); pathErr == nil {
		_ = writeClaudeQuota(path, snap)
	}
	return claudeQuotaStatusFromSnapshot(&snap, now)
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (r *Runner) QuotaFreshness() time.Duration {
	return claudeQuotaFreshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (r *Runner) SupportedLimitIDs() []string {
	return append([]string(nil), supportedLimitIDs...)
}

// AccountStatus implements harnesses.AccountHarness. Claude embeds
// account/plan info inside its quota probe, so this method projects the
// quota cache's Account field onto AccountSnapshot.
func (r *Runner) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	snap, ok := readClaudeQuota()
	if !ok || snap == nil {
		return harnesses.AccountSnapshot{Source: "cache"}, nil
	}
	return claudeAccountSnapshotFromQuotaSnapshot(snap, now), nil
}

// RefreshAccount implements harnesses.AccountHarness by driving the
// quota probe (which carries account evidence) and projecting the
// resulting cached snapshot onto AccountSnapshot.
func (r *Runner) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if _, err := r.RefreshQuota(ctx); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return r.AccountStatus(ctx, time.Now())
}

// AccountFreshness implements harnesses.AccountHarness.
func (r *Runner) AccountFreshness() time.Duration {
	return claudeAccountFreshness
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness. It calls
// the live PTY discovery helper with a sensible timeout; on failure,
// returns ErrModelDiscoveryEvidenceMissing per the no-static-fallback principle.
func (r *Runner) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	timeout := 10 * time.Second
	var opts []QuotaPTYOption
	if r.Binary != "" {
		opts = append(opts, WithQuotaPTYCommand(r.Binary))
	}
	snapshot, err := ReadClaudeModelDiscoveryViaPTY(timeout, opts...)
	if err != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("model discovery PTY: %w", err)
	}
	if len(snapshot.Models) == 0 {
		return harnesses.ModelDiscoverySnapshot{}, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness. Returns
// harnesses.ErrAliasNotResolvable when the supplied family is not a
// recognized claude alias or when the supplied discovery snapshot has
// no concrete model matching the family.
func (r *Runner) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(family))
	if !isSupportedClaudeAlias(normalized) {
		return "", harnesses.ErrAliasNotResolvable
	}
	resolved := resolveClaudeFamilyAlias(normalized, snapshot)
	if resolved == "" {
		return "", harnesses.ErrAliasNotResolvable
	}
	return resolved, nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (r *Runner) SupportedAliases() []string {
	return append([]string(nil), supportedAliases...)
}

func isSupportedClaudeAlias(family string) bool {
	for _, a := range supportedAliases {
		if a == family {
			return true
		}
	}
	return false
}

// claudeQuotaStatusFromSnapshot projects the per-harness snapshot onto
// harnesses.QuotaStatus, deriving State and RoutingPreference via the
// existing internal routing helper so today's "PreferClaude +
// freshness" semantics map cleanly onto CONTRACT-004's RoutingPreference
// enum.
func claudeQuotaStatusFromSnapshot(snap *claudeQuotaSnapshot, now time.Time) harnesses.QuotaStatus {
	decision := decideClaudeQuotaRouting(snap, now, claudeQuotaFreshness)
	pref, state := mapClaudeRoutingPreference(decision)
	status := harnesses.QuotaStatus{
		Source:            snap.Source,
		CapturedAt:        snap.CapturedAt,
		Fresh:             decision.Fresh,
		Age:               decision.Age,
		State:             state,
		Windows:           append([]harnesses.QuotaWindow(nil), snap.Windows...),
		RoutingPreference: pref,
		Reason:            quotaReasonForProjection(decision, state),
	}
	if snap.Account != nil {
		acct := claudeAccountSnapshotFromQuotaSnapshot(snap, now)
		status.Account = &acct
	}
	return status
}

// quotaReasonForProjection trims the routing decision's diagnostic
// reason so QuotaStatus.Reason carries information only when it adds
// value beyond the State enum. The trivial QuotaOK success path
// ("fresh snapshot has headroom") is suppressed so the public Status
// string matches the contract-003 fixture (bare "ok"). All non-OK
// states keep the decision reason because it surfaces why the state is
// not ok (stale snapshot, incomplete account, exhausted window).
func quotaReasonForProjection(decision claudeQuotaRoutingDecision, state harnesses.QuotaStateValue) string {
	if state == harnesses.QuotaOK {
		return ""
	}
	return decision.Reason
}

// mapClaudeRoutingPreference translates today's PreferClaude+freshness
// decision into the CONTRACT-004 (RoutingPreference, QuotaStateValue)
// pair. Legacy semantics: missing snapshot ⇒ Unavailable/Unknown;
// stale ⇒ Stale/Blocked ("assume limited"); fresh+PreferClaude ⇒
// OK/Available; fresh+!PreferClaude ⇒ Blocked/Blocked.
func mapClaudeRoutingPreference(d claudeQuotaRoutingDecision) (harnesses.RoutingPreference, harnesses.QuotaStateValue) {
	if !d.SnapshotPresent {
		return harnesses.RoutingPreferenceUnknown, harnesses.QuotaUnavailable
	}
	if !d.Fresh {
		return harnesses.RoutingPreferenceBlocked, harnesses.QuotaStale
	}
	if d.PreferClaude {
		return harnesses.RoutingPreferenceAvailable, harnesses.QuotaOK
	}
	return harnesses.RoutingPreferenceBlocked, harnesses.QuotaBlocked
}

// claudeAccountSnapshotFromQuotaSnapshot derives an AccountSnapshot from
// the quota cache's embedded Account field. Plan-type "unknown" (written
// by markClaudeQuotaExhaustedFromMessage on runtime errors) is reported
// as unauthenticated evidence rather than positive authentication.
func claudeAccountSnapshotFromQuotaSnapshot(snap *claudeQuotaSnapshot, now time.Time) harnesses.AccountSnapshot {
	out := harnesses.AccountSnapshot{
		Source:     "cache",
		CapturedAt: snap.CapturedAt,
		Fresh:      isClaudeQuotaFresh(snap, now, claudeAccountFreshness),
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

// Compile-time interface satisfaction.
var (
	_ harnesses.Harness               = (*Runner)(nil)
	_ harnesses.QuotaHarness          = (*Runner)(nil)
	_ harnesses.AccountHarness        = (*Runner)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Runner)(nil)
)
