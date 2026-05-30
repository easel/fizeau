package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
)

func TestExecuteHTTPProviderHarnessResolvesByType(t *testing.T) {
	cases := []struct {
		name         string
		harness      string
		providerName string
		providerType string
	}{
		{name: "lmstudio", harness: "lmstudio", providerName: "bragi", providerType: "lmstudio"},
		{name: "omlx", harness: "omlx", providerName: "vidar-omlx", providerType: "omlx"},
		{name: "openrouter", harness: "openrouter", providerName: "router", providerType: "openrouter"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := openAIChatServer(t, "pong")
			defer srv.Close()

			sc := &fakeServiceConfig{
				providers: map[string]ServiceProviderEntry{
					tc.providerName: {
						Type:    tc.providerType,
						BaseURL: srv.URL + "/v1",
						APIKey:  "test",
						Model:   "configured-model",
					},
				},
				names:       []string{tc.providerName},
				defaultName: tc.providerName,
			}
			svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			final := executeAndFinal(t, svc, ServiceExecuteRequest{
				Prompt:          "ping",
				Harness:         tc.harness,
				Timeout:         5 * time.Second,
				ProviderTimeout: 2 * time.Second,
			})
			if final.Status != "success" {
				t.Fatalf("Status = %q, want success (error=%q)", final.Status, final.Error)
			}
			if final.RoutingActual == nil {
				t.Fatal("RoutingActual is nil")
			}
			if final.RoutingActual.Harness != tc.harness {
				t.Fatalf("RoutingActual.Harness = %q, want %q", final.RoutingActual.Harness, tc.harness)
			}
			if final.RoutingActual.Provider != tc.providerName {
				t.Fatalf("RoutingActual.Provider = %q, want %q", final.RoutingActual.Provider, tc.providerName)
			}
			if final.RoutingActual.Model != "configured-model" {
				t.Fatalf("RoutingActual.Model = %q, want configured-model", final.RoutingActual.Model)
			}
		})
	}
}

func TestResolveConfiguredNativeProviderSelectionOrder(t *testing.T) {
	t.Run("exact name wins over type fallback", func(t *testing.T) {
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"lmstudio": {Type: "openai", BaseURL: "http://exact.invalid/v1", Model: "exact-model"},
				"bragi":    {Type: "lmstudio", BaseURL: "http://bragi.invalid/v1", Model: "type-model"},
			},
			names:       []string{"bragi", "lmstudio"},
			defaultName: "bragi",
		}
		svc := &service{opts: ServiceOptions{ServiceConfig: sc}}
		got := svc.resolveConfiguredNativeProvider(ServiceExecuteRequest{Provider: "lmstudio"})
		if got.Name != "lmstudio" {
			t.Fatalf("resolved provider = %q, want literal lmstudio", got.Name)
		}
		if got.Entry.Model != "exact-model" {
			t.Fatalf("resolved model = %q, want exact-model", got.Entry.Model)
		}
	})

	t.Run("matching default provider is preferred", func(t *testing.T) {
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: "http://bragi.invalid/v1"},
				"vidar": {Type: "lmstudio", BaseURL: "http://vidar.invalid/v1"},
			},
			names:       []string{"vidar", "bragi"},
			defaultName: "bragi",
		}
		svc := &service{opts: ServiceOptions{ServiceConfig: sc}}
		got := svc.resolveConfiguredNativeProvider(ServiceExecuteRequest{Harness: "lmstudio"})
		if got.Name != "bragi" {
			t.Fatalf("resolved provider = %q, want default bragi", got.Name)
		}
	})

	t.Run("stable order is used when default type mismatches", func(t *testing.T) {
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"router": {Type: "openrouter", BaseURL: "http://router.invalid/v1"},
				"vidar":  {Type: "lmstudio", BaseURL: "http://vidar.invalid/v1"},
				"bragi":  {Type: "lmstudio", BaseURL: "http://bragi.invalid/v1"},
			},
			names:       []string{"router", "vidar", "bragi"},
			defaultName: "router",
		}
		svc := &service{opts: ServiceOptions{ServiceConfig: sc}}
		got := svc.resolveConfiguredNativeProvider(ServiceExecuteRequest{Harness: "lmstudio"})
		if got.Name != "vidar" {
			t.Fatalf("resolved provider = %q, want first matching ProviderNames entry vidar", got.Name)
		}
	})

	t.Run("fiz harness still falls back to default provider", func(t *testing.T) {
		sc := &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"bragi": {Type: "lmstudio", BaseURL: "http://bragi.invalid/v1"},
			},
			names:       []string{"bragi"},
			defaultName: "bragi",
		}
		svc := &service{opts: ServiceOptions{ServiceConfig: sc}}
		got := svc.resolveConfiguredNativeProvider(ServiceExecuteRequest{Harness: "fiz"})
		if got.Name != "bragi" {
			t.Fatalf("resolved provider = %q, want default bragi", got.Name)
		}
	})
}

