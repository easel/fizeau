package harnesses

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContinuationTypesMatchContract(t *testing.T) {
	requestType := reflect.TypeOf(ContinuationRequest{})
	wantFields := []struct {
		name   string
		typeOf reflect.Type
	}{
		{name: "ParentSessionID", typeOf: reflect.TypeOf("")},
		{name: "Request", typeOf: reflect.TypeOf(ExecuteRequest{})},
	}
	if requestType.NumField() != len(wantFields) {
		t.Fatalf("ContinuationRequest has %d fields, want exactly %d", requestType.NumField(), len(wantFields))
	}
	for i, want := range wantFields {
		got := requestType.Field(i)
		if got.Name != want.name || got.Type != want.typeOf {
			t.Errorf("ContinuationRequest field %d = %s %v, want %s %v", i, got.Name, got.Type, want.name, want.typeOf)
		}
		if got.Tag != "" {
			t.Errorf("ContinuationRequest field %s has tag %q, want no tag", got.Name, got.Tag)
		}
	}
	executeRequestType := reflect.TypeOf(ExecuteRequest{})
	wantExecuteRequestFields := []string{
		"Prompt",
		"SystemPrompt",
		"Provider",
		"Model",
		"WorkDir",
		"Permissions",
		"Temperature",
		"Seed",
		"Reasoning",
		"Timeout",
		"IdleTimeout",
		"SessionLogDir",
		"SessionID",
		"LifecycleStateDir",
		"CleanupTimeout",
		"Metadata",
	}
	if executeRequestType.NumField() != len(wantExecuteRequestFields) {
		t.Fatalf("ExecuteRequest has %d fields, want reviewed continuation-safe set of %d", executeRequestType.NumField(), len(wantExecuteRequestFields))
	}
	for i, want := range wantExecuteRequestFields {
		if got := executeRequestType.Field(i).Name; got != want {
			t.Errorf("ExecuteRequest field %d = %q, want continuation-safe field %q", i, got, want)
		}
	}

	preparedType := reflect.TypeOf((*PreparedContinuation)(nil)).Elem()
	if preparedType.NumMethod() != 1 {
		t.Fatalf("PreparedContinuation has %d methods, want exactly 1", preparedType.NumMethod())
	}
	start, ok := preparedType.MethodByName("Start")
	if !ok {
		t.Fatal("PreparedContinuation.Start is missing")
	}
	assertContinuationMethod(t, start, []reflect.Type{
		reflect.TypeOf((*context.Context)(nil)).Elem(),
	}, []reflect.Type{
		reflect.TypeOf((<-chan Event)(nil)),
		reflect.TypeOf((*error)(nil)).Elem(),
	})

	harnessType := reflect.TypeOf((*ContinuationHarness)(nil)).Elem()
	baseHarnessType := reflect.TypeOf((*Harness)(nil)).Elem()
	if harnessType.NumMethod() != baseHarnessType.NumMethod()+1 {
		t.Fatalf("ContinuationHarness has %d methods, want Harness's %d plus PrepareContinuation", harnessType.NumMethod(), baseHarnessType.NumMethod())
	}
	for i := 0; i < baseHarnessType.NumMethod(); i++ {
		baseMethod := baseHarnessType.Method(i)
		continuationMethod, exists := harnessType.MethodByName(baseMethod.Name)
		if !exists || continuationMethod.Type != baseMethod.Type {
			t.Errorf("ContinuationHarness does not preserve Harness.%s exactly", baseMethod.Name)
		}
	}
	prepare, ok := harnessType.MethodByName("PrepareContinuation")
	if !ok {
		t.Fatal("ContinuationHarness.PrepareContinuation is missing")
	}
	assertContinuationMethod(t, prepare, []reflect.Type{
		reflect.TypeOf((*context.Context)(nil)).Elem(),
		reflect.TypeOf(ContinuationRequest{}),
	}, []reflect.Type{
		preparedType,
		reflect.TypeOf((*error)(nil)).Elem(),
	})

	if got, want := ErrContinuationRequestInvalid.Error(), "invalid continuation request"; got != want {
		t.Errorf("ErrContinuationRequestInvalid = %q, want %q", got, want)
	}
	if got, want := ErrContinuationEvidenceUnavailable.Error(), "continuation evidence unavailable"; got != want {
		t.Errorf("ErrContinuationEvidenceUnavailable = %q, want %q", got, want)
	}
	if !errors.Is(ErrContinuationRequestInvalid, ErrContinuationRequestInvalid) ||
		!errors.Is(ErrContinuationEvidenceUnavailable, ErrContinuationEvidenceUnavailable) {
		t.Fatal("continuation sentinels do not preserve errors.Is identity")
	}

	runner := &testContinuationRunner{store: newTestContinuationStore(t.TempDir(), "test-owner", time.Now())}
	prepared, err := runner.PrepareContinuation(context.Background(), validTestContinuationRequest(""))
	if prepared != nil || !errors.Is(err, ErrContinuationRequestInvalid) {
		t.Fatalf("empty parent preparation = (%T, %T), want nil/ErrContinuationRequestInvalid", prepared, err)
	}
}

