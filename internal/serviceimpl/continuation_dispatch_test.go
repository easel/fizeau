package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

const continuationNativeToken = "native-resume-token-must-not-escape"

type continuationTestRunner struct {
	prepareCalls int
	startCalls   int
	evidence     bool
	evidencePath string
	child        *continuationTestChild
	startErr     error
	executeCalls int
}

func (*continuationTestRunner) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "claude-tui"}
}
func (*continuationTestRunner) HealthCheck(context.Context) error { return nil }
func (r *continuationTestRunner) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	r.executeCalls++
	return nil, errors.New("unexpected ordinary execute")
}
func (r *continuationTestRunner) PrepareContinuation(_ context.Context, req harnesses.ContinuationRequest) (harnesses.PreparedContinuation, error) {
	r.prepareCalls++
	if req.ParentSessionID == "" || req.Request.SessionID == "" {
		return nil, harnesses.ErrContinuationRequestInvalid
	}
	if r.child != nil && (r.child.created || r.child.lease || r.child.started) {
		return nil, errors.New("prepare was not side-effect free")
	}
	return continuationTestPrepared{runner: r}, nil
}

type continuationTestPrepared struct{ runner *continuationTestRunner }

func (p continuationTestPrepared) Start(context.Context) (<-chan harnesses.Event, error) {
	p.runner.startCalls++
	if p.runner.child == nil || !p.runner.child.created || !p.runner.child.lease {
		return nil, errors.New("start before child lease")
	}
	p.runner.child.started = true
	if p.runner.startErr != nil {
		return nil, p.runner.startErr
	}
	// This is the route-private evidence durability point. It deliberately
	// precedes the successful implementation final.
	if p.runner.evidencePath != "" {
		if err := os.WriteFile(p.runner.evidencePath, []byte(continuationNativeToken), 0o600); err != nil {
			return nil, err
		}
	}
	p.runner.evidence = true
	events := make(chan harnesses.Event, 1)
	events <- harnesses.Event{Type: harnesses.EventTypeFinal}
	close(events)
	return events, nil
}

type continuationTestChild struct {
	created, lease, started, terminal bool
	leaseID, containmentID            string
	terminalErr                       error
	order                             []string
	onTerminal                        func(error)
}

func (c *continuationTestChild) Create(context.Context) error {
	c.created = true
	c.order = append(c.order, "child")
	return nil
}
func (c *continuationTestChild) AcquireLease(context.Context) error {
	if !c.created {
		return errors.New("lease before child")
	}
	c.lease = true
	c.leaseID, c.containmentID = "child-lease", "child-containment"
	c.order = append(c.order, "lease")
	return nil
}
func (c *continuationTestChild) TerminalizeStartFailure(_ context.Context, err error) {
	c.terminal, c.terminalErr = true, err
	c.order = append(c.order, "terminal")
	if c.onTerminal != nil {
		c.onTerminal(err)
	}
}

