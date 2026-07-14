package fizeau

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestExecuteDispatchFailureRecordsExactRouteCooldownForNextRoute(t *testing.T) {
	svc := routeAttemptTestService(t, 30*time.Second)

	before, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen"})
	if err != nil {
		t.Fatalf("ResolveRoute before Execute: %v", err)
	}
	if before.Provider != "bragi" {
		t.Fatalf("before Execute Provider: got %q, want bragi", before.Provider)
	}
	beforeBragi := findCandidate(t, before, "fiz", "bragi")

	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:          "try once",
		Model:           "qwen",
		Timeout:         2 * time.Second,
		ProviderTimeout: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainRouteAttemptServiceEvents(t, ch, 5*time.Second)
	final := finalRouteAttemptServiceData(t, events)
	if final.Status != "failed" {
		t.Fatalf("final.Status = %q, want failed", final.Status)
	}
	if final.RoutingActual == nil {
		t.Fatal("final.RoutingActual is nil")
	}
	if final.RoutingActual.Provider != "bragi" || final.RoutingActual.Model != "qwen" {
		t.Fatalf("final.RoutingActual = %#v, want attempted bragi/qwen", final.RoutingActual)
	}
	if final.RoutingActual.FailureClass != "transport" {
		t.Fatalf("final.RoutingActual.FailureClass = %q, want transport", final.RoutingActual.FailureClass)
	}

	after, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen"})
	if err != nil {
		t.Fatalf("ResolveRoute after Execute: %v", err)
	}
	afterBragi := findCandidate(t, after, "fiz", "bragi")
	if !afterBragi.Eligible {
		t.Fatalf("bragi should remain eligible under an exact soft cooldown: %#v", afterBragi)
	}
	if afterBragi.Score >= beforeBragi.Score {
		t.Fatalf("bragi score after failure=%v, want below baseline %v", afterBragi.Score, beforeBragi.Score)
	}
	openrouter := findCandidate(t, after, "fiz", "openrouter")
	if !openrouter.Eligible {
		t.Fatalf("sibling openrouter route should remain available: %#v", openrouter)
	}

	in := svc.buildRoutingInputs(context.Background())
	svc.applyRouteAttemptCooldowns(&in)
	if _, ok := in.ProviderUnreachable["bragi"]; ok {
		t.Fatalf("ProviderUnreachable=%#v, harness-bearing failure must not hard-gate bragi", in.ProviderUnreachable)
	}
	foundExact := false
	for key := range in.ExactRouteCooldowns {
		if key.Harness == "fiz" && key.Provider == "bragi" && key.Model == "qwen" {
			foundExact = true
			break
		}
	}
	if !foundExact {
		t.Fatalf("ExactRouteCooldowns=%#v, want attempted fiz/bragi/qwen route", in.ExactRouteCooldowns)
	}
}

func TestRecordRouteAttempt_DemotesFailedProviderForAutomaticRouting(t *testing.T) {
	svc := routeAttemptTestService(t, 30*time.Second)

	before, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model: "qwen",
	})
	if err != nil {
		t.Fatalf("ResolveRoute before failure: %v", err)
	}
	if before.Provider != "bragi" {
		t.Fatalf("before failure Provider: got %q, want bragi", before.Provider)
	}

	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:  "fiz",
		Provider: "bragi",
		Model:    "qwen",
		Status:   "failed",
		Reason:   "timeout",
		Error:    "context deadline exceeded",
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	after, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model: "qwen",
	})
	if err != nil {
		t.Fatalf("ResolveRoute after failure: %v", err)
	}
	if after.Provider == "bragi" {
		t.Fatalf("after failure Provider: got bragi, want a non-cooldown provider")
	}

	pinned, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model:    "qwen",
		Provider: "bragi",
	})
	if err != nil {
		t.Fatalf("ResolveRoute with provider pin after failure: %v", err)
	}
	if pinned.Provider != "bragi" {
		t.Fatalf("provider pin after failure: got %q, want bragi", pinned.Provider)
	}
}