func TestResolveConfiguredNativeProviderSkipsUnreachableDefaultProbe(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"lmstudio": {
				Type:           "lmstudio",
				BaseURL:        "http://grendel.invalid/v1",
				ServerInstance: "grendel",
				Model:          "model-a",
				Endpoints: []ServiceProviderEndpoint{{
					Name:           "desk-a",
					BaseURL:        "http://grendel.invalid/v1",
					ServerInstance: "desk-a",
				}},
			},
		},
		names:       []string{"lmstudio"},
		defaultName: "lmstudio",
	}
	svc := newResolveRouteProbeTestService(t, sc, func(context.Context, string, string) bool {
		return false
	})
	svc.providerProbe.RecordProbe("lmstudio", "desk-a", false, time.Now().UTC())

	got := svc.resolveConfiguredNativeProvider(ServiceExecuteRequest{})
	if got.Provider != nil || got.Name != "" || got.Entry.Model != "" {
		t.Fatalf("resolveConfiguredNativeProvider returned %#v, want zero value when default provider probe is unreachable", got)
	}
}

func TestExecuteHTTPProviderHarnessNoMatchDiagnostic(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"router":     {Type: "openrouter", BaseURL: "http://router.invalid/v1"},
			"vidar-omlx": {Type: "omlx", BaseURL: "http://omlx.invalid/v1"},
		},
		names:       []string{"router", "vidar-omlx"},
		defaultName: "router",
	}
	svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	final := executeAndFinal(t, svc, ServiceExecuteRequest{
		Prompt:  "ping",
		Harness: "lmstudio",
		Timeout: 2 * time.Second,
	})
	if final.Status != "failed" {
		t.Fatalf("Status = %q, want failed", final.Status)
	}
	for _, want := range []string{
		`harness "lmstudio": no configured provider matches type "lmstudio"`,
		"router (openrouter)",
		"vidar-omlx (omlx)",
	} {
		if !strings.Contains(final.Error, want) {
			t.Fatalf("error %q does not contain %q", final.Error, want)
		}
	}
}

