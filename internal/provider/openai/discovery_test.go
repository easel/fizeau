package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "qwen3.5-27b", "object": "model"},
				{"id": "llama3.1-8b", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	models, err := DiscoverModels(context.Background(), srv.URL+"/v1", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"qwen3.5-27b", "llama3.1-8b"}, models)
}

func TestDiscoverModels_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := DiscoverModels(context.Background(), srv.URL+"/v1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestRankModels(t *testing.T) {
	candidates := []string{"qwen3.5-27b", "llama3.1-8b", "deepseek-r1-distill-qwen-32b"}

	t.Run("no pattern no catalog returns original order", func(t *testing.T) {
		ranked, err := RankModels(candidates, nil, "")
		require.NoError(t, err)
		require.Len(t, ranked, 3)
		assert.Equal(t, "qwen3.5-27b", ranked[0].ID)
		assert.Equal(t, 1, ranked[0].Score)
	})

	t.Run("pattern match raises score", func(t *testing.T) {
		ranked, err := RankModels(candidates, nil, "llama")
		require.NoError(t, err)
		assert.Equal(t, "llama3.1-8b", ranked[0].ID)
		assert.Equal(t, 2, ranked[0].Score)
		assert.True(t, ranked[0].PatternMatch)
	})

	t.Run("case insensitive pattern", func(t *testing.T) {
		ranked, err := RankModels(candidates, nil, "DEEPSEEK")
		require.NoError(t, err)
		assert.Equal(t, "deepseek-r1-distill-qwen-32b", ranked[0].ID)
	})

	t.Run("catalog recognized is highest score", func(t *testing.T) {
		known := map[string]string{"deepseek-r1-distill-qwen-32b": "code-high"}
		ranked, err := RankModels(candidates, known, "")
		require.NoError(t, err)
		assert.Equal(t, "deepseek-r1-distill-qwen-32b", ranked[0].ID)
		assert.Equal(t, 3, ranked[0].Score)
		assert.Equal(t, "code-high", ranked[0].CatalogRef)
	})

	t.Run("catalog beats pattern", func(t *testing.T) {
		known := map[string]string{"llama3.1-8b": "code-economy"}
		ranked, err := RankModels(candidates, known, "qwen")
		require.NoError(t, err)
		assert.Equal(t, "llama3.1-8b", ranked[0].ID)
		assert.Equal(t, 3, ranked[0].Score)
	})

	t.Run("no match falls back to first uncategorized", func(t *testing.T) {
		ranked, err := RankModels(candidates, nil, "gpt-4o")
		require.NoError(t, err)
		assert.Equal(t, "qwen3.5-27b", ranked[0].ID)
	})

	t.Run("empty candidates", func(t *testing.T) {
		ranked, err := RankModels(nil, nil, "qwen")
		require.NoError(t, err)
		assert.Empty(t, ranked)
		assert.Equal(t, "", SelectModel(ranked))
	})

	t.Run("invalid pattern returns error", func(t *testing.T) {
		_, err := RankModels(candidates, nil, "[invalid")
		require.Error(t, err)
	})
}

func TestSelectModel(t *testing.T) {
	t.Run("returns first model ID", func(t *testing.T) {
		ranked := []ScoredModel{{ID: "qwen3.5-27b", Score: 3}, {ID: "llama3.1-8b", Score: 1}}
		assert.Equal(t, "qwen3.5-27b", SelectModel(ranked))
	})

	t.Run("empty list returns empty string", func(t *testing.T) {
		assert.Equal(t, "", SelectModel(nil))
	})
}

func TestProvider_LazyModelDiscovery(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list",
				"data": []map[string]interface{}{
					{"id": "discovered-model-1", "object": "model"},
				},
			})
			return
		}
		// Reject actual chat requests in this unit test
		http.Error(w, "not a chat test", http.StatusBadRequest)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL + "/v1", APIKey: "test"})

	// First resolveModel call triggers discovery.
	m, err := p.resolveModel(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "discovered-model-1", m)
	assert.Equal(t, 1, callCount)

	// Second call should hit the cache — no additional HTTP request.
	m2, err := p.resolveModel(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "discovered-model-1", m2)
	assert.Equal(t, 1, callCount, "discovery endpoint should only be called once")
}

