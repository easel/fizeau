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
