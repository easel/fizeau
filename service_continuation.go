package fizeau

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/processlifecycle"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// ContinuationPolicy controls whether Continue must resume harness-owned
// conversation state or may start a fresh child session.
type ContinuationPolicy string

const (
	ContinuationRequireResume ContinuationPolicy = "require_resume"
	ContinuationPreferResume  ContinuationPolicy = "prefer_resume"
	ContinuationFreshSession  ContinuationPolicy = "fresh_session"
)

// ContinuationDisposition records what an accepted continuation actually did.
type ContinuationDisposition string

const (
	ContinuationResumed ContinuationDisposition = "resumed"
	ContinuationFresh   ContinuationDisposition = "fresh"
)

// ServiceContinuationRequest asks Fizeau to continue a completed Fizeau
// session. SessionID is always a Fizeau session ID; provider- and
// harness-native conversation identifiers remain behind the service boundary.
type ServiceContinuationRequest struct {
	SessionID     string
	Prompt        string
	Policy        ContinuationPolicy
	FreshRequest  ServiceExecuteRequest
	Metadata      map[string]string
	CorrelationID string
}

var (
	ErrContinuationPolicyInvalid      = errors.New("invalid continuation policy")
	ErrContinuationSessionUnavailable = errors.New("continuation session unavailable")
	ErrContinuationUnsupported        = errors.New("continuation unsupported")
)

// Continue resumes a completed Fizeau session when its exact route supports
// continuation, or starts a fresh child only when the requested policy permits
// it. Harness-native evidence never crosses this facade.
func (s *service) Continue(ctx context.Context, req ServiceContinuationRequest) (<-chan ServiceEvent, error) {
	if !validContinuationPolicy(req.Policy) {
		return nil, ErrContinuationPolicyInvalid
	}
	if req.SessionID == "" {
		return nil, ErrContinuationSessionUnavailable
	}
	if req.Prompt == "" {
		return nil, errors.New("continuation prompt is required")
	}
	switch req.Policy {
	case ContinuationRequireResume:
		if !reflect.ValueOf(req.FreshRequest).IsZero() {
			return nil, errors.New("continuation FreshRequest must be zero for require_resume")
		}
	case ContinuationPreferResume, ContinuationFreshSession:
		if err := validateServiceExecuteRequest(effectiveContinuationFreshRequest(req)); err != nil {
			return nil, err
		}
	}
	if s == nil {
		return nil, ErrContinuationSessionUnavailable
	}

	// A continuation is always a child of a completed Fizeau session, even
	// when policy selects a fresh execution. This validates the parent log and
	// obtains the exact registered route without scanning, constructing, or
	// probing any runner.
	parent, err := serviceimpl.ResolveCompletedParentRoute(s.continuationLocators, s.routeRunners, req.SessionID)
	if err != nil {
		return nil, ErrContinuationSessionUnavailable
	}

	childID := generateSessionID()
	childRequest := effectiveContinuationFreshRequest(req)
	if childRequest.SessionLogDir == "" {
		childRequest.SessionLogDir = s.serviceSessionLogDir()
	}
	decision := continuationRouteDecision(parent.Route.Key())
	coordinatorRequest := s.executeCoordinatorRequest(childRequest, decision, childID, nil)
	// The parent resolver issued this binding through Lookup. Do not Bind here:
	// a continuation must use precisely the route instance that owns the
	// parent, never a newly constructed or fuzzy-matched substitute.
	coordinatorRequest.RouteRunner = parent.Route
	coordinatorRequest.RouteRunnerError = nil
	lineage := continuationLineage{
		ParentSessionID: parent.ParentSessionID,
		Policy:          req.Policy,
	}
	ports := s.continuationCoordinatorPorts(childRequest, decision, childID, lineage)
	coordinator := serviceimpl.ExecuteCoordinator{Hub: s.hub, Registry: s.registry}

	switch req.Policy {
	case ContinuationFreshSession:
		// Fresh policy deliberately bypasses the optional continuation
		// capability. Valid lineage is enough to accept this child.
		lineage.Disposition = ContinuationFresh
		ports = s.continuationCoordinatorPorts(childRequest, decision, childID, lineage)
		s.hub.OpenSession(childID)
		return coordinator.RunResolved(ctx, coordinatorRequest, ports), nil
	case ContinuationRequireResume, ContinuationPreferResume:
		prepared, prepareErr := serviceimpl.PrepareRegisteredContinuation(ctx, serviceimpl.ContinuationDispatchRequest{
			ParentSessionID: parent.ParentSessionID,
			Route:           parent.Route,
			Request:         continuationHarnessRequest(coordinatorRequest),
		})
		if prepareErr == nil {
			lineage.Disposition = ContinuationResumed
			ports = s.continuationCoordinatorPorts(childRequest, decision, childID, lineage)
			child := newContinuationChild(s, childID, coordinatorRequest.LifecycleBaseDir)
			return coordinator.RunPreparedContinuation(ctx, coordinatorRequest, child, prepared, ports), nil
		}
		if req.Policy == ContinuationRequireResume {
			// Preparation precedes child registration, lease acquisition, and
			// Start, so this typed failure has no child or process effects.
			return nil, ErrContinuationUnsupported
		}
		// prefer_resume may fall back only after the completed-parent check
		// above and only because preparation was unavailable. A Start failure
		// happens after child acceptance and is terminal, not a fallback cue.
		lineage.Disposition = ContinuationFresh
		ports = s.continuationCoordinatorPorts(childRequest, decision, childID, lineage)
		s.hub.OpenSession(childID)
		return coordinator.RunResolved(ctx, coordinatorRequest, ports), nil
	default:
		panic("validated continuation policy missing switch case")
	}
}

