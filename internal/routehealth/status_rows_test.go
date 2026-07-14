package routehealth

import (
	"reflect"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
)

func TestBuildStatusRowsFiltersOrdersAndProjects(t *testing.T) {
	asOf := time.Date(2026, 7, 14, 18, 0, 0, 0, time.FixedZone("snapshot", -4*60*60))
	healthAt := asOf.Add(-time.Minute)
	quotaAt := asOf.Add(-2 * time.Minute)
	discoveredAt := asOf.Add(-3 * time.Minute)
	quotaRemaining := 17

	projected := modelsnapshot.KnownModel{
		Provider:              "alpha",
		Harness:               "fiz",
		ID:                    "model-a",
		EndpointName:          "zeta",
		EndpointBaseURL:       "http://alpha-zeta.invalid:9000/v1",
		ServerInstance:        "zeta-server",
		Billing:               modelcatalog.BillingModelFixed,
		DiscoveredVia:         modelsnapshot.SourcePropsAPI,
		DiscoveredAt:          discoveredAt,
		Power:                 8,
		CostInputPerM:         1.25,
		CostOutputPerM:        2.5,
		ContextWindow:         65536,
		QuotaRemaining:        &quotaRemaining,
		RecentP50Latency:      37 * time.Millisecond,
		Status:                modelsnapshot.StatusAvailable,
		HealthFreshnessAt:     healthAt,
		HealthFreshnessSource: "health-probe",
		QuotaFreshnessAt:      quotaAt,
		QuotaFreshnessSource:  "quota-probe",
		ActualCashSpend:       true,
		EffectiveCost:         3.75,
		EffectiveCostSource:   "catalog",
		AutoRoutable:          true,
		ExactPinOnly:          true,
		ExclusionReason:       "operator-only",
	}
	snapshot := modelsnapshot.ModelSnapshot{
		AsOf: asOf,
		Models: []modelsnapshot.KnownModel{
			{Provider: "beta", Harness: "fiz", ID: "model-z", EndpointName: "only", EndpointBaseURL: "http://beta-z.invalid:8000/v1", ServerInstance: "server-b"},
			{Provider: "beta", Harness: "", ID: "model-a", EndpointName: "beta", EndpointBaseURL: "http://beta.invalid:8000/v1"},
			projected,
			{Provider: "beta", Harness: "fiz", ID: "model-z", EndpointName: "only", EndpointBaseURL: "http://beta-a.invalid:8000/v1", ServerInstance: "server-z"},
			{Provider: "beta", Harness: "fiz", ID: "model-z", EndpointName: "only", EndpointBaseURL: "http://beta-z.invalid:8000/v1", ServerInstance: "server-a"},
			{Provider: "alpha", Harness: "fiz", ID: "model-a", EndpointName: "alpha", EndpointBaseURL: "http://127.0.0.1:8080/v1"},
			{Provider: "alpha", Harness: "codex", ID: "model-a", EndpointName: "non-fiz"},
			{Provider: "unconfigured", Harness: "fiz", ID: "model-a", EndpointName: "unknown-provider"},
			{Provider: "alpha", Harness: "fiz", ID: "  ", EndpointName: "blank-model"},
		},
	}
	before := modelsnapshot.ModelSnapshot{
		AsOf:   snapshot.AsOf,
		Models: append([]modelsnapshot.KnownModel(nil), snapshot.Models...),
	}

	got := BuildStatusRows(StatusRowsInput{
		Snapshot: snapshot,
		ConfiguredProviders: map[string]struct{}{
			"alpha": {},
			"beta":  {},
		},
	})

	if !reflect.DeepEqual(snapshot, before) {
		t.Fatalf("BuildStatusRows mutated input snapshot\nbefore=%#v\nafter=%#v", before.Models, snapshot.Models)
	}
	if len(got) != 2 || got[0].Model != "model-a" || got[1].Model != "model-z" {
		t.Fatalf("entry order=%#v, want model-a then model-z", got)
	}
	if len(got[1].Candidates) != 3 ||
		got[1].Candidates[0].ServerInstance != "server-z" ||
		got[1].Candidates[1].ServerInstance != "server-a" ||
		got[1].Candidates[2].ServerInstance != "server-b" {
		t.Fatalf("base-URL/server order=%#v, want beta-a/server-z then beta-z/server-a then beta-z/server-b", got[1].Candidates)
	}
	if !statusRowLess(
		modelsnapshot.KnownModel{Provider: "alpha", EndpointName: "same", EndpointBaseURL: "http://same.invalid", ServerInstance: "same", ID: "model-a"},
		modelsnapshot.KnownModel{Provider: "alpha", EndpointName: "same", EndpointBaseURL: "http://same.invalid", ServerInstance: "same", ID: "model-b"},
	) {
		t.Fatal("statusRowLess did not use model ID as the final ordering tie-breaker")
	}
	entry := got[0]
	if entry.Strategy != "auto" || len(entry.Candidates) != 3 {
		t.Fatalf("model-a entry=%#v, want auto with three admitted candidates", entry)
	}
	wantOrder := []struct {
		provider string
		endpoint string
		priority int
	}{
		{provider: "alpha", endpoint: "alpha", priority: 3},
		{provider: "alpha", endpoint: "zeta", priority: 2},
		{provider: "beta", endpoint: "beta", priority: 1},
	}
	for i, want := range wantOrder {
		candidate := entry.Candidates[i]
		if candidate.Provider != want.provider || candidate.Endpoint != want.endpoint || candidate.Priority != want.priority {
			t.Fatalf("candidate[%d]=%+v, want provider=%s endpoint=%s priority=%d", i, candidate, want.provider, want.endpoint, want.priority)
		}
	}
	if got := entry.Candidates[0].ServerInstance; got != "127.0.0.1:8080" {
		t.Fatalf("normalized server_instance=%q, want 127.0.0.1:8080", got)
	}

	candidate := entry.Candidates[1]
	if candidate.Provider != projected.Provider || candidate.Endpoint != projected.EndpointName || candidate.Model != projected.ID || candidate.ServerInstance != projected.ServerInstance {
		t.Fatalf("identity projection=%+v, want projected row identity", candidate)
	}
	if candidate.Billing != projected.Billing || candidate.ActualCashSpend != projected.ActualCashSpend || candidate.EffectiveCost != projected.EffectiveCost || candidate.EffectiveCostSource != projected.EffectiveCostSource {
		t.Fatalf("billing/cost projection=%+v, want row billing/cost", candidate)
	}
	if !candidate.Healthy || candidate.Cooldown != nil {
		t.Fatalf("health projection=(%v, %+v), want healthy without cooldown", candidate.Healthy, candidate.Cooldown)
	}
	if candidate.SourceStatus != string(projected.Status) || candidate.AutoRoutable != projected.AutoRoutable || candidate.ExactPinOnly != projected.ExactPinOnly || candidate.ExclusionReason != projected.ExclusionReason {
		t.Fatalf("eligibility projection=%+v, want row status fields", candidate)
	}
	if candidate.Power != projected.Power || candidate.ContextLength != projected.ContextWindow || candidate.CostInputPerMTok != projected.CostInputPerM || candidate.CostOutputPerMTok != projected.CostOutputPerM {
		t.Fatalf("catalog projection=%+v, want row power/context/cost fields", candidate)
	}
	if candidate.RecentLatencyMS != 37 || candidate.ProviderReliabilityRate != 0 {
		t.Fatalf("metric projection latency=%v reliability=%v, want 37 and 0", candidate.RecentLatencyMS, candidate.ProviderReliabilityRate)
	}
	if candidate.QuotaRemaining == nil || *candidate.QuotaRemaining != quotaRemaining || candidate.QuotaRemaining == projected.QuotaRemaining {
		t.Fatalf("quota_remaining=%p/%v, want cloned pointer containing %d", candidate.QuotaRemaining, candidate.QuotaRemaining, quotaRemaining)
	}
	if candidate.SnapshotCapturedAt != asOf || !candidate.HealthFreshnessAt.Equal(healthAt) || candidate.HealthFreshnessSource != projected.HealthFreshnessSource {
		t.Fatalf("snapshot/health freshness projection=%+v", candidate)
	}
	if !candidate.QuotaFreshnessAt.Equal(quotaAt) || candidate.QuotaFreshnessSource != projected.QuotaFreshnessSource {
		t.Fatalf("quota freshness projection=%+v", candidate)
	}
	if !candidate.ModelDiscoveryFreshnessAt.Equal(discoveredAt) || candidate.ModelDiscoveryFreshnessSource != string(projected.DiscoveredVia) {
		t.Fatalf("discovery freshness projection=%+v", candidate)
	}

	if rows := BuildStatusRows(StatusRowsInput{}); rows != nil {
		t.Fatalf("empty input rows=%#v, want nil", rows)
	}
	if rows := BuildStatusRows(StatusRowsInput{
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{{Provider: "alpha", Harness: "codex", ID: "model-a"}}},
		ConfiguredProviders: map[string]struct{}{
			"alpha": {},
		},
	}); rows != nil {
		t.Fatalf("all-filtered rows=%#v, want nil", rows)
	}
}

