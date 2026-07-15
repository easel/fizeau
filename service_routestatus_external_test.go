package fizeau_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestRouteStatusPublicContractExternal(t *testing.T) {
	serviceType := reflect.TypeOf((*fizeau.FizeauService)(nil)).Elem()
	method, ok := serviceType.MethodByName("RouteStatus")
	if !ok {
		t.Fatal("FizeauService.RouteStatus is missing")
	}
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if method.Type.NumIn() != 1 || method.Type.In(0) != contextType {
		t.Fatalf("RouteStatus inputs = %v, want (context.Context)", method.Type)
	}
	if method.Type.NumOut() != 2 ||
		method.Type.Out(0) != reflect.TypeOf((*fizeau.RouteStatusReport)(nil)) ||
		method.Type.Out(1) != errorType {
		t.Fatalf("RouteStatus outputs = %v, want (*fizeau.RouteStatusReport, error)", method.Type)
	}

	stringType := reflect.TypeOf("")
	boolType := reflect.TypeOf(false)
	intType := reflect.TypeOf(0)
	floatType := reflect.TypeOf(float64(0))
	timeType := reflect.TypeOf(time.Time{})
	billingType := reflect.TypeOf(fizeau.BillingModel(""))
	assertExactStructContract(t, reflect.TypeOf(fizeau.RouteStatusReport{}), []publicFieldContract{
		{"Routes", reflect.TypeOf([]fizeau.RouteStatusEntry{}), ""},
		{"GeneratedAt", timeType, ""},
		{"SnapshotCapturedAt", timeType, ""},
		{"GlobalCooldowns", reflect.TypeOf([]fizeau.CooldownState{}), ""},
		{"RoutingQuality", reflect.TypeOf(fizeau.RoutingQualityMetrics{}), ""},
	})
	assertExactStructContract(t, reflect.TypeOf(fizeau.RouteStatusEntry{}), []publicFieldContract{
		{"Model", stringType, ""},
		{"Strategy", stringType, ""},
		{"SelectedEndpoint", stringType, ""},
		{"SelectedServerInstance", stringType, ""},
		{"Sticky", reflect.TypeOf(fizeau.RouteStickyState{}), ""},
		{"Candidates", reflect.TypeOf([]fizeau.RouteCandidateStatus{}), ""},
		{"LastDecision", reflect.TypeOf((*fizeau.RouteDecision)(nil)), ""},
		{"LastDecisionAt", timeType, ""},
	})
	assertExactStructContract(t, reflect.TypeOf(fizeau.RouteCandidateStatus{}), []publicFieldContract{
		{"Provider", stringType, ""},
		{"Endpoint", stringType, ""},
		{"Model", stringType, ""},
		{"ServerInstance", stringType, ""},
		{"Billing", billingType, ""},
		{"ActualCashSpend", boolType, ""},
		{"EffectiveCost", floatType, ""},
		{"EffectiveCostSource", stringType, ""},
		{"Priority", intType, ""},
		{"Healthy", boolType, ""},
		{"Cooldown", reflect.TypeOf((*fizeau.CooldownState)(nil)), ""},
		{"SourceStatus", stringType, ""},
		{"AutoRoutable", boolType, ""},
		{"ExactPinOnly", boolType, ""},
		{"ExclusionReason", stringType, ""},
		{"Power", intType, ""},
		{"ContextLength", intType, ""},
		{"CostInputPerMTok", floatType, ""},
		{"CostOutputPerMTok", floatType, ""},
		{"RecentLatencyMS", floatType, ""},
		{"ProviderReliabilityRate", floatType, ""},
		{"QuotaRemaining", reflect.TypeOf((*int)(nil)), ""},
		{"SnapshotCapturedAt", timeType, ""},
		{"HealthFreshnessAt", timeType, ""},
		{"HealthFreshnessSource", stringType, ""},
		{"QuotaFreshnessAt", timeType, ""},
		{"QuotaFreshnessSource", stringType, ""},
		{"ModelDiscoveryFreshnessAt", timeType, ""},
		{"ModelDiscoveryFreshnessSource", stringType, ""},
	})
	assertExactStructContract(t, reflect.TypeOf(fizeau.CooldownState{}), []publicFieldContract{
		{"Reason", stringType, ""},
		{"Until", timeType, ""},
		{"FailCount", intType, ""},
		{"LastError", stringType, ""},
		{"LastAttempt", timeType, ""},
	})
}

