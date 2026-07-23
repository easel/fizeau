package grok

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/easel/fizeau/internal/harnesses"
)

// grokQuotaFreshness is the constant freshness window for grok quota
// evidence. Mirrors defaultGrokQuotaStaleAfter (kept as a separate name so
// the CONTRACT-004 method has a stable, contract-named constant).
const grokQuotaFreshness = defaultGrokQuotaStaleAfter

// grokAccountFreshness is the constant freshness window for grok account
// evidence. Grok reads account metadata from auth.json, which the CLI
// refreshes itself (~7-day token expiry), so a long window is appropriate.
const grokAccountFreshness = 7 * 24 * time.Hour

// supportedLimitIDs is the stable public set of Windows[].LimitID values
// grok's quota probes emit. Mirrored in doc.go for human readers.
var supportedLimitIDs = []string{
	"grok",
	"grok-weekly",
}

// supportedAliases is the stable public set of family aliases
// ResolveModelAlias recognizes. Mirrored in doc.go for human readers.
var supportedAliases = []string{"grok", "grok-4"}

// refreshGroup serializes concurrent RefreshQuota / RefreshAccount calls
// across all *Runner instances. Per CONTRACT-004 invariant #6 Runners are
// stateless wrappers — sharing the singleflight group at package scope keeps
// single-flight semantics correct when callers construct fresh runner
// instances per call.
var refreshGroup singleflight.Group

// QuotaStatus implements harnesses.QuotaHarness. It reads the cached
// snapshot owned by this harness and projects it onto QuotaStatus. A missing
// or undecodable cache is reported as State=QuotaUnavailable on a valid
// QuotaStatus value (no error) per CONTRACT-004 §"Errors are reserved for
// call failure."
func (r *Runner) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.QuotaStatus{}, err
	}
	snap, ok := readGrokQuota()
	if !ok || snap == nil {
		return harnesses.QuotaStatus{
			Source:            "cache",
			State:             harnesses.QuotaUnavailable,
			RoutingPreference: harnesses.RoutingPreferenceUnknown,
			Reason:            "no cached snapshot",
		}, nil
	}
	return grokQuotaStatusFromSnapshot(snap, now), nil
}

// RefreshQuota implements harnesses.QuotaHarness. It probes the grok TUI's
// /usage show surface via PTY and persists the result through the harness's
// owned cache. Single-flight per harness: concurrent callers share one
// cohort and observe the just-written cached state. Probe failure is
// reported as State=QuotaUnavailable on a valid QuotaStatus value, not as an
// error.
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

// refreshQuotaLocked is the single-flight critical section. It runs the PTY
// probe, writes the cache on success, and returns the projected status.
// Total probe failure is folded into State=QuotaUnavailable (or
// QuotaUnauthenticated for a missing binary).
func (r *Runner) refreshQuotaLocked(ctx context.Context) harnesses.QuotaStatus {
	now := time.Now()
	timeout := grokQuotaFreshness
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
	windows, _, ptyErr := captureGrokQuotaViaPTY(ctx, timeout, opts...)
	if ptyErr == nil && len(windows) > 0 {
		snap := grokQuotaSnapshot{
			CapturedAt: now.UTC(),
			Windows:    windows,
			Source:     "pty",
			Account:    readGrokAccountOrNil(),
		}
		if path, pathErr := grokQuotaCachePath(); pathErr == nil {
			_ = writeGrokQuota(path, snap)
		}
		return grokQuotaStatusFromSnapshot(&snap, now)
	}
	state := harnesses.QuotaUnavailable
	reason := "no quota evidence from PTY probe"
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
	return grokQuotaFreshness
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (r *Runner) SupportedLimitIDs() []string {
	return append([]string(nil), supportedLimitIDs...)
}

// AccountStatus implements harnesses.AccountHarness. Grok reads non-secret
// account metadata from ~/.grok/auth.json; this method projects that onto
// AccountSnapshot.
func (r *Runner) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return readGrokAccountSnapshot(now), nil
}

// RefreshAccount implements harnesses.AccountHarness by re-reading the grok
// auth file (the source of account evidence) and projecting onto
// AccountSnapshot. Single-flight per harness via the package-scoped
// singleflight group.
func (r *Runner) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	v, err, _ := refreshGroup.Do("refresh-account", func() (any, error) {
		return readGrokAccountSnapshot(time.Now()), nil
	})
	if err != nil {
		return harnesses.AccountSnapshot{}, err
	}
	return v.(harnesses.AccountSnapshot), nil
}

// AccountFreshness implements harnesses.AccountHarness.
func (r *Runner) AccountFreshness() time.Duration {
	return grokAccountFreshness
}

// readGrokAccountSnapshot reads grok auth state and projects it onto
// AccountSnapshot. A missing or unreadable auth file is reported as
// Unauthenticated on a valid snapshot value, not as an error.
func readGrokAccountSnapshot(now time.Time) harnesses.AccountSnapshot {
	snap := harnesses.AccountSnapshot{Source: "cache"}
	if path, err := grokAuthPath(); err == nil && path != "" {
		snap.Source = path
		if st, statErr := os.Stat(path); statErr == nil {
			snap.CapturedAt = st.ModTime().UTC()
			snap.Fresh = now.UTC().Sub(snap.CapturedAt) <= grokAccountFreshness
		}
	}
	account, ok := readGrokAccount()
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
// the live CLI discovery helper with a sensible timeout; on failure it falls
// back to the grok CLI's on-disk models cache.
func (r *Runner) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	return r.DefaultModelSnapshotWithContext(context.Background())
}