func TestProvider_ModelPatternFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "llama3.1-8b"},
				{"id": "qwen3.5-27b"},
			},
		})
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL + "/v1", ModelPattern: "qwen"})
	m, err := p.resolveModel(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "qwen3.5-27b", m)

	// Full list should be available after resolution.
	discovered := p.DiscoveredModels()
	require.Len(t, discovered, 2)
	assert.Equal(t, "qwen3.5-27b", discovered[0].ID) // pattern-matched, ranked first
	assert.True(t, discovered[0].PatternMatch)
}

func TestNormalizeModelID(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		result, err := NormalizeModelID("qwen3-coder-next", []string{"qwen3-coder-next", "llama3.1-8b"})
		require.NoError(t, err)
		assert.Equal(t, "qwen3-coder-next", result)
	})

	t.Run("exact match case insensitive", func(t *testing.T) {
		result, err := NormalizeModelID("Qwen3-Coder-Next", []string{"qwen3-coder-next", "llama3.1-8b"})
		require.NoError(t, err)
		assert.Equal(t, "qwen3-coder-next", result)
	})

	t.Run("suffix match normalizes bare name to prefixed", func(t *testing.T) {
		result, err := NormalizeModelID("qwen3-coder-next", []string{"qwen/qwen3-coder-next", "llama3.1-8b"})
		require.NoError(t, err)
		assert.Equal(t, "qwen/qwen3-coder-next", result)
	})

	t.Run("suffix match case insensitive", func(t *testing.T) {
		result, err := NormalizeModelID("QWEN3-CODER-NEXT", []string{"qwen/qwen3-coder-next"})
		require.NoError(t, err)
		assert.Equal(t, "qwen/qwen3-coder-next", result)
	})

	t.Run("separator normalization resolves a single concrete model", func(t *testing.T) {
		result, err := NormalizeModelID("qwen36", []string{"Qwen-3.6-27b-MLX-8bit"})
		require.NoError(t, err)
		assert.Equal(t, "Qwen-3.6-27b-MLX-8bit", result)
	})

	t.Run("ambiguous suffix match returns error", func(t *testing.T) {
		_, err := NormalizeModelID("foo", []string{"org1/foo", "org2/foo"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ambiguous")
		assert.Contains(t, err.Error(), "org1/foo")
		assert.Contains(t, err.Error(), "org2/foo")
	})

	t.Run("no match returns original", func(t *testing.T) {
		result, err := NormalizeModelID("nonexistent", []string{"qwen/qwen3-coder-next", "llama3.1-8b"})
		require.NoError(t, err)
		assert.Equal(t, "nonexistent", result)
	})

	t.Run("empty requested returns empty", func(t *testing.T) {
		result, err := NormalizeModelID("", []string{"qwen/qwen3-coder-next"})
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("empty catalog returns original", func(t *testing.T) {
		result, err := NormalizeModelID("foo", nil)
		require.NoError(t, err)
		assert.Equal(t, "foo", result)
	})
}

// vidarOMLXCatalog is the catalog captured from a real 2026-04-21 vidar-omlx
// server that triggered the "Model 'qwen3.5-27b' not found" 404 — the driving
// scenario for MatchModelIDs.
var vidarOMLXCatalog = []string{
	"Qwen3.5-122B-A10B-RAM-100GB-MLX",
	"MiniMax-M2.5-MLX-4bit",
	"Qwen3-Coder-Next-MLX-4bit",
	"gemma-4-31B-it-MLX-4bit",
	"Qwen3.5-27B-4bit",
	"Qwen3.5-27B-Claude-4.6-Opus-Distilled-MLX-4bit",
	"Qwen3.6-35B-A3B-4bit",
	"Qwen3.6-35B-A3B-nvfp4",
	"gpt-oss-20b-MXFP4-Q8",
}

func TestMatchModelIDs(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		catalog   []string
		want      []string
	}{
		{
			name:      "qwen36 normalizes to a single concrete model",
			requested: "qwen36",
			catalog:   []string{"Qwen-3.6-27b-MLX-8bit"},
			want:      []string{"Qwen-3.6-27b-MLX-8bit"},
		},
		{
			name:      "qwen3.6 matches both quantization variants",
			requested: "qwen3.6",
			catalog:   vidarOMLXCatalog,
			want:      []string{"Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"},
		},
		{
			name:      "qwen/qwen3.6 strips the vendor prefix and matches same variants",
			requested: "qwen/qwen3.6",
			catalog:   vidarOMLXCatalog,
			want:      []string{"Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"},
		},
		{
			name:      "case-insensitive uppercase request matches lowercase-normalized catalog",
			requested: "QWEN3.6",
			catalog:   vidarOMLXCatalog,
			want:      []string{"Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"},
		},
		{
			name:      "qwen3.5-27b matches the plain 4bit AND the distilled variant (this is the real 404 case)",
			requested: "qwen3.5-27b",
			catalog:   vidarOMLXCatalog,
			want:      []string{"Qwen3.5-27B-4bit", "Qwen3.5-27B-Claude-4.6-Opus-Distilled-MLX-4bit"},
		},
		{
			name:      "qwen3-coder-next is a single match",
			requested: "qwen3-coder-next",
			catalog:   vidarOMLXCatalog,
			want:      []string{"Qwen3-Coder-Next-MLX-4bit"},
		},
		{
			name:      "gpt-oss-20b is a single match",
			requested: "gpt-oss-20b",
			catalog:   vidarOMLXCatalog,
			want:      []string{"gpt-oss-20b-MXFP4-Q8"},
		},
		{
			name:      "minimax case-insensitive substring match",
			requested: "minimax",
			catalog:   vidarOMLXCatalog,
			want:      []string{"MiniMax-M2.5-MLX-4bit"},
		},
		{
			name:      "qwen matches every Qwen-family entry",
			requested: "qwen",
			catalog:   vidarOMLXCatalog,
			want: []string{
				"Qwen3.5-122B-A10B-RAM-100GB-MLX",
				"Qwen3-Coder-Next-MLX-4bit",
				"Qwen3.5-27B-4bit",
				"Qwen3.5-27B-Claude-4.6-Opus-Distilled-MLX-4bit",
				"Qwen3.6-35B-A3B-4bit",
				"Qwen3.6-35B-A3B-nvfp4",
			},
		},
		{
			name:      "nothing matches nothing",
			requested: "nonexistent-foo",
			catalog:   vidarOMLXCatalog,
			want:      nil,
		},
		{
			name:      "empty request returns empty",
			requested: "",
			catalog:   vidarOMLXCatalog,
			want:      nil,
		},
		{
			name:      "whitespace-only request returns empty",
			requested: "   ",
			catalog:   vidarOMLXCatalog,
			want:      nil,
		},
		{
			name:      "empty catalog returns empty",
			requested: "qwen3.6",
			catalog:   nil,
			want:      nil,
		},
		{
			name:      "catalog ordering is preserved in the result",
			requested: "qwen3.5",
			catalog: []string{
				"Qwen3.5-27B-4bit",
				"Qwen3.5-122B-A10B-RAM-100GB-MLX",
				"Qwen3.5-27B-Claude-4.6-Opus-Distilled-MLX-4bit",
			},
			want: []string{
				"Qwen3.5-27B-4bit",
				"Qwen3.5-122B-A10B-RAM-100GB-MLX",
				"Qwen3.5-27B-Claude-4.6-Opus-Distilled-MLX-4bit",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchModelIDs(tc.requested, tc.catalog)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProvider_KnownModelsCatalogRank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"data": []map[string]interface{}{
				{"id": "llama3.1-8b"},
				{"id": "gpt-4o"},
				{"id": "qwen3.5-27b"},
			},
		})
	}))
	defer srv.Close()

	// Simulate catalog recognizing gpt-4o.
	known := map[string]string{"gpt-4o": "code-high"}
	p := New(Config{BaseURL: srv.URL + "/v1", KnownModels: known})
	m, err := p.resolveModel(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "gpt-4o", m) // catalog-recognized should be selected

	discovered := p.DiscoveredModels()
	require.Len(t, discovered, 3)
	assert.Equal(t, "gpt-4o", discovered[0].ID)
	assert.Equal(t, "code-high", discovered[0].CatalogRef)
	assert.Equal(t, 3, discovered[0].Score)
}