// TestRecordRouteAttempt_DialFailureDemotesExactRouteWithoutHardGate verifies
// that caller feedback carrying a harness stays scoped to that exact route.
// Discovery failures may still hard-gate a known-down provider, but one routed
// attempt is only a soft score demotion and cannot poison sibling routes.
func TestRecordRouteAttempt_DialFailureDemotesExactRouteWithoutHardGate(t *testing.T) {
	cases := []struct {
		name string
		err  string
	}{
		{"dial tcp timeout", `openai: Post "http://bragi:1234/v1/chat/completions": dial tcp 100.127.38.115:1234: i/o timeout`},
		{"connection refused", `dial tcp 192.168.2.106:8020: connection refused`},
		{"502 bad gateway", `POST "http://bragi:1234/v1/chat/completions": 502 Bad Gateway `},
		{"no route to host", `dial tcp: no route to host`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := routeAttemptTestService(t, 30*time.Second)

			before, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen"})
			if err != nil {
				t.Fatalf("ResolveRoute before failure: %v", err)
			}
			if before.Provider != "bragi" {
				t.Fatalf("baseline provider: got %q, want bragi", before.Provider)
			}
			beforeBragi := findCandidate(t, before, "fiz", "bragi")

			if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
				Harness:  "fiz",
				Provider: "bragi",
				Model:    "qwen",
				Status:   "failed",
				Error:    c.err,
			}); err != nil {
				t.Fatalf("RecordRouteAttempt: %v", err)
			}

			// A harness-bearing dial-class failure demotes only the exact
			// candidate. It remains eligible, while the sibling route can win.
			after, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen"})
			if err != nil {
				t.Fatalf("ResolveRoute after failure: %v", err)
			}
			if after.Provider == "bragi" {
				t.Fatalf("after dial failure provider: got bragi, want soft demotion to select the available sibling")
			}
			bragiCand := findCandidate(t, after, "fiz", "bragi")
			if !bragiCand.Eligible {
				t.Errorf("bragi should remain eligible after exact-route dial failure: %#v", bragiCand)
			}
			if bragiCand.FilterReason != "" {
				t.Errorf("bragi.FilterReason = %q, want no hard-gate reason", bragiCand.FilterReason)
			}
			if bragiCand.Score >= beforeBragi.Score {
				t.Errorf("bragi score after failure=%v, want below baseline %v", bragiCand.Score, beforeBragi.Score)
			}
			openrouter := findCandidate(t, after, "fiz", "openrouter")
			if !openrouter.Eligible {
				t.Errorf("sibling openrouter route should remain eligible: %#v", openrouter)
			}

			in := svc.buildRoutingInputs(context.Background())
			svc.applyRouteAttemptCooldowns(&in)
			key := routing.RouteCooldownKey{Harness: "fiz", Provider: "bragi", Model: "qwen"}
			if _, ok := in.ExactRouteCooldowns[key]; !ok {
				t.Errorf("ExactRouteCooldowns=%#v, want %#v", in.ExactRouteCooldowns, key)
			}
			if _, ok := in.ProviderCooldowns["bragi"]; ok {
				t.Errorf("ProviderCooldowns=%#v, harness-bearing route must not populate provider-wide cooldown", in.ProviderCooldowns)
			}
			if _, ok := in.ProviderUnreachable["bragi"]; ok {
				t.Errorf("ProviderUnreachable=%#v, harness-bearing route must not populate provider-wide hard gate", in.ProviderUnreachable)
			}

			// Explicit provider pin still selects bragi because cooldown is soft.
			pinned, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: "qwen", Provider: "bragi"})
			if err != nil {
				t.Fatalf("ResolveRoute with provider pin: %v", err)
			}
			if pinned.Provider != "bragi" {
				t.Fatalf("explicit pin after dial failure: got %q, want bragi", pinned.Provider)
			}
		})
	}
}

func TestRecordRouteAttempt_SuccessClearsFailure(t *testing.T) {
	svc := routeAttemptTestService(t, 30*time.Second)
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:  "fiz",
		Provider: "bragi",
		Model:    "qwen",
		Status:   "failed",
		Error:    "502 bad gateway",
	}); err != nil {
		t.Fatalf("RecordRouteAttempt failed: %v", err)
	}
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:  "fiz",
		Provider: "bragi",
		Model:    "qwen",
		Status:   "success",
	}); err != nil {
		t.Fatalf("RecordRouteAttempt success: %v", err)
	}

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model:    "qwen",
		Provider: "bragi",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec.Provider != "bragi" {
		t.Fatalf("Provider after success clear: got %q, want bragi", dec.Provider)
	}
}

