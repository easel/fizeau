package modelregistry

import (
	"slices"
	"testing"
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
		"models/Qwen3.5-27B.safetensors":                        "Qwen3.5-27B",
		"/x/y/model.BIN":                                        "model",
		"plain-name":                                            "plain-name",
		"":                                                      "",
		"/":                                                     "",
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
		"Qwen3.6-27B":                              "Qwen3.6-27B",
		"":                                         "",
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
