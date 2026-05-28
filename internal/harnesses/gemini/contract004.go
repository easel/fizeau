package gemini

import (
	"context"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

// geminiQuotaFreshness is the constant freshness window for gemini quota
// evidence. Mirrors defaultGeminiQuotaStaleAfter; kept as a separate name
// so the CONTRACT-004 method has a stable, contract-named constant.
const geminiQuotaFreshness = defaultGeminiQuotaStaleAfter

// geminiAccountFreshness is the constant freshness window for gemini
// account/auth evidence. Gemini auth (~/.gemini OAuth and friends) rotates
// on a much slower cadence than quota; the scheduler runs it on its own
// 7-day ticker.
const geminiAccountFreshness = geminiAuthFreshnessWindow

// supportedLimitIDs is the stable public set of Windows[].LimitID values
// gemini's /model manage probe emits. One per tier surfaced by the
// CLI dialog. Mirrored in doc.go for human readers.
var supportedLimitIDs = []string{
	"gemini-pro",
	"gemini-flash",
	"gemini-flash-lite",
}

// supportedAliases is the stable public set of family aliases
// ResolveModelAlias recognizes. Concrete tier names (e.g.
// "gemini-2.5-flash") are not aliases — they pass through unchanged
// when the routing layer requests them. Mirrored in doc.go.
var supportedAliases = []string{
	"gemini",
	"gemini-2",
	"gemini-2.5",
}

// refreshGroup serializes concurrent RefreshQuota / RefreshAccount calls
// across all *Runner instances. Per CONTRACT-004 invariant #6 Runners are
// stateless wrappers — sharing the singleflight group at package scope
// keeps single-flight semantics correct when callers construct fresh
// runner instances per call.
var refreshGroup singleflight.Group

// QuotaStatus implements harnesses.QuotaHarness. It reads the cached
// snapshot owned by this harness and projects it onto QuotaStatus. A
// missing or undecodable cache is reported as State=QuotaUnavailable on
// a valid QuotaStatus value (no error) per CONTRACT-004 §"Errors are
// reserved for call failure."
func (r *Runner) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	snap, ok := readGeminiQuota()
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	return geminiQuotaStatusFromSnapshot(snap, now), nil
}