type publicFieldContract struct {
	name string
	typ  reflect.Type
	tag  reflect.StructTag
}

func assertExactStructContract(t *testing.T, typ reflect.Type, want []publicFieldContract) {
	t.Helper()
	if typ.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for i, expected := range want {
		field := typ.Field(i)
		if field.Name != expected.name || field.Type != expected.typ || field.Tag != expected.tag {
			t.Errorf("%s field[%d] = %s %s %q, want %s %s %q",
				typ.Name(), i, field.Name, field.Type, field.Tag,
				expected.name, expected.typ, expected.tag)
		}
	}
}

func newPublicRouteStatusFacade(t *testing.T, config *providerFacadeConfig) fizeau.FizeauService {
	t.Helper()
	t.Setenv("PATH", "")
	cacheDir, err := os.MkdirTemp("", "fizeau-public-route-status-*")
	if err != nil {
		t.Fatalf("create route-status cache dir: %v", err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 20; attempt++ {
			if err := os.RemoveAll(cacheDir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Errorf("remove route-status cache dir %s", cacheDir)
	})
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)

	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       config,
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}

func TestPublicRouteStatusEmptyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("FIZEAU_CACHE_DIR", filepath.Join(home, "cache"))
	t.Setenv("PATH", "")
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ConfigPath:          filepath.Join(home, "project", "config.yaml"),
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New with nil ServiceConfig: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if report == nil {
		t.Fatal("RouteStatus returned a nil report")
	}
	if len(report.Routes) != 0 {
		t.Fatalf("Routes = %#v, want no configured routes", report.Routes)
	}
	if report.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt is zero")
	}
}

