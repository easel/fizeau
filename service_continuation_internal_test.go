package fizeau

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/serviceimpl"
	"github.com/easel/fizeau/internal/session"
)

func TestExecuteOmitsContinuationLineage(t *testing.T) {
	dir := t.TempDir()
	svc := &service{}
	log := svc.openExecuteSessionLog(ServiceExecuteRequest{
		Prompt:        "ordinary execute",
		SessionLogDir: dir,
	}, RouteDecision{}, "ordinary-execute")
	log.WriteEnd(nil, harnesses.FinalData{Status: "success"})
	log.Close()

	events, err := session.ReadEvents(filepath.Join(dir, "ordinary-execute.jsonl"))
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want session.start + session.end", len(events))
	}
	for _, event := range events {
		assertContinuationKeysOmitted(t, event.Data)
	}

	final, err := json.Marshal(ServiceFinalData{Status: "success"})
	if err != nil {
		t.Fatalf("Marshal final: %v", err)
	}
	assertContinuationKeysOmitted(t, final)
}

func TestContinuationEffectiveFreshRequestInternal(t *testing.T) {
	innerMetadata := map[string]string{"source": "inner", "correlation_id": "invalid inner value"}
	outerMetadata := map[string]string{"source": "outer"}
	req := ServiceContinuationRequest{
		Prompt:        "outer prompt",
		Metadata:      outerMetadata,
		CorrelationID: "outer-correlation",
		FreshRequest: ServiceExecuteRequest{
			Prompt:        "inner prompt",
			Metadata:      innerMetadata,
			CorrelationID: "invalid inner correlation id",
			Role:          "reviewer",
			MaxTokens:     42,
		},
	}

	got := effectiveContinuationFreshRequest(req)
	if got.Prompt != req.Prompt || got.CorrelationID != req.CorrelationID {
		t.Fatalf("effective prompt/correlation = %q/%q, want outer %q/%q", got.Prompt, got.CorrelationID, req.Prompt, req.CorrelationID)
	}
	if !reflect.DeepEqual(got.Metadata, outerMetadata) {
		t.Fatalf("effective metadata = %#v, want outer %#v", got.Metadata, outerMetadata)
	}
	if got.Role != "reviewer" || got.MaxTokens != 42 {
		t.Fatalf("unrelated fresh fields were not preserved: %#v", got)
	}
	if req.FreshRequest.Prompt != "inner prompt" || !reflect.DeepEqual(req.FreshRequest.Metadata, innerMetadata) ||
		req.FreshRequest.CorrelationID != "invalid inner correlation id" {
		t.Fatalf("effective request construction mutated caller input: %#v", req.FreshRequest)
	}

	// Unsupported preflight does not consult service-owned routing, hub, log,
	// session, or process state. A nil receiver makes any such consultation a
	// deterministic test failure while the surface remains unimplemented.
	var svc *service
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{
		SessionID:     "parent",
		Prompt:        "continue",
		Policy:        ContinuationFreshSession,
		CorrelationID: "outer-correlation",
	})
	if events != nil || !errors.Is(err, ErrContinuationSessionUnavailable) {
		t.Fatalf("nil-state continuation preflight = (%v, %v), want nil/%v", events, err, ErrContinuationSessionUnavailable)
	}
}

type continuationFixtureRunner struct {
	prepareCalls atomic.Int64
	executeCalls atomic.Int64
	prepareErr   error
}

func (*continuationFixtureRunner) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "claude-tui", Type: "subprocess"}
}
func (*continuationFixtureRunner) HealthCheck(context.Context) error { return nil }
func (r *continuationFixtureRunner) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	r.executeCalls.Add(1)
	return continuationFixtureEvents(), nil
}
func (r *continuationFixtureRunner) PrepareContinuation(context.Context, harnesses.ContinuationRequest) (harnesses.PreparedContinuation, error) {
	r.prepareCalls.Add(1)
	if r.prepareErr != nil {
		return nil, r.prepareErr
	}
	return continuationFixturePrepared{}, nil
}
func (*continuationFixtureRunner) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{Name: "claude-tui", Transport: harnesses.PortableRuntimeTransportSubprocess, Mode: harnesses.PortableRuntimeStructuralUnpinned}
}

type continuationFixturePrepared struct{}

func (continuationFixturePrepared) Start(context.Context) (<-chan harnesses.Event, error) {
	return continuationFixtureEvents(), nil
}

func continuationFixtureEvents() <-chan harnesses.Event {
	data, _ := json.Marshal(harnesses.FinalData{Status: "success"})
	ch := make(chan harnesses.Event, 1)
	ch <- harnesses.Event{Type: harnesses.EventTypeFinal, Time: time.Now().UTC(), Data: data}
	close(ch)
	return ch
}