func TestBuildStatusRowsCooldownScope(t *testing.T) {
	now := time.Date(2026, 7, 14, 18, 0, 0, 0, time.UTC)
	input := StatusRowsInput{
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{
			{Provider: "bragi", Harness: "fiz", ID: "qwen", EndpointName: "primary", ServerInstance: "desk-a"},
			{Provider: "bragi", Harness: "fiz", ID: "qwen", EndpointName: "secondary", ServerInstance: "desk-b"},
			{Provider: "openrouter", Harness: "fiz", ID: "qwen", EndpointName: "remote", ServerInstance: "remote-a"},
		}},
		ConfiguredProviders: map[string]struct{}{"bragi": {}, "openrouter": {}},
		CooldownTTL:         30 * time.Second,
		ActiveAttempts: []Record{
			{Key: Key{Harness: "fiz", Provider: "bragi@primary", ServerInstance: "desk-a", Model: "qwen"}, Reason: "older", RecordedAt: now},
			{Key: Key{Harness: "fiz", Provider: "bragi", Endpoint: "primary", ServerInstance: "desk-a", Model: "qwen"}, Reason: "newest-exact", Error: "timeout", RecordedAt: now.Add(time.Second)},
			{Key: Key{Harness: "codex", Provider: "bragi", Endpoint: "primary", ServerInstance: "desk-a", Model: "qwen"}, Reason: "non-fiz", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Harness: "fiz", Provider: "bragi", Endpoint: "secondary", ServerInstance: "desk-a", Model: "qwen"}, Reason: "wrong-endpoint", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Harness: "fiz", Provider: "bragi", Endpoint: "primary", ServerInstance: "desk-b", Model: "qwen"}, Reason: "wrong-server", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Harness: "fiz", Provider: "bragi", Endpoint: "primary", ServerInstance: "desk-a", Model: "other"}, Reason: "wrong-model", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Harness: "fiz", Provider: "bragi", Endpoint: "primary", Model: "qwen"}, Reason: "partial-exact", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Provider: "bragi", Model: "qwen"}, Reason: "partial-legacy", RecordedAt: now.Add(10 * time.Second)},
			{Key: Key{Provider: "bragi@primary"}, Reason: "qualified-is-not-provider-wide", RecordedAt: now.Add(10 * time.Second)},
		},
	}

	rows := BuildStatusRows(input)
	if len(rows) != 1 || len(rows[0].Candidates) != 3 {
		t.Fatalf("rows=%#v, want one entry with three candidates", rows)
	}
	primary := rows[0].Candidates[0]
	if primary.Endpoint != "primary" || primary.Healthy || primary.Cooldown == nil {
		t.Fatalf("primary=%+v, want exact cooldown", primary)
	}
	if primary.Cooldown.Reason != "newest-exact" || primary.Cooldown.LastError != "timeout" || !primary.Cooldown.LastAttempt.Equal(now.Add(time.Second)) {
		t.Fatalf("primary cooldown=%+v, want newest exact match", primary.Cooldown)
	}
	if want := now.Add(31 * time.Second); !primary.Cooldown.Until.Equal(want) {
		t.Fatalf("primary cooldown until=%v, want %v", primary.Cooldown.Until, want)
	}
	secondary := rows[0].Candidates[1]
	if secondary.Endpoint != "secondary" || !secondary.Healthy || secondary.Cooldown != nil {
		t.Fatalf("secondary=%+v, exact primary cooldown leaked", secondary)
	}
	otherProvider := rows[0].Candidates[2]
	if otherProvider.Provider != "openrouter" || !otherProvider.Healthy || otherProvider.Cooldown != nil {
		t.Fatalf("other-provider candidate=%+v, bragi cooldown leaked across providers", otherProvider)
	}

	legacyAt := now.Add(20 * time.Second)
	input.ActiveAttempts = append(input.ActiveAttempts, Record{
		Key:        Key{Provider: "bragi"},
		RecordedAt: legacyAt,
	})
	rows = BuildStatusRows(input)
	for _, candidate := range rows[0].Candidates {
		if candidate.Provider == "openrouter" {
			if !candidate.Healthy || candidate.Cooldown != nil {
				t.Fatalf("other-provider candidate=%+v, provider-wide bragi cooldown leaked", candidate)
			}
			continue
		}
		if candidate.Healthy || candidate.Cooldown == nil || candidate.Cooldown.Reason != "route_attempt_failure" || candidate.Cooldown.FailCount != 1 || !candidate.Cooldown.LastAttempt.Equal(legacyAt) {
			t.Fatalf("candidate=%+v, want newest defaulted legacy provider-wide cooldown with FailCount=1", candidate)
		}
	}
}

