package fizeau

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/routing"
)

// TestServiceExecuteRequestNoPreResolved is an AST guard against the
// removed PreResolved field. ADR-005 step 1 deleted it; reintroducing it
// would silently re-enable caller-supplied route decisions.
func TestServiceExecuteRequestNoPreResolved(t *testing.T) {
	requireStructHasNoField(t, "service.go", "ServiceExecuteRequest", "PreResolved")
	requireStructHasNoField(t, "service.go", "RouteRequest", "PreResolved")
}

// TestServiceExecuteRequestHasAutoSelectionFields is an AST guard for the
// EstimatedPromptTokens / RequiresTools fields that ADR-005 step 1 added
// to ServiceExecuteRequest and RouteRequest.
func TestServiceExecuteRequestHasAutoSelectionFields(t *testing.T) {
	requireStructHasField(t, "service.go", "ServiceExecuteRequest", "EstimatedPromptTokens")
	requireStructHasField(t, "service.go", "ServiceExecuteRequest", "RequiresTools")
	requireStructHasField(t, "service.go", "RouteRequest", "EstimatedPromptTokens")
	requireStructHasField(t, "service.go", "RouteRequest", "RequiresTools")
}

func requireStructHasField(t *testing.T, file, structName, field string) {
	t.Helper()
	if structHasField(t, file, structName, field) {
		return
	}
	t.Fatalf("expected struct %s in %s to declare field %s", structName, file, field)
}

func requireStructHasNoField(t *testing.T, file, structName, field string) {
	t.Helper()
	if !structHasField(t, file, structName, field) {
		return
	}
	t.Fatalf("struct %s in %s must not declare field %s (ADR-005 step 1)", structName, file, field)
}

func structHasField(t *testing.T, file, structName, field string) bool {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var found bool
	ast.Inspect(parsed, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != structName {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, f := range st.Fields.List {
			for _, name := range f.Names {
				if name.Name == field {
					found = true
					return false
				}
			}
		}
		return false
	})
	return found
}

// TestRouteCandidateExposesComponentScores verifies that ResolveRoute
// returns candidates whose component scores are populated from the
// internal routing engine's per-axis signals.
func TestRouteCandidateExposesComponentScores(t *testing.T) {
	cand := routing.Candidate{
		Harness:            "fiz",
		Provider:           "local",
		Billing:            BillingModelFixed,
		Model:              "model-a",
		Score:              42,
		CostUSDPer1kTokens: 0.012,
		CostSource:         routing.CostSourceCatalog,
		Eligible:           true,
		Reason:             "profile=cheap; score=42",
		LatencyMS:          150,
		SuccessRate:        0.95,
		CostClass:          "cheap",
		StickyAffinity:     250,
		ScoreComponents: map[string]float64{
			"base":                100,
			"cost":                -6,
			"deployment_locality": 15,
			"quota_health":        12,
			"utilization":         -4,
			"performance":         9,
			"power":               21,
			"context_headroom":    0,
			"sticky_affinity":     250,
		},
	}
	got := routeCandidateFromInternal(cand, RoutePowerPolicy{MinPower: 6, MaxPower: 8})
	if got.Components.Cost != 0.012 {
		t.Errorf("Components.Cost=%v, want 0.012", got.Components.Cost)
	}
	if got.Billing != BillingModelFixed {
		t.Errorf("Billing=%q, want %q", got.Billing, BillingModelFixed)
	}
	if got.Components.Utilization != 0 {
		t.Errorf("Components.Utilization=%v, want 0 for unknown", got.Components.Utilization)
	}
	if got.Components.LatencyMS != 150 {
		t.Errorf("Components.LatencyMS=%v, want 150", got.Components.LatencyMS)
	}
	if got.Components.SuccessRate != 0.95 {
		t.Errorf("Components.SuccessRate=%v, want 0.95", got.Components.SuccessRate)
	}
	if got.Components.StickyAffinity != 250 {
		t.Errorf("Components.StickyAffinity=%v, want 250", got.Components.StickyAffinity)
	}
	if got.Components.Capability == 0 {
		t.Errorf("Components.Capability=0; want non-zero for cheap class")
	}
	if got.Components.PowerWeightedCapability != 21 {
		t.Errorf("Components.PowerWeightedCapability=%v, want 21", got.Components.PowerWeightedCapability)
	}
	if got.Components.PowerHintFit != 0 {
		t.Errorf("Components.PowerHintFit=%v, want 0 within bounds", got.Components.PowerHintFit)
	}
	if got.Components.LatencyWeight != 9 {
		t.Errorf("Components.LatencyWeight=%v, want 9", got.Components.LatencyWeight)
	}
	if got.Components.PlacementBonus != 265 {
		t.Errorf("Components.PlacementBonus=%v, want 265", got.Components.PlacementBonus)
	}
	if got.Components.QuotaBonus != 12 {
		t.Errorf("Components.QuotaBonus=%v, want 12", got.Components.QuotaBonus)
	}
	if got.Components.MarginalCostPenalty != 6 {
		t.Errorf("Components.MarginalCostPenalty=%v, want 6", got.Components.MarginalCostPenalty)
	}
	if got.Components.AvailabilityPenalty != 4 {
		t.Errorf("Components.AvailabilityPenalty=%v, want 4", got.Components.AvailabilityPenalty)
	}
	if got.Components.StaleSignalPenalty != 0 {
		t.Errorf("Components.StaleSignalPenalty=%v, want 0", got.Components.StaleSignalPenalty)
	}
	if got.FilterReason != "" {
		t.Errorf("eligible candidate FilterReason=%q, want empty", got.FilterReason)
	}

	raw, err := json.Marshal(got.Components)
	if err != nil {
		t.Fatalf("marshal components: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal components: %v", err)
	}
	for _, key := range []string{"power_weighted_capability", "power_hint_fit", "latency_weight", "placement_bonus", "quota_bonus", "marginal_cost_penalty", "availability_penalty", "stale_signal_penalty"} {
		if _, ok := generic[key]; !ok {
			t.Fatalf("missing SD-005 component %q in %s", key, raw)
		}
	}
}

