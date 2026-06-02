package quota

import (
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// TestViewFromStatus_FailOpenOnUnconfirmedQuota pins the FEAT-004 fail-open
// contract: a subscription harness is marked Exhausted (the single fail-closed
// signal feeding SubscriptionOK) ONLY on proven QuotaBlocked. Every
// unconfirmed-state snapshot — unavailable (probe failed), unknown, stale —
// must leave Exhausted=false so routing keeps the harness eligible (a TUI/CLI
// drift that breaks /status parsing can never fabricate "no viable provider").
func TestViewFromStatus_FailOpenOnUnconfirmedQuota(t *testing.T) {
	cases := []struct {
		name          string
		status        harnesses.QuotaStatus
		wantExhausted bool
		wantPresent   bool
	}{
		{
			name:          "proven blocked is the only fail-closed state",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaBlocked, RoutingPreference: harnesses.RoutingPreferenceBlocked},
			wantExhausted: true,
			wantPresent:   true,
		},
		{
			name:          "unavailable (probe failed) fails open",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaUnavailable, RoutingPreference: harnesses.RoutingPreferenceUnknown},
			wantExhausted: false,
			wantPresent:   false,
		},
		{
			name:          "unknown fails open",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaUnknown, RoutingPreference: harnesses.RoutingPreferenceUnknown},
			wantExhausted: false,
			wantPresent:   true,
		},
		{
			name:          "stale fails open",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaStale, RoutingPreference: harnesses.RoutingPreferenceUnknown},
			wantExhausted: false,
			wantPresent:   true,
		},
		{
			name:          "unauthenticated is not exhaustion",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaUnauthenticated, RoutingPreference: harnesses.RoutingPreferenceUnknown},
			wantExhausted: false,
			wantPresent:   true,
		},
		{
			name:          "healthy is available and not exhausted",
			status:        harnesses.QuotaStatus{State: harnesses.QuotaOK, RoutingPreference: harnesses.RoutingPreferenceAvailable, Fresh: true},
			wantExhausted: false,
			wantPresent:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viewFromStatus("codex", tc.status)
			if v.Exhausted != tc.wantExhausted {
				t.Errorf("Exhausted = %v, want %v (state=%q)", v.Exhausted, tc.wantExhausted, tc.status.State)
			}
			if v.Present != tc.wantPresent {
				t.Errorf("Present = %v, want %v", v.Present, tc.wantPresent)
			}
		})
	}
}

// TestViewFromStatus_GeminiAllWindowsBlockedIsExhausted pins that gemini's
// window-based exhaustion (all windows blocked, none ok) is treated as proven
// exhaustion, while a mix or unknown windows fail open.
func TestViewFromStatus_GeminiAllWindowsBlockedIsExhausted(t *testing.T) {
	allBlocked := harnesses.QuotaStatus{
		State:             harnesses.QuotaOK, // gemini reports per-window, not top-level State
		RoutingPreference: harnesses.RoutingPreferenceUnknown,
		Windows: []harnesses.QuotaWindow{
			{State: "blocked"},
			{State: "blocked"},
		},
	}
	if v := viewFromStatus("gemini", allBlocked); !v.Exhausted {
		t.Errorf("gemini all-windows-blocked: Exhausted = false, want true")
	}

	mixed := harnesses.QuotaStatus{
		State:             harnesses.QuotaOK,
		RoutingPreference: harnesses.RoutingPreferenceAvailable,
		Windows: []harnesses.QuotaWindow{
			{State: "blocked"},
			{State: "ok"},
		},
	}
	if v := viewFromStatus("gemini", mixed); v.Exhausted {
		t.Errorf("gemini mixed windows: Exhausted = true, want false (fail open)")
	}
}
