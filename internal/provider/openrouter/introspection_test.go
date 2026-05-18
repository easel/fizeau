package openrouter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/provider/openrouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenRouterIntrospect_ModelsFixture parses the recorded /api/v1/models fixture and
// asserts the derived ProviderIntrospection matches expected OpenRouter capabilities.
func TestOpenRouterIntrospect_ModelsFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/models/openrouter_models.json")
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	result, err := openrouter.Introspect(context.Background(), srv.URL+"/v1", "", srv.Client())
	require.NoError(t, err)
	require.NotNil(t, result)

	// AC2: OpenRouter supports the reasoning block on its API; static ProtocolCapabilities
	// declare ThinkingWireFormatOpenRouter. Introspection confirms that capability.
	assert.Equal(t, string(openai.ThinkingWireFormatOpenRouter), result.EffectiveThinkingFormat,
		"OpenRouter supports reasoning wire format")
	assert.NotNil(t, result.Raw, "Raw response should be captured for audit")
}

// TestOpenRouterIntrospect_ConnectionRefused verifies that an unreachable server
// returns an error (caller falls through to static defaults).
func TestOpenRouterIntrospect_ConnectionRefused(t *testing.T) {
	result, err := openrouter.Introspect(context.Background(), "http://127.0.0.1:1/v1", "", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestOpenRouterIntrospect_ManualSmoke runs against the live OpenRouter API endpoint.
// Use: OPENROUTER_API_KEY=your-api-key go test ./internal/provider/openrouter/... -run TestOpenRouterIntrospect_ManualSmoke -v
func TestOpenRouterIntrospect_ManualSmoke(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set; skipping live introspection smoke test")
	}

	// Live OpenRouter endpoint is on the public internet; manual test only.
	baseURL := "https://openrouter.ai/api/v1"
	result, err := openrouter.Introspect(context.Background(), baseURL, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	t.Logf("ThinkingFormat: %s", result.EffectiveThinkingFormat)
	t.Logf("Raw: %+v", result.Raw)
}
