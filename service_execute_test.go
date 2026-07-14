//go:build testseam

package fizeau_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

type testServiceConfig struct {
	providers   map[string]fizeau.ServiceProviderEntry
	names       []string
	defaultName string
}

func (c *testServiceConfig) ProviderNames() []string { return c.names }
func (c *testServiceConfig) DefaultProviderName() string {
	return c.defaultName
}
func (c *testServiceConfig) Provider(name string) (fizeau.ServiceProviderEntry, bool) {
	entry, ok := c.providers[name]
	return entry, ok
}
func (c *testServiceConfig) HealthCooldown() time.Duration { return 0 }
func (c *testServiceConfig) WorkDir() string               { return "" }
func (c *testServiceConfig) SessionLogDir() string         { return "" }

// drainEvents collects everything from ch until it closes or the deadline
// fires. The final element (when present) is always EventTypeFinal.
func drainEvents(t *testing.T, ch <-chan fizeau.ServiceEvent, timeout time.Duration) []fizeau.ServiceEvent {
	t.Helper()
	var events []fizeau.ServiceEvent
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-deadline.C:
			t.Fatalf("timed out after %s waiting for channel close; collected %d events", timeout, len(events))
			return events
		}
	}
}

// findFinal returns the final event (the last EventTypeFinal in the slice)
// or nil if absent.
func findFinal(events []fizeau.ServiceEvent) *fizeau.ServiceEvent {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "final" {
			ev := events[i]
			return &ev
		}
	}
	return nil
}

// finalStatus extracts the status field from a final event's JSON payload.
func finalStatus(t *testing.T, ev *fizeau.ServiceEvent) string {
	t.Helper()
	if ev == nil {
		return ""
	}
	var payload struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	return payload.Status
}

// finalError extracts the error message from a final event's JSON payload.
func finalError(t *testing.T, ev *fizeau.ServiceEvent) string {
	t.Helper()
	if ev == nil {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	return payload.Error
}

func finalText(t *testing.T, ev *fizeau.ServiceEvent) string {
	t.Helper()
	if ev == nil {
		return ""
	}
	var payload struct {
		FinalText string `json:"final_text"`
	}
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	return payload.FinalText
}

func finalData(t *testing.T, ev *fizeau.ServiceEvent) fizeau.ServiceFinalData {
	t.Helper()
	if ev == nil {
		t.Fatal("expected final event")
	}
	var payload fizeau.ServiceFinalData
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	return payload
}

func eventPayload[T any](t *testing.T, ev fizeau.ServiceEvent) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("unmarshal %s payload: %v", ev.Type, err)
	}
	return payload
}

func eventTypes(events []fizeau.ServiceEvent) []string {
	types := make([]string, len(events))
	for i, ev := range events {
		types[i] = string(ev.Type)
	}
	return types
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func indexEventType(events []fizeau.ServiceEvent, want string) int {
	for i, ev := range events {
		if string(ev.Type) == want {
			return i
		}
	}
	return -1
}

func TestExecute_ReturnsExplicitHarnessModelErrorBeforeDispatch(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "gemini",
		Model:   "minimax/minimax-m2.7",
	})
	if err == nil {
		t.Fatal("expected Execute to return typed error")
	}
	if ch != nil {
		t.Fatalf("expected no event channel for typed pre-resolution error, got %#v", ch)
	}
	if !errors.Is(err, fizeau.ErrHarnessModelIncompatible{}) {
		t.Fatalf("errors.Is should match ErrHarnessModelIncompatible: %T %v", err, err)
	}
	var typed *fizeau.ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "gemini" || typed.Model != "minimax/minimax-m2.7" {
		t.Fatalf("typed error=%#v, want gemini/minimax", typed)
	}
}