func TestContinuationHarnessReceivesOnlyFizeauSessionRef(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store := newTestContinuationStore(t.TempDir(), "owner-a", now)
	const parentID = "fizeau-parent-123"
	if err := store.write(parentID, "private-native-reference", now); err != nil {
		t.Fatalf("write private continuation evidence: %v", err)
	}
	runner := &testContinuationRunner{store: store}
	childRequest := ExecuteRequest{
		Prompt:            "continue the work",
		SystemPrompt:      "preserve the plan",
		Provider:          "anthropic",
		Model:             "claude-model-a",
		WorkDir:           "/workspace",
		Permissions:       "safe",
		Temperature:       0.42,
		Seed:              17,
		Reasoning:         "medium",
		Timeout:           19 * time.Second,
		IdleTimeout:       23 * time.Second,
		SessionLogDir:     "/logs/child",
		SessionID:         "prospective-child-fizeau-id",
		LifecycleStateDir: "/state/child",
		CleanupTimeout:    29 * time.Second,
		Metadata:          map[string]string{"caller-authored": "private-native-reference"},
	}

	prepared, err := runner.PrepareContinuation(context.Background(), ContinuationRequest{
		ParentSessionID: parentID,
		Request:         childRequest,
	})
	if err != nil {
		t.Fatalf("PrepareContinuation: %v", err)
	}
	if prepared == nil {
		t.Fatal("PrepareContinuation returned nil prepared continuation")
	}
	received := runner.receivedRequests()
	if len(received) != 1 {
		t.Fatalf("runner received %d continuation requests, want 1", len(received))
	}
	want := ContinuationRequest{ParentSessionID: parentID, Request: childRequest}
	if !reflect.DeepEqual(received[0], want) {
		t.Fatalf("runner request = %#v, want %#v", received[0], want)
	}
}

