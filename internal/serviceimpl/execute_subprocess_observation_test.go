package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
)

type finalObservationHarness struct {
	execute func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error)
}

func TestExecuteSubprocessCannotOriginateContextCapacity(t *testing.T) {
	runner := finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 3)
		ch <- harnesses.Event{Type: harnesses.EventTypeContextCapacity, Data: json.RawMessage(`{"action":"rejected","call_kind":"main"}`)}
		ch <- harnesses.Event{Type: harnesses.EventTypeProgress, Data: json.RawMessage(`{"phase":"fixture"}`)}
		ch <- finalObservationEvent(harnesses.FinalData{
			Status: "success",
			ContextCapacity: &harnesses.ContextCapacityData{
				Action: "rejected", CallKind: "main",
			},
		})
		close(ch)
		return ch, nil
	}}
	var observed, emitted []harnesses.EventType
	var final harnesses.FinalData
	RunSubprocess(context.Background(), SubprocessRequest{}, runner, SubprocessCallbacks{
		ObserveEvent: func(event harnesses.Event) harnesses.Event {
			observed = append(observed, event.Type)
			return event
		},
		EmitEvent: func(event harnesses.Event) bool {
			emitted = append(emitted, event.Type)
			if event.Type == harnesses.EventTypeFinal {
				final = decodeFinalObservationEvent(t, event)
			}
			return true
		},
	})

	want := []harnesses.EventType{harnesses.EventTypeProgress, harnesses.EventTypeFinal}
	if !reflect.DeepEqual(observed, want) || !reflect.DeepEqual(emitted, want) {
		t.Fatalf("subprocess events observed/emitted = %v/%v, want %v", observed, emitted, want)
	}
	if final.Cause != harnesses.TerminalCauseCompleted || final.ContextCapacity != nil {
		t.Fatalf("subprocess final retained service-owned capacity authority: %#v", final)
	}
}

func (h finalObservationHarness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "final-observation-test"}
}

func (h finalObservationHarness) HealthCheck(context.Context) error { return nil }

func (h finalObservationHarness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	return h.execute(ctx, req)
}

