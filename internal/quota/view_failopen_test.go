package quota

import (
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routing"
)

func TestMaxQuotaWindowUsedPercent(t *testing.T) {
	windows := []harnesses.QuotaWindow{
		{Name: "5h", UsedPercent: 10},
		{Name: "weekly", UsedPercent: 85},
		{Name: "model", UsedPercent: 42},
	}
	wantWindows := append([]harnesses.QuotaWindow(nil), windows...)

	if got := MaxQuotaWindowUsedPercent(windows); got != 85 {
		t.Fatalf("MaxQuotaWindowUsedPercent() = %.1f, want 85", got)
	}
	if !reflect.DeepEqual(windows, wantWindows) {
		t.Fatalf("MaxQuotaWindowUsedPercent mutated input:\n got: %#v\nwant: %#v", windows, wantWindows)
	}
	if got := MaxQuotaWindowUsedPercent(nil); got != 0 {
		t.Fatalf("MaxQuotaWindowUsedPercent(nil) = %.1f, want 0", got)
	}
}

func TestTrendFromUsage(t *testing.T) {
	tests := []struct {
		name        string
		percentUsed int
		fresh       bool
		want        string
	}{
		{name: "stale low usage is unknown", percentUsed: 0, fresh: false, want: routing.QuotaTrendUnknown},
		{name: "fresh below burning is healthy", percentUsed: 69, fresh: true, want: routing.QuotaTrendHealthy},
		{name: "burning lower boundary", percentUsed: 70, fresh: true, want: routing.QuotaTrendBurning},
		{name: "burning upper boundary", percentUsed: 89, fresh: true, want: routing.QuotaTrendBurning},
		{name: "exhausting boundary", percentUsed: 90, fresh: true, want: routing.QuotaTrendExhausting},
		{name: "high stale usage retains observed trend", percentUsed: 95, fresh: false, want: routing.QuotaTrendExhausting},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrendFromUsage(tt.percentUsed, tt.fresh); got != tt.want {
				t.Fatalf("TrendFromUsage(%d, %v) = %q, want %q", tt.percentUsed, tt.fresh, got, tt.want)
			}
		})
	}
}

// TestViewFromStatus_TopLevelExhaustionRequiresFreshEvidence pins the
// FEAT-004 fail-open contract: Exhausted, the single fail-closed signal
// feeding SubscriptionOK, requires fresh positive proof. Unconfirmed evidence
// must leave the harness eligible; parser or probe failures cannot fabricate
// "no viable provider".
func TestViewFromStatus_TopLevelExhaustionRequiresFreshEvidence(t *testing.T) {
	cases := []struct {
		name          string
		status        harnesses.QuotaStatus
		wantExhausted bool
		wantPresent   bool
	}{
		{
			name:          "fresh blocked is proven",
			status:        harnesses.QuotaStatus{Fresh: true, State: harnesses.QuotaBlocked, RoutingPreference: harnesses.RoutingPreferenceBlocked},
			wantExhausted: true,
			wantPresent:   true,
		},
		{
			name:          "stale blocked fails open",
			status:        harnesses.QuotaStatus{Fresh: false, State: harnesses.QuotaBlocked, RoutingPreference: harnesses.RoutingPreferenceBlocked},
			wantExhausted: false,
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
			status:        harnesses.QuotaStatus{Fresh: true, State: harnesses.QuotaUnauthenticated, RoutingPreference: harnesses.RoutingPreferenceUnknown},
			wantExhausted: false,
			wantPresent:   true,
		},
		{
			name:          "incomplete zero-value state fails open",
			status:        harnesses.QuotaStatus{Fresh: true, RoutingPreference: harnesses.RoutingPreferenceBlocked},
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

func TestViewFromStatus_GeminiAllWindowsBlockedRequiresFreshEvidence(t *testing.T) {
	tests := []struct {
		name      string
		fresh     bool
		state     harnesses.QuotaStateValue
		windows   []harnesses.QuotaWindow
		exhausted bool
	}{
		{
			name:      "fresh all blocked is proven",
			fresh:     true,
			state:     harnesses.QuotaBlocked,
			windows:   []harnesses.QuotaWindow{{State: "blocked"}, {State: "blocked"}},
			exhausted: true,
		},
		{
			name:      "fresh all exhausted is proven",
			fresh:     true,
			state:     harnesses.QuotaBlocked,
			windows:   []harnesses.QuotaWindow{{State: "exhausted"}, {State: "exhausted"}},
			exhausted: true,
		},
		{
			name:    "stale all blocked fails open",
			fresh:   false,
			state:   harnesses.QuotaBlocked,
			windows: []harnesses.QuotaWindow{{State: "blocked"}, {State: "blocked"}},
		},
		{
			name:    "fresh top-level blocked with mixed windows fails open",
			fresh:   true,
			state:   harnesses.QuotaBlocked,
			windows: []harnesses.QuotaWindow{{State: "blocked"}, {State: "ok"}},
		},
		{
			name:    "fresh top-level blocked with unknown window fails open",
			fresh:   true,
			state:   harnesses.QuotaBlocked,
			windows: []harnesses.QuotaWindow{{State: "blocked"}, {State: "unknown"}},
		},
		{
			name:    "only unknown windows fail open",
			fresh:   true,
			state:   harnesses.QuotaBlocked,
			windows: []harnesses.QuotaWindow{{State: "unknown"}, {State: "unknown"}},
		},
		{
			name:    "fresh top-level blocked with no windows fails open",
			fresh:   true,
			state:   harnesses.QuotaBlocked,
			windows: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := harnesses.QuotaStatus{
				Fresh: tt.fresh,
				// Gemini's per-window evidence is authoritative for this
				// projection. Even a top-level blocked signal is incomplete
				// unless every reported window proves exhaustion.
				State:             tt.state,
				RoutingPreference: harnesses.RoutingPreferenceBlocked,
				Windows:           tt.windows,
			}
			view := viewFromStatus("gemini", status)
			if view.Exhausted != tt.exhausted {
				t.Fatalf("Exhausted = %v, want %v", view.Exhausted, tt.exhausted)
			}
			if tt.exhausted && view.Trend != routing.QuotaTrendExhausting {
				t.Fatalf("Trend = %q, want %q", view.Trend, routing.QuotaTrendExhausting)
			}
		})
	}
}

func TestViewFromStatus_WindowsAreCopied(t *testing.T) {
	windows := []harnesses.QuotaWindow{
		{Name: "5h", State: "ok", UsedPercent: 25},
		{Name: "weekly", State: "blocked", UsedPercent: 100},
	}
	wantInput := append([]harnesses.QuotaWindow(nil), windows...)

	view := viewFromStatus("codex", harnesses.QuotaStatus{
		Fresh:   true,
		State:   harnesses.QuotaOK,
		Windows: windows,
	})
	if !reflect.DeepEqual(windows, wantInput) {
		t.Fatalf("viewFromStatus mutated input:\n got: %#v\nwant: %#v", windows, wantInput)
	}
	if len(view.Windows) != len(windows) {
		t.Fatalf("returned windows length = %d, want %d", len(view.Windows), len(windows))
	}

	view.Windows[0].Name = "changed-return"
	if windows[0].Name != "5h" {
		t.Fatalf("returned windows alias input: input name = %q", windows[0].Name)
	}
	windows[1].Name = "changed-input"
	if view.Windows[1].Name != "weekly" {
		t.Fatalf("input windows alias return: returned name = %q", view.Windows[1].Name)
	}
}