func TestExecuteEndpointFirstRoutingSkipsDeadAndNormalizesModel(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cache := &discoverycache.Cache{Root: cacheDir}

	var deadChatCalls atomic.Int64
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			deadChatCalls.Add(1)
		}
		http.Error(w, "dead endpoint should be skipped", http.StatusInternalServerError)
	}))
	defer dead.Close()

	live := openAIModelChatServer(t, []string{"Qwen3.6-35B-A3B-4bit"}, "Qwen3.6-35B-A3B-4bit", "pong")
	defer live.Close()
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", live.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"Qwen3.6-35B-A3B-4bit"})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"dead": {Type: "lmstudio", BaseURL: dead.URL + "/v1"},
			"live": {Type: "omlx", BaseURL: live.URL + "/v1"},
		},
		names:       []string{"dead", "live"},
		defaultName: "dead",
	}
	svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	final := executeAndFinal(t, svc, ServiceExecuteRequest{
		Prompt:          "ping",
		Model:           "qwen3.6",
		Timeout:         5 * time.Second,
		ProviderTimeout: 2 * time.Second,
	})
	if final.Status != "success" {
		t.Fatalf("Status = %q, want success (error=%q)", final.Status, final.Error)
	}
	if final.RoutingActual == nil {
		t.Fatal("RoutingActual is nil")
	}
	if final.RoutingActual.Provider != "live" {
		t.Fatalf("RoutingActual.Provider = %q, want live", final.RoutingActual.Provider)
	}
	if final.RoutingActual.Model != "Qwen3.6-35B-A3B-4bit" {
		t.Fatalf("RoutingActual.Model = %q, want normalized server model", final.RoutingActual.Model)
	}
	if got := deadChatCalls.Load(); got != 0 {
		t.Fatalf("dead endpoint chat calls = %d, want 0", got)
	}
}

func TestExecuteEndpointFirstRoutingIgnoresStaleCacheForDeadEndpoint(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cache := &discoverycache.Cache{Root: cacheDir}

	var deadChatCalls atomic.Int64
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			deadChatCalls.Add(1)
		}
		http.Error(w, "dead endpoint should be skipped", http.StatusInternalServerError)
	}))
	defer dead.Close()

	live := openAIModelChatServer(t, []string{"Qwen3.6-35B-A3B-4bit"}, "Qwen3.6-35B-A3B-4bit", "pong")
	defer live.Close()
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", live.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"Qwen3.6-35B-A3B-4bit"})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"dead": {Type: "lmstudio", BaseURL: dead.URL + "/v1"},
			"live": {Type: "omlx", BaseURL: live.URL + "/v1"},
		},
		names:       []string{"dead", "live"},
		defaultName: "dead",
	}
	rawSvc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := rawSvc.(*service)

	cacheKey := newCatalogCacheKey(dead.URL+"/v1", "", nil)
	_, err = svc.catalog.Get(context.Background(), cacheKey, func(context.Context) ([]string, error) {
		return []string{"Qwen3.6-35B-A3B-4bit"}, nil
	})
	if err != nil {
		t.Fatalf("seed catalog cache: %v", err)
	}

	final := executeAndFinal(t, svc, ServiceExecuteRequest{
		Prompt:          "ping",
		Model:           "qwen3.6",
		Timeout:         5 * time.Second,
		ProviderTimeout: 2 * time.Second,
	})
	if final.Status != "success" {
		t.Fatalf("Status = %q, want success (error=%q)", final.Status, final.Error)
	}
	if final.RoutingActual == nil || final.RoutingActual.Provider != "live" {
		t.Fatalf("RoutingActual = %#v, want provider live", final.RoutingActual)
	}
	if got := deadChatCalls.Load(); got != 0 {
		t.Fatalf("dead endpoint chat calls = %d, want 0", got)
	}
}

