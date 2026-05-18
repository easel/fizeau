// Package vllm wraps the OpenAI-compat HTTP surface exposed by `vllm serve`
// (https://docs.vllm.ai/). vLLM is a high-throughput inference server with
// one behavior worth distinguishing in the catalog stack: by default it
// applies the target model's HuggingFace `generation_config.json` when the
// client omits sampler fields. Most other local servers we wrap (omlx,
// lmstudio, lucebox) cannot do this — MLX / GGUF repackaging typically drops
// generation_config.json from the bundle, and the servers ship their own
// presets instead.
//
// The implication for ADR-007's catalog-stale nudge: when a vLLM-served
// request omits sampler fields, the user is not "decoding greedy" — the
// server is honoring the model creator's recommended bundle. The CLI
// reflects that with a softer message.
//
// Capabilities mirror lmstudio (Tools / Stream / StructuredOutput true) and
// add ImplicitGenerationConfig=true. Reasoning is model-dependent and not
// declared at the provider level; per-model thinking-mode controls live in
// the catalog ModelEntry, matching the lmstudio precedent.
//
// Default port 8000 follows the vLLM docs. Auth is optional: vLLM accepts
// unauthenticated requests by default and gates with --api-key (or
// VLLM_API_KEY) when the operator sets one. The Config.APIKey field flows
// through unchanged.
package vllm

import (
	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/registry"
	"github.com/easel/fizeau/internal/reasoning"
)

const DefaultBaseURL = "http://localhost:8000/v1"

func init() {
	registry.Register(registry.Descriptor{
		Type: "vllm",
		Factory: func(in registry.Inputs) agentcore.Provider {
			return New(Config{
				BaseURL:      in.BaseURL,
				APIKey:       in.APIKey,
				Model:        in.Model,
				ModelPattern: in.ModelPattern,
				KnownModels:  in.KnownModels,
				Headers:      in.Headers,
				Reasoning:    in.Reasoning,
			})
		},
		DefaultBaseURL:  DefaultBaseURL,
		DefaultPort:     8000,
		IntrospectionFn: Introspect,
	})
}

// ProtocolCapabilities matches the OpenAI-compat surface vLLM exposes,
// including thinking-mode controls for Qwen-family models. vLLM's Qwen3
// deployments accept `enable_thinking` and `thinking_budget` as top-level
// chat-completions fields, so we emit the Qwen wire format. Like LM Studio,
// vLLM hosts mixed model families (Qwen, Llama, Gemma, etc.), so leave
// StrictThinkingModelMatch=false — reasoning controls silently no-op on
// non-Qwen models instead of failing the request, which means a profile
// declaring `reasoning: low` for a non-thinking model just degrades
// gracefully.
var ProtocolCapabilities = openai.ProtocolCapabilities{
	Tools:                    true,
	Stream:                   true,
	StructuredOutput:         true,
	Thinking:                 true,
	ThinkingFormat:           openai.ThinkingWireFormatQwen,
	StrictThinkingModelMatch: false,
	ImplicitGenerationConfig: true,
}

type Config struct {
	BaseURL      string
	APIKey       string
	Model        string
	ModelPattern string
	KnownModels  map[string]string
	Headers      map[string]string
	Reasoning    reasoning.Reasoning
}

func New(cfg Config) *openai.Provider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return openai.New(openai.Config{
		BaseURL:        baseURL,
		APIKey:         cfg.APIKey,
		Model:          cfg.Model,
		ProviderName:   "vllm",
		ProviderSystem: "vllm",
		ModelPattern:   cfg.ModelPattern,
		KnownModels:    cfg.KnownModels,
		Headers:        cfg.Headers,
		Reasoning:      cfg.Reasoning,
		Capabilities:   &ProtocolCapabilities,
	})
}