func TestRouteAttemptPreservesServerInstance(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	now := time.Date(2026, 7, 14, 15, 30, 0, 0, time.UTC)

	for _, serverInstance := range []string{"desk-a", "desk-b"} {
		if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
			Harness:        " fiz ",
			Provider:       " local ",
			Endpoint:       " primary ",
			ServerInstance: " " + serverInstance + " ",
			Model:          " qwen ",
			Status:         "failed",
			Timestamp:      now,
		}); err != nil {
			t.Fatalf("RecordRouteAttempt(%s): %v", serverInstance, err)
		}
	}

	records := svc.activeRouteAttempts(now, time.Minute)
	if len(records) != 2 {
		t.Fatalf("activeRouteAttempts len = %d, want 2 exact server routes: %+v", len(records), records)
	}
	byServer := make(map[string]routehealth.Key, len(records))
	for _, record := range records {
		byServer[record.Key.ServerInstance] = record.Key
	}
	for _, serverInstance := range []string{"desk-a", "desk-b"} {
		key, ok := byServer[serverInstance]
		if !ok {
			t.Fatalf("active route for server instance %q missing: %+v", serverInstance, records)
		}
		if key.Harness != "fiz" || key.Provider != "local" || key.Endpoint != "primary" || key.Model != "qwen" {
			t.Fatalf("key for %s = %+v, want exact normalized fiz/local/primary/%s/qwen route", serverInstance, key, serverInstance)
		}
	}

	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "qwen",
		Status:         "success",
		Timestamp:      now.Add(time.Second),
	}); err != nil {
		t.Fatalf("RecordRouteAttempt(success): %v", err)
	}
	records = svc.activeRouteAttempts(now.Add(time.Second), time.Minute)
	if len(records) != 1 || records[0].Key.ServerInstance != "desk-b" {
		t.Fatalf("success should clear only desk-a; active routes = %+v", records)
	}

	attempt, ok := routeAttemptFromFinal(harnesses.FinalData{
		Status: "success",
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "fiz",
			Provider:       "local@primary",
			ServerInstance: " desk-c ",
			Model:          "qwen",
		},
	})
	if !ok || attempt.ServerInstance != "desk-c" {
		t.Fatalf("routeAttemptFromFinal ServerInstance = %q, ok=%v; want desk-c, true", attempt.ServerInstance, ok)
	}
}

func TestRecordRouteAttemptReturnsPersistenceFailureAfterMemoryUpdate(t *testing.T) {
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("block snapshot directory"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	svc := newTestService(t, ServiceOptions{
		PersistRouteHealth: filepath.Join(blockingFile, "routehealth.json"),
	})
	now := time.Date(2026, 7, 14, 15, 30, 0, 0, time.UTC)
	err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "qwen",
		Status:         "failed",
		Timestamp:      now,
	})
	if err == nil {
		t.Fatal("RecordRouteAttempt error = nil, want snapshot persistence failure")
	}
	if !strings.Contains(err.Error(), "route health snapshot") {
		t.Fatalf("RecordRouteAttempt error = %q, want route health snapshot context", err)
	}

	records := svc.activeRouteAttempts(now, time.Minute)
	if len(records) != 1 {
		t.Fatalf("activeRouteAttempts len = %d, want retained in-memory attempt: %+v", len(records), records)
	}
	want := routehealth.Key{
		Harness:        "fiz",
		Provider:       "local",
		Endpoint:       "primary",
		ServerInstance: "desk-a",
		Model:          "qwen",
	}
	if records[0].Key != want {
		t.Fatalf("retained route key = %+v, want %+v", records[0].Key, want)
	}
}

func TestRecordRouteAttempt_TTLExpiryRemovesDemotion(t *testing.T) {
	svc := routeAttemptTestService(t, 10*time.Millisecond)
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:   "fiz",
		Provider:  "bragi",
		Model:     "qwen",
		Status:    "failed",
		Timestamp: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Model:    "qwen",
		Provider: "bragi",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec.Provider != "bragi" {
		t.Fatalf("Provider after TTL expiry: got %q, want bragi", dec.Provider)
	}
}