func TestPublicRouteStatusMultiEndpointSnapshot(t *testing.T) {
	const model = "qwen3.5-27b"
	primary := externalModelsServer(t, []string{model})
	backup := externalModelsServer(t, []string{model})
	svc := newPublicRouteStatusFacade(t, &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {
				Type:    "lmstudio",
				BaseURL: primary.URL + "/v1",
				Endpoints: []fizeau.ServiceProviderEndpoint{
					{Name: "primary", BaseURL: primary.URL + "/v1"},
					{Name: "backup", BaseURL: backup.URL + "/v1"},
				},
				Model: model,
			},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	})

	models, err := svc.ListModels(context.Background(), fizeau.ModelFilter{Provider: "bragi"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("ListModels rows = %d, want 2: %#v", len(models), models)
	}
	wantRows := make(map[string]fizeau.ModelInfo, len(models))
	var primaryRow *fizeau.ModelInfo
	for _, info := range models {
		key := info.Provider + "\x00" + info.ID + "\x00" + info.EndpointName + "\x00" + info.ServerInstance
		wantRows[key] = info
		if info.Provider == "bragi" && info.EndpointName == "primary" {
			row := info
			primaryRow = &row
		}
	}
	if primaryRow == nil || primaryRow.ServerInstance == "" {
		t.Fatalf("primary ListModels row is missing normalized server identity: %#v", models)
	}
	recordedAt := time.Now().UTC().Add(-time.Second)
	if err := svc.RecordRouteAttempt(context.Background(), fizeau.RouteAttempt{
		Harness:        "fiz",
		Provider:       primaryRow.Provider,
		Endpoint:       primaryRow.EndpointName,
		ServerInstance: primaryRow.ServerInstance,
		Model:          primaryRow.ID,
		Status:         "failed",
		Reason:         "route_attempt_failure",
		Timestamp:      recordedAt,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 {
		t.Fatalf("Routes = %d, want 1: %#v", len(report.Routes), report.Routes)
	}
	entry := report.Routes[0]
	if entry.Model != model || entry.Strategy != "auto" {
		t.Fatalf("route identity = %q/%q, want %q/auto", entry.Model, entry.Strategy, model)
	}
	if len(entry.Candidates) != 2 {
		t.Fatalf("Candidates = %d, want 2: %#v", len(entry.Candidates), entry.Candidates)
	}
	byEndpoint := make(map[string]fizeau.RouteCandidateStatus, len(entry.Candidates))
	for _, candidate := range entry.Candidates {
		key := candidate.Provider + "\x00" + candidate.Model + "\x00" + candidate.Endpoint + "\x00" + candidate.ServerInstance
		if _, ok := wantRows[key]; !ok {
			t.Errorf("RouteStatus candidate %q has no matching ListModels row", key)
		}
		byEndpoint[candidate.Endpoint] = candidate
		if candidate.Provider != "bragi" || candidate.Model != model || candidate.ServerInstance == "" {
			t.Errorf("candidate identity = %#v", candidate)
		}
		if candidate.Billing != fizeau.BillingModelFixed {
			t.Errorf("candidate billing = %q, want fixed", candidate.Billing)
		}
		if !candidate.SnapshotCapturedAt.Equal(report.SnapshotCapturedAt) {
			t.Errorf("candidate snapshot = %v, want report snapshot %v", candidate.SnapshotCapturedAt, report.SnapshotCapturedAt)
		}
	}
	primaryCandidate, ok := byEndpoint["primary"]
	if !ok {
		t.Fatalf("candidate endpoints = %#v, want primary and backup", byEndpoint)
	}
	if primaryCandidate.Healthy || primaryCandidate.Cooldown == nil {
		t.Fatalf("primary candidate = %#v, want endpoint-scoped cooldown", primaryCandidate)
	}
	if primaryCandidate.Cooldown.Reason != "route_attempt_failure" ||
		!primaryCandidate.Cooldown.LastAttempt.Equal(recordedAt) {
		t.Fatalf("primary cooldown = %#v, want route_attempt_failure at %v", primaryCandidate.Cooldown, recordedAt)
	}
	backupCandidate, ok := byEndpoint["backup"]
	if !ok {
		t.Fatalf("candidate endpoints = %#v, want primary and backup", byEndpoint)
	}
	if !backupCandidate.Healthy || backupCandidate.Cooldown != nil {
		t.Fatalf("backup candidate = %#v, want healthy without cooldown", backupCandidate)
	}
}

func TestPublicRouteStatusLastDecision(t *testing.T) {
	const (
		model          = "qwen3.5-27b"
		endpoint       = "primary"
		serverInstance = "bragi-instance"
	)
	models := externalModelsServer(t, []string{model})
	svc := newPublicRouteStatusFacade(t, &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {
				Type:           "lmstudio",
				BaseURL:        models.URL + "/v1",
				ServerInstance: serverInstance,
				Endpoints: []fizeau.ServiceProviderEndpoint{{
					Name: endpoint, BaseURL: models.URL + "/v1", ServerInstance: serverInstance,
				}},
				Model: model,
			},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	})
	if _, err := svc.ListModels(context.Background(), fizeau.ModelFilter{Provider: "bragi"}); err != nil {
		t.Fatalf("prime model snapshot: %v", err)
	}

	decision, err := svc.ResolveRoute(context.Background(), fizeau.RouteRequest{
		Policy:        "air-gapped",
		Provider:      "bragi",
		Model:         model,
		CorrelationID: "public-route-status-last-decision",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if decision == nil {
		t.Fatal("ResolveRoute returned a nil decision")
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	var found *fizeau.RouteStatusEntry
	for i := range report.Routes {
		if report.Routes[i].Model == model {
			found = &report.Routes[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("route %q not found in %#v", model, report.Routes)
	}
	if !reflect.DeepEqual(found.LastDecision, decision) {
		t.Fatalf("LastDecision = %#v, want ResolveRoute decision %#v", found.LastDecision, decision)
	}
	if found.LastDecisionAt.IsZero() {
		t.Fatal("LastDecisionAt is zero")
	}
	if found.SelectedEndpoint != endpoint || found.SelectedServerInstance != serverInstance {
		t.Fatalf("selected endpoint/server = %q/%q, want %q/%q", found.SelectedEndpoint, found.SelectedServerInstance, endpoint, serverInstance)
	}
	if found.Sticky != decision.Sticky {
		t.Fatalf("Sticky = %#v, want decision evidence %#v", found.Sticky, decision.Sticky)
	}
}

func TestPublicRouteStatusCooldownObservation(t *testing.T) {
	const model = "qwen3.5-27b"
	models := externalModelsServer(t, []string{model})
	svc := newPublicRouteStatusFacade(t, &providerFacadeConfig{
		providers: map[string]fizeau.ServiceProviderEntry{
			"bragi": {
				Type: "lmstudio", BaseURL: models.URL + "/v1", Model: model,
			},
			"backup": {
				Type: "lmstudio", BaseURL: models.URL + "/v1", Model: model,
			},
		},
		names:       []string{"bragi", "backup"},
		defaultName: "bragi",
	})
	if _, err := svc.ListModels(context.Background(), fizeau.ModelFilter{}); err != nil {
		t.Fatalf("prime model snapshot: %v", err)
	}
	recordedAt := time.Now().UTC().Add(-time.Second)
	if err := svc.RecordRouteAttempt(context.Background(), fizeau.RouteAttempt{
		Provider:  "bragi",
		Status:    "failed",
		Reason:    "rate_limit",
		Timestamp: recordedAt,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	report, err := svc.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(report.Routes) != 1 || len(report.Routes[0].Candidates) != 2 {
		t.Fatalf("routes/candidates = %#v, want one route with two candidates", report.Routes)
	}
	byProvider := make(map[string]fizeau.RouteCandidateStatus, 2)
	for _, candidate := range report.Routes[0].Candidates {
		byProvider[candidate.Provider] = candidate
	}
	bragi, ok := byProvider["bragi"]
	if !ok {
		t.Fatal("bragi candidate not found")
	}
	if bragi.Healthy || bragi.Cooldown == nil {
		t.Fatalf("bragi candidate = %#v, want an observed cooldown", bragi)
	}
	if bragi.Cooldown.Reason != "rate_limit" || !bragi.Cooldown.LastAttempt.Equal(recordedAt) || bragi.Cooldown.Until.IsZero() {
		t.Fatalf("bragi cooldown = %#v, want rate_limit at %v", bragi.Cooldown, recordedAt)
	}
	backup, ok := byProvider["backup"]
	if !ok {
		t.Fatal("backup candidate not found")
	}
	if !backup.Healthy || backup.Cooldown != nil {
		t.Fatalf("backup candidate = %#v, want healthy without cooldown", backup)
	}
}

func TestRouteStatusRepresentativeJSONStable(t *testing.T) {
	generatedAt := time.Date(2026, 7, 14, 12, 34, 56, 0, time.UTC)
	snapshotAt := generatedAt.Add(-time.Minute)
	lastAttempt := generatedAt.Add(-20 * time.Second)
	quota := 7
	cooldown := fizeau.CooldownState{
		Reason:      "rate_limit",
		Until:       generatedAt.Add(10 * time.Second),
		FailCount:   1,
		LastError:   "429 too many requests",
		LastAttempt: lastAttempt,
	}
	report := fizeau.RouteStatusReport{
		Routes: []fizeau.RouteStatusEntry{{
			Model:                  "model-a",
			Strategy:               "auto",
			SelectedEndpoint:       "primary",
			SelectedServerInstance: "server-a",
			Sticky: fizeau.RouteStickyState{
				KeyPresent:     true,
				Assignment:     "reused",
				ServerInstance: "server-a",
				Reason:         "live sticky lease reused",
				Bonus:          1.5,
			},
			Candidates: []fizeau.RouteCandidateStatus{{
				Provider:                      "provider-a",
				Endpoint:                      "primary",
				Model:                         "model-a",
				ServerInstance:                "server-a",
				Billing:                       fizeau.BillingModelFixed,
				ActualCashSpend:               false,
				EffectiveCost:                 0.25,
				EffectiveCostSource:           "catalog",
				Priority:                      2,
				Healthy:                       false,
				Cooldown:                      &cooldown,
				SourceStatus:                  "available",
				AutoRoutable:                  true,
				ExactPinOnly:                  false,
				ExclusionReason:               "",
				Power:                         8,
				ContextLength:                 32768,
				CostInputPerMTok:              0.1,
				CostOutputPerMTok:             0.2,
				RecentLatencyMS:               12.5,
				ProviderReliabilityRate:       0.75,
				QuotaRemaining:                &quota,
				SnapshotCapturedAt:            snapshotAt,
				HealthFreshnessAt:             snapshotAt.Add(-time.Second),
				HealthFreshnessSource:         "probe",
				QuotaFreshnessAt:              snapshotAt.Add(-2 * time.Second),
				QuotaFreshnessSource:          "provider",
				ModelDiscoveryFreshnessAt:     snapshotAt.Add(-3 * time.Second),
				ModelDiscoveryFreshnessSource: "provider_api",
			}},
			LastDecision:   nil,
			LastDecisionAt: generatedAt.Add(-5 * time.Second),
		}},
		GeneratedAt:        generatedAt,
		SnapshotCapturedAt: snapshotAt,
		GlobalCooldowns:    []fizeau.CooldownState{cooldown},
		RoutingQuality: fizeau.RoutingQualityMetrics{
			AutoAcceptanceRate:       0.5,
			OverrideDisagreementRate: 1,
			OverrideClassBreakdown: []fizeau.OverrideClassBucket{{
				PromptFeatureBucket: "tokens=tiny,tools=no,reasoning=none",
				Axis:                "model",
				Match:               false,
				Count:               1,
				SuccessOutcomes:     1,
			}},
			TotalRequests:  2,
			TotalOverrides: 1,
		},
	}

	got, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	want := strings.TrimSpace(`
{
  "Routes": [
    {
      "Model": "model-a",
      "Strategy": "auto",
      "SelectedEndpoint": "primary",
      "SelectedServerInstance": "server-a",
      "Sticky": {
        "key_present": true,
        "assignment": "reused",
        "server_instance": "server-a",
        "reason": "live sticky lease reused",
        "bonus": 1.5
      },
      "Candidates": [
        {
          "Provider": "provider-a",
          "Endpoint": "primary",
          "Model": "model-a",
          "ServerInstance": "server-a",
          "Billing": "fixed",
          "ActualCashSpend": false,
          "EffectiveCost": 0.25,
          "EffectiveCostSource": "catalog",
          "Priority": 2,
          "Healthy": false,
          "Cooldown": {
            "Reason": "rate_limit",
            "Until": "2026-07-14T12:35:06Z",
            "FailCount": 1,
            "LastError": "429 too many requests",
            "LastAttempt": "2026-07-14T12:34:36Z"
          },
          "SourceStatus": "available",
          "AutoRoutable": true,
          "ExactPinOnly": false,
          "ExclusionReason": "",
          "Power": 8,
          "ContextLength": 32768,
          "CostInputPerMTok": 0.1,
          "CostOutputPerMTok": 0.2,
          "RecentLatencyMS": 12.5,
          "ProviderReliabilityRate": 0.75,
          "QuotaRemaining": 7,
          "SnapshotCapturedAt": "2026-07-14T12:33:56Z",
          "HealthFreshnessAt": "2026-07-14T12:33:55Z",
          "HealthFreshnessSource": "probe",
          "QuotaFreshnessAt": "2026-07-14T12:33:54Z",
          "QuotaFreshnessSource": "provider",
          "ModelDiscoveryFreshnessAt": "2026-07-14T12:33:53Z",
          "ModelDiscoveryFreshnessSource": "provider_api"
        }
      ],
      "LastDecision": null,
      "LastDecisionAt": "2026-07-14T12:34:51Z"
    }
  ],
  "GeneratedAt": "2026-07-14T12:34:56Z",
  "SnapshotCapturedAt": "2026-07-14T12:33:56Z",
  "GlobalCooldowns": [
    {
      "Reason": "rate_limit",
      "Until": "2026-07-14T12:35:06Z",
      "FailCount": 1,
      "LastError": "429 too many requests",
      "LastAttempt": "2026-07-14T12:34:36Z"
    }
  ],
  "RoutingQuality": {
    "auto_acceptance_rate": 0.5,
    "override_disagreement_rate": 1,
    "override_class_breakdown": [
      {
        "prompt_feature_bucket": "tokens=tiny,tools=no,reasoning=none",
        "axis": "model",
        "match": false,
        "count": 1,
        "success_outcomes": 1,
        "stalled_outcomes": 0,
        "failed_outcomes": 0,
        "cancelled_outcomes": 0,
        "unknown_outcomes": 0
      }
    ],
    "total_requests": 2,
    "total_overrides": 1
  }
}`)
	if string(got) != want {
		t.Fatalf("RouteStatus JSON changed\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
