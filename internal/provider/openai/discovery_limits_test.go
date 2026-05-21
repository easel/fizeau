package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/easel/fizeau/internal/provider/lmstudio"
	"github.com/easel/fizeau/internal/provider/omlx"
	"github.com/easel/fizeau/internal/provider/openrouter"
	"github.com/stretchr/testify/assert"
)

// lmStudioServer returns an httptest server that serves /api/v0/models/{model}
// with the given loaded and max context lengths, and /v1/models for probing.
func lmStudioServer(loaded, max int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list",
				"data": []map[string]interface{}{
					{"id": "test-model", "object": "model"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
}

// omlxServer returns an httptest server that serves /v1/models/status with
// a single model entry, and /v1/models for probing.
func omlxServer(modelID string, maxContext, maxTokens int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/status" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{
						"id":                 modelID,
						"max_context_window": maxContext,
						"max_tokens":         maxTokens,
					},
				},
			})
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list",
				"data": []map[string]interface{}{
					{"id": modelID, "object": "model"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
}

// openrouterServer returns an httptest server that serves /v1/models with
// a single model entry.
func openrouterServer(modelID string, contextLength, maxTokens int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"id":             modelID,
					"context_length": contextLength,
					"top_provider": map[string]interface{}{
						"max_completion_tokens": maxTokens,
					},
				},
			},
		})
	}))
}

func TestLookupModelLimits_LMStudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models/qwen3.5-27b" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                    "qwen3.5-27b",
				"loaded_context_length": 2048,
				"max_context_length":    4096,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	limits := lmstudio.LookupModelLimits(context.Background(), srv.URL+"/v1", "qwen3.5-27b")
	assert.Equal(t, 2048, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_LMStudio_FallsBackToMaxContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models/qwen3.5-27b" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                    "qwen3.5-27b",
				"loaded_context_length": 0,
				"max_context_length":    4096,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	limits := lmstudio.LookupModelLimits(context.Background(), srv.URL+"/v1", "qwen3.5-27b")
	assert.Equal(t, 4096, limits.ContextLength)
}

func TestLookupModelLimits_LMStudio_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	limits := lmstudio.LookupModelLimits(context.Background(), srv.URL+"/v1", "qwen3.5-27b")
	assert.Equal(t, 0, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_Omlx(t *testing.T) {
	srv := omlxServer("Qwen3.5-27B-4bit", 3000, 1500)
	defer srv.Close()

	limits := omlx.LookupModelLimits(context.Background(), srv.URL+"/v1", "Qwen3.5-27B-4bit")
	assert.Equal(t, 3000, limits.ContextLength)
	assert.Equal(t, 1500, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_Omlx_CaseInsensitive(t *testing.T) {
	srv := omlxServer("Qwen3.5-27B-4bit", 3000, 1500)
	defer srv.Close()

	limits := omlx.LookupModelLimits(context.Background(), srv.URL+"/v1", "qwen3.5-27b-4bit")
	assert.Equal(t, 3000, limits.ContextLength)
	assert.Equal(t, 1500, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_Omlx_UnknownModel(t *testing.T) {
	srv := omlxServer("Qwen3.5-27B-4bit", 3000, 1500)
	defer srv.Close()

	limits := omlx.LookupModelLimits(context.Background(), srv.URL+"/v1", "nonexistent-model")
	assert.Equal(t, 0, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_Omlx_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	limits := omlx.LookupModelLimits(context.Background(), srv.URL+"/v1", "Qwen3.5-27B-4bit")
	assert.Equal(t, 0, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_OpenRouter(t *testing.T) {
	srv := openrouterServer("openai/gpt-4o-mini", 128000, 4096)
	defer srv.Close()

	limits := openrouter.LookupModelLimits(context.Background(), srv.URL+"/v1", "sk-test", nil, "openai/gpt-4o-mini")
	assert.Equal(t, 128000, limits.ContextLength)
	assert.Equal(t, 4096, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_OpenRouter_WithHeaders(t *testing.T) {
	srv := openrouterServer("custom/model", 2048, 512)
	defer srv.Close()

	headers := map[string]string{"X-Custom": "value"}
	limits := openrouter.LookupModelLimits(context.Background(), srv.URL+"/v1", "sk-test", headers, "custom/model")
	assert.Equal(t, 2048, limits.ContextLength)
	assert.Equal(t, 512, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_OpenRouter_UnknownModel(t *testing.T) {
	srv := openrouterServer("openai/gpt-4o-mini", 128000, 4096)
	defer srv.Close()

	limits := openrouter.LookupModelLimits(context.Background(), srv.URL+"/v1", "sk-test", nil, "nonexistent-model")
	assert.Equal(t, 0, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}

func TestLookupModelLimits_OpenRouter_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	limits := openrouter.LookupModelLimits(context.Background(), srv.URL+"/v1", "sk-test", nil, "openai/gpt-4o-mini")
	assert.Equal(t, 0, limits.ContextLength)
	assert.Equal(t, 0, limits.MaxCompletionTokens)
}