func TestRecordRouteAttempt_FromFinalSplitsEndpointProviderRef(t *testing.T) {
	attempt, ok := routeAttemptFromFinal(harnesses.FinalData{
		Status:     "failed",
		Error:      "502 bad gateway",
		DurationMS: 125,
		RoutingActual: &harnesses.RoutingActual{
			Harness:      "fiz",
			Provider:     "bragi@rack-a",
			Model:        "qwen",
			FailureClass: "protocol",
		},
	})
	if !ok {
		t.Fatal("routeAttemptFromFinal should record endpoint-qualified dispatch failures")
	}
	if attempt.Provider != "bragi" || attempt.Endpoint != "rack-a" {
		t.Fatalf("attempt provider split = %q/%q, want bragi/rack-a", attempt.Provider, attempt.Endpoint)
	}
	if attempt.Reason != "protocol" {
		t.Fatalf("attempt.Reason = %q, want protocol", attempt.Reason)
	}
	if attempt.Duration != 125*time.Millisecond {
		t.Fatalf("attempt.Duration = %s, want 125ms", attempt.Duration)
	}
}

func TestRecordRouteAttempt_FromFinalIgnoresSemanticFailures(t *testing.T) {
	if _, ok := routeAttemptFromFinal(harnesses.FinalData{
		Status: "failed",
		Error:  "validator rejected malformed tool payload",
		RoutingActual: &harnesses.RoutingActual{
			Harness:      "fiz",
			Provider:     "bragi",
			Model:        "qwen",
			FailureClass: "unknown",
		},
	}); ok {
		t.Fatal("routeAttemptFromFinal should ignore non-dispatch failures")
	}
}

func TestRouteHealth_PersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routehealth.json")
	now := time.Now().UTC()
	recordedAt := now.Add(-5 * time.Second)

	svc1 := newTestService(t, ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err := svc1.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:   "fiz",
		Provider:  "bragi",
		Model:     "qwen",
		Status:    "failed",
		Reason:    "timeout",
		Error:     "dial tcp: i/o timeout",
		Timestamp: recordedAt,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	rawSvc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		HealthSignalTTL:     10 * time.Minute,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc2 := rawSvc.(*service)

	records := svc2.activeRouteAttempts(now, time.Minute)
	if len(records) != 1 {
		t.Fatalf("activeRouteAttempts len = %d, want 1", len(records))
	}
	if records[0].Key.Provider != "bragi" {
		t.Fatalf("provider = %q, want bragi", records[0].Key.Provider)
	}
	if !records[0].RecordedAt.Equal(recordedAt) {
		t.Fatalf("RecordedAt = %v, want %v", records[0].RecordedAt, recordedAt)
	}
}

func TestRouteHealth_TTLExpiresStaleSignals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routehealth.json")
	now := time.Now().UTC()

	svc1 := newTestService(t, ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err := svc1.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:   "fiz",
		Provider:  "bragi",
		Model:     "qwen",
		Status:    "failed",
		Timestamp: now.Add(-11 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	rawSvc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		HealthSignalTTL:     10 * time.Minute,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc2 := rawSvc.(*service)

	if got := svc2.activeRouteAttempts(now, time.Hour); len(got) != 0 {
		t.Fatalf("activeRouteAttempts = %d, want 0 after persisted TTL expiry", len(got))
	}
}

func TestRouteHealth_MissingFileBootsClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-routehealth.json")
	var logs bytes.Buffer

	rawSvc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		Logger:              &logs,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := rawSvc.(*service)

	if got := svc.activeRouteAttempts(time.Now().UTC(), time.Hour); len(got) != 0 {
		t.Fatalf("activeRouteAttempts = %d, want empty store", len(got))
	}
	assertSingleWarningLine(t, logs.String(), path)
}

func TestRouteHealth_CorruptFileBootsClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routehealth.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}
	var logs bytes.Buffer

	rawSvc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		PersistRouteHealth:  path,
		Logger:              &logs,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := rawSvc.(*service)

	if got := svc.activeRouteAttempts(time.Now().UTC(), time.Hour); len(got) != 0 {
		t.Fatalf("activeRouteAttempts = %d, want empty store", len(got))
	}
	assertSingleWarningLine(t, logs.String(), path)
}