// RefreshQuota implements harnesses.QuotaHarness. It probes the gemini
// CLI via PTY unconditionally and persists the result through the
// harness's owned cache. Single-flight per package: concurrent callers
// share one cohort and observe the just-written cached state. Probe
// failure (binary missing, PTY error, parse failure) is reported as
// State=QuotaUnavailable on a valid QuotaStatus value, not as an error.
func (r *Runner) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	v, err, _ := refreshGroup.Do("gemini:refresh-quota", func() (any, error) {
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
	auth := readAuthEvidence(now)
	if !auth.Authenticated {
		return harnesses.QuotaStatus{
			Source:            "auth",
			CapturedAt:        now.UTC(),
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "gemini not configured or authenticated",
		}
	}
	timeout := geminiQuotaFreshness
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
	windows, _, err := captureGeminiQuotaViaPTY(ctx, timeout, opts...)
	if err != nil {
		state := harnesses.QuotaUnavailable
		reason := err.Error()
		lower := strings.ToLower(reason)
		switch {
		case strings.Contains(lower, "not found") || strings.Contains(lower, "executable file not found"):
			state = harnesses.QuotaUnavailable
		case strings.Contains(lower, "unauth") || strings.Contains(lower, "credential"):
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
	snap := geminiQuotaSnapshot{
		CapturedAt: now.UTC(),
		Windows:    windows,
		Source:     "pty",
		Account:    auth.Account,
	}
	if path, pathErr := geminiQuotaCachePath(); pathErr == nil {
		_ = writeGeminiQuota(path, snap)
	}
	return geminiQuotaStatusFromSnapshot(&snap, now)
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (r *Runner) QuotaFreshness() time.Duration {
	return geminiQuotaFreshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (r *Runner) SupportedLimitIDs() []string {
	return append([]string(nil), supportedLimitIDs...)
}

// AccountStatus implements harnesses.AccountHarness. Gemini's account
// state is sourced from authSnapshot (read from ~/.gemini and env).
func (r *Runner) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	auth := readAuthEvidence(now)
	return geminiAccountSnapshotFromAuth(auth), nil
}

// RefreshAccount implements harnesses.AccountHarness. readAuthEvidence
// is filesystem-only (no network probe), so refresh is the same call as
// status. Single-flight on the package's refresh group keeps concurrent
// callers consistent with the QuotaHarness contract shape.
func (r *Runner) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	v, err, _ := refreshGroup.Do("gemini:refresh-account", func() (any, error) {
		auth := readAuthEvidence(time.Now())
		return geminiAccountSnapshotFromAuth(auth), nil
	})
	if err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return v.(harnesses.AccountSnapshot), nil
}

// AccountFreshness implements harnesses.AccountHarness. Gemini auth
// rotates on a 7-day cadence, decoupled from the 15-minute quota
// cadence — the refresh scheduler runs them on independent tickers.
func (r *Runner) AccountFreshness() time.Duration {
	return geminiAccountFreshness
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness. For gemini,
// which provides no direct PTY /model discovery command, it reads from the
// supplied CLI output or returns empty on error.
func (r *Runner) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	return harnesses.ModelDiscoverySnapshot{}, harnesses.ErrModelDiscoveryEvidenceMissing
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness. Returns
// harnesses.ErrAliasNotResolvable when the supplied family is not a
// recognized gemini alias or when the discovery snapshot has no concrete
// model matching the family.
func (r *Runner) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(family))
	if !isSupportedGeminiAlias(normalized) {
		return "", harnesses.ErrAliasNotResolvable
	}
	resolved := resolveGeminiModelAlias(normalized, snapshot)
	if resolved == "" || resolved == normalized {
		// resolveGeminiModelAlias returns the input unchanged when no
		// concrete model matches. Treat that as not-resolvable so the
		// CONTRACT-004 negative-path semantics are preserved.
		return "", harnesses.ErrAliasNotResolvable
	}
	return resolved, nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (r *Runner) SupportedAliases() []string {
	return append([]string(nil), supportedAliases...)
}

func isSupportedGeminiAlias(family string) bool {
	for _, a := range supportedAliases {
		if a == family {
			return true
		}
	}
	return false
}

// geminiQuotaStatusFromSnapshot projects the per-harness snapshot onto
// harnesses.QuotaStatus, deriving State and RoutingPreference via the
// existing decideGeminiQuotaRouting helper. Tier facts (Flash / Flash
// Lite / Pro) live in Windows with their distinguishing LimitID; no tier
// names appear in Detail.
func geminiQuotaStatusFromSnapshot(snap *geminiQuotaSnapshot, now time.Time) harnesses.QuotaStatus {
	decision := decideGeminiQuotaRouting(snap, now, geminiQuotaFreshness)
	pref, state := mapGeminiRoutingPreference(decision)
	status := harnesses.QuotaStatus{
		Source:            snap.Source,
		CapturedAt:        snap.CapturedAt,
		Fresh:             decision.fresh,
		Age:               decision.age,
		State:             state,
		Windows:           append([]harnesses.QuotaWindow(nil), snap.Windows...),
		RoutingPreference: pref,
		Reason:            decision.reason,
	}
	if snap.Account != nil {
		acct := harnesses.AccountSnapshot{
			Source:     "cache",
			CapturedAt: snap.CapturedAt,
			Fresh:      isGeminiQuotaFresh(snap, now, geminiAccountFreshness),
			PlanType:   snap.Account.PlanType,
			Email:      snap.Account.Email,
			OrgName:    snap.Account.OrgName,
		}
		if snap.Account.PlanType != "" || snap.Account.Email != "" {
			acct.Authenticated = true
		}
		status.Account = &acct
	}
	return status
}

// mapGeminiRoutingPreference translates today's preferGemini+freshness
// decision into the CONTRACT-004 (RoutingPreference, QuotaStateValue)
// pair. Legacy semantics: missing snapshot ⇒ Unavailable/Unknown;
// stale ⇒ Stale/Blocked ("assume limited"); fresh+preferGemini ⇒
// OK/Available; fresh+!preferGemini ⇒ Blocked/Blocked.
func mapGeminiRoutingPreference(d geminiQuotaRoutingDecision) (harnesses.RoutingPreference, harnesses.QuotaStateValue) {
	if !d.snapshotPresent {
		return harnesses.RoutingPreferenceUnknown, harnesses.QuotaUnavailable
	}
	if !d.fresh {
		return harnesses.RoutingPreferenceBlocked, harnesses.QuotaStale
	}
	if d.preferGemini {
		return harnesses.RoutingPreferenceAvailable, harnesses.QuotaOK
	}
	return harnesses.RoutingPreferenceBlocked, harnesses.QuotaBlocked
}

// geminiAccountSnapshotFromAuth projects an authSnapshot onto the
// universal AccountSnapshot type. Authenticated is true iff the
// underlying auth evidence reports an authenticated session;
// Unauthenticated is the explicit negative signal so the projection
// distinguishes "no evidence" from "evidence says signed out".
func geminiAccountSnapshotFromAuth(auth authSnapshot) harnesses.AccountSnapshot {
	out := harnesses.AccountSnapshot{
		Source:     auth.Source,
		CapturedAt: auth.CapturedAt,
		Fresh:      auth.Fresh,
		Detail:     auth.Detail,
	}
	if auth.Account != nil {
		out.PlanType = auth.Account.PlanType
		out.Email = auth.Account.Email
		out.OrgName = auth.Account.OrgName
	}
	if auth.Authenticated {
		out.Authenticated = true
	} else {
		out.Unauthenticated = true
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