// TestRouteCandidateFilterReasonClassification verifies the public
// FilterReason enum surfaces canonical values for ineligible candidates.
// The mapping is a typed passthrough: the internal engine sets
// routing.FilterReason at the rejection site, and routeCandidateFromInternal
// forwards it to the public string surface without parsing the free-form
// Reason text (agent-2c55b8a4).
func TestRouteCandidateFilterReasonClassification(t *testing.T) {
	cases := []struct {
		name     string
		typed    routing.FilterReason
		want     string
		freeform string // arbitrary text — must not influence classification
	}{
		{"context too small", routing.FilterReasonContextTooSmall, FilterReasonContextTooSmall, "ctx window 4096 < required 8000"},
		{"no tool support", routing.FilterReasonNoToolSupport, FilterReasonNoToolSupport, "tools unavailable"},
		{"reasoning unsupported", routing.FilterReasonReasoningUnsupported, FilterReasonReasoningUnsupported, "thinking high not advertised"},
		{"unhealthy", routing.FilterReasonUnhealthy, FilterReasonUnhealthy, "harness offline"},
		{"scored below top", routing.FilterReasonScoredBelowTop, FilterReasonScoredBelowTop, ""},
		{"above max power", routing.FilterReasonAboveMaxPower, FilterReasonAboveMaxPower, "power 10 exceeds max_power=8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeCandidateFromInternal(routing.Candidate{
				Eligible:     false,
				Reason:       tc.freeform,
				FilterReason: tc.typed,
			}, RoutePowerPolicy{})
			if got.FilterReason != tc.want {
				t.Errorf("typed=%q (Reason=%q): FilterReason=%q, want %q", tc.typed, tc.freeform, got.FilterReason, tc.want)
			}
		})
	}
}

// TestResolveRouteIsInformationalOnly verifies that ResolveRoute returns
// ranked candidates through the engine flow.
func TestResolveRouteIsInformationalOnly(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: "model-a"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	svc := publicRouteTraceService(sc)

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{
		Harness: "fiz",
		Model:   "model-a",
	})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if dec == nil || len(dec.Candidates) == 0 {
		t.Fatalf("expected engine-flow decision, got %#v", dec)
	}
}

