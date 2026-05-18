// Package rapidmlx wraps the OpenAI-compatible HTTP surface exposed by
// Rapid-MLX (https://github.com/raullenchai/Rapid-MLX). It is a concrete
// provider type distinct from vLLM so the service layer can keep provider
// identity separate from utilization probing, which Rapid-MLX exposes on a
// different observability endpoint family.
package rapidmlx

import (
	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/registry"
	"github.com/easel/fizeau/internal/reasoning"
)

const DefaultBaseURL = "http://localhost:8000/v1"

func init() {
	registry.Register(registry.Descriptor{
		Type: "rapid-mlx",
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

// ProtocolCapabilities keeps Rapid-MLX on the standard OpenAI-compatible
// surface. The provider remains distinct from vLLM at the type level.
var ProtocolCapabilities = openai.OpenAIProtocolCapabilities

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
		ProviderName:   "rapid-mlx",
		ProviderSystem: "rapid-mlx",
		ModelPattern:   cfg.ModelPattern,
		KnownModels:    cfg.KnownModels,
		Headers:        cfg.Headers,
		Reasoning:      cfg.Reasoning,
		Capabilities:   &ProtocolCapabilities,
	})
}
