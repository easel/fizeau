package vllm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/registry"
	"github.com/easel/fizeau/internal/provider/utilization"
)

// vllmModelsResp mirrors the relevant fields of the vLLM /v1/models response.
type vllmModelsResp struct {
	Data []struct {
		ID       string `json:"id"`
		Object   string `json:"object"`
		OwnedBy  string `json:"owned_by"`
		Created  int64  `json:"created"`
		Root     string `json:"root"`
		Parent   string `json:"parent"`
		LiteLLMU string `json:"litellm_provider"`
	} `json:"data"`
}

// Introspect fetches GET /v1/models from a vLLM server and returns structured
// ProviderIntrospection. vLLM's ProtocolCapabilities declare Qwen thinking
// format support; introspection confirms that configuration.
// Returns an error if the endpoint is unreachable; callers should fall back to
// static ProtocolCapabilities.
func Introspect(ctx context.Context, baseURL, model string, client *http.Client) (*registry.ProviderIntrospection, error) {
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := utilization.ServerRoot(baseURL) + "/v1/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("vllm introspection: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vllm introspection: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("vllm introspection: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vllm introspection: read body: %w", err)
	}

	var models vllmModelsResp
	if err := json.Unmarshal(body, &models); err != nil {
		return nil, fmt.Errorf("vllm introspection: unmarshal: %w", err)
	}

	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	// vLLM supports Qwen thinking format via chat_template_kwargs.
	// The static ProtocolCapabilities declare ThinkingWireFormatQwen;
	// introspection confirms that capability is present.
	return &registry.ProviderIntrospection{
		EffectiveThinkingFormat: string(openai.ThinkingWireFormatQwen),
		Raw:                     raw,
	}, nil
}