func TestExecute_ReturnsProfilePinConflictBeforeProviderCall(t *testing.T) {
	var calls atomic.Int64
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			calls.Add(1)
			return fizeau.FakeResponse{Text: "should not dispatch"}, nil
		},
	}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Policy:  "smart",
		Harness: "fiz",
	})
	if err == nil {
		t.Fatal("expected Execute to return typed error")
	}
	if ch != nil {
		t.Fatalf("expected no event channel for typed pre-resolution error, got %#v", ch)
	}
	if calls.Load() != 0 {
		t.Fatalf("provider calls=%d, want 0", calls.Load())
	}
	if !errors.Is(err, fizeau.ErrPolicyRequirementUnsatisfied{}) {
		t.Fatalf("errors.Is should match ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	var typed *fizeau.ErrPolicyRequirementUnsatisfied
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrPolicyRequirementUnsatisfied: %T %v", err, err)
	}
	if typed.Policy != "smart" || typed.AttemptedPin != "Harness=fiz" || typed.Requirement != "subscription-only" {
		t.Fatalf("typed error=%#v, want smart/Harness=fiz/subscription-only", typed)
	}
}

// TestExecute_NativePathWithFakeProvider verifies that a native-path
// Execute drives loop.go through the FakeProvider seam, emits a routing
// decision, forwards events with metadata, and terminates with success.
func TestExecute_NativePathWithFakeProvider(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{Text: "hello world", Usage: fizeau.TokenUsage{Input: 10, Output: 5, Total: 15}},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp

	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := fizeau.ServiceExecuteRequest{
		Prompt:   "hi",
		Harness:  "fiz",
		Model:    "fake-model",
		Provider: "fake",
		Metadata: map[string]string{"bead_id": "test-bead-1"},
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	if got := finalStatus(t, final); got != "success" {
		t.Errorf("status: want success, got %q (err=%q)", got, finalError(t, final))
	}
	if got := finalText(t, final); got != "hello world" {
		t.Errorf("final_text: want %q, got %q", "hello world", got)
	}
	payload := finalData(t, final)
	if payload.Usage == nil || payload.Usage.InputTokens == nil || payload.Usage.OutputTokens == nil || payload.Usage.TotalTokens == nil {
		t.Fatalf("usage: expected input/output/total tokens, got %#v", payload.Usage)
	}
	if *payload.Usage.InputTokens != 10 || *payload.Usage.OutputTokens != 5 || *payload.Usage.TotalTokens != 15 {
		t.Fatalf("usage tokens: got %#v, want input=10 output=5 total=15", payload.Usage)
	}
	if payload.RoutingActual == nil || payload.RoutingActual.Harness != "fiz" || payload.RoutingActual.Provider != "fake" || payload.RoutingActual.Model != "fake-model" {
		t.Fatalf("routing_actual: got %#v", payload.RoutingActual)
	}
	// First event is the routing_decision.
	if events[0].Type != "routing_decision" {
		t.Errorf("first event type: want routing_decision, got %q", events[0].Type)
	}
}

func TestDrainExecute_NativeServiceExecuteWithFakeProvider(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{Text: "APPROVE\nTyped drain works.", Usage: fizeau.TokenUsage{Input: 8, Output: 4, Total: 12}},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp

	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:   "review",
		Harness:  "fiz",
		Model:    "fake-model",
		Provider: "fake",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result, err := fizeau.DrainExecute(context.Background(), ch)
	if err != nil {
		t.Fatalf("DrainExecute: %v", err)
	}
	if result.FinalStatus != "success" {
		t.Fatalf("FinalStatus: got %q (err=%q)", result.FinalStatus, result.TerminalError)
	}
	if result.FinalText != "APPROVE\nTyped drain works." {
		t.Fatalf("FinalText: got %q", result.FinalText)
	}
	if result.Usage == nil || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 12 {
		t.Fatalf("Usage: got %#v", result.Usage)
	}
	if result.RoutingActual == nil || result.RoutingActual.Harness != "fiz" {
		t.Fatalf("RoutingActual: got %#v", result.RoutingActual)
	}
}

func TestRequestExecutionDoesNotFetchRemoteManifest(t *testing.T) {
	t.Run("Execute", func(t *testing.T) {
		var hits atomic.Int32
		blocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			http.Error(w, "unexpected remote fetch", http.StatusInternalServerError)
		}))
		defer blocker.Close()

		opts := fizeau.ServiceOptions{
			ServiceConfig: &testServiceConfig{
				providers: map[string]fizeau.ServiceProviderEntry{
					"anthropic": {Type: "anthropic", BaseURL: blocker.URL + "/v1", Model: "unused"},
				},
				names:       []string{"anthropic"},
				defaultName: "anthropic",
			},
		}
		opts.FakeProvider = &fizeau.FakeProvider{
			Static: []fizeau.FakeResponse{{Text: "ok", Usage: fizeau.TokenUsage{Input: 1, Output: 1, Total: 2}}},
		}
		svc, err := fizeau.New(opts)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
			Prompt:   "ping",
			Harness:  "fiz",
			Provider: "anthropic",
			Model:    "fake-model",
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		result, err := fizeau.DrainExecute(context.Background(), ch)
		if err != nil {
			t.Fatalf("DrainExecute: %v", err)
		}
		if result.FinalStatus != "success" {
			t.Fatalf("FinalStatus = %q, want success", result.FinalStatus)
		}
		if got := hits.Load(); got != 0 {
			t.Fatalf("remote fetch hits = %d, want 0", got)
		}
	})

	t.Run("ResolveRoute", func(t *testing.T) {
		var hits atomic.Int32
		blocker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			http.Error(w, "unexpected remote fetch", http.StatusInternalServerError)
		}))
		defer blocker.Close()

		svc, err := fizeau.New(fizeau.ServiceOptions{
			ServiceConfig: &testServiceConfig{
				providers: map[string]fizeau.ServiceProviderEntry{
					"anthropic": {Type: "anthropic", BaseURL: blocker.URL + "/v1", Model: "unused"},
				},
				names:       []string{"anthropic"},
				defaultName: "anthropic",
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, _ = svc.ResolveRoute(context.Background(), fizeau.RouteRequest{
			Harness: "fiz",
		})
		if got := hits.Load(); got != 0 {
			t.Fatalf("remote fetch hits = %d, want 0", got)
		}
	})
}

