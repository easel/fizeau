package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestExecuteRejectsUnsupportedSubprocessModelBeforeRun(t *testing.T) {
	svc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:  "should not run",
		Harness: "codex",
		Model:   "not-a-real-model",
	})
	if err == nil {
		t.Fatal("expected Execute to return typed model incompatibility")
	}
	if ch != nil {
		t.Fatalf("expected no event channel for typed pre-resolution error, got %#v", ch)
	}
	if !errors.Is(err, ErrHarnessModelIncompatible{}) {
		t.Fatalf("errors.Is should match ErrHarnessModelIncompatible: %T %v", err, err)
	}
	var typed *ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "codex" || typed.Model != "not-a-real-model" {
		t.Fatalf("typed error=%#v, want codex/not-a-real-model", typed)
	}
}

// TestModelSupportedForHarnessClaudeTuiAcceptsFamilyWithoutDiscovery pins the
// fix for the execute gate that blocked claude-tui from running catalog-tier
// models. claude-tui's interactive /model picker only lists the CURRENT model,
// so live discovery (subprocessHarnessModelIDs) returns the resolved tier ID
// only by luck — on a cold/incomplete cache it is empty, and the gate must NOT
// reject a claude-family model on that basis (the running session can /model to
// any family member). The catalog "claude" surface routes bare-tier IDs like
// "sonnet-4.6" (no "claude-" prefix), so the gate must be family-aware, not a
// simple "claude-" prefix check.
func TestModelSupportedForHarnessClaudeTuiAcceptsFamilyWithoutDiscovery(t *testing.T) {
	cfg := harnesses.HarnessConfig{}
	// Family models that a default-policy route resolves to. The bare-tier
	// forms (no "claude-" prefix) are the ones the old "claude-" prefix check
	// rejected; all must pass for both claude and claude-tui.
	for _, tc := range []struct {
		name  string
		model string
	}{
		{"claude-tui", "sonnet-4.6"},
		{"claude-tui", "claude-opus-4.7"},
		{"claude-tui", "claude-haiku-5.5"},
		{"claude-tui", "opus-4.7"},
		{"claude-tui", "haiku-5.5"},
		{"claude", "sonnet-4.6"},
	} {
		if !modelSupportedForHarness(tc.name, cfg, tc.model, "") {
			t.Errorf("modelSupportedForHarness(%q, %q) = false, want true (family-aware, no discovery)", tc.name, tc.model)
		}
	}
	// A non-claude model must still be rejected for claude-tui.
	if modelSupportedForHarness("claude-tui", cfg, "gpt-5.4", "") {
		t.Error("modelSupportedForHarness(claude-tui, gpt-5.4) = true, want false (not a claude-family model)")
	}
}

func TestExecuteRejectsUnsupportedSubprocessReasoningBeforeRun(t *testing.T) {
	svc, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{},
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:    "should not run",
		Harness:   "claude",
		Reasoning: ReasoningMinimal,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	final := drainValidationFinal(t, ch)
	if final.Status != "failed" {
		t.Fatalf("Status: got %q, want failed", final.Status)
	}
	if !strings.Contains(final.Error, "unsupported reasoning") || !strings.Contains(final.Error, "claude") {
		t.Fatalf("Error: got %q", final.Error)
	}
}

func drainValidationFinal(t *testing.T, ch <-chan ServiceEvent) struct {
	Status string `json:"status"`
	Error  string `json:"error"`
} {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before final event")
			}
			if ev.Type != "final" {
				continue
			}
			var payload struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("unmarshal final: %v", err)
			}
			return payload
		case <-timeout.C:
			t.Fatal("timed out waiting for final event")
		}
	}
}