func TestBuildStatusRowsMetricFallback(t *testing.T) {
	provider := "alpha"
	model := "model-a"
	exactZero := ProviderModelKey(Key{Provider: provider, Endpoint: "exact-zero", Model: model})
	snapshotMetric := ProviderModelKey(Key{Provider: provider, Endpoint: "snapshot", Model: model})
	providerWide := ProviderModelKey(Key{Provider: provider, Model: model})

	successRate := map[string]float64{
		exactZero:      0,
		snapshotMetric: 0.7,
		providerWide:   0.91,
	}
	latencyMS := map[string]float64{
		exactZero:      0,
		snapshotMetric: 99,
		providerWide:   42,
	}
	wantSuccessRate := map[string]float64{
		exactZero:      0,
		snapshotMetric: 0.7,
		providerWide:   0.91,
	}
	wantLatencyMS := map[string]float64{
		exactZero:      0,
		snapshotMetric: 99,
		providerWide:   42,
	}

	rows := BuildStatusRows(StatusRowsInput{
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{
			{Provider: provider, Harness: "fiz", ID: model, EndpointName: "exact-zero"},
			{Provider: provider, Harness: "fiz", ID: model, EndpointName: "fallback"},
			{Provider: provider, Harness: "fiz", ID: model, EndpointName: "snapshot", RecentP50Latency: 25 * time.Millisecond},
			{Provider: "beta", Harness: "fiz", ID: model, EndpointName: "absent"},
		}},
		ConfiguredProviders: map[string]struct{}{provider: {}, "beta": {}},
		SuccessRate:         successRate,
		LatencyMS:           latencyMS,
	})

	if !reflect.DeepEqual(successRate, wantSuccessRate) || !reflect.DeepEqual(latencyMS, wantLatencyMS) {
		t.Fatalf("BuildStatusRows mutated metric inputs: success=%#v latency=%#v", successRate, latencyMS)
	}
	if len(rows) != 1 || len(rows[0].Candidates) != 4 {
		t.Fatalf("rows=%#v, want four metric candidates", rows)
	}
	byEndpoint := make(map[string]StatusCandidate, 4)
	for _, candidate := range rows[0].Candidates {
		byEndpoint[candidate.Endpoint] = candidate
	}
	if candidate := byEndpoint["exact-zero"]; candidate.ProviderReliabilityRate != 0 || candidate.RecentLatencyMS != 0 {
		t.Fatalf("exact-zero metrics=%+v, exact map presence must suppress provider fallback", candidate)
	}
	if candidate := byEndpoint["fallback"]; candidate.ProviderReliabilityRate != 0.91 || candidate.RecentLatencyMS != 42 {
		t.Fatalf("fallback metrics=%+v, want provider-wide reliability=.91 latency=42", candidate)
	}
	if candidate := byEndpoint["snapshot"]; candidate.ProviderReliabilityRate != 0.7 || candidate.RecentLatencyMS != 25 {
		t.Fatalf("snapshot metrics=%+v, snapshot latency must beat metric latency", candidate)
	}
	if candidate := byEndpoint["absent"]; candidate.ProviderReliabilityRate != 0 || candidate.RecentLatencyMS != 0 {
		t.Fatalf("absent metrics=%+v, want zero values when exact and provider fallback keys are absent", candidate)
	}
}
