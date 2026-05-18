package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/easel/fizeau/internal/provider/registry"
	"github.com/easel/fizeau/internal/provider/utilization"
)

// ollamaModelsResp mirrors the relevant fields of the Ollama /api/tags response.
type ollamaModelsResp struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// Introspect fetches GET /api/tags from Ollama and returns structured
// ProviderIntrospection. Ollama does not currently expose reasoning capabilities;
// the adapter confirms this and falls through to static defaults (Thinking: false).
// Returns an error if the endpoint is unreachable; callers should fall back to
// static ProtocolCapabilities.
func Introspect(ctx context.Context, baseURL, model string, client *http.Client) (*registry.ProviderIntrospection, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := utilization.ServerRoot(baseURL) + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("ollama introspection: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama introspection: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("ollama introspection: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama introspection: read body: %w", err)
	}

	var resp_data ollamaModelsResp
	if err := json.Unmarshal(body, &resp_data); err != nil {
		return nil, fmt.Errorf("ollama introspection: unmarshal: %w", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	// Ollama does not currently expose reasoning capabilities at the API level.
	// Leave EffectiveThinkingFormat empty; static default (Thinking: false) applies.
	return &registry.ProviderIntrospection{
		Raw: raw,
	}, nil
}