// TestRoutingDecisionEventComponentsCarriesPerCandidateScores verifies
// the routing-decision event payload includes per-candidate component
// scores and filter_reason fields (ADR-005 AC#5).
func TestRoutingDecisionEventComponentsCarriesPerCandidateScores(t *testing.T) {
	candidates := []RouteCandidate{
		{
			Harness:            "fiz",
			Provider:           "alpha",
			Billing:            BillingModelFixed,
			Model:              "alpha-1",
			Score:              80,
			CostUSDPer1kTokens: 0.002,
			CostSource:         routing.CostSourceCatalog,
			Eligible:           true,
			Reason:             "profile=cheap; score=80.0",
			Components: RouteCandidateComponents{
				Power:                   7,
				Cost:                    0.002,
				CostClass:               "local",
				LatencyMS:               120,
				SpeedTPS:                40,
				Utilization:             0.25,
				SuccessRate:             0.9,
				QuotaOK:                 true,
				QuotaPercentUsed:        20,
				QuotaTrend:              routing.QuotaTrendHealthy,
				Capability:              1,
				StickyAffinity:          250,
				PowerWeightedCapability: 19,
				PowerHintFit:            0,
				LatencyWeight:           12,
				PlacementBonus:          265,
				QuotaBonus:              12,
				MarginalCostPenalty:     0,
				AvailabilityPenalty:     4,
				StaleSignalPenalty:      0,
			},
		},
		{
			Harness:      "fiz",
			Provider:     "beta",
			Model:        "beta-1",
			Eligible:     false,
			Reason:       "context window 4096 < required 8000",
			FilterReason: FilterReasonContextTooSmall,
		},
	}
	out := routingDecisionEventCandidates(candidates)
	if len(out) != 2 {
		t.Fatalf("len(out)=%d, want 2", len(out))
	}
	if out[0].Billing != BillingModelFixed {
		t.Errorf("first candidate Billing=%q, want %q", out[0].Billing, BillingModelFixed)
	}
	if out[0].Components.LatencyMS != 120 {
		t.Errorf("first candidate LatencyMS=%v, want 120", out[0].Components.LatencyMS)
	}
	if out[0].Components.SuccessRate != 0.9 {
		t.Errorf("first candidate SuccessRate=%v, want 0.9", out[0].Components.SuccessRate)
	}
	if out[0].Components.Power != 7 || out[0].Components.SpeedTPS != 40 || out[0].Components.QuotaTrend != routing.QuotaTrendHealthy {
		t.Errorf("first candidate Components=%#v, want power/speed/quota carried through", out[0].Components)
	}
	if out[0].Components.Utilization != 0.25 || out[0].Components.StickyAffinity != 250 {
		t.Errorf("first candidate Components=%#v, want utilization/sticky affinity carried through", out[0].Components)
	}
	if out[0].Components.PowerWeightedCapability != 19 || out[0].Components.LatencyWeight != 12 || out[0].Components.PlacementBonus != 265 {
		t.Errorf("first candidate SD-005 components=%#v, want power/latency/placement carried through", out[0].Components)
	}
	if out[0].Components.QuotaBonus != 12 || out[0].Components.AvailabilityPenalty != 4 {
		t.Errorf("first candidate SD-005 quota/availability components=%#v, want carry through", out[0].Components)
	}
	raw, err := json.Marshal(out[0].Components)
	if err != nil {
		t.Fatalf("marshal routing-decision components: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal routing-decision components: %v", err)
	}
	for _, key := range []string{"power_weighted_capability", "power_hint_fit", "latency_weight", "placement_bonus", "quota_bonus", "marginal_cost_penalty", "availability_penalty", "stale_signal_penalty"} {
		if _, ok := generic[key]; !ok {
			t.Fatalf("missing SD-005 component %q in %s", key, raw)
		}
	}
	if out[0].FilterReason != "" {
		t.Errorf("eligible candidate event FilterReason=%q, want empty", out[0].FilterReason)
	}
	if out[1].FilterReason != FilterReasonContextTooSmall {
		t.Errorf("rejected candidate event FilterReason=%q, want %q", out[1].FilterReason, FilterReasonContextTooSmall)
	}
}