func continuationFixtureService(t *testing.T, runner *continuationFixtureRunner) (*service, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := serviceimpl.NewContinuationLocatorStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := harnesses.RouteRunnerKey{Harness: "claude-tui", Provider: "anthropic", Endpoint: "test", ServerInstance: "one", Model: "claude-sonnet-5"}
	authority := harnesses.NewRouteRunnerAuthority(map[string]harnesses.Harness{"claude-tui": runner}, nil)
	if _, err := authority.Register(key, runner); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(t, ServiceOptions{SessionLogDir: dir})
	svc.hub = serviceimpl.NewSessionHub()
	svc.routeRunners = authority
	svc.continuationLocators = store
	decision := continuationRouteDecision(key)
	parent := svc.openExecuteSessionLog(ServiceExecuteRequest{Prompt: "parent", SessionLogDir: dir}, decision, "parent")
	parent.WriteEnd(nil, harnesses.FinalData{Status: "success", RoutingActual: &harnesses.RoutingActual{
		Harness: key.Harness, Provider: key.Provider, ServerInstance: key.ServerInstance, Model: key.Model,
	}})
	parent.Close()
	if err := store.WritePending("parent", parent.Path(), key); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkComplete("parent"); err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestContinueSupportedResume(t *testing.T) {
	runner := &continuationFixtureRunner{}
	svc, dir := continuationFixtureService(t, runner)
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{
		SessionID: "parent", Prompt: "resume", Policy: ContinuationPreferResume,
		FreshRequest: ServiceExecuteRequest{SessionLogDir: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := DrainExecute(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	if result.Final == nil || runner.prepareCalls.Load() != 1 || runner.executeCalls.Load() != 0 {
		t.Fatalf("resume = final=%v prepare=%d execute=%d", result.Final, runner.prepareCalls.Load(), runner.executeCalls.Load())
	}
	entries, err := session.ReadEvents(result.Final.SessionLogPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range entries {
		if event.Type != "session.start" && event.Type != "session.end" {
			continue
		}
		if !bytes.Contains(event.Data, []byte(`"parent_session_id":"parent"`)) || !bytes.Contains(event.Data, []byte(`"continuation_policy":"prefer_resume"`)) || !bytes.Contains(event.Data, []byte(`"continuation":"resumed"`)) {
			t.Fatalf("missing service-owned lineage in %s: %s", event.Type, event.Data)
		}
	}
}

func TestContinueFreshSessionNeverProbesCapability(t *testing.T) {
	runner := &continuationFixtureRunner{}
	svc, dir := continuationFixtureService(t, runner)
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{
		SessionID: "parent", Prompt: "fresh", Policy: ContinuationFreshSession,
		FreshRequest: ServiceExecuteRequest{SessionLogDir: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DrainExecute(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if runner.prepareCalls.Load() != 0 || runner.executeCalls.Load() != 1 {
		t.Fatalf("fresh capability calls = prepare %d execute %d", runner.prepareCalls.Load(), runner.executeCalls.Load())
	}
}

func TestContinuePreferResumeFreshFallbackRequiresValidLineage(t *testing.T) {
	runner := &continuationFixtureRunner{prepareErr: harnesses.ErrContinuationEvidenceUnavailable}
	svc, dir := continuationFixtureService(t, runner)
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{SessionID: "parent", Prompt: "fresh after unavailable resume", Policy: ContinuationPreferResume, FreshRequest: ServiceExecuteRequest{SessionLogDir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := DrainExecute(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	if runner.prepareCalls.Load() != 1 || runner.executeCalls.Load() != 1 || result.Final == nil {
		t.Fatalf("fallback calls = prepare %d execute %d final=%v", runner.prepareCalls.Load(), runner.executeCalls.Load(), result.Final)
	}
	if events, err := svc.Continue(context.Background(), ServiceContinuationRequest{SessionID: "missing", Prompt: "no lineage", Policy: ContinuationPreferResume, FreshRequest: ServiceExecuteRequest{SessionLogDir: dir}}); events != nil || !errors.Is(err, ErrContinuationSessionUnavailable) {
		t.Fatalf("missing lineage = (%v, %v)", events, err)
	}
}

func TestContinueRequireResumeUnsupportedCreatesNoSessionOrSpawn(t *testing.T) {
	runner := &continuationFixtureRunner{prepareErr: harnesses.ErrContinuationEvidenceUnavailable}
	svc, dir := continuationFixtureService(t, runner)
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := svc.Continue(context.Background(), ServiceContinuationRequest{SessionID: "parent", Prompt: "must resume", Policy: ContinuationRequireResume})
	if events != nil || !errors.Is(err, ErrContinuationUnsupported) || runner.executeCalls.Load() != 0 {
		t.Fatalf("unsupported resume = (%v, %v), execute=%d", events, err, runner.executeCalls.Load())
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("unsupported resume created child artifact: before=%v after=%v", before, after)
	}
}

func assertContinuationKeysOmitted(t *testing.T, raw []byte) {
	t.Helper()
	for _, key := range [][]byte{
		[]byte(`"parent_session_id"`),
		[]byte(`"continuation_policy"`),
		[]byte(`"continuation"`),
	} {
		if bytes.Contains(raw, key) {
			t.Fatalf("ordinary Execute JSON contains %s: %s", key, raw)
		}
	}
}