func TestExecute_NativeReasoningForwarded(t *testing.T) {
	var got fizeau.Reasoning
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			got = req.Reasoning
			return fizeau.FakeResponse{Text: "done"}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:    "hi",
		Harness:   "fiz",
		Provider:  "fake",
		Model:     "fake-model",
		Reasoning: fizeau.ReasoningOff,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if final := findFinal(events); final == nil || finalStatus(t, final) != "success" {
		t.Fatalf("expected success final, got %#v", final)
	}
	if got != fizeau.ReasoningOff {
		t.Fatalf("Reasoning forwarded to native provider = %q, want off", got)
	}
}

func TestExecute_NativeSamplingForwarded(t *testing.T) {
	var gotTemperature *float64
	var gotSeed int64
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			gotTemperature = req.Temperature
			gotSeed = req.Seed
			return fizeau.FakeResponse{Text: "done"}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	temperature := float32(0.25)
	seed := int64(98765)
	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:      "hi",
		Harness:     "fiz",
		Provider:    "fake",
		Model:       "fake-model",
		Temperature: &temperature,
		Seed:        &seed,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if final := findFinal(events); final == nil || finalStatus(t, final) != "success" {
		t.Fatalf("expected success final, got %#v", final)
	}
	if gotTemperature == nil || *gotTemperature != 0.25 {
		t.Fatalf("Temperature forwarded to native provider = %v, want 0.25", gotTemperature)
	}
	if gotSeed != 98765 {
		t.Fatalf("Seed forwarded to native provider = %d, want 98765", gotSeed)
	}
}

