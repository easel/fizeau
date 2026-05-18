package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/registry"
)

// openrouterModelsResp mirrors the relevant fields of OpenRouter's /api/v1/models response.
type openrouterModelsResp struct {
	Data []struct {
		ID                    string         `json:"id"`
		Name                  string         `json:"name"`
		Pricing               map[string]any `json:"pricing"`
		ContextLength         int            `json:"context_length"`
		ArchitectureTokenizer string         `json:"architecture_tokenizer"`
		SupportedParameters   []string       `json:"supported_parameters"`
	} `json:"data"`
}

// Introspect fetches GET /api/v1/models from OpenRouter and returns structured
// ProviderIntrospection. OpenRouter routes to multiple upstream providers, each
// with different reasoning-wire preferences (some honor `effort` tiers, others
// require `max_tokens`). The L1 introspection confirms that OpenRouter broadly
// supports reasoning; L2 catalog classification disambiguates per-model wire form.
// Returns an error if the endpoint is unreachable; callers should fall back to
// static ProtocolCapabilities.
func Introspect(ctx context.Context, baseURL, model string, client *http.Client) (*registry.ProviderIntrospection, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimSuffix(baseURL, "/v1") + "/api/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("openrouter introspection: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter introspection: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("openrouter introspection: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openrouter introspection: read body: %w", err)
	}

	var models openrouterModelsResp
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("openrouter introspection: unmarshal: %w", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	// OpenRouter supports the reasoning block on its API; static ProtocolCapabilities
	// declare ThinkingWireFormatOpenRouter. Which subkey (effort vs max_tokens) bites
	// depends on the upstream model routed to; that classification lives in L2 (catalog).
	return &registry.ProviderIntrospection{
		EffectiveThinkingFormat: string(openai.ThinkingWireFormatOpenRouter),
		Raw:                     raw,
	}, nil
}
