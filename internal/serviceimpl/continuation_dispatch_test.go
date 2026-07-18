package serviceimpl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

const continuationNativeToken = "native-resume-token-must-not-escape"

type continuationTestRunner struct {
	prepareCalls int
	startCalls   int
	evidence     bool
	child        *continuationTestChild
	startErr     error
}

func (*continuationTestRunner) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{Name: "claude-tui"}
}
func (*continuationTestRunner) HealthCheck(context.Context) error { return nil }
func (*continuationTestRunner) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
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

func continuationRequest(binding harnesses.RouteRunnerBinding, child *continuationTestChild) ContinuationDispatchRequest {
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

func TestContinuationEvidenceCommitsBeforeSuccessfulTerminal(t *testing.T) {
	runner, child := &continuationTestRunner{}, &continuationTestChild{}
	runner.child = child
	prepared, err := PrepareRegisteredContinuation(context.Background(), continuationRequest(continuationBinding(t, harnesses.RouteRunnerKey{Harness: "claude-tui"}, runner), child))
	if err != nil {
		t.Fatal(err)
	}
	events, err := StartPreparedContinuation(context.Background(), child, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Type != harnesses.EventTypeFinal || !runner.evidence {
		t.Fatal("successful final observed before private evidence committed")
	}
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
	runner, child := &continuationTestRunner{startErr: errors.New("native rejected " + continuationNativeToken)}, &continuationTestChild{}
	runner.child = child
	prepared, err := PrepareRegisteredContinuation(context.Background(), continuationRequest(continuationBinding(t, harnesses.RouteRunnerKey{Harness: "claude-tui"}, runner), child))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StartPreparedContinuation(context.Background(), child, prepared); err == nil {
		t.Fatal("Start succeeded")
	}
	if !child.terminal || runner.startCalls != 1 || child.terminalErr == nil {
		t.Fatalf("start failure not terminalized once: %+v calls=%d", child, runner.startCalls)
	}
}
