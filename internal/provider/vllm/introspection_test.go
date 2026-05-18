package vllm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/vllm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVLLMIntrospect_ModelsFixture parses the recorded /v1/models fixture and
// asserts the derived ProviderIntrospection matches expected vLLM capabilities.
func TestVLLMIntrospect_ModelsFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/models/vllm_models.json")
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	result, err := vllm.Introspect(context.Background(), srv.URL, "", srv.Client())
	require.NoError(t, err)
	require.NotNil(t, result)

	// AC2: vLLM supports Qwen thinking format via static ProtocolCapabilities;
	// introspection confirms that capability.
	assert.Equal(t, string(openai.ThinkingWireFormatQwen), result.EffectiveThinkingFormat,
		"vLLM supports Qwen thinking wire format")
	assert.NotNil(t, result.Raw, "Raw response should be captured for audit")
}

// TestVLLMIntrospect_ConnectionRefused verifies that an unreachable server
// returns an error (caller falls through to static defaults).
func TestVLLMIntrospect_ConnectionRefused(t *testing.T) {
	result, err := vllm.Introspect(context.Background(), "http://127.0.0.1:1", "", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestVLLMIntrospect_ManualSmoke runs against a live vLLM endpoint when
// VLLM_BASE_URL is set.
// Use: VLLM_BASE_URL=http://localhost:8000/v1 go test ./internal/provider/vllm/... -run TestVLLMIntrospect_ManualSmoke -v
func TestVLLMIntrospect_ManualSmoke(t *testing.T) {
	baseURL := os.Getenv("VLLM_BASE_URL")
	if baseURL == "" {
		t.Skip("VLLM_BASE_URL not set; skipping live introspection smoke test")
	}

	result, err := vllm.Introspect(context.Background(), baseURL, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	t.Logf("ThinkingFormat: %s", result.EffectiveThinkingFormat)
	t.Logf("Raw: %+v", result.Raw)
}
