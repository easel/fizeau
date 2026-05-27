package llamaserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	agent "github.com/easel/fizeau/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLlamaserver_EnableThinking_WireBody verifies that llamaserver emits
// enable_thinking inside a chat_template_kwargs envelope (not at top level),
// matching ADR-010 amendment 2026-05-11 §8'-9' and llama.cpp expectations.
func TestLlamaserver_EnableThinking_WireBody(t *testing.T) {
	const model = "Qwen3.6-27B-UD-Q3_K_XL.gguf"

	t.Run("enabled/emits chat_template_kwargs.enable_thinking=true", func(t *testing.T) {
		body, err := captureLlamaserverChatBody(t, model, agent.Options{Reasoning: agent.ReasoningTokens(4096)})
		require.NoError(t, err)
		assertLlamaserverQwenReasoningWire(t, body, true, 4096)
	})
}

// TestLlamaserver_EnableThinking_Disabled verifies that when thinking is
// disabled, the chat_template_kwargs.enable_thinking field is explicitly set
// to false per ADR-010 §9'.
func TestLlamaserver_EnableThinking_Disabled(t *testing.T) {
	const model = "Qwen3.6-27B-UD-Q3_K_XL.gguf"

	t.Run("disabled/emits chat_template_kwargs.enable_thinking=false", func(t *testing.T) {
		body, err := captureLlamaserverChatBody(t, model, agent.Options{Reasoning: agent.ReasoningOff})
		require.NoError(t, err)
		assertLlamaserverQwenReasoningWire(t, body, false, 0)
	})
}

// captureLlamaserverChatBody constructs a llamaserver provider with the given
// model and options, captures the request body sent to the mock server, and
// returns it.
func captureLlamaserverChatBody(t *testing.T, model string, opts agent.Options) ([]byte, error) {
	t.Helper()
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1",
			"model":"` + model + `",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}
		}`))
	}))
	defer srv.Close()

	// Create a llamaserver provider with the given config.
	p := New(Config{
		BaseURL: srv.URL + "/v1",
		APIKey:  "test",
		Model:   model,
	})

	// Make a chat call and capture the request body.
	_, err := p.Chat(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "hello"}}, nil, opts)
	return capturedBody, err
}

// assertLlamaserverQwenReasoningWire asserts that the request body correctly
// emits enable_thinking inside the chat_template_kwargs envelope (not at the
// top level), and that no top-level enable_thinking or thinking_budget keys exist.
func assertLlamaserverQwenReasoningWire(t *testing.T, body []byte, wantEnabled bool, wantBudget int) {
	t.Helper()
	require.NotNil(t, body)
	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &reqBody))

	// AC 1: ensure no top-level enable_thinking or thinking_budget keys
	assert.NotContains(t, reqBody, "enable_thinking", "qwen controls must use chat_template_kwargs envelope: %s", string(body))
	assert.NotContains(t, reqBody, "thinking_budget", "qwen controls must use chat_template_kwargs envelope: %s", string(body))

	// AC 2: ensure chat_template_kwargs exists and contains the nested enable_thinking
	ctk, ok := reqBody["chat_template_kwargs"].(map[string]interface{})
	require.True(t, ok, "request body must include chat_template_kwargs: %s", string(body))
	assert.Equal(t, wantEnabled, ctk["enable_thinking"], "chat_template_kwargs.enable_thinking mismatch: %s", string(body))

	// If thinking is enabled, check the budget
	if wantEnabled {
		assert.Equal(t, float64(wantBudget), ctk["thinking_budget"], "chat_template_kwargs.thinking_budget mismatch: %s", string(body))
	}

	// Ensure no top-level thinking map is present (different wire format)
	if _, ok := reqBody["thinking"]; ok {
		t.Fatalf("qwen reasoning controls must not use thinking map: %s", string(body))
	}
}
