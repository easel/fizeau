package modelsnapshot

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
)

// realSindriProps is a trimmed but faithful capture of the GET /props payload
// from the sindri lucebox-dflash speculative-decoding server (2026-06-07). Its
// /v1/models id is the generic alias "dflash" (no catalog power); the real model
// identity lives only in /props.
const realSindriProps = `{
  "build_info": "luce-dflash v0.0.0+cpp props_schema=2",
  "capabilities": {"reasoning_supported": true, "speculative_supported": true, "tools_supported": true},
  "model": {"arch": "qwen35", "draft_path": "/opt/lucebox-hub/server/models/draft/dflash-draft-3.6-q4_k_m.gguf", "tokenizer_id": null},
  "model_alias": "dflash",
  "model_card": {
    "name": "Qwen3.6 27B",
    "source": "https://huggingface.co/Qwen/Qwen3.6-27B",
    "max_tokens": 32768
  },
  "model_path": "/opt/lucebox-hub/server/models/Qwen3.6-27B-Q4_K_M.gguf"
}`

func TestParsePropsDiscovery_RecoversRealModelIdentity(t *testing.T) {
	ids, _ := parsePropsDiscovery([]byte(realSindriProps))

	// The catalog fuzzy matcher needs at least one of these to resolve a power.
	// "Qwen3.6 27B" (model_card.name) canonicalizes to the same key as the
	// catalog's "qwen3.6-27b" (power 5).
	want := []string{
		"dflash",             // model_alias
		"Qwen3.6 27B",        // model_card.name
		"Qwen3.6-27B",        // model_card.source last segment
		"Qwen3.6-27B-Q4_K_M", // model_path basename, .gguf stripped
	}
	for _, w := range want {
		if !slices.Contains(ids, w) {
			t.Errorf("parsePropsDiscovery() missing %q; got %v", w, ids)
		}
	}
}

func TestModelNameFromPath(t *testing.T) {
	cases := map[string]string{
		"/opt/lucebox-hub/server/models/Qwen3.6-27B-Q4_K_M.gguf": "Qwen3.6-27B-Q4_K_M",
		"models/Qwen3.5-27B.safetensors":                         "Qwen3.5-27B",
		"/x/y/model.BIN":                                         "model",
		"plain-name":                                             "plain-name",
		"":                                                       "",
		"/":                                                      "",
	}
	for in, want := range cases {
		if got := modelNameFromPath(in); got != want {
			t.Errorf("modelNameFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLastPathSegment(t *testing.T) {
	cases := map[string]string{
		"https://huggingface.co/Qwen/Qwen3.6-27B":  "Qwen3.6-27B",
		"https://huggingface.co/Qwen/Qwen3.6-27B/": "Qwen3.6-27B",
		"Qwen3.6-27B": "Qwen3.6-27B",
		"":            "",
	}
	for in, want := range cases {
		if got := lastPathSegment(in); got != want {
			t.Errorf("lastPathSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasPropsDiscovery_LlamaCppTypes(t *testing.T) {
	for _, typ := range []string{"lucebox", "ds4", "vidar-ds4", "llama-server", "sindri-llamacpp"} {
		if !hasPropsDiscovery(typ) {
			t.Errorf("hasPropsDiscovery(%q) = false, want true", typ)
		}
	}
	for _, typ := range []string{"openai", "lmstudio", "vllm", "omlx", "openrouter"} {
		if hasPropsDiscovery(typ) {
			t.Errorf("hasPropsDiscovery(%q) = true, want false", typ)
		}
	}
}

func TestParsePropsLimitEvidence_Aliases(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantCtx    int
		wantOutput int
	}{
		{
			name:       "default settings and model card",
			body:       `{"default_generation_settings":{"n_ctx":65536},"model_card":{"max_tokens":32768}}`,
			wantCtx:    65536,
			wantOutput: 32768,
		},
		{
			name:       "top level fields",
			body:       `{"context_window":131072,"max_completion_tokens":16384}`,
			wantCtx:    131072,
			wantOutput: 16384,
		},
		{
			name:       "runtime context and generation params",
			body:       `{"runtime":{"max_ctx":262144},"default_generation_settings":{"params":{"max_tokens":8192}}}`,
			wantCtx:    262144,
			wantOutput: 8192,
		},
		{
			name:       "nonpositive generation max is not a limit",
			body:       `{"runtime":{"max_ctx":32768},"default_generation_settings":{"params":{"max_tokens":-1}}}`,
			wantCtx:    32768,
			wantOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePropsLimitEvidence([]byte(tt.body))
			if got.ContextWindow != tt.wantCtx || got.MaxCompletionTokens != tt.wantOutput {
				t.Fatalf("parsePropsLimitEvidence() = %+v, want context=%d output=%d", got, tt.wantCtx, tt.wantOutput)
			}
			if got.ContextWindow > 0 && got.ContextWindowSource != limitSourceProviderAPI {
				t.Errorf("context source = %q, want %q", got.ContextWindowSource, limitSourceProviderAPI)
			}
			if got.MaxCompletionTokens > 0 && got.MaxCompletionTokensSource != limitSourceProviderAPI {
				t.Errorf("output source = %q, want %q", got.MaxCompletionTokensSource, limitSourceProviderAPI)
			}
		})
	}
}

func TestPropsLimitEvidence_CacheRoundTrip(t *testing.T) {
	cache := &discoverycache.Cache{Root: t.TempDir()}
	src := discoverySource("test-props", time.Hour, time.Second)
	payload, err := json.Marshal(discoveryPayload{
		CapturedAt:                time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
		Models:                    []string{"served-alias"},
		ContextWindow:             65536,
		ContextWindowSource:       limitSourceProviderAPI,
		MaxCompletionTokens:       32768,
		MaxCompletionTokensSource: limitSourceProviderAPI,
	})
	if err != nil {
		t.Fatalf("marshal discovery payload: %v", err)
	}
	if err := cache.Refresh(src, func(context.Context) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatalf("cache refresh: %v", err)
	}

	result := readDiscoveryCache(cache, src, "provider", SourcePropsAPI, discoveryIdentity{Provider: "provider"})
	if len(result.Models) != 1 {
		t.Fatalf("cached models = %+v, want one", result.Models)
	}
	got := result.Models[0]
	if got.ContextWindow != 65536 || got.ContextWindowSource != limitSourceProviderAPI {
		t.Errorf("cached context evidence = %d/%q, want 65536/%q", got.ContextWindow, got.ContextWindowSource, limitSourceProviderAPI)
	}
	if got.MaxCompletionTokens != 32768 || got.MaxCompletionTokensSource != limitSourceProviderAPI {
		t.Errorf("cached output evidence = %d/%q, want 32768/%q", got.MaxCompletionTokens, got.MaxCompletionTokensSource, limitSourceProviderAPI)
	}

	legacyIDs, _, legacyLimits, err := parseDiscoveryPayload([]byte(`["legacy-model"]`), "provider")
	if err != nil || len(legacyIDs) != 1 || legacyIDs[0] != "legacy-model" || legacyLimits != (limitEvidence{}) {
		t.Errorf("legacy cache decode = ids=%v limits=%+v err=%v", legacyIDs, legacyLimits, err)
	}
}