// lmStudioServer returns an httptest server that serves /api/v0/models/{model}
// with the given loaded and max context lengths.
func lmStudioServer(loaded, max int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v0/models/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":                    strings.TrimPrefix(r.URL.Path, "/api/v0/models/"),
			"loaded_context_length": loaded,
			"max_context_length":    max,
		})
	}))
}

// omlxServer returns an httptest server that serves /v1/models/status with
// a single model entry.
func omlxServer(modelID string, maxContext, maxTokens int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/status" {
			http.NotFound(w, r)
			return
		}
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
	}))
}

func TestProbeProviderFlavor_ExplicitOverride(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:9999/v1", "custom-provider")
	assert.Equal(t, "custom-provider", result.ProviderType)
	assert.Equal(t, 1.0, result.Confidence)
}

func TestProbeProviderFlavor_PortHeuristic_LMStudio(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:1234/v1", "")
	assert.Equal(t, "lmstudio", result.ProviderType)
	assert.Equal(t, 0.9, result.Confidence)
}

func TestProbeProviderFlavor_PortHeuristic_Omlx(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:1235/v1", "")
	assert.Equal(t, "omlx", result.ProviderType)
	assert.Equal(t, 0.9, result.Confidence)
}

func TestProbeProviderFlavor_PortHeuristic_Ollama(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:11434/v1", "")
	assert.Equal(t, "ollama", result.ProviderType)
	assert.Equal(t, 0.9, result.Confidence)
}

