package openai

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

func TestProtocolSupport_DefaultOpenAI(t *testing.T) {
	p := New(Config{BaseURL: "https://api.openai.com/v1"})
	assert.True(t, p.SupportsTools())
	assert.True(t, p.SupportsStream())
	assert.True(t, p.SupportsStructuredOutput())
	assert.False(t, p.SupportsThinking())
}

func TestProtocolSupport_ProviderCapabilitiesOverride(t *testing.T) {
	caps := ProtocolCapabilities{
		Tools:            true,
		Stream:           true,
		StructuredOutput: false,
		Thinking:         true,
	}

	p := New(Config{
		BaseURL:      "http://localhost:1234/v1",
		Capabilities: &caps,
	})

	assert.True(t, p.SupportsTools())
	assert.True(t, p.SupportsStream())
	assert.False(t, p.SupportsStructuredOutput())
	assert.True(t, p.SupportsThinking())
}

// TestThinkingWireFormatOpenAIEffort_Serialize verifies that the
// ThinkingWireFormatOpenAIEffort wire format produces a JSON body with
// the OpenAI reasoning_effort key.
func TestThinkingWireFormatOpenAIEffort_Serialize(t *testing.T) {
	const model = "deepseek-v4-flash"
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

	caps := OpenAIProtocolCapabilities
	caps.Thinking = true
	caps.ThinkingFormat = ThinkingWireFormatOpenAIEffort
	caps.SupportedRequestParams = []string{"reasoning_effort", "think"}

	p := New(Config{
		BaseURL:        srv.URL + "/v1",
		APIKey:         "test",
		Model:          model,
		ProviderSystem: "ds4",
		Capabilities:   &caps,
	})

	_, err := p.Chat(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "hello"}}, nil, agent.Options{Reasoning: agent.ReasoningHigh})
	require.NoError(t, err)
	require.NotNil(t, capturedBody)

	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal(capturedBody, &reqBody))

	// Verify the wire body uses the OpenAI reasoning_effort key
	effort, ok := reqBody["reasoning_effort"].(string)
	require.True(t, ok, "request body must include reasoning_effort string: %s", string(capturedBody))
	assert.Equal(t, "high", effort)

	// Verify other wire formats are not present
	assert.NotContains(t, reqBody, "thinking")
	assert.NotContains(t, reqBody, "enable_thinking")
	assert.NotContains(t, reqBody, "thinking_budget")
}
