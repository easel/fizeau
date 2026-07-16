package fizeau

import (
	"context"
	"testing"
)

func TestExecuteRouteRequestCarriesMaxTokens(t *testing.T) {
	got := executeRouteRequest(ServiceExecuteRequest{MaxTokens: 4096})
	if got.MaxTokens != 4096 {
		t.Fatalf("MaxTokens=%d, want 4096", got.MaxTokens)
	}
}

func TestOverrideRouteRequestCarriesMaxTokens(t *testing.T) {
	got := overrideRouteRequest(ServiceExecuteRequest{
		Harness:   "fiz",
		Provider:  "local",
		Model:     "model-a",
		MaxTokens: 2048,
	}, []string{overrideAxisHarness, overrideAxisProvider, overrideAxisModel})
	if got.MaxTokens != 2048 {
		t.Fatalf("MaxTokens=%d, want 2048", got.MaxTokens)
	}
	if got.Harness != "" || got.Provider != "" || got.Model != "" {
		t.Fatalf("override axes were not stripped: %#v", got)
	}
}

func TestResolveRouteRejectsNegativeMaxTokens(t *testing.T) {
	var service *service
	decision, err := service.ResolveRoute(context.Background(), RouteRequest{MaxTokens: -1})
	if decision != nil || err == nil || err.Error() != "invalid MaxTokens -1: must be >= 0" {
		t.Fatalf("ResolveRoute decision=%#v err=%v, want synchronous MaxTokens validation", decision, err)
	}
}

func TestExecuteRejectsNegativeMaxTokensBeforeSession(t *testing.T) {
	var service *service
	events, err := service.Execute(context.Background(), ServiceExecuteRequest{MaxTokens: -1})
	if events != nil || err == nil || err.Error() != "invalid MaxTokens -1: must be >= 0" {
		t.Fatalf("Execute events=%#v err=%v, want pre-session MaxTokens validation", events, err)
	}
}
