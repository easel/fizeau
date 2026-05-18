package ollama_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/easel/fizeau/internal/provider/ollama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOllamaIntrospect_TagsFixture parses the recorded /api/tags fixture and
// asserts the derived ProviderIntrospection matches expected Ollama capabilities.
func TestOllamaIntrospect_TagsFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/props/ollama_tags.json")
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	result, err := ollama.Introspect(context.Background(), srv.URL+"/v1", "", srv.Client())
	require.NoError(t, err)
	require.NotNil(t, result)

	// AC2: Ollama does not currently expose reasoning capabilities at the API level.
	// EffectiveThinkingFormat should be empty, indicating no L1 introspection override
	// of static ProtocolCapabilities (Thinking: false).
	assert.Equal(t, "", result.EffectiveThinkingFormat,
		"Ollama lacks reasoning capability; no thinking format override")
	assert.NotNil(t, result.Raw, "Raw response should be captured for audit")
}

// TestOllamaIntrospect_ConnectionRefused verifies that an unreachable server
// returns an error (caller falls through to static defaults).
func TestOllamaIntrospect_ConnectionRefused(t *testing.T) {
	result, err := ollama.Introspect(context.Background(), "http://127.0.0.1:1/v1", "", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestOllamaIntrospect_ManualSmoke runs against a live Ollama endpoint when
// OLLAMA_BASE_URL is set.
// Use: OLLAMA_BASE_URL=http://localhost:11434/v1 go test ./internal/provider/ollama/... -run TestOllamaIntrospect_ManualSmoke -v
func TestOllamaIntrospect_ManualSmoke(t *testing.T) {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		t.Skip("OLLAMA_BASE_URL not set; skipping live introspection smoke test")
	}

	result, err := ollama.Introspect(context.Background(), baseURL, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	t.Logf("ThinkingFormat: %s", result.EffectiveThinkingFormat)
	t.Logf("Raw: %+v", result.Raw)
}
