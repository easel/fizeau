package serviceimpl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/runtimesignals"
)

func TestMain(m *testing.M) {
	os.Exit(runServiceimplRuntimeSignalTests(m))
}

func runServiceimplRuntimeSignalTests(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "serviceimpl-runtime-cache-*")
	if err != nil {
		panic(err)
	}

	os.Setenv("FIZEAU_CACHE_DIR", filepath.Join(tmp, "cache"))
	code := m.Run()
	_ = os.RemoveAll(tmp)
	return code
}

func TestRunNative_RuntimeSignalWritesCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ratelimit-remaining-requests", "9")
		w.Header().Set("x-ratelimit-reset-requests", "100ms")
		time.Sleep(10 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-1",
			"model": "gpt-4o",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "pong",
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
	defer srv.Close()

	provider := openai.New(openai.Config{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test",
		Model:   "gpt-4o",
	})

	var final harnesses.FinalData
	RunNative(context.Background(), NativeRequest{
		Prompt:   "hello",
		NoStream: true,
		Decision: NativeDecision{
			Harness:  "fiz",
			Provider: "openai",
			Model:    "gpt-4o",
		},
		Started: time.Now(),
	}, NativeCallbacks{
		ResolveProvider: func(_ NativeProviderRequest) NativeProviderResolution {
			return NativeProviderResolution{
				Provider: provider,
				Name:     "openai",
				Model:    "gpt-4o",
			}
		},
		Finalize: func(fd harnesses.FinalData, _ TerminalOrigin) {
			final = fd
		},
	})

	if final.Status != "success" {
		t.Fatalf("Status = %q, want success", final.Status)
	}

	cacheRoot := filepath.Join(os.Getenv("FIZEAU_CACHE_DIR"))
	sig, ok := runtimesignals.ReadCached(&discoverycache.Cache{Root: cacheRoot}, "openai")
	if !ok {
		t.Fatal("expected runtime cache entry for openai")
	}
	if sig.Status != runtimesignals.StatusAvailable {
		t.Fatalf("Status = %q, want available", sig.Status)
	}
	if sig.QuotaRemaining == nil || *sig.QuotaRemaining != 9 {
		t.Fatalf("QuotaRemaining = %#v, want 9", sig.QuotaRemaining)
	}
	if sig.RecentP50Latency <= 0 {
		t.Fatalf("RecentP50Latency = %v, want > 0", sig.RecentP50Latency)
	}
}
