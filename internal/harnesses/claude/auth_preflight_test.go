package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
)

// TestExecute_AuthPreflightFailsBeforeSubprocess (fizeau-0c5ae39c AC1): a
// simulated expired/missing credential probe aborts before the Binary body
// runs and surfaces a typed credential failure class.
func TestExecute_AuthPreflightFailsBeforeSubprocess(t *testing.T) {
	// Point Binary at a script that would fail the test if executed.
	bin := filepath.Join(t.TempDir(), "claude-must-not-run")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho MUST_NOT_RUN >&2\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Binary: bin,
		AuthUsabilityProbe: func() anthropic.AuthUsability {
			return anthropic.AuthUsability{
				Class:      anthropic.FailureClassCredentialInvalid,
				Diagnostic: "OAuth session expired and could not be refreshed",
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := r.Execute(ctx, harnesses.ExecuteRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var final harnesses.FinalData
	gotFinal := false
	for ev := range ch {
		if ev.Type != "final" {
			continue
		}
		gotFinal = true
		if err := json.Unmarshal(ev.Data, &final); err != nil {
			t.Fatalf("unmarshal final: %v", err)
		}
	}
	if !gotFinal {
		t.Fatal("expected final event from auth preflight")
	}
	if final.Status != "failed" {
		t.Fatalf("status=%q want failed", final.Status)
	}
	if final.RoutingActual == nil || final.RoutingActual.FailureClass != anthropic.FailureClassCredentialInvalid {
		t.Fatalf("RoutingActual=%#v want FailureClass=%q", final.RoutingActual, anthropic.FailureClassCredentialInvalid)
	}
	if final.Error == "" {
		t.Fatal("expected non-empty Error with remediation")
	}
}
