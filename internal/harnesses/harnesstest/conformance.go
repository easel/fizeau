package harnesstest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// RunHarnessConformance asserts the contract every harnesses.Harness
// implementation MUST honor:
//
//   - Info returns a HarnessInfo with a non-empty Name.
//   - HealthCheck is callable with a non-nil context and either
//     returns nil or a non-nil error; the suite does not invoke any
//     binary and does not assert a particular outcome.
//   - Execute honors the setup-vs-runtime error contract documented on
//     the Harness interface: a non-nil error means the returned
//     channel is nil; a nil error means the returned channel is
//     non-nil. The suite drives Execute with an already-cancelled
//     context to keep real harnesses from launching real binaries.
//
// The suite is safe to run against in-memory synthetic harnesses and
// against real harnesses (e.g. &claude.Runner{}); in the latter case
// Execute typically returns an error because the binary path is empty
// or the context is cancelled before setup completes.
func RunHarnessConformance(t *testing.T, h harnesses.Harness) {
	t.Helper()
	if h == nil {
		t.Fatal("RunHarnessConformance called with nil harness")
	}

	t.Run("Info", func(t *testing.T) {
		info := h.Info()
		if info.Name == "" {
			t.Error("Info().Name is empty")
		}
	})

	t.Run("HealthCheck", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Either nil or error is acceptable; the contract is that the
		// call returns without panicking.
		_ = h.HealthCheck(ctx)
	})

	t.Run("ExecuteSetupVsRuntimeContract", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		ch, err := h.Execute(ctx, harnesses.ExecuteRequest{})
		if err != nil {
			if ch != nil {
				t.Fatalf("Execute returned non-nil channel together with error %v", err)
			}
			return
		}
		if ch == nil {
			t.Fatal("Execute returned nil channel with nil error")
		}
		// Drain whatever the harness already enqueued so we don't leak
		// goroutines. Bound it so real harnesses cannot hang the suite.
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer drainCancel()
		drained := false
		for !drained {
			select {
			case _, ok := <-ch:
				if !ok {
					drained = true
				}
			case <-drainCtx.Done():
				// Harness owns the channel; we don't fail the
				// contract on a stuck producer here — the
				// Execute-event contract has its own dedicated
				// tests elsewhere. Stop draining.
				drained = true
			}
		}
	})
}

// RunQuotaHarnessConformance asserts the QuotaHarness contract.
//
//   - The base Harness contract via RunHarnessConformance.
//   - QuotaStatus returns a valid value (no error) for a cold cache.
//   - QuotaFreshness is positive.
//   - SupportedLimitIDs is callable and stable (two calls return the
//     same multiset).
//   - RefreshQuota returns without error and the emitted
//     Windows[].LimitID is a subset of SupportedLimitIDs().
//   - Concurrent RefreshQuota calls all return without error and share
//     a consistent post-state (single-flight contract shape; exact
//     probe count is NOT asserted here — that belongs to harness-local
//     tests, e.g. the synthetic's probe counter).
func RunQuotaHarnessConformance(t *testing.T, h harnesses.QuotaHarness) {
	t.Helper()
	if h == nil {
		t.Fatal("RunQuotaHarnessConformance called with nil harness")
	}

	RunHarnessConformance(t, h)

	t.Run("QuotaFreshnessPositive", func(t *testing.T) {
		if got := h.QuotaFreshness(); got <= 0 {
			t.Errorf("QuotaFreshness() = %v, want > 0", got)
		}
	})

	t.Run("SupportedLimitIDsStable", func(t *testing.T) {
		a := h.SupportedLimitIDs()
		b := h.SupportedLimitIDs()
		if !stringSetEqual(a, b) {
			t.Errorf("SupportedLimitIDs not stable: first=%v second=%v", a, b)
		}
	})

	t.Run("QuotaStatusReturnsValue", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := h.QuotaStatus(ctx, time.Now()); err != nil {
			t.Errorf("QuotaStatus returned error %v; absence of evidence MUST be reported via QuotaStateValue, not error", err)
		}
	})

	t.Run("RefreshQuotaWindowsLimitIDSubset", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, err := h.RefreshQuota(ctx)
		if err != nil {
			// Probe failure MUST be reported as QuotaStateValue, not error.
			t.Errorf("RefreshQuota returned error %v; probe failure MUST be reported via QuotaStateValue", err)
			return
		}
		supported := stringSet(h.SupportedLimitIDs())
		for i, w := range status.Windows {
			if w.LimitID == "" {
				continue
			}
			if _, ok := supported[w.LimitID]; !ok {
				t.Errorf("RefreshQuota Windows[%d].LimitID = %q is not in SupportedLimitIDs() = %v", i, w.LimitID, h.SupportedLimitIDs())
			}
		}
	})

	t.Run("RefreshQuotaSingleFlightShape", func(t *testing.T) {
		const N = 8
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []harnesses.QuotaStatus
			errs    []error
		)
		wg.Add(N)
		for i := 0; i < N; i++ {
			go func() {
				defer wg.Done()
				s, err := h.RefreshQuota(ctx)
				mu.Lock()
				results = append(results, s)
				errs = append(errs, err)
				mu.Unlock()
			}()
		}
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Errorf("concurrent RefreshQuota[%d] returned error %v; expected nil", i, err)
			}
		}
		// Post-state consistency: a final QuotaStatus read MUST be a
		// valid value (no error). The conformance suite does not
		// pin individual field values, only the contract shape.
		if _, err := h.QuotaStatus(ctx, time.Now()); err != nil {
			t.Errorf("QuotaStatus after concurrent RefreshQuota returned error %v", err)
		}
	})
}