func TestExecuteSubprocessFinalInvokesObservationBeforeDelivery(t *testing.T) {
	for _, delivery := range []string{"emit", "finalize"} {
		t.Run(delivery, func(t *testing.T) {
			dir := t.TempDir()
			registry := processlifecycle.NewFileRegistry(dir)
			record := cleanupTestRecord("observation-order-"+delivery, processlifecycle.StateOwned)
			if err := registry.Create(context.Background(), record); err != nil {
				t.Fatalf("Create lifecycle record: %v", err)
			}

			runner := finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
				ch := make(chan harnesses.Event, 1)
				ch <- finalObservationEvent(harnesses.FinalData{
					Status: "failed",
					RoutingActual: &harnesses.RoutingActual{
						Harness:      "adapter-harness",
						Provider:     "adapter-provider",
						Model:        "adapter-model",
						FailureClass: "credential_invalid",
					},
				})
				close(ch)
				return ch, nil
			}}

			order := make(chan string, 4)
			observationCalled := make(chan struct{}, 1)
			written := make(chan harnesses.FinalData, 1)
			delivered := make(chan harnesses.FinalData, 1)
			var observed harnesses.FinalData
			callbacks := SubprocessCallbacks{
				ObserveFinal: func(final harnesses.FinalData) error {
					order <- "observe"
					observed = final
					if final.RoutingActual != nil {
						routing := *final.RoutingActual
						observed.RoutingActual = &routing
					}
					observed.Warnings = append([]harnesses.FinalWarning(nil), final.Warnings...)
					observationCalled <- struct{}{}
					final.RoutingActual.Harness = "observer-mutation"
					final.Warnings = append(final.Warnings, harnesses.FinalWarning{Code: "observer-mutation"})
					return nil
				},
				WriteEnd: func(_ map[string]string, final harnesses.FinalData) {
					order <- "write"
					written <- final
				},
				ObserveEvent: func(event harnesses.Event) harnesses.Event {
					order <- "event"
					return event
				},
			}
			if delivery == "emit" {
				callbacks.EmitEvent = func(event harnesses.Event) bool {
					order <- "emit"
					delivered <- decodeFinalObservationEvent(t, event)
					return true
				}
				callbacks.Finalize = func(harnesses.FinalData) {
					t.Error("Finalize called when EmitEvent is configured")
				}
			} else {
				callbacks.Finalize = func(final harnesses.FinalData) {
					order <- "finalize"
					delivered <- final
				}
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				RunSubprocess(context.Background(), SubprocessRequest{
					SessionID:         record.OperationID,
					LifecycleStateDir: dir,
					CleanupTimeout:    cleanupCoordinationTimeout,
					SessionLogPath:    "/tmp/fizeau-observation-session.jsonl",
					Decision: ExecuteRunnerDecision{
						Harness:        "claude",
						Provider:       "anthropic",
						ServerInstance: "account-primary",
						Model:          "claude-sonnet",
					},
				}, runner, callbacks)
			}()

			select {
			case <-observationCalled:
				t.Fatal("observation preceded subprocess cleanup")
			case <-time.After(30 * time.Millisecond):
			}
			record.State = processlifecycle.StateCompleted
			record.Revision = 2
			record.Timestamps.UpdatedAt = time.Now().UTC()
			record.Timestamps.CleanupCompletedAt = record.Timestamps.UpdatedAt
			if err := registry.Update(context.Background(), record, 1); err != nil {
				t.Fatalf("Update lifecycle record: %v", err)
			}
			if err := registry.Delete(context.Background(), record.RecordID, 2); err != nil {
				t.Fatalf("Delete lifecycle record: %v", err)
			}

			select {
			case <-observationCalled:
			case <-time.After(cleanupCoordinationTimeout):
				t.Fatal("final was not observed after cleanup")
			}
			select {
			case <-done:
			case <-time.After(cleanupCoordinationTimeout):
				t.Fatal("RunSubprocess did not return")
			}

			if observed.Outcome != harnesses.SessionOutcomeFailed || observed.Cause != harnesses.TerminalCauseHarnessFailed || observed.Stage != harnesses.SessionStageHarness {
				t.Fatalf("observation terminal tuple = %+v", observed)
			}
			if observed.SessionLogPath != "/tmp/fizeau-observation-session.jsonl" {
				t.Fatalf("observation session log path = %q", observed.SessionLogPath)
			}
			wantRouting := &harnesses.RoutingActual{
				Harness:        "claude",
				Provider:       "anthropic",
				ServerInstance: "account-primary",
				Model:          "claude-sonnet",
				FailureClass:   "credential_invalid",
			}
			if !reflect.DeepEqual(observed.RoutingActual, wantRouting) {
				t.Fatalf("observation routing actual = %+v, want %+v", observed.RoutingActual, wantRouting)
			}

			writtenFinal := <-written
			deliveredFinal := <-delivered
			if writtenFinal.RoutingActual == nil || writtenFinal.RoutingActual.Harness != "claude" || len(writtenFinal.Warnings) != 0 {
				t.Fatalf("WriteEnd final was changed by observer mutation: %+v", writtenFinal)
			}
			if !reflect.DeepEqual(writtenFinal, deliveredFinal) {
				t.Fatalf("written final differs from delivered final after observer mutation:\nwritten: %+v\ndelivered: %+v", writtenFinal, deliveredFinal)
			}

			wantOrder := []string{"observe", "write", "event", delivery}
			for i, want := range wantOrder {
				select {
				case got := <-order:
					if got != want {
						t.Fatalf("callback order[%d] = %q, want %q", i, got, want)
					}
				default:
					t.Fatalf("callback order ended before %q", want)
				}
			}
			select {
			case got := <-order:
				t.Fatalf("unexpected extra callback %q", got)
			default:
			}
		})
	}
}

func TestExecuteSubprocessFinalObservationRunsExactlyOnce(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness)
	}{
		{
			name: "harness final",
			setup: func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				return context.Background(), SubprocessRequest{}, finalObservationEventHarness("success")
			},
		},
		{
			name: "synthesized stream final",
			setup: func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				return context.Background(), SubprocessRequest{}, finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
					ch := make(chan harnesses.Event)
					close(ch)
					return ch, nil
				}}
			},
		},
		{
			name: "spawn failure",
			setup: func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				return context.Background(), SubprocessRequest{}, finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
					return nil, errors.New("spawn failed")
				}}
			},
		},
		{
			name: "cancellation",
			setup: func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, SubprocessRequest{}, finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
					return make(chan harnesses.Event), nil
				}}
			},
		},
		{
			name: "cleanup supersession",
			setup: func(t *testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				dir := t.TempDir()
				registry := processlifecycle.NewFileRegistry(dir)
				record := cleanupTestRecord("observation-cleanup-failure", processlifecycle.StateCleanupFailed)
				record.EscapeEvidence = []processlifecycle.EscapeEvidence{{Kind: "boundary_not_empty", Detail: "child remains"}}
				if err := registry.Create(context.Background(), record); err != nil {
					t.Fatalf("Create lifecycle record: %v", err)
				}
				return context.Background(), SubprocessRequest{
					SessionID:         record.OperationID,
					LifecycleStateDir: dir,
					CleanupTimeout:    time.Second,
				}, finalObservationEventHarness("success")
			},
		},
		{
			name: "duplicate harness final",
			setup: func(*testing.T) (context.Context, SubprocessRequest, finalObservationHarness) {
				return context.Background(), SubprocessRequest{}, finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
					ch := make(chan harnesses.Event, 2)
					ch <- finalObservationEvent(harnesses.FinalData{Status: "success", FinalText: "first"})
					ch <- finalObservationEvent(harnesses.FinalData{Status: "failed", FinalText: "duplicate"})
					close(ch)
					return ch, nil
				}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, req, runner := tt.setup(t)
			var observations, writes, terminals int
			var observed, delivered harnesses.FinalData
			RunSubprocess(ctx, req, runner, SubprocessCallbacks{
				ObserveFinal: func(final harnesses.FinalData) error {
					observations++
					observed = final
					return nil
				},
				WriteEnd: func(_ map[string]string, final harnesses.FinalData) {
					writes++
				},
				EmitEvent: func(event harnesses.Event) bool {
					if event.Type == harnesses.EventTypeFinal {
						terminals++
						delivered = decodeFinalObservationEvent(t, event)
					}
					return true
				},
			})

			if observations != 1 || writes != 1 || terminals != 1 {
				t.Fatalf("callback counts = observations %d, writes %d, terminals %d; want 1 each", observations, writes, terminals)
			}
			if !reflect.DeepEqual(observed, delivered) {
				t.Fatalf("observed final differs from delivered final:\nobserved: %+v\ndelivered: %+v", observed, delivered)
			}
			if tt.name == "duplicate harness final" && delivered.FinalText != "first" {
				t.Fatalf("duplicate input delivered %q, want first final only", delivered.FinalText)
			}
			if tt.name == "cleanup supersession" && delivered.Cause != harnesses.TerminalCauseCleanupFailed {
				t.Fatalf("cleanup final cause = %q, want %q", delivered.Cause, harnesses.TerminalCauseCleanupFailed)
			}
		})
	}
}