func TestRoutingDecisionEventCandidatesExposeRawScoreComponents(t *testing.T) {
	candidates := []RouteCandidate{
		{
			Harness: "fiz",
			Model:   "alpha-1",
			ScoreComponents: map[string]float64{
				"base":                100,
				"power":               18,
				"cost":                -4,
				"quota_health":        6,
				"deployment_locality": 12,
				"utilization":         -3,
				"context_headroom":    0.15,
				"performance":         9,
			},
		},
		{
			Harness: "codex",
			Model:   "beta-1",
			ScoreComponents: map[string]float64{
				"base":                100,
				"power":               21,
				"cost":                0,
				"quota_health":        12,
				"deployment_locality": 0,
				"utilization":         5,
				"context_headroom":    0.25,
				"performance":         14,
			},
		},
	}

	out := routingDecisionEventCandidates(candidates)
	if len(out) != len(candidates) {
		t.Fatalf("len(out)=%d, want %d", len(out), len(candidates))
	}
	for i, candidate := range candidates {
		for key, want := range candidate.ScoreComponents {
			if got := out[i].ScoreComponents[key]; got != want {
				t.Fatalf("candidate %d ScoreComponents[%q]=%v, want %v in %#v", i, key, got, want, out[i].ScoreComponents)
			}
		}
		if out[i].Components != (ServiceRoutingDecisionComponents{}) {
			t.Fatalf("candidate %d aggregate Components=%#v, want untouched zero aggregate", i, out[i].Components)
		}
	}
	candidates[0].ScoreComponents["base"] = -999
	if out[0].ScoreComponents["base"] != 100 {
		t.Fatalf("event ScoreComponents aliases route candidate map; base=%v, want copied 100", out[0].ScoreComponents["base"])
	}

	raw, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal routing-decision candidate: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal routing-decision candidate: %v", err)
	}
	if _, ok := generic["score_components"]; !ok {
		t.Fatalf("missing score_components in %s", raw)
	}
}

// TestRoutingDecisionRejectedCandidatesMatchSnapshotEvidence verifies that
// rejected routing candidates keep typed filter reasons and enough identity
// to be matched back to list-models rows by provider/model/endpoint/server.
func TestRoutingDecisionRejectedCandidatesMatchSnapshotEvidence(t *testing.T) {
	// Isolate discovery: an empty PATH makes subscription harnesses (claude/
	// codex/gemini) unavailable so ListModels never spawns a live PTY scrape,
	// and a temp cache dir keeps the snapshot cache hermetic.
	t.Setenv("FIZEAU_CACHE_DIR", tempDiscoveryCacheDir(t))
	t.Setenv("PATH", "")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "qwen3.5-27b"}},
		})
	}))
	t.Cleanup(modelsSrv.Close)

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {
				Type:                "lmstudio",
				BaseURL:             modelsSrv.URL + "/v1",
				IncludeByDefault:    false,
				IncludeByDefaultSet: true,
				Endpoints: []ServiceProviderEndpoint{
					{Name: "primary", BaseURL: modelsSrv.URL + "/v1"},
					{Name: "backup", BaseURL: modelsSrv.URL + "/v1"},
				},
				Model: "qwen3.5-27b",
			},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := publicRouteTraceService(sc)

	rows, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// Filter to only fiz-harness models (HTTP providers). The unfiltered ListModels
	// now includes subscription-harness tiers (claude/codex/gemini), so we filter
	// to only the HTTP provider models for this test.
	var fizRows []ModelInfo
	for _, row := range rows {
		if row.Harness == "fiz" || row.Harness == "" {
			fizRows = append(fizRows, row)
		}
	}
	if len(fizRows) != 2 {
		t.Fatalf("ListModels fiz rows = %d, want 2 (all rows=%#v)", len(fizRows), rows)
	}
	want := make(map[string]ModelInfo, len(fizRows))
	for _, row := range fizRows {
		key := row.Provider + "\x00" + row.ID + "\x00" + row.EndpointName + "\x00" + row.ServerInstance
		want[key] = row
	}

	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Provider:  "bragi@primary",
		Endpoint:  "primary",
		Model:     "qwen3.5-27b",
		Status:    "failed",
		Reason:    "route_attempt_failure",
		Timestamp: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("RecordRouteAttempt: %v", err)
	}

	dec, err := svc.ResolveRoute(context.Background(), RouteRequest{})
	if dec == nil {
		t.Fatalf("ResolveRoute returned nil decision: %v", err)
	}

	sawRejected := false
	for _, cand := range dec.Candidates {
		if cand.Harness != "fiz" || cand.Provider == "" {
			continue
		}
		baseProvider, endpoint, ok := splitEndpointProviderRef(cand.Provider)
		if !ok {
			baseProvider = cand.Provider
		}
		if cand.Endpoint != "" {
			endpoint = cand.Endpoint
		}
		key := baseProvider + "\x00" + cand.Model + "\x00" + endpoint + "\x00" + cand.ServerInstance
		if _, ok := want[key]; !ok {
			t.Fatalf("candidate %q does not match a list-models row; candidates=%#v rows=%#v", key, dec.Candidates, rows)
		}
		if !cand.Eligible {
			sawRejected = true
			if cand.FilterReason == "" {
				t.Fatalf("rejected candidate missing typed filter reason: %#v", cand)
			}
		}
	}
	if !sawRejected {
		t.Fatalf("expected at least one rejected candidate: %#v", dec.Candidates)
	}
}