func TestRouteStatus_RouteAttemptCooldownSurfaces(t *testing.T) {
	svc := routeAttemptTestService(t, 30*time.Second)
	recordedAt := time.Now().Add(-time.Second).UTC()
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:   "fiz",
		Provider:  "bragi",
		Model:     "qwen",
		Status:    "failed",
		Reason:    "rate_limit",
		Error:     "429 too many requests",
		Timestamp: recordedAt,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 {
		t.Fatalf("Routes: got %d, want 1", len(report.Routes))
	}
	byProvider := make(map[string]RouteCandidateStatus)
	for _, cand := range report.Routes[0].Candidates {
		byProvider[cand.Provider] = cand
	}
	bragi := byProvider["bragi"]
	if bragi.Healthy {
		t.Fatal("bragi should be unhealthy while route-attempt cooldown is active")
	}
	if bragi.Cooldown == nil {
		t.Fatal("bragi cooldown should be populated")
	}
	if bragi.Cooldown.Reason != "rate_limit" {
		t.Fatalf("Cooldown.Reason: got %q, want rate_limit", bragi.Cooldown.Reason)
	}
	if bragi.Cooldown.LastError != "429 too many requests" {
		t.Fatalf("Cooldown.LastError: got %q", bragi.Cooldown.LastError)
	}
	if !bragi.Cooldown.LastAttempt.Equal(recordedAt) {
		t.Fatalf("Cooldown.LastAttempt: got %s, want %s", bragi.Cooldown.LastAttempt, recordedAt)
	}
	if !byProvider["openrouter"].Healthy {
		t.Fatal("openrouter should remain healthy")
	}
}

func TestRouteAttempts_ProviderModelKeying(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	ctx := context.Background()

	keyX := routing.ProviderModelKey("providerA", "", "modelX")
	keyY := routing.ProviderModelKey("providerA", "", "modelY")

	for i := 0; i < 3; i++ {
		if err := svc.RecordRouteAttempt(ctx, RouteAttempt{
			Harness:  "fiz",
			Provider: "providerA",
			Model:    "modelX",
			Status:   "failed",
			Error:    "boom",
			Duration: 100 * time.Millisecond,
		}); err != nil {
			t.Fatalf("RecordRouteAttempt failure %d on modelX: %v", i, err)
		}
	}

	successRate, latencyMS := svc.routeMetricSignals(time.Now(), 30*time.Second)
	if got, want := successRate[keyX], 0.0; got != want {
		t.Fatalf("after 3 failures on modelX: successRate[%s]=%v, want %v", keyX, got, want)
	}
	if _, ok := successRate[keyY]; ok {
		t.Fatalf("modelY signal should be untouched by modelX failures, got successRate[%s]=%v", keyY, successRate[keyY])
	}
	if _, ok := latencyMS[keyY]; ok {
		t.Fatalf("modelY latency should be untouched by modelX failures, got latencyMS[%s]=%v", keyY, latencyMS[keyY])
	}

	if err := svc.RecordRouteAttempt(ctx, RouteAttempt{
		Harness:  "fiz",
		Provider: "providerA",
		Model:    "modelY",
		Status:   "success",
		Duration: 50 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt success on modelY: %v", err)
	}

	successRate, _ = svc.routeMetricSignals(time.Now(), 30*time.Second)
	if got, want := successRate[keyX], 0.0; got != want {
		t.Fatalf("after success on modelY: successRate[%s]=%v, want %v (cross-pollution)", keyX, got, want)
	}
	if got, want := successRate[keyY], 1.0; got != want {
		t.Fatalf("after success on modelY: successRate[%s]=%v, want %v", keyY, got, want)
	}
}

func TestResolveRoute_CodexUsesDurableQuotaCache(t *testing.T) {
	dir := t.TempDir()
	codexQuotaPath := filepath.Join(dir, "codex-quota.json")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", codexQuotaPath)
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))
	writeCodexQuotaCacheFile(t, codexQuotaPath, time.Now().UTC(), "pty",
		&harnesses.AccountInfo{PlanType: "ChatGPT Pro"},
		[]harnesses.QuotaWindow{
			{Name: "5h", WindowMinutes: 300, UsedPercent: 25, State: "ok"},
		},
	)

	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) {
		return filepath.Join(dir, file), nil
	}
	svc := &service{opts: ServiceOptions{}, registry: registry}
	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Harness: "codex", Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec.Harness != "codex" || dec.Model != "gpt-5.5" {
		t.Fatalf("ResolveRoute: got harness=%q model=%q, want codex gpt-5.5", dec.Harness, dec.Model)
	}
}

