package serviceimpl

import (
	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/provider/quotaheaders"
	// Provider packages self-register into the factory registry. Keep these
	// imports beside BuildNativeProvider so binaries using only the public
	// service facade still load every production provider factory.
	_ "github.com/easel/fizeau/internal/provider/anthropic"
	_ "github.com/easel/fizeau/internal/provider/ds4"
	_ "github.com/easel/fizeau/internal/provider/llamaserver"
	_ "github.com/easel/fizeau/internal/provider/lmstudio"
	_ "github.com/easel/fizeau/internal/provider/lucebox"
	_ "github.com/easel/fizeau/internal/provider/ollama"
	_ "github.com/easel/fizeau/internal/provider/omlx"
	_ "github.com/easel/fizeau/internal/provider/openai"
	_ "github.com/easel/fizeau/internal/provider/openrouter"
	_ "github.com/easel/fizeau/internal/provider/rapidmlx"
	"github.com/easel/fizeau/internal/provider/registry"
	_ "github.com/easel/fizeau/internal/provider/vllm"
)

// NativeProviderBuildInput is the API-neutral service-time provider factory
// input used by the root facade.
type NativeProviderBuildInput struct {
	Name                string
	Entry               ProviderEntry
	QuotaSignalObserver func(quotaheaders.Signal)
}

// BuildNativeProvider constructs one configured native provider through the
// canonical provider registry. Provider selection remains the root facade's
// responsibility; this function owns only concrete factory mechanics.
func BuildNativeProvider(input NativeProviderBuildInput) agentcore.Provider {
	if input.Entry.ConfigError != "" {
		return nil
	}
	typ := NormalizeProviderType(input.Entry.Type)
	descriptor, ok := registry.Lookup(typ)
	if !ok {
		return nil
	}
	return descriptor.Factory(registry.Inputs{
		ProviderName:        input.Name,
		BaseURL:             input.Entry.BaseURL,
		APIKey:              input.Entry.APIKey,
		Model:               input.Entry.Model,
		ModelReasoningWire:  nativeModelReasoningWireMap(),
		QuotaSignalObserver: input.QuotaSignalObserver,
	})
}

// nativeModelReasoningWireMap returns the catalog reasoning_wire map for the
// native provider factory. Models without an explicit wire form are omitted.
func nativeModelReasoningWireMap() map[string]string {
	cat, err := modelcatalog.Default()
	if err != nil {
		return nil
	}
	all := cat.AllModels()
	if len(all) == 0 {
		return nil
	}
	out := make(map[string]string, len(all))
	for id, entry := range all {
		if entry.ReasoningWire != "" {
			out[id] = entry.ReasoningWire
		}
	}
	return out
}
