package codex

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

// codexQuotaFreshness is the constant freshness window for codex quota
// evidence. Mirrors defaultCodexQuotaStaleAfter (kept as a separate name
// so the CONTRACT-004 method has a stable, contract-named constant).
const codexQuotaFreshness = defaultCodexQuotaStaleAfter

// codexAccountFreshness is the constant freshness window for codex
// account evidence. Codex reads account metadata from auth.json which
// changes only on re-auth, so a long window is appropriate.
const codexAccountFreshness = 7 * 24 * time.Hour

// supportedLimitIDs is the stable public set of Windows[].LimitID values
// codex's quota probes emit. Mirrored in doc.go for human readers.
var supportedLimitIDs = []string{
	"codex",
	"codex-weekly",
}

// supportedAliases is the stable public set of family aliases
// ResolveModelAlias recognizes. Mirrored in doc.go for human readers.
var supportedAliases = []string{"gpt", "gpt-5"}

// refreshGroup serializes concurrent RefreshQuota / RefreshAccount calls
// across all *Runner instances. Per CONTRACT-004 invariant #6 Runners are
// stateless wrappers — sharing the singleflight group at package scope
// keeps single-flight semantics correct when callers construct fresh
// runner instances per call (which the dispatcher does today).
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
	snap, ok := readCodexQuota()
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	return codexQuotaStatusFromSnapshot(snap, now), nil
}

// RefreshQuota implements harnesses.QuotaHarness. It probes the codex
// CLI via PTY first and folds the session-token-count source in as an
// internal fallback when the PTY probe yields no evidence. The result
// is persisted through the harness's owned cache. Single-flight per
// harness: concurrent callers share one cohort and observe the
// just-written cached state. Probe failure is reported as
// State=QuotaUnavailable on a valid QuotaStatus value, not as an error.
func (r *Runner) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	v, err, _ := refreshGroup.Do("refresh-quota", func() (any, error) {
		return r.refreshQuotaLocked(ctx), nil
	})
	if err != nil {
		return harnesses.QuotaStatus{}, err
	}
	return v.(harnesses.QuotaStatus), nil
}

// refreshQuotaLocked is the single-flight critical section. It runs the
// PTY probe, falls back to the session-token-count source when PTY
// yields no evidence, writes the cache on success, and returns the
// projected status. Total probe failure is folded into
// State=QuotaUnavailable.
func (r *Runner) refreshQuotaLocked(ctx context.Context) harnesses.QuotaStatus {
	now := time.Now()
	timeout := codexQuotaFreshness
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
	windows, _, ptyErr := captureCodexQuotaViaPTY(ctx, timeout, opts...)
	if ptyErr == nil && len(windows) > 0 {
		snap := codexQuotaSnapshot{
			CapturedAt: now.UTC(),
			Windows:    windows,
			Source:     "pty",
			Account:    readCodexAccountOrNil(),
		}
		if path, pathErr := codexQuotaCachePath(); pathErr == nil {
			_ = writeCodexQuota(path, snap)
		}
		return codexQuotaStatusFromSnapshot(&snap, now)
	}
	if snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now)); ok && snap != nil {
		if snap.Account == nil {
			snap.Account = readCodexAccountOrNil()
		}
		if path, pathErr := codexQuotaCachePath(); pathErr == nil {
			_ = writeCodexQuota(path, *snap)
		}
		return codexQuotaStatusFromSnapshot(snap, now)
	}
	state := harnesses.QuotaUnavailable
	reason := "no quota evidence from PTY probe or session token-count fallback"
	if ptyErr != nil {
		reason = ptyErr.Error()
		lower := strings.ToLower(reason)
		if strings.Contains(lower, "not found in path") || strings.Contains(lower, "executable file not found") {
			state = harnesses.QuotaUnauthenticated
		}
	}
	return harnesses.QuotaStatus{
		Source:            "pty",
		CapturedAt:        now.UTC(),
		State:             state,
		RoutingPreference: harnesses.RoutingPreferenceUnknown,
		Reason:            reason,
	}
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (r *Runner) QuotaFreshness() time.Duration {
	return codexQuotaFreshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (r *Runner) SupportedLimitIDs() []string {
	return append([]string(nil), supportedLimitIDs...)
}

// AccountStatus implements harnesses.AccountHarness. Codex reads
// non-secret account metadata from auth.json via codexAuthPath /
// readCodexAccount; this method projects that onto AccountSnapshot.
func (r *Runner) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return readCodexAccountSnapshot(now), nil
}

// RefreshAccount implements harnesses.AccountHarness by re-reading the
// codex auth file (the source of account evidence) and projecting onto
// AccountSnapshot. Single-flight per harness via the package-scoped
// singleflight group.
func (r *Runner) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	v, err, _ := refreshGroup.Do("refresh-account", func() (any, error) {
		return readCodexAccountSnapshot(time.Now()), nil
	})
	if err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return v.(harnesses.AccountSnapshot), nil
}

// AccountFreshness implements harnesses.AccountHarness.
func (r *Runner) AccountFreshness() time.Duration {
	return codexAccountFreshness
}