func TestContinuationEvidenceUnavailableBeforeSpawn(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	const (
		parentID = "fizeau-parent-unavailable"
		evidence = "native-evidence-must-stay-private"
	)
	cases := []struct {
		name  string
		setup func(*testing.T, *testContinuationStore)
	}{
		{name: "missing"},
		{
			name: "unreadable",
			setup: func(t *testing.T, store *testContinuationStore) {
				t.Helper()
				path := store.evidencePath(parentID)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatalf("create evidence directory parent: %v", err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("create unreadable evidence fixture: %v", err)
				}
			},
		},
		{
			name: "stale",
			setup: func(t *testing.T, store *testContinuationStore) {
				t.Helper()
				if err := store.write(parentID, evidence, now.Add(-2*store.maxAge)); err != nil {
					t.Fatalf("write stale evidence fixture: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestContinuationStore(t.TempDir(), "owner-a", now)
			if tc.setup != nil {
				tc.setup(t, store)
			}
			runner := &testContinuationRunner{store: store}
			prepared, err := runner.PrepareContinuation(context.Background(), validTestContinuationRequest(parentID))
			assertContinuationUnavailable(t, prepared, err, evidence, store.root, store.namespace)
			state := runner.state()
			if state.executeCalls != 0 || state.startCalls != 0 || state.streams != 0 || state.events != 0 {
				t.Fatalf("unavailable evidence caused execution effects: %+v", state)
			}
		})
	}
}

func TestContinuationPrivateEvidenceSurvivesRunnerReconstruction(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	const (
		namespace = "owner-a"
		parentID  = "fizeau-parent-reconstructed"
		evidence  = "opaque-native-session-987654"
	)

	original := &testContinuationRunner{store: newTestContinuationStore(root, namespace, now)}
	if err := original.recordPrivateEvidence(parentID, evidence); err != nil {
		t.Fatalf("record private continuation evidence: %v", err)
	}
	reconstructed := &testContinuationRunner{store: newTestContinuationStore(root, namespace, now)}
	prepared, err := reconstructed.PrepareContinuation(context.Background(), validTestContinuationRequest(parentID))
	if err != nil {
		t.Fatalf("reconstructed runner PrepareContinuation: %v", err)
	}
	privatePrepared, ok := prepared.(*testPreparedContinuation)
	if !ok {
		t.Fatalf("prepared continuation type = %T, want route-private test handle", prepared)
	}
	if privatePrepared.evidence != evidence {
		t.Fatal("reconstructed runner did not reopen the original private evidence")
	}
	events, startErr := prepared.Start(context.Background())
	if startErr != nil || events == nil {
		t.Fatalf("prepared Start = (%T, %T), want event stream/nil", events, startErr)
	}
	var finals int
	for event := range events {
		if event.Type == EventTypeFinal {
			finals++
			var final FinalData
			if err := json.Unmarshal(event.Data, &final); err != nil {
				t.Fatalf("decode prepared final: %v", err)
			}
			if final.Status != "success" || final.Outcome != SessionOutcomeSuccess ||
				final.Cause != TerminalCauseCompleted || final.Stage != SessionStageHarness {
				t.Fatalf("prepared final = %#v, want success/completed/harness", final)
			}
		}
		if strings.Contains(string(event.Data), evidence) {
			t.Fatal("prepared continuation event leaked private evidence")
		}
	}
	if finals != 1 {
		t.Fatalf("prepared Start emitted %d final events, want 1", finals)
	}
	secondEvents, secondErr := prepared.Start(context.Background())
	if secondEvents != nil || secondErr == nil {
		t.Fatalf("second prepared Start = (%T, %T), want nil/error", secondEvents, secondErr)
	}
	if strings.Contains(secondErr.Error(), evidence) {
		t.Fatal("second-Start error leaked private evidence")
	}
	state := reconstructed.state()
	if state.executeCalls != 0 || state.startCalls != 1 || state.streams != 1 || state.events != 1 {
		t.Fatalf("single-use prepared state = %+v, want exactly one start/stream/event", state)
	}

	for _, tc := range []struct {
		name  string
		store *testContinuationStore
	}{
		{name: "different namespace", store: newTestContinuationStore(root, "owner-b", now)},
		{name: "different store root", store: newTestContinuationStore(t.TempDir(), namespace, now)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := &testContinuationRunner{store: tc.store}
			otherPrepared, otherErr := other.PrepareContinuation(context.Background(), validTestContinuationRequest(parentID))
			assertContinuationUnavailable(t, otherPrepared, otherErr, evidence, namespace, root)
		})
	}
}

func assertContinuationUnavailable(t *testing.T, prepared PreparedContinuation, err error, forbidden ...string) {
	t.Helper()
	if prepared != nil {
		t.Fatalf("unavailable evidence returned non-nil prepared type %T", prepared)
	}
	if !errors.Is(err, ErrContinuationEvidenceUnavailable) {
		t.Fatalf("unavailable evidence returned error type %T, want ErrContinuationEvidenceUnavailable", err)
	}
	if err.Error() != ErrContinuationEvidenceUnavailable.Error() {
		t.Fatal("unavailable-evidence error must be the bare redacted sentinel")
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatal("unavailable-evidence error leaked private store data")
		}
	}
}

func assertContinuationMethod(t *testing.T, method reflect.Method, wantIn, wantOut []reflect.Type) {
	t.Helper()
	if method.Type.NumIn() != len(wantIn) || method.Type.NumOut() != len(wantOut) {
		t.Fatalf("%s signature has %d inputs/%d outputs, want %d/%d", method.Name, method.Type.NumIn(), method.Type.NumOut(), len(wantIn), len(wantOut))
	}
	for i, want := range wantIn {
		if got := method.Type.In(i); got != want {
			t.Errorf("%s input %d = %v, want %v", method.Name, i, got, want)
		}
	}
	for i, want := range wantOut {
		if got := method.Type.Out(i); got != want {
			t.Errorf("%s output %d = %v, want %v", method.Name, i, got, want)
		}
	}
}

type testContinuationStore struct {
	root      string
	namespace string
	now       time.Time
	maxAge    time.Duration
}

func newTestContinuationStore(root, namespace string, now time.Time) *testContinuationStore {
	return &testContinuationStore{root: root, namespace: namespace, now: now, maxAge: time.Hour}
}

