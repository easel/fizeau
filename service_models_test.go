package fizeau

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/serverinstance"
)

// fakeModelsServer returns an httptest.Server that serves the given model IDs from /v1/models.
func fakeModelsServer(models []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			data := make([]map[string]any, len(models))
			for i, m := range models {
				data[i] = map[string]any{"id": m}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		http.NotFound(w, r)
	}))
}

func TestListModels_noServiceConfig(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	_, err := svc.ListModels(context.Background(), ModelFilter{})
	if err == nil {
		t.Fatal("expected error when ServiceConfig is nil")
	}
}

func TestListModels_utilizationProjection(t *testing.T) {
	server := fakeModelsServer([]string{"qwen3.5-27b"})
	defer server.Close()

	config := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"bragi": {Type: "lmstudio", BaseURL: server.URL + "/v1"},
		},
		names:       []string{"bragi"},
		defaultName: "bragi",
	}
	svc := newTestService(t, ServiceOptions{ServiceConfig: config})
	svc.routeSticky = routehealth.NewStickyState()
	instance := serverinstance.FromBaseURL(server.URL + "/v1")
	svc.routeSticky.RecordUtilization("bragi", instance, "qwen3.5-27b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(2),
		QueuedRequests: utilization.Int(1),
		Source:         utilization.SourceVLLMMetrics,
		Freshness:      utilization.FreshnessFresh,
	})

	infos, err := svc.ListModels(context.Background(), ModelFilter{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("want 1 model, got %d: %v", len(infos), modelInfoDebug(infos))
	}
	got := infos[0].Utilization
	if got.Source != string(utilization.SourceVLLMMetrics) || got.Freshness != string(utilization.FreshnessFresh) {
		t.Fatalf("utilization source/freshness = %#v, want fresh vllm metrics", got)
	}
	if got.ActiveRequests == nil || *got.ActiveRequests != 2 {
		t.Errorf("utilization active = %#v, want 2", got.ActiveRequests)
	}
	if got.QueuedRequests == nil || *got.QueuedRequests != 1 {
		t.Errorf("utilization queued = %#v, want 1", got.QueuedRequests)
	}
}

func modelInfoDebug(infos []ModelInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.Provider + ":" + info.ID + "(billing=" + string(info.Billing) + ")"
	}
	return out
}
