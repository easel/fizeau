package serviceimpl

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
)

type loopingNativeProvider struct{}

func (loopingNativeProvider) Chat(context.Context, []agentcore.Message, []agentcore.ToolDef, agentcore.Options) (agentcore.Response, error) {
	return agentcore.Response{ToolCalls: []agentcore.ToolCall{{
		ID:        "same-call",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"README.md"}`),
	}}}, nil
}

type loopingReadTool struct{}

func (loopingReadTool) Name() string                                             { return "read" }
func (loopingReadTool) Description() string                                      { return "read" }
func (loopingReadTool) Schema() json.RawMessage                                  { return json.RawMessage(`{"type":"object"}`) }
func (loopingReadTool) Parallel() bool                                           { return true }
func (loopingReadTool) Execute(context.Context, json.RawMessage) (string, error) { return "same", nil }

func TestRunNativeClassifiesToolLoopMachineReadably(t *testing.T) {
	var (
		final  harnesses.FinalData
		origin TerminalOrigin
	)
	RunNative(context.Background(), NativeRequest{
		Prompt:  "loop",
		Tools:   []agentcore.Tool{loopingReadTool{}},
		Started: time.Now(),
		Decision: NativeDecision{
			Harness:  "fiz",
			Provider: "test",
			Model:    "test-model",
		},
	}, NativeCallbacks{
		ResolveProvider: func(NativeProviderRequest) NativeProviderResolution {
			return NativeProviderResolution{Provider: loopingNativeProvider{}, Name: "test", Model: "test-model"}
		},
		Finalize: func(got harnesses.FinalData, gotOrigin TerminalOrigin) {
			final = got
			origin = gotOrigin
		},
	})

	if origin != TerminalOriginToolLoop {
		t.Fatalf("origin = %v, want tool-loop", origin)
	}
	classified := ClassifyTerminalFinal(final, origin, nil)
	if classified.Cause != harnesses.TerminalCauseToolLoopFailed || classified.Stage != harnesses.SessionStageToolLoop {
		t.Fatalf("typed tuple = %q/%q/%q", classified.Outcome, classified.Cause, classified.Stage)
	}
}
