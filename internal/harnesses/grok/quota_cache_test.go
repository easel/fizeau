package grok

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func testGrokWindows(usedPercent float64, state string) []harnesses.QuotaWindow {
	return []harnesses.QuotaWindow{{
		Name: "weekly", LimitID: "grok-weekly", WindowMinutes: 10080,
		UsedPercent: usedPercent, State: state, ResetsAt: "July 27, 09:48",
	}}
}

func subscribedAccount() *harnesses.AccountInfo {
	return &harnesses.AccountInfo{Email: "user@example.com", PlanType: "Grok subscription"}
}

func TestGrokQuotaCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grok-quota.json")
	t.Setenv(grokQuotaCacheEnv, path)
	// Point auth at a non-existent file so writeGrokQuota does not absorb
	// the developer's real ~/.grok/auth.json.
	t.Setenv(grokAuthPathEnv, filepath.Join(t.TempDir(), "absent-auth.json"))

	now := time.Now().UTC()
	snap := grokQuotaSnapshot{
		CapturedAt: now,
		Windows:    testGrokWindows(93, "ok"),
		Source:     "pty",
		Account:    subscribedAccount(),
	}
	if err := writeGrokQuota(path, snap); err != nil {
		t.Fatalf("writeGrokQuota: %v", err)
	}
	got, ok := readGrokQuota()
	if !ok || got == nil {
		t.Fatal("readGrokQuota returned no snapshot")
	}
	if len(got.Windows) != 1 || got.Windows[0].UsedPercent != 93 {
		t.Fatalf("Windows = %+v", got.Windows)
	}
	if got.Account == nil || got.Account.PlanType != "Grok subscription" {
		t.Fatalf("Account = %+v", got.Account)
	}
	if got.Source != "pty" {
		t.Errorf("Source = %q", got.Source)
	}
}

func TestDecideGrokQuotaRouting(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name    string
		snap    *grokQuotaSnapshot
		prefer  bool
		present bool
		fresh   bool
	}{
		{name: "nil snapshot", snap: nil},
		{
			name: "stale snapshot",
			snap: &grokQuotaSnapshot{
				CapturedAt: now.Add(-time.Hour),
				Windows:    testGrokWindows(10, "ok"),
				Account:    subscribedAccount(),
			},
			present: true,
		},
		{
			name: "fresh with headroom",
			snap: &grokQuotaSnapshot{
				CapturedAt: now,
				Windows:    testGrokWindows(93, "ok"),
				Account:    subscribedAccount(),
			},
			present: true, fresh: true, prefer: true,
		},
		{
			name: "fresh but blocked window",
			snap: &grokQuotaSnapshot{
				CapturedAt: now,
				Windows:    testGrokWindows(97, "blocked"),
				Account:    subscribedAccount(),
			},
			present: true, fresh: true,
		},
		{
			name: "fresh at 95 percent used",
			snap: &grokQuotaSnapshot{
				CapturedAt: now,
				Windows:    testGrokWindows(95, "blocked"),
				Account:    subscribedAccount(),
			},
			present: true, fresh: true,
		},
		{
			name: "fresh without windows",
			snap: &grokQuotaSnapshot{
				CapturedAt: now,
				Account:    subscribedAccount(),
			},
			present: true, fresh: true,
		},
		{
			name: "fresh without subscription account",
			snap: &grokQuotaSnapshot{
				CapturedAt: now,
				Windows:    testGrokWindows(10, "ok"),
				Account:    &harnesses.AccountInfo{PlanType: "xAI API key"},
			},
			present: true, fresh: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideGrokQuotaRouting(tc.snap, now, defaultGrokQuotaStaleAfter)
			if d.PreferGrok != tc.prefer {
				t.Errorf("PreferGrok = %v, want %v (%s)", d.PreferGrok, tc.prefer, d.Reason)
			}
			if d.SnapshotPresent != tc.present {
				t.Errorf("SnapshotPresent = %v, want %v", d.SnapshotPresent, tc.present)
			}
			if d.Fresh != tc.fresh {
				t.Errorf("Fresh = %v, want %v", d.Fresh, tc.fresh)
			}
		})
	}
}

func TestGrokQuotaStatusProjection(t *testing.T) {
	now := time.Now().UTC()

	fresh := &grokQuotaSnapshot{
		CapturedAt: now,
		Windows:    testGrokWindows(93, "ok"),
		Source:     "pty",
		Account:    subscribedAccount(),
	}
	status := grokQuotaStatusFromSnapshot(fresh, now)
	if status.State != harnesses.QuotaOK {
		t.Errorf("State = %q, want ok (%s)", status.State, status.Reason)
	}
	if status.RoutingPreference != harnesses.RoutingPreferenceAvailable {
		t.Errorf("RoutingPreference = %v, want available", status.RoutingPreference)
	}
	if len(status.Windows) != 1 || status.Windows[0].LimitID != "grok-weekly" {
		t.Errorf("Windows = %+v", status.Windows)
	}
	if status.Account == nil || !status.Account.Authenticated {
		t.Errorf("Account = %+v, want authenticated", status.Account)
	}

	stale := &grokQuotaSnapshot{
		CapturedAt: now.Add(-2 * time.Hour),
		Windows:    testGrokWindows(10, "ok"),
		Account:    subscribedAccount(),
	}
	status = grokQuotaStatusFromSnapshot(stale, now)
	if status.State != harnesses.QuotaStale {
		t.Errorf("stale State = %q, want stale", status.State)
	}
	if status.RoutingPreference != harnesses.RoutingPreferenceBlocked {
		t.Errorf("stale RoutingPreference = %v, want blocked", status.RoutingPreference)
	}

	blocked := &grokQuotaSnapshot{
		CapturedAt: now,
		Windows:    testGrokWindows(97, "blocked"),
		Account:    subscribedAccount(),
	}
	status = grokQuotaStatusFromSnapshot(blocked, now)
	if status.State != harnesses.QuotaBlocked {
		t.Errorf("blocked State = %q, want blocked", status.State)
	}
}

func TestGrokLimitIDFallback(t *testing.T) {
	if got := grokLimitIDForWindowMinutes(10080); got != "grok-weekly" {
		t.Errorf("weekly limit id = %q", got)
	}
	if got := grokLimitIDForWindowMinutes(300); got != "grok" {
		t.Errorf("short-window limit id = %q", got)
	}
}