func TestProbeProviderFlavor_PortHeuristic_VLLM(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:8000/v1", "")
	assert.Equal(t, "vllm", result.ProviderType)
	assert.Equal(t, 0.9, result.Confidence)
}

func TestProbeProviderFlavor_PortHeuristic_NoMatch(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:9999/v1", "")
	assert.Equal(t, "", result.ProviderType)
	assert.Equal(t, 0.0, result.Confidence)
}

func TestProbeProviderFlavor_ConcurrentProbe_Omlx(t *testing.T) {
	srv := omlxServer("test-model", 3000, 1500)
	defer srv.Close()

	result := probeProviderFlavor(context.Background(), srv.URL+"/v1", "")
	assert.Equal(t, "omlx", result.ProviderType)
	assert.InDelta(t, 0.5, result.Confidence, 0.01)
}

func TestProbeProviderFlavor_ConcurrentProbeRaceResolution(t *testing.T) {
	srvOmlx := omlxServer("test-model", 3000, 1500)
	defer srvOmlx.Close()

	result := probeProviderFlavor(context.Background(), srvOmlx.URL+"/v1", "")
	assert.Equal(t, "omlx", result.ProviderType)

	result2 := probeProviderFlavor(context.Background(), srvOmlx.URL+"/v1", "")
	assert.Equal(t, result.ProviderType, result2.ProviderType, "concurrent probes should resolve to same provider type")
}

func TestProbeProviderFlavor_ExplicitOverrideBeatsConcurrentProbe(t *testing.T) {
	srv := omlxServer("test-model", 3000, 1500)
	defer srv.Close()

	result := probeProviderFlavor(context.Background(), srv.URL+"/v1", "custom-provider")
	assert.Equal(t, "custom-provider", result.ProviderType)
	assert.Equal(t, 1.0, result.Confidence)
}

func TestProbeProviderFlavor_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := probeProviderFlavor(ctx, srv.URL+"/v1", "")
	assert.Equal(t, "", result.ProviderType)
}

func TestProbeProviderFlavor_UnresponsiveServer(t *testing.T) {
	result := probeProviderFlavor(context.Background(), "http://localhost:54321/v1", "")
	assert.Equal(t, "", result.ProviderType)
}
