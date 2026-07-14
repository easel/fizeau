package fizeau

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func TestHarnessCleanupTimeoutDefaultAndValidation(t *testing.T) {
	if got := (ServiceOptions{}).harnessCleanupTimeout(); got != 10*time.Second {
		t.Fatalf("zero HarnessCleanupTimeout = %s, want 10s", got)
	}
	if got := (ServiceOptions{HarnessCleanupTimeout: 750 * time.Millisecond}).harnessCleanupTimeout(); got != 750*time.Millisecond {
		t.Fatalf("explicit HarnessCleanupTimeout = %s", got)
	}
	if _, err := New(ServiceOptions{HarnessCleanupTimeout: -time.Nanosecond}); err == nil || !strings.Contains(err.Error(), "HarnessCleanupTimeout") {
		t.Fatalf("negative HarnessCleanupTimeout error = %v", err)
	}
}

func TestHarnessCleanupTimeoutIndependentFromStaleHarnessReaperGrace(t *testing.T) {
	opts := ServiceOptions{HarnessCleanupTimeout: 125 * time.Millisecond, StaleHarnessReaperGrace: 3 * time.Hour}
	if got := opts.harnessCleanupTimeout(); got != 125*time.Millisecond {
		t.Fatalf("cleanup timeout = %s", got)
	}
	if got := opts.staleHarnessReaperGrace(); got != 3*time.Hour {
		t.Fatalf("stale reaper grace = %s", got)
	}
	zero := ServiceOptions{}
	if zero.harnessCleanupTimeout() != 10*time.Second || zero.staleHarnessReaperGrace() != 5*time.Minute {
		t.Fatalf("independent defaults = cleanup %s stale %s", zero.harnessCleanupTimeout(), zero.staleHarnessReaperGrace())
	}
}

type lifecycleForwardingHarness struct {
	request chan<- harnesses.ExecuteRequest
}

func (h lifecycleForwardingHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "forwarding"}
}
func (h lifecycleForwardingHarness) HealthCheck(context.Context) error { return nil }
func (h lifecycleForwardingHarness) Execute(_ context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	h.request <- req
	raw, _ := json.Marshal(harnesses.FinalData{Status: "success"})
	events := make(chan harnesses.Event, 1)
	events <- harnesses.Event{Type: harnesses.EventTypeFinal, Data: raw}
	close(events)
	return events, nil
}

func TestRunSubprocessForwardsLifecycleOptions(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "sessions")
	wantStateDir := filepath.Join(filepath.Dir(logDir), "harness-sessions")
	svc := &service{opts: ServiceOptions{SessionLogDir: logDir, HarnessCleanupTimeout: 175 * time.Millisecond}}
	stateDir, err := processlifecycle.StateDirectory(svc.serviceSessionLogDir())
	if err != nil {
		t.Fatalf("lifecycle state directory: %v", err)
	}
	captured := make(chan harnesses.ExecuteRequest, 1)
	serviceimpl.RunSubprocess(context.Background(), serviceimpl.SubprocessRequest{
		SessionID:         "svc-forward",
		LifecycleStateDir: stateDir,
		CleanupTimeout:    svc.opts.harnessCleanupTimeout(),
		Decision:          serviceimpl.ExecuteRunnerDecision{Harness: "codex"},
	}, lifecycleForwardingHarness{request: captured}, serviceimpl.SubprocessCallbacks{
		EmitEvent: func(harnesses.Event) bool { return true },
	})
	got := <-captured
	if got.SessionID != "svc-forward" || got.LifecycleStateDir != wantStateDir || got.CleanupTimeout != 175*time.Millisecond {
		t.Fatalf("lifecycle forwarding = session %q dir %q timeout %s", got.SessionID, got.LifecycleStateDir, got.CleanupTimeout)
	}
}
