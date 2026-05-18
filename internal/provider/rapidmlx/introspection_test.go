package rapidmlx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/easel/fizeau/internal/provider/rapidmlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRapidMLXIntrospect_ModelsFixture parses the recorded /v1/models fixture and
// asserts the derived ProviderIntrospection matches expected Rapid-MLX capabilities.
func TestRapidMLXIntrospect_ModelsFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/models/rapidmlx_models.json")
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

	result, err := rapidmlx.Introspect(context.Background(), srv.URL, "", srv.Client())
	require.NoError(t, err)
	require.NotNil(t, result)

	// AC2: Rapid-MLX does not currently expose reasoning capabilities at the API level.
	// EffectiveThinkingFormat should be empty, indicating no L1 introspection override
	// of static ProtocolCapabilities (Thinking: false).
	assert.Equal(t, "", result.EffectiveThinkingFormat,
		"Rapid-MLX lacks reasoning capability; no thinking format override")
	assert.NotNil(t, result.Raw, "Raw response should be captured for audit")
}

// TestRapidMLXIntrospect_ConnectionRefused verifies that an unreachable server
// returns an error (caller falls through to static defaults).
func TestRapidMLXIntrospect_ConnectionRefused(t *testing.T) {
	result, err := rapidmlx.Introspect(context.Background(), "http://127.0.0.1:1", "", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestRapidMLXIntrospect_ManualSmoke runs against a live Rapid-MLX endpoint when
// RAPIDMLX_BASE_URL is set.
// Use: RAPIDMLX_BASE_URL=http://localhost:8000/v1 go test ./internal/provider/rapidmlx/... -run TestRapidMLXIntrospect_ManualSmoke -v
func TestRapidMLXIntrospect_ManualSmoke(t *testing.T) {
	baseURL := os.Getenv("RAPIDMLX_BASE_URL")
	if baseURL == "" {
		t.Skip("RAPIDMLX_BASE_URL not set; skipping live introspection smoke test")
	}

	result, err := rapidmlx.Introspect(context.Background(), baseURL, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	t.Logf("ThinkingFormat: %s", result.EffectiveThinkingFormat)
	t.Logf("Raw: %+v", result.Raw)
}
