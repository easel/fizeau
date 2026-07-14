package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeOpenAIModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "Qwen3.6-35B-A3B-4bit"},
				{"id": "Qwen3.6-35B-A3B-nvfp4"},
			},
		})
	}))
	defer srv.Close()
	ids, err := probeOpenAIModels(context.Background(), srv.URL+"/v1", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Qwen3.6-35B-A3B-4bit", "Qwen3.6-35B-A3B-nvfp4"}, ids)
}

func TestProbeOpenAIModels_404ReturnsDiscoveryUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := probeOpenAIModels(context.Background(), srv.URL+"/v1", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrDiscoveryUnsupported()),
		"404 must classify as discovery-unsupported so the cache enables passthrough")
}

func TestProbeOpenAIModels_502ReturnsReachabilityError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := probeOpenAIModels(context.Background(), srv.URL+"/v1", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, openai.ErrEndpointUnreachable),
		"5xx must wrap as ReachabilityError so routing can skip")
}

func TestProbeOpenAIModels_DialFailureReturnsReachabilityError(t *testing.T) {
	// http://127.0.0.1:1 is RFC-invalid and will reliably fail to dial.
	_, err := probeOpenAIModels(context.Background(), "http://127.0.0.1:1/v1", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, openai.ErrEndpointUnreachable),
		"dial failure must wrap as ReachabilityError")
}

func TestProbeOpenAIModels_AuthErrorBubbles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := probeOpenAIModels(context.Background(), srv.URL+"/v1", "")
	require.Error(t, err)
	// 401 is neither reachability nor discovery-unsupported; it's a plain error.
	assert.False(t, errors.Is(err, openai.ErrEndpointUnreachable))
	assert.False(t, errors.Is(err, ErrDiscoveryUnsupported()))
}

func TestExtractStatusCode(t *testing.T) {
	cases := map[string]int{
		"HTTP 502: Bad Gateway":     502,
		"HTTP 404: not found":       404,
		"HTTP 200: ok":              200,
		"HTTP 999: weird":           999,
		"nothing here":              0,
		"HTTP foo: oops":            0,
		"context deadline exceeded": 0,
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, want, extractStatusCode(input))
		})
	}
}