func TestBuildRoutingInputs_CodexQuotaStaleIsEligibleBlockedIsIneligible(t *testing.T) {
	dir := t.TempDir()
	codexQuotaPath := filepath.Join(dir, "codex-quota.json")
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", codexQuotaPath)
	registry := harnesses.NewRegistry()
	svc := &service{opts: ServiceOptions{}, registry: registry}

	writeCodexQuotaCacheFile(t, codexQuotaPath, time.Now().UTC().Add(-20*time.Minute), "pty",
		nil,
		[]harnesses.QuotaWindow{{Name: "5h", UsedPercent: 25, State: "ok"}},
	)
	codex := routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "codex")
	if !codex.SubscriptionOK || !codex.QuotaStale {
		t.Fatalf("stale codex quota should fail open: SubscriptionOK=%v QuotaStale=%v", codex.SubscriptionOK, codex.QuotaStale)
	}

	writeCodexQuotaCacheFile(t, codexQuotaPath, time.Now().UTC(), "pty",
		nil,
		[]harnesses.QuotaWindow{{Name: "5h", UsedPercent: 96, State: "blocked"}},
	)
	codex = routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "codex")
	if codex.SubscriptionOK || codex.QuotaTrend != "exhausting" {
		t.Fatalf("blocked codex quota: SubscriptionOK=%v QuotaTrend=%q", codex.SubscriptionOK, codex.QuotaTrend)
	}
}

func TestBuildRoutingInputs_GeminiQuotaGatesAutoRouting(t *testing.T) {
	dir := t.TempDir()
	quotaPath := filepath.Join(dir, "gemini-quota.json")
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", quotaPath)
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(dir, "missing-codex-quota.json"))
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(dir, "missing-claude-quota.json"))

	registry := harnesses.NewRegistry()
	svc := &service{opts: ServiceOptions{}, registry: registry}

	// Missing cache is unknown, not proven exhausted: keep the harness
	// subscription-eligible but score it down with QuotaOK=false.
	t.Setenv("GOOGLE_API_KEY", "test")
	gemini := routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "gemini")
	if !gemini.SubscriptionOK || gemini.QuotaOK {
		t.Fatalf("missing gemini quota cache should fail open with QuotaOK=false: %+v", gemini)
	}

	// Stale snapshot is also unknown, not proven exhausted.
	writeGeminiTestQuota(t, quotaPath, geminiTestQuotaSnapshot{
		CapturedAt: time.Now().UTC().Add(-1 * time.Hour),
		Source:     "pty",
		Windows: []harnesses.QuotaWindow{
			{Name: "Flash", LimitID: "gemini-flash", UsedPercent: 4, State: "ok"},
		},
	})
	gemini = routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "gemini")
	if !gemini.SubscriptionOK || !gemini.QuotaStale {
		t.Fatalf("stale gemini quota should fail open: SubscriptionOK=%v QuotaStale=%v", gemini.SubscriptionOK, gemini.QuotaStale)
	}

	// Fresh but all tiers exhausted: routing must still mark ineligible.
	writeGeminiTestQuota(t, quotaPath, geminiTestQuotaSnapshot{
		CapturedAt: time.Now().UTC(),
		Source:     "pty",
		Windows: []harnesses.QuotaWindow{
			{Name: "Flash", LimitID: "gemini-flash", UsedPercent: 100, State: "exhausted"},
			{Name: "Pro", LimitID: "gemini-pro", UsedPercent: 100, State: "exhausted"},
		},
	})
	gemini = routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "gemini")
	if gemini.SubscriptionOK {
		t.Fatalf("all tiers exhausted must block gemini auto-routing: %+v", gemini)
	}
	if gemini.QuotaTrend != routing.QuotaTrendExhausting {
		t.Fatalf("all-exhausted snapshot should report exhausting trend, got %q", gemini.QuotaTrend)
	}

	// Fresh with at least one non-exhausted tier: routing marks eligible.
	writeGeminiTestQuota(t, quotaPath, geminiTestQuotaSnapshot{
		CapturedAt: time.Now().UTC(),
		Source:     "pty",
		Windows: []harnesses.QuotaWindow{
			{Name: "Flash", LimitID: "gemini-flash", UsedPercent: 4, State: "ok"},
			{Name: "Pro", LimitID: "gemini-pro", UsedPercent: 100, State: "exhausted"},
		},
	})
	gemini = routingHarnessEntry(t, svc.buildRoutingInputs(context.Background()).Harnesses, "gemini")
	if !gemini.SubscriptionOK || !gemini.QuotaOK {
		t.Fatalf("fresh gemini quota with headroom should mark gemini SubscriptionOK/QuotaOK: %+v", gemini)
	}
}