func (s *testContinuationStore) evidencePath(parentID string) string {
	return filepath.Join(s.root, continuationTestKey(s.namespace), continuationTestKey(parentID)+".opaque")
}

func (s *testContinuationStore) write(parentID, evidence string, capturedAt time.Time) error {
	path := s.evidencePath(parentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(evidence), 0o600); err != nil {
		return err
	}
	return os.Chtimes(path, capturedAt, capturedAt)
}

func (s *testContinuationStore) read(parentID string) (string, error) {
	path := s.evidencePath(parentID)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || s.now.Sub(info.ModTime()) > s.maxAge {
		return "", ErrContinuationEvidenceUnavailable
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", ErrContinuationEvidenceUnavailable
	}
	return string(data), nil
}

func continuationTestKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type testContinuationRunner struct {
	store *testContinuationStore
	mu    sync.Mutex

	received     []ContinuationRequest
	executeCalls int
	startCalls   int
	streams      int
	events       int
}

func (r *testContinuationRunner) Info() HarnessInfo {
	return HarnessInfo{Name: "continuation-test", Type: "subprocess", Available: true}
}

func (r *testContinuationRunner) HealthCheck(ctx context.Context) error {
	return ctx.Err()
}

func (r *testContinuationRunner) Execute(context.Context, ExecuteRequest) (<-chan Event, error) {
	r.mu.Lock()
	r.executeCalls++
	r.mu.Unlock()
	return nil, errors.New("continuation test runner does not execute")
}

func (r *testContinuationRunner) PrepareContinuation(ctx context.Context, req ContinuationRequest) (PreparedContinuation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.received = append(r.received, cloneTestContinuationRequest(req))
	r.mu.Unlock()
	if req.ParentSessionID == "" {
		return nil, ErrContinuationRequestInvalid
	}
	evidence, err := r.store.read(req.ParentSessionID)
	if err != nil {
		return nil, ErrContinuationEvidenceUnavailable
	}
	return &testPreparedContinuation{runner: r, evidence: evidence}, nil
}

func (r *testContinuationRunner) recordPrivateEvidence(parentID, evidence string) error {
	return r.store.write(parentID, evidence, r.store.now)
}

type testPreparedContinuation struct {
	runner   *testContinuationRunner
	evidence string
	mu       sync.Mutex
	started  bool
}

func (p *testPreparedContinuation) Start(ctx context.Context) (<-chan Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil, errors.New("prepared continuation already started")
	}
	p.started = true
	p.mu.Unlock()
	data, err := json.Marshal(FinalData{
		Status:          "success",
		Outcome:         SessionOutcomeSuccess,
		Cause:           TerminalCauseCompleted,
		Stage:           SessionStageHarness,
		FinalCostSource: CostSourceUnknown,
	})
	if err != nil {
		return nil, err
	}
	p.runner.mu.Lock()
	p.runner.startCalls++
	p.runner.streams++
	p.runner.events++
	p.runner.mu.Unlock()
	events := make(chan Event, 1)
	events <- Event{Type: EventTypeFinal, Sequence: 1, Time: p.runner.store.now, Data: data}
	close(events)
	return events, nil
}

type testContinuationRunnerState struct {
	executeCalls int
	startCalls   int
	streams      int
	events       int
}

func (r *testContinuationRunner) state() testContinuationRunnerState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return testContinuationRunnerState{
		executeCalls: r.executeCalls,
		startCalls:   r.startCalls,
		streams:      r.streams,
		events:       r.events,
	}
}

func (r *testContinuationRunner) receivedRequests() []ContinuationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	requests := make([]ContinuationRequest, len(r.received))
	for i, req := range r.received {
		requests[i] = cloneTestContinuationRequest(req)
	}
	return requests
}

func cloneTestContinuationRequest(req ContinuationRequest) ContinuationRequest {
	cloned := req
	if req.Request.Metadata != nil {
		cloned.Request.Metadata = make(map[string]string, len(req.Request.Metadata))
		for key, value := range req.Request.Metadata {
			cloned.Request.Metadata[key] = value
		}
	}
	return cloned
}

func validTestContinuationRequest(parentID string) ContinuationRequest {
	return ContinuationRequest{
		ParentSessionID: parentID,
		Request: ExecuteRequest{
			Prompt:      "continue",
			Model:       "model-a",
			Permissions: "safe",
		},
	}
}

var _ Harness = (*testContinuationRunner)(nil)
var _ ContinuationHarness = (*testContinuationRunner)(nil)
var _ PreparedContinuation = (*testPreparedContinuation)(nil)
