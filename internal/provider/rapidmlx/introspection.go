package rapidmlx

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

// rapidmlxModelsResp mirrors the relevant fields of the Rapid-MLX /v1/models response.
type rapidmlxModelsResp struct {
	Data []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// Introspect fetches GET /v1/models from a Rapid-MLX server and returns structured
// ProviderIntrospection. Rapid-MLX is an OpenAI-compatible provider that does not
// currently expose reasoning capabilities; introspection confirms the absence of
// thinking-related metadata.
// Returns an error if the endpoint is unreachable; callers should fall back to
// static ProtocolCapabilities.
func Introspect(ctx context.Context, baseURL, model string, client *http.Client) (*registry.ProviderIntrospection, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := utilization.ServerRoot(baseURL) + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("rapid-mlx introspection: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rapid-mlx introspection: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("rapid-mlx introspection: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rapid-mlx introspection: read body: %w", err)
	}

	var models rapidmlxModelsResp
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("rapid-mlx introspection: unmarshal: %w", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	// Rapid-MLX does not currently expose reasoning capabilities at the API level.
	// Leave EffectiveThinkingFormat empty; static default (Thinking: false) applies.
	return &registry.ProviderIntrospection{
		Raw: raw,
	}, nil
}