func continuationHarnessRequest(req serviceimpl.ExecuteRequest) harnesses.ExecuteRequest {
	lifecycleStateDir, err := processlifecycle.StateDirectory(req.LifecycleBaseDir)
	if err != nil {
		lifecycleStateDir = ""
	}
	return harnesses.ExecuteRequest{
		Prompt: req.Prompt, SystemPrompt: req.SystemPrompt,
		Provider: req.Decision.Provider, Model: req.Decision.Model,
		WorkDir: req.WorkDir, Permissions: req.Permissions,
		Temperature: continuationTemperature(req.Temperature), Seed: continuationSeed(req.Seed),
		Reasoning: string(req.Reasoning), Timeout: req.Timeout, IdleTimeout: req.IdleTimeout,
		SessionLogDir: req.SessionLogDir, SessionID: req.SessionID,
		LifecycleStateDir: lifecycleStateDir, CleanupTimeout: req.CleanupTimeout,
		Metadata: req.Metadata,
	}
}

func continuationTemperature(value *float32) float32 {
	if value == nil {
		return 0
	}
	return *value
}

func continuationSeed(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// continuationChild is the root-owned admission lifecycle for a prepared
// continuation. It deliberately does not own the harness process boundary:
// that lease is acquired by the route when PreparedContinuation.Start runs.
// Its job is to establish the distinct Fizeau child identity and a fresh
// service lifecycle root before native Start may run. The coordinator remains
// the sole owner of durable terminals, locator promotion, cleanup
// supersession, and hub closure.
type continuationChild struct {
	service          *service
	sessionID        string
	lifecycleBaseDir string

	mu           sync.Mutex
	created      bool
	lease        *continuationChildLease
	startFailure error
}

// continuationChildLease is intentionally private and contains no
// harness-native identity. A new instance is minted for every resumed child;
// the service lifecycle root is made available before Start so the route can
// acquire its own process-boundary lease under that child identity.
type continuationChildLease struct {
	sessionID string
	stateDir  string
}

func newContinuationChild(s *service, sessionID, lifecycleBaseDir string) *continuationChild {
	return &continuationChild{service: s, sessionID: sessionID, lifecycleBaseDir: lifecycleBaseDir}
}

func (c *continuationChild) Create(context.Context) error {
	if c == nil || c.service == nil || c.service.hub == nil || c.sessionID == "" {
		return errors.New("continuation child session is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.created {
		return fmt.Errorf("continuation child %q already created", c.sessionID)
	}
	// The coordinator also opens this ID before it begins its stream so a
	// TailSessionLog subscriber can attach immediately. Re-opening here makes
	// the child seam the production admission point and is idempotent at the
	// hub boundary.
	c.service.hub.OpenSession(c.sessionID)
	c.created = true
	return nil
}

func (c *continuationChild) AcquireLease(context.Context) error {
	if c == nil {
		return errors.New("continuation child is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.created {
		return errors.New("continuation child lease before child creation")
	}
	if c.lease != nil {
		return fmt.Errorf("continuation child %q already has a lease", c.sessionID)
	}
	stateDir, err := processlifecycle.StateDirectory(c.lifecycleBaseDir)
	if err != nil {
		return fmt.Errorf("prepare continuation lifecycle state: %w", err)
	}
	c.lease = &continuationChildLease{sessionID: c.sessionID, stateDir: stateDir}
	return nil
}

func (c *continuationChild) TerminalizeStartFailure(_ context.Context, err error) {
	if c == nil || err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Do not publish a competing terminal here. The coordinator immediately
	// commits the one durable, cleanup-aware terminal after this handoff.
	c.startFailure = err
}

func continuationRouteDecision(key harnesses.RouteRunnerKey) RouteDecision {
	return RouteDecision{
		Harness: key.Harness, Provider: key.Provider, Endpoint: key.Endpoint,
		ServerInstance: key.ServerInstance, Model: key.Model,
		Reason: "continuation_parent_route",
	}
}

func effectiveContinuationFreshRequest(req ServiceContinuationRequest) ServiceExecuteRequest {
	effective := req.FreshRequest
	effective.Prompt = req.Prompt
	effective.Metadata = req.Metadata
	effective.CorrelationID = req.CorrelationID
	return effective
}

func validContinuationPolicy(policy ContinuationPolicy) bool {
	switch policy {
	case ContinuationRequireResume, ContinuationPreferResume, ContinuationFreshSession:
		return true
	default:
		return false
	}
}