// DefaultModelSnapshotWithContext implements
// harnesses.ContextModelDiscoveryHarness. The `grok models` subcommand is the
// primary source; the CLI's own models_cache.json is fallback evidence when
// the subcommand fails (e.g. transient network or auth issues with cached
// state still present).
func (r *Runner) DefaultModelSnapshotWithContext(ctx context.Context) (harnesses.ModelDiscoverySnapshot, error) {
	cliCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot, cliErr := readGrokModelDiscoveryFromCLI(cliCtx, r.Binary)
	if cliErr == nil && len(snapshot.Models) > 0 {
		return snapshot, nil
	}
	if cached, cacheErr := readGrokModelDiscoveryFromModelsCache(""); cacheErr == nil && len(cached.Models) > 0 {
		return cached, nil
	}
	if cliErr != nil {
		return harnesses.ModelDiscoverySnapshot{}, fmt.Errorf("model discovery CLI: %w", cliErr)
	}
	return harnesses.ModelDiscoverySnapshot{}, harnesses.ErrModelDiscoveryEvidenceMissing
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness. Returns
// harnesses.ErrAliasNotResolvable when the supplied family is not a
// recognized grok alias or when the supplied discovery snapshot has no
// concrete model matching the family.
func (r *Runner) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(family))
	if !isSupportedGrokAlias(normalized) {
		return "", harnesses.ErrAliasNotResolvable
	}
	resolved := resolveGrokModelAlias(normalized, snapshot)
	if resolved == "" || resolved == normalized {
		// resolveGrokModelAlias returns the input string when no concrete
		// model matches; treat that as unresolvable per CONTRACT-004.
		return "", harnesses.ErrAliasNotResolvable
	}
	return resolved, nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (r *Runner) SupportedAliases() []string {
	return append([]string(nil), supportedAliases...)
}

func isSupportedGrokAlias(family string) bool {
	for _, a := range supportedAliases {
		if a == family {
			return true
		}
	}
	return false
}

// grokQuotaStatusFromSnapshot projects the per-harness snapshot onto
// harnesses.QuotaStatus, deriving State and RoutingPreference via the
// decideGrokQuotaRouting helper so "PreferGrok + freshness" semantics map
// cleanly onto CONTRACT-004's RoutingPreference enum.
func grokQuotaStatusFromSnapshot(snap *grokQuotaSnapshot, now time.Time) harnesses.QuotaStatus {
	decision := decideGrokQuotaRouting(snap, now, grokQuotaFreshness)
	pref, state := mapGrokRoutingPreference(decision)
	windows := make([]harnesses.QuotaWindow, len(snap.Windows))
	for i, w := range snap.Windows {
		if w.LimitID == "" {
			w.LimitID = grokLimitIDForWindowMinutes(w.WindowMinutes)
		}
		windows[i] = w
	}
	status := harnesses.QuotaStatus{
		Source:            snap.Source,
		CapturedAt:        snap.CapturedAt,
		Fresh:             decision.Fresh,
		Age:               decision.Age,
		State:             state,
		Windows:           windows,
		RoutingPreference: pref,
		Reason:            decision.Reason,
	}
	if snap.Account != nil {
		acct := grokAccountSnapshotFromQuotaSnapshot(snap, now)
		status.Account = &acct
	}
	return status
}

// grokLimitIDForWindowMinutes maps a window duration onto the supported
// limit-ID set when the parser did not stamp one.
func grokLimitIDForWindowMinutes(windowMinutes int) string {
	if windowMinutes >= 10080 {
		return "grok-weekly"
	}
	return "grok"
}

// mapGrokRoutingPreference translates the PreferGrok+freshness decision into
// the CONTRACT-004 (RoutingPreference, QuotaStateValue) pair. Semantics
// mirror codex: missing snapshot ⇒ Unavailable/Unknown; stale ⇒
// Stale/Blocked ("assume limited"); fresh+PreferGrok ⇒ OK/Available;
// fresh+!PreferGrok ⇒ Blocked/Blocked.
func mapGrokRoutingPreference(d grokQuotaRoutingDecision) (harnesses.RoutingPreference, harnesses.QuotaStateValue) {
	if !d.SnapshotPresent {
		return harnesses.RoutingPreferenceUnknown, harnesses.QuotaUnavailable
	}
	if !d.Fresh {
		return harnesses.RoutingPreferenceBlocked, harnesses.QuotaStale
	}
	if d.PreferGrok {
		return harnesses.RoutingPreferenceAvailable, harnesses.QuotaOK
	}
	return harnesses.RoutingPreferenceBlocked, harnesses.QuotaBlocked
}

// grokAccountSnapshotFromQuotaSnapshot derives an AccountSnapshot from the
// quota cache's embedded Account field. An empty plan-type is reported as
// unauthenticated evidence rather than positive authentication.
func grokAccountSnapshotFromQuotaSnapshot(snap *grokQuotaSnapshot, now time.Time) harnesses.AccountSnapshot {
	out := harnesses.AccountSnapshot{
		Source:     "cache",
		CapturedAt: snap.CapturedAt,
		Fresh:      isGrokQuotaFresh(snap, now, grokAccountFreshness),
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
	_ harnesses.Harness                      = (*Runner)(nil)
	_ harnesses.QuotaHarness                 = (*Runner)(nil)
	_ harnesses.AccountHarness               = (*Runner)(nil)
	_ harnesses.ModelDiscoveryHarness        = (*Runner)(nil)
	_ harnesses.ContextModelDiscoveryHarness = (*Runner)(nil)
)