func TestBuildRoutingInputs_SecondaryHarnesses(t *testing.T) {
	registry := harnesses.NewRegistry()
	svc := &service{opts: ServiceOptions{}, registry: registry}
	inputs := svc.buildRoutingInputs(context.Background())

	opencode := routingHarnessEntry(t, inputs.Harnesses, "opencode")
	if opencode.AutoRoutingEligible || opencode.DefaultModel != "opencode/gpt-5.4" {
		t.Fatalf("opencode routing metadata: AutoRoutingEligible=%v DefaultModel=%q", opencode.AutoRoutingEligible, opencode.DefaultModel)
	}
	if !containsRouteString(opencode.SupportedModels, "opencode/gpt-5.4") {
		t.Fatalf("opencode supported models missing default: %v", opencode.SupportedModels)
	}
	if !containsRouteString(opencode.SupportedReasoning, "max") {
		t.Fatalf("opencode reasoning metadata missing max: %v", opencode.SupportedReasoning)
	}

	pi := routingHarnessEntry(t, inputs.Harnesses, "pi")
	if pi.AutoRoutingEligible || pi.DefaultModel != "gemini-2.5-flash" {
		t.Fatalf("pi routing metadata: AutoRoutingEligible=%v DefaultModel=%q", pi.AutoRoutingEligible, pi.DefaultModel)
	}
	if !containsRouteString(pi.SupportedReasoning, "xhigh") {
		t.Fatalf("pi reasoning metadata missing xhigh: %v", pi.SupportedReasoning)
	}

	gemini := routingHarnessEntry(t, inputs.Harnesses, "gemini")
	if gemini.AutoRoutingEligible || gemini.DefaultModel != "gemini-2.5-flash" {
		t.Fatalf("gemini routing metadata: AutoRoutingEligible=%v DefaultModel=%q", gemini.AutoRoutingEligible, gemini.DefaultModel)
	}
	if !containsRouteString(gemini.SupportedModels, "gemini-2.5-pro") || !containsRouteString(gemini.SupportedModels, "gemini-2.5-flash-lite") {
		t.Fatalf("gemini supported models not populated from registry: %v", gemini.SupportedModels)
	}
	if len(gemini.SupportedReasoning) != 0 {
		t.Fatalf("gemini should not advertise reasoning controls: %v", gemini.SupportedReasoning)
	}
}

func TestResolveRoute_GeminiCatalogModelsResolveByConcreteModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "redacted")
	dir := t.TempDir()
	quotaPath := filepath.Join(dir, "gemini-quota.json")
	t.Setenv("FIZEAU_GEMINI_QUOTA_CACHE", quotaPath)
	writeGeminiTestQuota(t, quotaPath, geminiTestQuotaSnapshot{
		CapturedAt: time.Now().UTC(),
		Source:     "pty",
		Windows: []harnesses.QuotaWindow{
			{Name: "Flash", LimitID: "gemini-flash", UsedPercent: 4, State: "ok"},
			{Name: "Flash Lite", LimitID: "gemini-flash-lite", UsedPercent: 0, State: "ok"},
			{Name: "Pro", LimitID: "gemini-pro", UsedPercent: 10, State: "ok"},
		},
	})
	registry := harnesses.NewRegistry()
	registry.LookPath = func(file string) (string, error) {
		if file == "gemini" {
			return "/usr/bin/gemini", nil
		}
		return "", os.ErrNotExist
	}
	svc := &service{opts: ServiceOptions{}, registry: registry}

	for policy, model := range map[string]string{
		"smart":   "gemini-2.5-pro",
		"default": "gemini-2.5-flash",
		"cheap":   "gemini-2.5-flash-lite",
	} {
		dec, err := svc.ResolveRoute(context.Background(), RouteRequest{Harness: "gemini", Policy: policy, Model: model})
		if err != nil {
			t.Fatalf("ResolveRoute policy=%s: %v", policy, err)
		}
		if dec.Harness != "gemini" || dec.Model != model {
			t.Fatalf("policy=%s: got harness=%q model=%q, want gemini/%s", policy, dec.Harness, dec.Model, model)
		}
	}
}