func continuationBinding(t *testing.T, key harnesses.RouteRunnerKey, runner harnesses.Harness) harnesses.RouteRunnerBinding {
	t.Helper()
	authority := harnesses.NewRouteRunnerAuthority(nil, nil)
	binding, err := authority.Register(key, runner)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func continuationRequest(binding harnesses.RouteRunnerBinding, child ContinuationChild) ContinuationDispatchRequest {
	return ContinuationDispatchRequest{
		ParentSessionID: "parent-fizeau-id",
		Route:           binding,
		Request:         harnesses.ExecuteRequest{SessionID: "child-fizeau-id", Metadata: map[string]string{"caller": continuationNativeToken}},
		Child:           child,
	}
}

func TestContinuationUsesRegisteredRouteInstance(t *testing.T) {
	key := harnesses.RouteRunnerKey{Harness: "claude-tui", Provider: "anthropic", Endpoint: "east", ServerInstance: "one", Model: "claude-sonnet-5"}
	registered, other := &continuationTestRunner{}, &continuationTestRunner{}
	child := &continuationTestChild{}
	registered.child = child
	prepared, err := PrepareRegisteredContinuation(context.Background(), continuationRequest(continuationBinding(t, key, registered), child))
	if err != nil {
		t.Fatal(err)
	}
	if registered.prepareCalls != 1 || other.prepareCalls != 0 {
		t.Fatalf("prepare calls registered/other = %d/%d", registered.prepareCalls, other.prepareCalls)
	}
	if _, err := StartPreparedContinuation(context.Background(), child, prepared); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationPrepareOrdersChildAndSpawn(t *testing.T) {
	runner, child := &continuationTestRunner{}, &continuationTestChild{}
	runner.child = child
	prepared, err := PrepareRegisteredContinuation(context.Background(), continuationRequest(continuationBinding(t, harnesses.RouteRunnerKey{Harness: "claude-tui"}, runner), child))
	if err != nil {
		t.Fatal(err)
	}
	if child.created || child.lease || child.started || runner.startCalls != 0 {
		t.Fatal("prepare created child, lease, or start effect")
	}
	if _, err := StartPreparedContinuation(context.Background(), child, prepared); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(child.order, ","); got != "child,lease" {
		t.Fatalf("pre-start order = %q", got)
	}
	if runner.startCalls != 1 || !child.started {
		t.Fatalf("start calls/started = %d/%v", runner.startCalls, child.started)
	}
}

func TestContinuationDispatchAcquiresFreshLifecycleLease(t *testing.T) {
	parentLease, parentContainment := "parent-lease", "parent-containment"
	runner, child := &continuationTestRunner{}, &continuationTestChild{}
	runner.child = child
	prepared, err := PrepareRegisteredContinuation(context.Background(), continuationRequest(continuationBinding(t, harnesses.RouteRunnerKey{Harness: "claude-tui"}, runner), child))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartPreparedContinuation(context.Background(), child, prepared); err != nil {
		t.Fatal(err)
	}
	if child.leaseID == parentLease || child.containmentID == parentContainment || !child.lease {
		t.Fatalf("child reused parent lifecycle identity: %+v", child)
	}
}

// evidenceOrderingLog makes the durable service-log boundary observable while
// still using the production SessionLog and locator implementations. It is
// deliberately not an in-memory callback: WriteEnd writes a real JSONL
// session.end record, and MarkComplete subsequently validates that record.
type evidenceOrderingLog struct {
	*SessionLog
	testingT     *testing.T
	evidencePath string
}

func (l *evidenceOrderingLog) WriteEnd(meta map[string]string, final harnesses.FinalData) {
	l.testingT.Helper()
	if _, err := os.Stat(l.evidencePath); err != nil {
		l.testingT.Fatalf("service attempted session.end before private evidence was durable: %v", err)
	}
	l.SessionLog.WriteEnd(meta, final)
}

func TestContinuationEvidenceCommitsBeforeSuccessfulTerminal(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := harnesses.RouteRunnerKey{Harness: "claude-tui", Provider: "anthropic", Endpoint: "east", ServerInstance: "one", Model: "claude-sonnet-5"}
	childID := "child-durable-evidence"
	sessionLog := OpenSessionLog(SessionLogOptions{
		Dir:       root,
		SessionID: childID,
		Start:     session.SessionStartData{ParentSessionID: "parent-fizeau-id", Continuation: "resumed"},
		EndBase: session.SessionEndData{
			ParentSessionID:        "parent-fizeau-id",
			Continuation:           "resumed",
			ResolvedHarness:        route.Harness,
			SelectedProvider:       route.Provider,
			SelectedEndpoint:       route.Endpoint,
			SelectedServerInstance: route.ServerInstance,
			ResolvedModel:          route.Model,
		},
	})
	evidencePath := filepath.Join(root, "private", "opaque-evidence")
	if err := os.Mkdir(filepath.Dir(evidencePath), 0o700); err != nil {
		t.Fatal(err)
	}
	log := &evidenceOrderingLog{SessionLog: sessionLog, testingT: t, evidencePath: evidencePath}
	out := make(chan harnesses.Event, 1)
	state := &executeRunState{
		req: ExecuteRequest{
			SessionID:            childID,
			FinalMetadata:        map[string]string{"caller": continuationNativeToken},
			ContinuationLocators: store,
		},
		out: out,
		log: log,
	}
	child := &continuationTestChild{}
	child.onTerminal = func(error) { t.Fatal("successful continuation terminalized as a failure") }
	// Use a small adapter rather than duplicating the session/log persistence
	// path in a fake callback.
	durableChild := &durableContinuationChild{continuationTestChild: child, store: store, sessionID: childID, logPath: sessionLog.Path(), route: route}
	runner := &continuationTestRunner{child: child, evidencePath: evidencePath}

	prepared, err := PrepareRegisteredContinuation(ctx, continuationRequest(continuationBinding(t, route, runner), durableChild))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := StartPreparedContinuation(ctx, durableChild, prepared)
	if err != nil {
		t.Fatal(err)
	}
	event := <-stream
	if event.Type != harnesses.EventTypeFinal || !runner.evidence {
		t.Fatal("prepared continuation did not commit evidence before its final")
	}
	// commitFinal is the production terminal path: WriteEnd precedes locator
	// promotion, and emitFinal follows both. The test observes each durable
	// artifact before allowing the public final to be read.
	state.commitFinal(ctx, harnesses.FinalData{Status: "success", FinalText: "resumed"}, TerminalOriginHarness)
	if _, err := store.ResolveCompleted(childID); err != nil {
		t.Fatalf("completed locator unavailable after durable terminal: %v", err)
	}
	select {
	case public := <-out:
		if public.Type != harnesses.EventTypeFinal {
			t.Fatalf("public event type = %q", public.Type)
		}
		if strings.Contains(string(public.Data), continuationNativeToken) {
			t.Fatal("private evidence escaped into public final")
		}
	default:
		t.Fatal("successful public final was not emitted after durable terminal")
	}
	sessionLog.Close()
	bytes, err := os.ReadFile(sessionLog.Path())
	if err != nil {
		t.Fatal(err)
	}
	// The caller supplied the same recognizable string as ordinary metadata;
	// it must survive projection while the route-private evidence never gains
	// another serialized representation.
	if strings.Count(string(bytes), continuationNativeToken) != 1 {
		t.Fatalf("private evidence escaped into session JSONL: %s", bytes)
	}
	durableEvents, err := session.ReadEvents(sessionLog.Path())
	if err != nil || !containsSessionEnd(durableEvents) {
		t.Fatalf("durable session.end missing after successful continuation: %v", err)
	}
	for _, event := range durableEvents {
		if event.Type != agentcore.EventSessionEnd {
			continue
		}
		var end session.SessionEndData
		if err := json.Unmarshal(event.Data, &end); err != nil {
			t.Fatal(err)
		}
		if end.Metadata["caller"] != continuationNativeToken {
			t.Fatalf("caller metadata was not preserved: %#v", end.Metadata)
		}
	}
}

// durableContinuationChild ties the existing child ordering seam to the real
// pending locator store. It keeps the route-private prepared object out of
// both the locator and session JSONL.
type durableContinuationChild struct {
	*continuationTestChild
	store     *ContinuationLocatorStore
	sessionID string
	logPath   string
	route     harnesses.RouteRunnerKey
}

func (c *durableContinuationChild) Create(ctx context.Context) error {
	if err := c.continuationTestChild.Create(ctx); err != nil {
		return err
	}
	return c.store.WritePending(c.sessionID, c.logPath, c.route)
}

func containsSessionEnd(events []agentcore.Event) bool {
	for _, event := range events {
		if event.Type == agentcore.EventSessionEnd {
			return true
		}
	}
	return false
}

func TestContinuationNativeReferenceIsNotSerialized(t *testing.T) {
	runner, child := &continuationTestRunner{startErr: errors.New(continuationNativeToken)}, &continuationTestChild{}
	runner.child = child
	req := continuationRequest(continuationBinding(t, harnesses.RouteRunnerKey{Harness: "claude-tui"}, runner), child)
	prepared, err := PrepareRegisteredContinuation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = StartPreparedContinuation(context.Background(), child, prepared)
	if err == nil || strings.Contains(err.Error(), continuationNativeToken) || strings.Contains(child.terminalErr.Error(), continuationNativeToken) {
		t.Fatalf("native reference escaped in start failure: %v / %v", err, child.terminalErr)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), continuationNativeToken) && !strings.Contains(string(raw), `"caller":"`+continuationNativeToken) {
		t.Fatalf("derived native reference serialized: %s", raw)
	}
}

func TestContinuationStartFailureTerminalizesChildWithoutFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := harnesses.RouteRunnerKey{Harness: "claude-tui", Provider: "anthropic", Endpoint: "east", ServerInstance: "one", Model: "claude-sonnet-5"}
	childID := "child-start-failure"
	log := OpenSessionLog(SessionLogOptions{Dir: root, SessionID: childID, EndBase: session.SessionEndData{
		ParentSessionID: "parent-fizeau-id", Continuation: "resumed", ResolvedHarness: route.Harness,
		SelectedProvider: route.Provider, SelectedEndpoint: route.Endpoint, SelectedServerInstance: route.ServerInstance, ResolvedModel: route.Model,
	}})
	out := make(chan harnesses.Event, 2)
	state := &executeRunState{
		req: ExecuteRequest{SessionID: childID, FinalMetadata: map[string]string{"caller": continuationNativeToken}, ContinuationLocators: store},
		out: out,
		log: log,
	}
	child := &continuationTestChild{}
	child.onTerminal = func(err error) {
		state.commitFinal(ctx, harnesses.FinalData{Status: "failed", Error: err.Error()}, TerminalOriginSpawn)
	}
	durableChild := &durableContinuationChild{continuationTestChild: child, store: store, sessionID: childID, logPath: log.Path(), route: route}
	runner := &continuationTestRunner{startErr: errors.New("native rejected " + continuationNativeToken), child: child}
	fresh := &continuationTestRunner{}
	prepared, err := PrepareRegisteredContinuation(ctx, continuationRequest(continuationBinding(t, route, runner), durableChild))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartPreparedContinuation(ctx, durableChild, prepared); err == nil {
		t.Fatal("Start succeeded")
	} else if strings.Contains(err.Error(), continuationNativeToken) {
		t.Fatalf("native start error escaped through service boundary: %v", err)
	}
	if !child.terminal || runner.startCalls != 1 || child.terminalErr == nil {
		t.Fatalf("start failure not terminalized once: %+v calls=%d", child, runner.startCalls)
	}
	if runner.executeCalls != 0 || fresh.executeCalls != 0 {
		t.Fatalf("native Start failure invoked fresh Execute fallback: resumed=%d fresh=%d", runner.executeCalls, fresh.executeCalls)
	}
	if _, err := store.ResolveCompleted(childID); err != nil {
		t.Fatalf("terminalized child did not durably complete its locator: %v", err)
	}
	select {
	case public := <-out:
		if public.Type != harnesses.EventTypeFinal || strings.Contains(string(public.Data), continuationNativeToken) || !strings.Contains(string(public.Data), `"status":"failed"`) {
			t.Fatalf("public terminal = %s", public.Data)
		}
	default:
		t.Fatal("terminalized child did not publish its failed final")
	}
	select {
	case event := <-out:
		t.Fatalf("unexpected second terminal/fallback event: %s", event.Data)
	default:
	}
	log.Close()
	locatorPath, err := store.LocatorPath(childID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{log.Path(), locatorPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "native rejected") || strings.Contains(string(raw), "resume_token") {
			t.Fatalf("native Start evidence escaped into durable artifact %s: %s", path, raw)
		}
	}
}
