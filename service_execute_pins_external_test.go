package fizeau_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	fizeau "github.com/easel/fizeau"
)

func TestExecuteUnknownHarnessPublicFailure(t *testing.T) {
	result := executeUnknownHarnessPublicFailure(t, "does-not-exist")
	if result.FinalStatus != "failed" || !strings.Contains(result.TerminalError, `unknown harness "does-not-exist"`) {
		t.Fatalf("result = %#v, want unknown-harness final failure", result)
	}
}

func TestExecuteRetiredAgentAliasPublicFailure(t *testing.T) {
	result := executeUnknownHarnessPublicFailure(t, "agent")
	if result.FinalStatus != "failed" || !strings.Contains(result.TerminalError, `unknown harness "agent"`) {
		t.Fatalf("result = %#v, want retired agent alias to remain unknown", result)
	}
}

func TestExecuteUnsupportedExplicitHarnessModelPublicFailure(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       &stubServiceConfig{},
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := svc.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt:  "should not run",
		Harness: "codex",
		Model:   "not-a-real-model",
	})
	if err == nil {
		t.Fatal("expected typed model incompatibility")
	}
	if ch != nil {
		t.Fatalf("event channel = %#v, want nil before dispatch", ch)
	}
	if !errors.Is(err, fizeau.ErrHarnessModelIncompatible{}) {
		t.Fatalf("errors.Is should match ErrHarnessModelIncompatible: %T %v", err, err)
	}
	var typed *fizeau.ErrHarnessModelIncompatible
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As should extract ErrHarnessModelIncompatible: %T %v", err, err)
	}
	if typed.Harness != "codex" || typed.Model != "not-a-real-model" {
		t.Fatalf("typed error = %#v, want codex/not-a-real-model", typed)
	}
}

func TestExecuteUnsupportedReasoningPublicFinalFailure(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       &stubServiceConfig{},
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := svc.Execute(ctx, fizeau.ServiceExecuteRequest{
		Prompt:    "should not run",
		Harness:   "claude",
		Reasoning: fizeau.ReasoningMinimal,
	})
	if err != nil {
		t.Fatalf("Execute returned synchronous error: %v", err)
	}
	result, err := fizeau.DrainExecute(ctx, ch)
	if err != nil {
		t.Fatalf("DrainExecute: %v", err)
	}
	if result.FinalStatus != "failed" ||
		!strings.Contains(result.TerminalError, "unsupported reasoning") ||
		!strings.Contains(result.TerminalError, "claude") {
		t.Fatalf("result = %#v, want unsupported-reasoning final failure for claude", result)
	}
}

func executeUnknownHarnessPublicFailure(t *testing.T, harness string) *fizeau.DrainExecuteResult {
	t.Helper()
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       &stubServiceConfig{},
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := svc.Execute(ctx, fizeau.ServiceExecuteRequest{
		Prompt:  "should not run",
		Harness: harness,
		Model:   "model",
	})
	if err != nil {
		t.Fatalf("Execute returned synchronous error: %v", err)
	}
	result, err := fizeau.DrainExecute(ctx, ch)
	if err != nil {
		t.Fatalf("DrainExecute: %v", err)
	}
	return result
}