// readCodexAccountSnapshot reads codex auth state and projects it onto
// AccountSnapshot. A missing or unreadable auth file is reported as
// Unauthenticated on a valid snapshot value, not as an error.
func readCodexAccountSnapshot(now time.Time) harnesses.AccountSnapshot {
	snap := harnesses.AccountSnapshot{Source: "cache"}
	if path, err := codexAuthPath(); err == nil && path != "" {
		snap.Source = path
		if st, statErr := os.Stat(path); statErr == nil {
			snap.CapturedAt = st.ModTime().UTC()
			snap.Fresh = now.UTC().Sub(snap.CapturedAt) <= codexAccountFreshness
		}
	}
	account, ok := readCodexAccount()
	if !ok || account == nil {
		snap.Unauthenticated = true
		return snap
	}
	snap.Email = account.Email
	snap.PlanType = account.PlanType
	snap.OrgName = account.OrgName
	if strings.TrimSpace(account.PlanType) == "" {
		snap.Unauthenticated = true
	} else {
		snap.Authenticated = true
	}
	return snap
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness. It calls
// the live PTY discovery helper with a sensible timeout; on failure,
// returns a snapshot with empty Models and error detail.
func (r *Runner) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	timeout := 10 * time.Second
	var opts []QuotaPTYOption
	if r.Binary != "" {
		opts = append(opts, WithQuotaPTYCommand(r.Binary))
	}
	snapshot, err := ReadCodexModelDiscoveryViaPTY(timeout, opts...)
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
// recognized codex alias or when the supplied discovery snapshot has
// no concrete model matching the family.
func (r *Runner) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(family))
	if !isSupportedCodexAlias(normalized) {
		return "", harnesses.ErrAliasNotResolvable
	}
	resolved := resolveCodexModelAlias(normalized, snapshot)
	if resolved == "" || resolved == normalized {
		// existing helper returns the input string when no concrete
		// model matches; treat that as unresolvable per CONTRACT-004.
		return "", harnesses.ErrAliasNotResolvable
	}
	return resolved, nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (r *Runner) SupportedAliases() []string {
	return append([]string(nil), supportedAliases...)
}

func isSupportedCodexAlias(family string) bool {
	for _, a := range supportedAliases {
		if a == family {
			return true
		}
	}
	return false
}

// codexQuotaStatusFromSnapshot projects the per-harness snapshot onto
// harnesses.QuotaStatus, deriving State and RoutingPreference via the
// existing decideCodexQuotaRouting helper so today's "PreferCodex +
// freshness" semantics map cleanly onto CONTRACT-004's RoutingPreference
// enum.
func codexQuotaStatusFromSnapshot(snap *codexQuotaSnapshot, now time.Time) harnesses.QuotaStatus {
	decision := decideCodexQuotaRouting(snap, now, codexQuotaFreshness)
	pref, state := mapCodexRoutingPreference(decision)
	status := harnesses.QuotaStatus{
		Source:            snap.Source,
		CapturedAt:        snap.CapturedAt,
		Fresh:             decision.Fresh,
		Age:               decision.Age,
		State:             state,
		Windows:           append([]harnesses.QuotaWindow(nil), snap.Windows...),
		RoutingPreference: pref,
		Reason:            decision.Reason,
	}
	if snap.Account != nil {
		acct := codexAccountSnapshotFromQuotaSnapshot(snap, now)
		status.Account = &acct
	}
	return status
}

// mapCodexRoutingPreference translates today's PreferCodex+freshness
// decision into the CONTRACT-004 (RoutingPreference, QuotaStateValue)
// pair. Legacy semantics: missing snapshot ⇒ Unavailable/Unknown;
// stale ⇒ Stale/Blocked ("assume limited"); fresh+PreferCodex ⇒
// OK/Available; fresh+!PreferCodex ⇒ Blocked/Blocked.
func mapCodexRoutingPreference(d codexQuotaRoutingDecision) (harnesses.RoutingPreference, harnesses.QuotaStateValue) {
	if !d.SnapshotPresent {
		return harnesses.RoutingPreferenceUnknown, harnesses.QuotaUnavailable
	}
	if !d.Fresh {
		return harnesses.RoutingPreferenceBlocked, harnesses.QuotaStale
	}
	if d.PreferCodex {
		return harnesses.RoutingPreferenceAvailable, harnesses.QuotaOK
	}
	return harnesses.RoutingPreferenceBlocked, harnesses.QuotaBlocked
}

// codexAccountSnapshotFromQuotaSnapshot derives an AccountSnapshot from
// the quota cache's embedded Account field. An empty plan-type is
// reported as unauthenticated evidence rather than positive
// authentication.
func codexAccountSnapshotFromQuotaSnapshot(snap *codexQuotaSnapshot, now time.Time) harnesses.AccountSnapshot {
	out := harnesses.AccountSnapshot{
		Source:     "cache",
		CapturedAt: snap.CapturedAt,
		Fresh:      isCodexQuotaFresh(snap, now, codexAccountFreshness),
	}
	if snap.Account == nil {
		return out
	}
	plan := strings.TrimSpace(snap.Account.PlanType)
	out.PlanType = plan
	out.Email = snap.Account.Email
	out.OrgName = snap.Account.OrgName
	if plan == "" {
		out.Unauthenticated = true
	} else {
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