func routeAttemptTestService(t *testing.T, cooldown time.Duration) *service {
	t.Helper()
	cacheDir := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	t.Setenv("PATH", "")
	cache := &discoverycache.Cache{Root: cacheDir}
	capturedAt := time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("bragi", "bragi", "http://127.0.0.1:9999/v1", ""), capturedAt, []string{"qwen"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("openrouter", "openrouter", "https://openrouter.invalid/v1", ""), capturedAt, []string{"qwen"})
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi":      {Type: "lmstudio", BaseURL: "http://127.0.0.1:9999/v1", Model: "qwen"},
			"openrouter": {Type: "openrouter", BaseURL: "https://openrouter.invalid/v1", APIKey: "sk-or-v1-route-attempt-fixture-key", Model: "qwen"},
		},
		names:          []string{"bragi", "openrouter"},
		defaultName:    "bragi",
		healthCooldown: cooldown,
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: sc})
	svc.hub = serviceimpl.NewSessionHub()
	return svc
}

func routingHarnessEntry(t *testing.T, entries []routing.HarnessEntry, name string) routing.HarnessEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("routing entry %q not found", name)
	return routing.HarnessEntry{}
}

func containsRouteString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertSingleWarningLine(t *testing.T, logs string, path string) {
	t.Helper()
	trimmed := strings.TrimSpace(logs)
	if trimmed == "" {
		t.Fatal("expected warning log, got empty output")
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) != 1 {
		t.Fatalf("warning log lines = %d, want 1: %q", len(lines), trimmed)
	}
	if !strings.Contains(lines[0], "warning:") {
		t.Fatalf("warning log = %q, want warning prefix", lines[0])
	}
	if !strings.Contains(lines[0], path) {
		t.Fatalf("warning log = %q, want path %q", lines[0], path)
	}
}

type geminiTestQuotaSnapshot struct {
	CapturedAt time.Time               `json:"captured_at"`
	Windows    []harnesses.QuotaWindow `json:"windows"`
	Source     string                  `json:"source"`
	Account    *harnesses.AccountInfo  `json:"account,omitempty"`
}

func writeCodexQuotaCacheFile(t *testing.T, path string, capturedAt time.Time, source string, account *harnesses.AccountInfo, windows []harnesses.QuotaWindow) {
	t.Helper()
	type codexCache struct {
		CapturedAt time.Time               `json:"captured_at"`
		Windows    []harnesses.QuotaWindow `json:"windows"`
		Source     string                  `json:"source"`
		Account    *harnesses.AccountInfo  `json:"account,omitempty"`
	}
	data, err := json.MarshalIndent(codexCache{CapturedAt: capturedAt, Windows: windows, Source: source, Account: account}, "", "  ")
	if err != nil {
		t.Fatalf("marshal codex quota cache: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir codex quota cache dir: %v", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		t.Fatalf("write codex quota cache: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		t.Fatalf("rename codex quota cache: %v", err)
	}
}

func writeGeminiTestQuota(t *testing.T, path string, snap geminiTestQuotaSnapshot) {
	t.Helper()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal gemini test quota: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir gemini quota dir: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write gemini quota: %v", err)
	}
}

func drainRouteAttemptServiceEvents(t *testing.T, ch <-chan ServiceEvent, timeout time.Duration) []ServiceEvent {
	t.Helper()
	var events []ServiceEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			t.Fatalf("timed out waiting for service events")
			return events
		}
	}
}

func finalRouteAttemptServiceData(t *testing.T, events []ServiceEvent) ServiceFinalData {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != ServiceEventTypeFinal {
			continue
		}
		var final ServiceFinalData
		if err := json.Unmarshal(events[i].Data, &final); err != nil {
			t.Fatalf("unmarshal final: %v", err)
		}
		return final
	}
	t.Fatalf("final event not found in %v", events)
	return ServiceFinalData{}
}