func TestExecute_NativeUnrestrictedToolsForwarded(t *testing.T) {
	var providerTools []string
	var hookHarness string
	var hookTools []string
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			providerTools = append([]string(nil), req.Tools...)
			return fizeau.FakeResponse{Text: "done"}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.ToolWiringHook = func(harness string, toolNames []string) {
		hookHarness = harness
		hookTools = append([]string(nil), toolNames...)
	}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:      "hi",
		Harness:     "fiz",
		Provider:    "fake",
		Model:       "fake-model",
		WorkDir:     t.TempDir(),
		Permissions: "unrestricted",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if final := findFinal(events); final == nil || finalStatus(t, final) != "success" {
		t.Fatalf("expected success final, got %#v", final)
	}

	for _, name := range []string{"read", "write", "edit", "bash", "find", "grep", "ls", "patch", "task"} {
		if !containsString(providerTools, name) {
			t.Fatalf("provider tools missing %q: %v", name, providerTools)
		}
		if !containsString(hookTools, name) {
			t.Fatalf("hook tools missing %q: %v", name, hookTools)
		}
	}
	if hookHarness != "fiz" {
		t.Fatalf("ToolWiringHook harness = %q, want fiz", hookHarness)
	}
	if !reflect.DeepEqual(providerTools, hookTools) {
		t.Fatalf("provider tools and hook tools differ:\nprovider=%v\nhook=%v", providerTools, hookTools)
	}
}

func TestExecute_NativeSafePermissionExposesReadOnlyTools(t *testing.T) {
	var providerTools []string
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			providerTools = append([]string(nil), req.Tools...)
			return fizeau.FakeResponse{Text: "done"}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:      "hi",
		Harness:     "fiz",
		Provider:    "fake",
		Model:       "fake-model",
		WorkDir:     t.TempDir(),
		Permissions: "safe",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if final := findFinal(events); final == nil || finalStatus(t, final) != "success" {
		t.Fatalf("expected success final, got %#v", final)
	}
	for _, name := range []string{"read", "find", "grep", "ls"} {
		if !containsString(providerTools, name) {
			t.Fatalf("safe tools missing %q: %v", name, providerTools)
		}
	}
	for _, name := range []string{"write", "edit", "bash", "patch", "task"} {
		if containsString(providerTools, name) {
			t.Fatalf("safe tools unexpectedly include %q: %v", name, providerTools)
		}
	}
}

func TestExecute_NativeSupervisedPermissionRejected(t *testing.T) {
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{{Text: "should not run"}},
	}
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:      "hi",
		Harness:     "fiz",
		Provider:    "fake",
		Model:       "fake-model",
		Permissions: "supervised",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final")
	}
	if got := finalStatus(t, final); got != "failed" {
		t.Fatalf("FinalStatus: got %q, want failed", got)
	}
	if !strings.Contains(finalError(t, final), "supervised") {
		t.Fatalf("FinalError: got %q, want supervised unsupported", finalError(t, final))
	}
}

