package fizeau_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

type publicQuotaStateStoreContract interface {
	MarkQuotaExhausted(string, time.Time)
	MarkAvailable(string)
	State(string, time.Time) (fizeau.ProviderQuotaState, time.Time)
	AllExhausted() map[string]time.Time
	ExhaustedAt(time.Time) map[string]time.Time
}

type publicBurnRateTrackerContract interface {
	SetBudget(string, int)
	Budget(string) int
	Used(string, time.Time) int
	Record(string, int, time.Time) (bool, time.Time)
	Reset()
}

var (
	_ fizeau.QuotaRecoveryProber    = func(context.Context, string) error { return nil }
	_ publicQuotaStateStoreContract = fizeau.NewProviderQuotaStateStore()
	_ publicBurnRateTrackerContract = fizeau.NewProviderBurnRateTracker()
)

func TestPublicServiceAPISmoke(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(fakeHome, ".config"))

	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ fizeau.FizeauService = svc

	if _, err := svc.ListHarnesses(context.Background()); err != nil {
		t.Fatalf("ListHarnesses: %v", err)
	}

	req := fizeau.ServiceExecuteRequest{
		Prompt:  "ping",
		Harness: "virtual",
		Model:   "test-model",
	}
	if req.Prompt == "" || req.Harness == "" || req.Model == "" {
		t.Fatalf("unexpected request shape: %+v", req)
	}

	tokPerSec := 42.5
	progress, err := json.Marshal(fizeau.ServiceProgressData{
		Phase:     "llm",
		State:     "complete",
		Source:    "native",
		Message:   "complete",
		TokPerSec: &tokPerSec,
	})
	if err != nil {
		t.Fatalf("marshal progress: %v", err)
	}
	event := fizeau.ServiceEvent{
		Type:     fizeau.ServiceEventTypeProgress,
		Sequence: 1,
		Time:     time.Date(2026, 5, 5, 14, 0, 0, 0, time.UTC),
		Metadata: map[string]string{"session_id": "svc-test"},
		Data:     progress,
	}
	if event.Type != fizeau.ServiceEventTypeProgress || len(event.Data) == 0 {
		t.Fatalf("unexpected event shape: %+v", event)
	}

	if err := svc.RecordRouteAttempt(context.Background(), fizeau.RouteAttempt{
		Harness:  "fiz",
		Provider: "public-provider",
		Model:    "public-model",
		Status:   "success",
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}
	if _, err := svc.RouteStatus(context.Background()); err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if err := fizeau.ValidateUsageSince("today"); err != nil {
		t.Fatalf("ValidateUsageSince: %v", err)
	}

	quotaStore := fizeau.NewProviderQuotaStateStore()
	quotaStore.MarkQuotaExhausted("public-provider", time.Now().Add(time.Minute))
	if state, _ := quotaStore.State("public-provider", time.Now()); state != fizeau.ProviderQuotaStateQuotaExhausted {
		t.Fatalf("quota state = %q, want quota_exhausted", state)
	}
	burnRate := fizeau.NewProviderBurnRateTracker()
	burnRate.SetBudget("public-provider", 100)
	if got := burnRate.Budget("public-provider"); got != 100 {
		t.Fatalf("burn-rate budget = %d, want 100", got)
	}

	metrics := fizeau.RoutingQualityMetrics{
		AutoAcceptanceRate:       1,
		OverrideDisagreementRate: 0,
		OverrideClassBreakdown: []fizeau.OverrideClassBucket{{
			PromptFeatureBucket: "tokens=tiny,tools=no,reasoning=none",
			Axis:                "model",
			Match:               true,
			Count:               1,
			SuccessOutcomes:     1,
		}},
		TotalRequests:  1,
		TotalOverrides: 1,
	}
	if metrics.OverrideClassBreakdown[0].Axis != "model" {
		t.Fatalf("unexpected routing-quality metric shape: %+v", metrics)
	}
}