func TestRouteObservationFailureDoesNotSuppressTerminal(t *testing.T) {
	runner := finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 1)
		ch <- finalObservationEvent(harnesses.FinalData{
			Status:   "success",
			Warnings: []harnesses.FinalWarning{{Code: "adapter_warning", Message: "preserved"}},
		})
		close(ch)
		return ch, nil
	}}

	var observations, writes, terminals int
	var written, delivered harnesses.FinalData
	RunSubprocess(context.Background(), SubprocessRequest{}, runner, SubprocessCallbacks{
		ObserveFinal: func(harnesses.FinalData) error {
			observations++
			return errors.New(strings.Repeat("é", 3000))
		},
		WriteEnd: func(_ map[string]string, final harnesses.FinalData) {
			writes++
			written = final
		},
		EmitEvent: func(event harnesses.Event) bool {
			if event.Type == harnesses.EventTypeFinal {
				terminals++
				delivered = decodeFinalObservationEvent(t, event)
			}
			return true
		},
	})

	if observations != 1 || writes != 1 || terminals != 1 {
		t.Fatalf("callback counts = observations %d, writes %d, terminals %d; want 1 each", observations, writes, terminals)
	}
	if !reflect.DeepEqual(written, delivered) {
		t.Fatalf("written final differs from delivered final:\nwritten: %+v\ndelivered: %+v", written, delivered)
	}
	if len(delivered.Warnings) != 2 {
		t.Fatalf("warnings = %#v, want adapter warning plus observation failure", delivered.Warnings)
	}
	warning := delivered.Warnings[1]
	if warning.Code != subprocessRouteObservationFailedWarningCode {
		t.Fatalf("warning code = %q, want %q", warning.Code, subprocessRouteObservationFailedWarningCode)
	}
	if len(warning.Message) > subprocessRouteObservationWarningMaxBytes {
		t.Fatalf("warning length = %d, want at most %d bytes", len(warning.Message), subprocessRouteObservationWarningMaxBytes)
	}
	if !utf8.ValidString(warning.Message) {
		t.Fatalf("warning is not valid UTF-8: %q", warning.Message)
	}
	if !strings.HasSuffix(warning.Message, subprocessRouteObservationWarningTruncation) {
		t.Fatalf("warning was not visibly truncated: %q", warning.Message)
	}
}

func finalObservationEventHarness(status string) finalObservationHarness {
	return finalObservationHarness{execute: func(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
		ch := make(chan harnesses.Event, 1)
		ch <- finalObservationEvent(harnesses.FinalData{Status: status})
		close(ch)
		return ch, nil
	}}
}

func finalObservationEvent(final harnesses.FinalData) harnesses.Event {
	raw, err := json.Marshal(final)
	if err != nil {
		panic(err)
	}
	return harnesses.Event{Type: harnesses.EventTypeFinal, Data: raw}
}

func decodeFinalObservationEvent(t *testing.T, event harnesses.Event) harnesses.FinalData {
	t.Helper()
	var final harnesses.FinalData
	if err := json.Unmarshal(event.Data, &final); err != nil {
		t.Fatalf("decode final event: %v", err)
	}
	return final
}