// @covers US-002-AC2
func TestExecute_NativeReadToolEmitsToolEvents(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello from service tools\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{
				ToolCalls: []fizeau.ToolCall{{
					ID:        "read-1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"hello.txt"}`),
				}},
			},
			{Text: "done"},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:   "read the fixture",
		Harness:  "fiz",
		Provider: "fake",
		Model:    "fake-model",
		WorkDir:  workDir,
		Metadata: map[string]string{
			"mode":     "replay",
			"cassette": "fiz-native",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	final := findFinal(events)
	if final == nil || finalStatus(t, final) != "success" {
		t.Fatalf("expected success final, got %#v (types=%v)", final, eventTypes(events))
	}

	var toolCall *fizeau.ServiceEvent
	var toolResult *fizeau.ServiceEvent
	for i := range events {
		switch events[i].Type {
		case "tool_call":
			ev := events[i]
			toolCall = &ev
		case "tool_result":
			ev := events[i]
			toolResult = &ev
		}
	}
	if toolCall == nil || toolResult == nil {
		t.Fatalf("expected tool_call and tool_result events, got %v", eventTypes(events))
	}
	for _, ev := range events {
		if ev.Metadata["mode"] != "replay" || ev.Metadata["cassette"] != "fiz-native" {
			t.Fatalf("event metadata not echoed for %s: %#v", ev.Type, ev.Metadata)
		}
	}
	if callIndex, resultIndex := indexEventType(events, "tool_call"), indexEventType(events, "tool_result"); callIndex < 0 || resultIndex < 0 || callIndex > resultIndex {
		t.Fatalf("tool event order invalid: %v", eventTypes(events))
	}

	call := eventPayload[struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}](t, *toolCall)
	if call.ID == "" || call.Name != "read" || !strings.Contains(string(call.Input), "hello.txt") {
		t.Fatalf("tool_call payload = %+v input=%s", call, string(call.Input))
	}
	result := eventPayload[struct {
		ID     string `json:"id"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}](t, *toolResult)
	if result.ID != call.ID {
		t.Fatalf("tool_result id = %q, want %q", result.ID, call.ID)
	}
	if result.Error != "" {
		t.Fatalf("tool_result error = %q", result.Error)
	}
	if !strings.Contains(result.Output, "hello from service tools") {
		t.Fatalf("tool_result output = %q", result.Output)
	}
}

// TestExecute_StallPolicy_ReadOnlyTrigger verifies that a fake provider
// emitting only read-only tool calls triggers the stall policy and
// terminates with Status="stalled".
func TestExecute_StallPolicy_ReadOnlyTrigger(t *testing.T) {
	// Dynamic provider that always asks for a `read` tool call. The agent
	// loop has no tool wired (Tools is nil) so each turn the model "asks"
	// but the loop reports an unknown-tool error and keeps looping. That
	// would normally hit the tool-call-loop limit; we cap iterations short
	// via a tight StallPolicy so the read-only ceiling fires first.
	callCount := 0
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			callCount++
			return fizeau.FakeResponse{
				ToolCalls: []fizeau.ToolCall{{
					ID:        "c1",
					Name:      "read",
					Arguments: json.RawMessage(`{"path":"/tmp/x"}`),
				}},
			}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := fizeau.ServiceExecuteRequest{
		Prompt:   "stall please",
		Harness:  "fiz",
		Provider: "fake",
		Model:    "fake-model",
		StallPolicy: &fizeau.StallPolicy{
			MaxReadOnlyToolIterations: 3,
		},
		Timeout: 5 * time.Second,
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 10*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	got := finalStatus(t, final)
	// The iteration ceiling derived from the stall policy may also fire
	// (read-only-tool-streak triggers cancel; loop reports either
	// "stalled" via our override or "cancelled"/"failed" depending on
	// timing). All three indicate termination short of natural success.
	if got == "success" {
		t.Errorf("expected non-success final, got %q", got)
	}
}

func TestExecute_StallPolicy_NonMutatingBashCountsAsNoProgress(t *testing.T) {
	callCount := 0
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			callCount++
			return fizeau.FakeResponse{
				ToolCalls: []fizeau.ToolCall{{
					ID:   "bash-loop",
					Name: "bash",
					Arguments: json.RawMessage([]byte(
						fmt.Sprintf(`{"command":"printf 'turn %d\\n'"}`, callCount),
					)),
				}},
			}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:      "stall on bash",
		Harness:     "fiz",
		Provider:    "fake",
		Model:       "fake-model",
		WorkDir:     t.TempDir(),
		Permissions: "unrestricted",
		StallPolicy: &fizeau.StallPolicy{
			MaxReadOnlyToolIterations: 3,
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 10*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	if got := finalStatus(t, final); got != "stalled" && got != "cancelled" && got != "failed" {
		t.Fatalf("expected stalled/cancelled/failed final, got %q", got)
	}
	foundStall := false
	for _, ev := range events {
		if ev.Type != "stall" {
			continue
		}
		stall := eventPayload[struct {
			Reason string `json:"reason"`
			Count  int64  `json:"count"`
		}](t, ev)
		if stall.Reason == "no_progress_tools_exceeded" {
			foundStall = true
			if stall.Count < 3 {
				t.Fatalf("stall count = %d, want at least 3", stall.Count)
			}
		}
	}
	if !foundStall {
		t.Fatalf("expected no_progress_tools_exceeded stall event, got types=%v", eventTypes(events))
	}
}

// TestExecute_OrphanModelFails verifies that a native-path request with
// no provider and no FakeProvider yields Status="failed" with an explicit
// orphan-model error message.
func TestExecute_OrphanModelFails(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{ServiceConfig: &stubServiceConfig{}, QuotaRefreshContext: canceledPublicRefreshContext()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := fizeau.ServiceExecuteRequest{
		Prompt:  "hi",
		Harness: "fiz",
		Model:   "no-such-model",
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 2*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	if got := finalStatus(t, final); got != "failed" {
		t.Errorf("status: want failed, got %q", got)
	}
	errMsg := finalError(t, final)
	if !strings.Contains(errMsg, "orphan model") && !strings.Contains(errMsg, "no provider") {
		t.Errorf("error: want orphan/no-provider message, got %q", errMsg)
	}
}

// TestExecute_TimeoutWallClock verifies that a wall-clock Timeout fires
// when the provider takes longer than the cap.
func TestExecute_TimeoutWallClock(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			// Sleep longer than the wall-clock cap so the request must
			// be cancelled; return an error to simulate the cancel
			// surface.
			time.Sleep(500 * time.Millisecond)
			return fizeau.FakeResponse{}, errors.New("provider should have been cancelled")
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := fizeau.ServiceExecuteRequest{
		Prompt:   "hi",
		Harness:  "fiz",
		Provider: "fake",
		Model:    "fake-model",
		Timeout:  100 * time.Millisecond,
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	got := finalStatus(t, final)
	// Either timed_out (our override caught it) or cancelled (loop saw
	// ctx.Done first) is acceptable — both indicate the wall-clock cap
	// fired.
	if got != "timed_out" && got != "cancelled" && got != "failed" {
		t.Errorf("status: want timed_out/cancelled/failed, got %q (err=%q)", got, finalError(t, final))
	}
}

// TestExecute_MetadataEchoedOnEvents verifies that req.Metadata is
// stamped onto every event the channel emits.
func TestExecute_MetadataEchoedOnEvents(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{Text: "ok"},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	wantMeta := map[string]string{
		"bead_id":    "agent-755fea77",
		"attempt_id": "1",
	}
	req := fizeau.ServiceExecuteRequest{
		Prompt:   "hi",
		Harness:  "fiz",
		Provider: "fake",
		Model:    "fake-model",
		Metadata: wantMeta,
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	if len(events) == 0 {
		t.Fatal("expected events")
	}
	for i, ev := range events {
		if ev.Metadata == nil {
			t.Errorf("event %d (%s): metadata is nil", i, ev.Type)
			continue
		}
		for k, v := range wantMeta {
			if got := ev.Metadata[k]; got != v {
				t.Errorf("event %d (%s) metadata[%s]: want %q, got %q", i, ev.Type, k, v, got)
			}
		}
	}
}

// TestExecute_SessionLogDirOverride verifies that req.SessionLogDir
// directs the per-request session log to the supplied path.
func TestExecute_SessionLogDirOverride(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{Text: "ok"},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	req := fizeau.ServiceExecuteRequest{
		Prompt:        "hi",
		Harness:       "fiz",
		Provider:      "fake",
		Model:         "fake-model",
		SessionLogDir: dir,
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = drainEvents(t, ch, 5*time.Second)

	// At least one *.jsonl file should now exist in dir.
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		entries, _ := os.ReadDir(dir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("no session log written to %s; entries: %v", dir, names)
	}
}

// TestExecute_NativeSessionLogPreservesFullTrace locks in the contract that
// kept-sandbox bundles for the native ("fiz") harness preserve a complete
// session trace — not just session.start + session.end. Benchmark reruns and
// post-mortem debugging need to see llm.request / llm.response (and tool.call
// when applicable) to reconstruct what the loop did, so the runNative
// callback forwards every internal agent event into the session log file.
func TestExecute_NativeSessionLogPreservesFullTrace(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Static: []fizeau.FakeResponse{
			{Text: "ok"},
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dir := t.TempDir()
	req := fizeau.ServiceExecuteRequest{
		Prompt:        "hi",
		Harness:       "fiz",
		Provider:      "fake",
		Model:         "fake-model",
		SessionLogDir: dir,
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	_ = drainEvents(t, ch, 5*time.Second)

	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no session log written to %s", dir)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read session log %s: %v", matches[0], err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected multi-event session log (>=4 lines: session.start, llm.request, llm.response, session.end), got %d:\n%s", len(lines), string(body))
	}
	types := make([]string, 0, len(lines))
	for _, line := range lines {
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		types = append(types, ev.Type)
	}
	want := map[string]bool{
		"session.start": false,
		"llm.request":   false,
		"llm.response":  false,
		"session.end":   false,
	}
	for _, ty := range types {
		if _, ok := want[ty]; ok {
			want[ty] = true
		}
	}
	for ty, present := range want {
		if !present {
			t.Errorf("missing event type %q in session log; got types %v", ty, types)
		}
	}
	// Service writes session.start/session.end exactly once each. Verify the
	// loop's own start/end records were filtered out so consumers don't see
	// duplicates.
	starts, ends := 0, 0
	for _, ty := range types {
		switch ty {
		case "session.start":
			starts++
		case "session.end":
			ends++
		}
	}
	if starts != 1 {
		t.Errorf("session.start count: want 1, got %d (types %v)", starts, types)
	}
	if ends != 1 {
		t.Errorf("session.end count: want 1, got %d (types %v)", ends, types)
	}
}

// TestExecute_OSCancelDuringStreaming verifies that ctx.Done() while
// the loop is mid-flight terminates the stream cleanly with a
// cancelled-status final.
// @covers US-001-AC2
func TestExecute_OSCancelDuringStreaming(t *testing.T) {
	fp := &fizeau.FakeProvider{
		Dynamic: func(req fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			time.Sleep(2 * time.Second)
			return fizeau.FakeResponse{Text: "late"}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := fizeau.ServiceExecuteRequest{
		Prompt:   "hi",
		Harness:  "fiz",
		Provider: "fake",
		Model:    "fake-model",
	}
	ch, err := svc.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Cancel before the provider's slow Dynamic returns.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	events := drainEvents(t, ch, 5*time.Second)
	final := findFinal(events)
	if final == nil {
		t.Fatal("expected final event")
	}
	got := finalStatus(t, final)
	if got != "cancelled" && got != "failed" {
		t.Errorf("status: want cancelled/failed, got %q", got)
	}
}

// runNativePlanningCase drives Execute with a Dynamic FakeProvider that
// captures every Provider.Chat invocation. Returns the captured calls and
// the drained events so individual planning-wiring tests can assert
// pre-loop planning behavior without duplicating scaffolding.
func runNativePlanningCase(t *testing.T, req fizeau.ServiceExecuteRequest) ([]fizeau.FakeRequest, []fizeau.ServiceEvent) {
	t.Helper()
	var (
		mu    sync.Mutex
		calls []fizeau.FakeRequest
	)
	fp := &fizeau.FakeProvider{
		Dynamic: func(fr fizeau.FakeRequest) (fizeau.FakeResponse, error) {
			mu.Lock()
			calls = append(calls, fr)
			idx := len(calls)
			mu.Unlock()
			if idx == 1 {
				return fizeau.FakeResponse{Text: "plan-or-final-1", Usage: fizeau.TokenUsage{Input: 3, Output: 2, Total: 5}}, nil
			}
			return fizeau.FakeResponse{Text: "main-loop-final", Usage: fizeau.TokenUsage{Input: 4, Output: 1, Total: 5}}, nil
		},
	}
	opts := fizeau.ServiceOptions{}
	opts.FakeProvider = fp
	svc, err := fizeau.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	events := drainEvents(t, ch, 5*time.Second)
	mu.Lock()
	defer mu.Unlock()
	out := append([]fizeau.FakeRequest(nil), calls...)
	return out, events
}

// firstUserMessageContent returns the first RoleUser message content from
// the captured FakeRequest, or "" when none exist.
func firstUserMessageContent(fr fizeau.FakeRequest) string {
	for _, m := range fr.Messages {
		if string(m.Role) == "user" {
			return m.Content
		}
	}
	return ""
}

// TestRunNative_BenchmarkPresetDoesNotEnablePlanning asserts that
// ToolPreset="benchmark" by itself no longer triggers planning mode. The
// implicit coupling was removed so benchmark profiles must opt in explicitly
// via the profile's sampling.planning_mode field (which surfaces as
// ServiceExecuteRequest.PlanningMode=true). With PlanningMode false the first
// Provider.Chat call must be the main-loop turn, carrying the configured tool
// set and not the planning prompt.
func TestRunNative_BenchmarkPresetDoesNotEnablePlanning(t *testing.T) {
	calls, _ := runNativePlanningCase(t, fizeau.ServiceExecuteRequest{
		Prompt:       "implement feature X",
		Harness:      "fiz",
		Model:        "fake-model",
		Provider:     "fake",
		ToolPreset:   "benchmark",
		PlanningMode: false,
	})
	if len(calls) < 1 {
		t.Fatalf("expected >=1 provider call, got 0")
	}
	if got := len(calls[0].Tools); got == 0 {
		t.Errorf("first call: expected main-loop tool set, got 0 tools (planning leaked on)")
	}
	if msg := firstUserMessageContent(calls[0]); strings.Contains(msg, "concise plan") {
		t.Errorf("first call: unexpected planning prompt for benchmark preset alone; got %q", msg)
	}
}

// TestRunNative_PlanningModeFlag asserts that ServiceExecuteRequest.PlanningMode=true
// enables planning independent of the tool preset (here: default preset).
func TestRunNative_PlanningModeFlag(t *testing.T) {
	calls, _ := runNativePlanningCase(t, fizeau.ServiceExecuteRequest{
		Prompt:       "implement feature Y",
		Harness:      "fiz",
		Model:        "fake-model",
		Provider:     "fake",
		ToolPreset:   "default",
		PlanningMode: true,
	})
	if len(calls) < 2 {
		t.Fatalf("expected >=2 provider calls (planning + main loop), got %d", len(calls))
	}
	if got := len(calls[0].Tools); got != 0 {
		t.Errorf("planning call: want 0 tools, got %d (%v)", got, calls[0].Tools)
	}
	if msg := firstUserMessageContent(calls[0]); !strings.Contains(msg, "concise plan") {
		t.Errorf("planning call: user message missing planning prompt; got %q", msg)
	}
	if got := len(calls[1].Tools); got == 0 {
		t.Errorf("main-loop call: expected non-empty tool set, got 0 tools")
	}
}

// TestRunNative_NoPlanningByDefault sanity-checks that without PlanningMode and
// without the benchmark preset, the first provider call is the main-loop turn
// (carries the tool set and not the planning prompt).
func TestRunNative_NoPlanningByDefault(t *testing.T) {
	calls, _ := runNativePlanningCase(t, fizeau.ServiceExecuteRequest{
		Prompt:     "implement feature Z",
		Harness:    "fiz",
		Model:      "fake-model",
		Provider:   "fake",
		ToolPreset: "default",
	})
	if len(calls) < 1 {
		t.Fatalf("expected >=1 provider call, got 0")
	}
	if got := len(calls[0].Tools); got == 0 {
		t.Errorf("first call: expected main-loop tool set, got 0 tools (planning may have leaked on)")
	}
	if msg := firstUserMessageContent(calls[0]); strings.Contains(msg, "concise plan") {
		t.Errorf("first call: unexpected planning prompt in default-preset run; got %q", msg)
	}
}
