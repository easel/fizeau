package llamaserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	agent "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/provider/llamaserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLlamaserver_EnableThinking_WireBody verifies that the llamaserver provider
// nests enable_thinking inside chat_template_kwargs, not at the top level.
// This is the fix from ADR-010 amendment §8-9': top-level enable_thinking is
// silently dropped by llama.cpp, but the chat_template_kwargs envelope activates thinking.
func TestLlamaserver_EnableThinking_WireBody(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}
		}`))
	}))
	defer srv.Close()

	p := llamaserver.New(llamaserver.Config{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test",
		Model:   "test-model",
	})

	// AC1: enable_thinking should be true inside chat_template_kwargs
	_, err := p.Chat(context.Background(),
		[]agent.Message{{Role: agent.RoleUser, Content: "hello"}},
		nil,
		agent.Options{Reasoning: agent.ReasoningTokens(4096)},
	)
	require.NoError(t, err)
	require.NotNil(t, capturedBody)

	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal(capturedBody, &reqBody))

	// AC1: No top-level enable_thinking key must exist
	assert.NotContains(t, reqBody, "enable_thinking",
		"llamaserver must nest enable_thinking inside chat_template_kwargs, not at top level: %s", string(capturedBody))
	assert.NotContains(t, reqBody, "thinking_budget",
		"llamaserver must nest thinking_budget inside chat_template_kwargs, not at top level: %s", string(capturedBody))

	// AC1: chat_template_kwargs.enable_thinking must be true
	ctk, ok := reqBody["chat_template_kwargs"].(map[string]interface{})
	require.True(t, ok, "request body must include chat_template_kwargs: %s", string(capturedBody))
	assert.Equal(t, true, ctk["enable_thinking"],
		"chat_template_kwargs.enable_thinking must be true: %s", string(capturedBody))
	assert.Equal(t, float64(4096), ctk["thinking_budget"],
		"chat_template_kwargs.thinking_budget must match requested tokens: %s", string(capturedBody))
}

// TestLlamaserver_EnableThinking_Disabled verifies that when thinking is disabled,
// the envelope is properly configured per ADR-010 §9' (enable_thinking: false).
func TestLlamaserver_EnableThinking_Disabled(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}
		}`))
	}))
	defer srv.Close()

	p := llamaserver.New(llamaserver.Config{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test",
		Model:   "test-model",
	})

	// AC3: when thinking is disabled, enable_thinking should be false inside chat_template_kwargs
	_, err := p.Chat(context.Background(),
		[]agent.Message{{Role: agent.RoleUser, Content: "hello"}},
		nil,
		agent.Options{Reasoning: agent.ReasoningOff},
	)
	require.NoError(t, err)
	require.NotNil(t, capturedBody)

	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal(capturedBody, &reqBody))

	// No top-level keys
	assert.NotContains(t, reqBody, "enable_thinking",
		"llamaserver must not emit top-level enable_thinking when disabled: %s", string(capturedBody))
	assert.NotContains(t, reqBody, "thinking_budget",
		"llamaserver must not emit top-level thinking_budget when disabled: %s", string(capturedBody))

	// AC3: chat_template_kwargs.enable_thinking must be false
	ctk, ok := reqBody["chat_template_kwargs"].(map[string]interface{})
	require.True(t, ok, "request body must include chat_template_kwargs: %s", string(capturedBody))
	assert.Equal(t, false, ctk["enable_thinking"],
		"chat_template_kwargs.enable_thinking must be false when reasoning is off: %s", string(capturedBody))
}