func TestExecuteEndpointFirstRoutingUsesMetricsBeforeConfigOrder(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cache := &discoverycache.Cache{Root: cacheDir}

	slow := openAIModelChatServer(t, []string{"Qwen3.6-35B-A3B-4bit"}, "Qwen3.6-35B-A3B-4bit", "slow")
	defer slow.Close()
	fast := openAIModelChatServer(t, []string{"Qwen3.6-35B-A3B-4bit"}, "Qwen3.6-35B-A3B-4bit", "fast")
	defer fast.Close()
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("slow", "slow", slow.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"Qwen3.6-35B-A3B-4bit"})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("fast", "fast", fast.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"Qwen3.6-35B-A3B-4bit"})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"slow": {Type: "lmstudio", BaseURL: slow.URL + "/v1"},
			"fast": {Type: "lmstudio", BaseURL: fast.URL + "/v1"},
		},
		names:       []string{"slow", "fast"},
		defaultName: "slow",
	}
	svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:  "fiz",
		Provider: "slow",
		Model:    "Qwen3.6-35B-A3B-4bit",
		Status:   "success",
		Duration: 5 * time.Second,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt slow: %v", err)
	}
	if err := svc.RecordRouteAttempt(context.Background(), RouteAttempt{
		Harness:  "fiz",
		Provider: "fast",
		Model:    "Qwen3.6-35B-A3B-4bit",
		Status:   "success",
		Duration: 100 * time.Millisecond,
	}); err != nil {
		t.Fatalf("RecordRouteAttempt fast: %v", err)
	}

	final := executeAndFinal(t, svc, ServiceExecuteRequest{
		Prompt:          "ping",
		Model:           "qwen3.6",
		Timeout:         5 * time.Second,
		ProviderTimeout: 2 * time.Second,
	})
	if final.Status != "success" {
		t.Fatalf("Status = %q, want success (error=%q)", final.Status, final.Error)
	}
	if final.RoutingActual == nil || final.RoutingActual.Provider != "fast" {
		t.Fatalf("RoutingActual = %#v, want provider fast", final.RoutingActual)
	}
	if final.FinalText != "fast" {
		t.Fatalf("FinalText = %q, want fast", final.FinalText)
	}
}

func TestExecuteEndpointFirstRoutingNoLiveModelMatchDiagnostic(t *testing.T) {
	t.Setenv("PATH", "")
	cacheDir := t.TempDir()
	t.Setenv("FIZEAU_CACHE_DIR", cacheDir)
	cache := &discoverycache.Cache{Root: cacheDir}

	live := openAIModelChatServer(t, []string{"other-model"}, "other-model", "pong")
	defer live.Close()
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("live", "live", live.URL+"/v1", ""), time.Date(2026, 5, 12, 15, 0, 0, 0, time.UTC), []string{"other-model"})

	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"live": {Type: "lmstudio", BaseURL: live.URL + "/v1"},
		},
		names:       []string{"live"},
		defaultName: "live",
	}
	svc, err := New(ServiceOptions{ServiceConfig: sc, QuotaRefreshContext: canceledRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:  "ping",
		Model:   "zxqv-no-match-20260506",
		Policy:  "air-gapped",
		Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected execute to fail with no-match model constraint")
	}
	var typed *ErrModelConstraintNoMatch
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrModelConstraintNoMatch: %T %v", err, err)
	}
	if typed.Model != "zxqv-no-match-20260506" {
		t.Fatalf("Model=%q, want zxqv-no-match-20260506", typed.Model)
	}
	if len(typed.Candidates) == 0 {
		t.Fatal("expected no-match diagnostic to include nearby candidates")
	}
}

func openAIChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "server-model",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": content,
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     1,
				"completion_tokens": 1,
				"total_tokens":      2,
			},
		})
	}))
}

func openAIModelChatServer(t *testing.T, models []string, wantModel, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			data := make([]map[string]string, 0, len(models))
			for _, id := range models {
				data = append(data, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/chat/completions":
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if payload.Model != wantModel {
				http.Error(w, "unexpected model "+payload.Model, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   wantModel,
				"choices": []map[string]any{
					{
						"index": 0,
						"message": map[string]any{
							"role":    "assistant",
							"content": content,
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     1,
					"completion_tokens": 1,
					"total_tokens":      2,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func executeAndFinal(t *testing.T, svc FizeauService, req ServiceExecuteRequest) harnesses.FinalData {
	t.Helper()
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var final *harnesses.FinalData
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if final == nil {
					t.Fatal("execute channel closed without final event")
				}
				return *final
			}
			if ev.Type == harnesses.EventTypeFinal {
				var parsed harnesses.FinalData
				if err := json.Unmarshal(ev.Data, &parsed); err != nil {
					t.Fatalf("unmarshal final: %v", err)
				}
				final = &parsed
			}
		case <-timer.C:
			t.Fatal("timed out waiting for final event")
		}
	}
}