// RunAccountHarnessConformance asserts the AccountHarness contract.
//
//   - The base Harness contract via RunHarnessConformance.
//   - AccountStatus returns a valid value (no error).
//   - AccountFreshness is positive.
//   - RefreshAccount returns without error.
//   - Concurrent RefreshAccount calls all succeed and post-state
//     remains consistent (single-flight contract shape).
func RunAccountHarnessConformance(t *testing.T, h harnesses.AccountHarness) {
	t.Helper()
	if h == nil {
		t.Fatal("RunAccountHarnessConformance called with nil harness")
	}

	RunHarnessConformance(t, h)

	t.Run("AccountFreshnessPositive", func(t *testing.T) {
		if got := h.AccountFreshness(); got <= 0 {
			t.Errorf("AccountFreshness() = %v, want > 0", got)
		}
	})

	t.Run("AccountStatusReturnsValue", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := h.AccountStatus(ctx, time.Now()); err != nil {
			t.Errorf("AccountStatus returned error %v; absence MUST be reported via AccountSnapshot fields", err)
		}
	})

	t.Run("RefreshAccountReturnsValue", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := h.RefreshAccount(ctx); err != nil {
			t.Errorf("RefreshAccount returned error %v; probe failure MUST be reported via AccountSnapshot fields", err)
		}
	})

	t.Run("RefreshAccountSingleFlightShape", func(t *testing.T) {
		const N = 8
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			errs []error
		)
		wg.Add(N)
		for i := 0; i < N; i++ {
			go func() {
				defer wg.Done()
				_, err := h.RefreshAccount(ctx)
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("concurrent RefreshAccount[%d] returned error %v", i, err)
			}
		}
		if _, err := h.AccountStatus(ctx, time.Now()); err != nil {
			t.Errorf("AccountStatus after concurrent RefreshAccount returned error %v", err)
		}
	})
}

// RunModelDiscoveryHarnessConformance asserts the ModelDiscoveryHarness
// contract.
//
//   - The base Harness contract via RunHarnessConformance.
//   - DefaultModelSnapshot returns a non-empty model list.
//   - For every alias in SupportedAliases(), ResolveModelAlias returns
//     a non-empty model and a nil error (positive path).
//   - ResolveModelAlias returns ErrAliasNotResolvable for an
//     out-of-set family (negative path).
//   - Empty SupportedAliases() (allowed for opencode/pi-style
//     harnesses) skips the positive-path assertion but still drives
//     the negative path.
func RunModelDiscoveryHarnessConformance(t *testing.T, h harnesses.ModelDiscoveryHarness) {
	t.Helper()
	if h == nil {
		t.Fatal("RunModelDiscoveryHarnessConformance called with nil harness")
	}

	RunHarnessConformance(t, h)

	t.Run("DefaultModelSnapshotNonEmpty", func(t *testing.T) {
		snap := h.DefaultModelSnapshot()
		if len(snap.Models) == 0 && snap.Detail == "" {
			t.Errorf("DefaultModelSnapshot().Models is empty with no error detail; at runtime, empty models must indicate discovery failure")
		}
	})

	snapshot := h.DefaultModelSnapshot()
	aliases := h.SupportedAliases()

	t.Run("SupportedAliasesStable", func(t *testing.T) {
		b := h.SupportedAliases()
		if !stringSetEqual(aliases, b) {
			t.Errorf("SupportedAliases not stable: first=%v second=%v", aliases, b)
		}
	})

	t.Run("ResolveModelAliasPositive", func(t *testing.T) {
		if len(aliases) == 0 || len(snapshot.Models) == 0 {
			t.Skip("SupportedAliases is empty or DefaultModelSnapshot has no models; positive path skipped")
		}
		for _, alias := range aliases {
			model, err := h.ResolveModelAlias(alias, snapshot)
			if err != nil {
				t.Errorf("ResolveModelAlias(%q) error = %v, want nil", alias, err)
				continue
			}
			if model == "" {
				t.Errorf("ResolveModelAlias(%q) returned empty model with nil error", alias)
			}
		}
	})

	t.Run("ResolveModelAliasNegative", func(t *testing.T) {
		const outOfSet = "__harnesstest_definitely_not_a_real_family__"
		if containsString(aliases, outOfSet) {
			t.Skipf("sentinel out-of-set family %q is in SupportedAliases(); skipping negative path", outOfSet)
		}
		model, err := h.ResolveModelAlias(outOfSet, snapshot)
		if !errors.Is(err, harnesses.ErrAliasNotResolvable) {
			t.Errorf("ResolveModelAlias(%q) error = %v, want ErrAliasNotResolvable", outOfSet, err)
		}
		if model != "" {
			t.Errorf("ResolveModelAlias(%q) returned model %q with non-resolvable error; want empty", outOfSet, model)
		}
	})
}

func stringSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}

func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := stringSet(a)
	for _, s := range b {
		if _, ok := as[s]; !ok {
			return false
		}
	}
	return true
}

func containsString(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
