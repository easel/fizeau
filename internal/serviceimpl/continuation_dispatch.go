package serviceimpl

import (
	"context"
	"errors"
	"fmt"

	"github.com/easel/fizeau/internal/harnesses"
)

// ContinuationChild is the service-owned child-session seam used between
// preparation and Start. Implementations create the child session and its
// pending locator before AcquireLease is called. Neither method receives, nor
// returns, harness-native continuation evidence.
type ContinuationChild interface {
	Create(context.Context) error
	AcquireLease(context.Context) error
	TerminalizeStartFailure(context.Context, error)
}

// ContinuationDispatchRequest contains the already-validated parent identity,
// exact runner binding, and normalized child request for a resume attempt.
// It deliberately has no policy or native-evidence fields: policy selection is
// owned by the public service boundary, and evidence remains route-private.
type ContinuationDispatchRequest struct {
	ParentSessionID string
	Route           harnesses.RouteRunnerBinding
	Request         harnesses.ExecuteRequest
	Child           ContinuationChild
}

// PrepareRegisteredContinuation invokes the optional capability on precisely
// the route object that executed the parent. It is intentionally the only
// operation before child creation: preparation errors leave no child, lease,
// containment boundary, event stream, or process to clean up.
func PrepareRegisteredContinuation(ctx context.Context, req ContinuationDispatchRequest) (harnesses.PreparedContinuation, error) {
	if req.ParentSessionID == "" || req.Request.SessionID == "" || !req.Route.Valid() {
		return nil, harnesses.ErrContinuationRequestInvalid
	}
	runner := req.Route.Runner()
	continuation, ok := runner.(harnesses.ContinuationHarness)
	if !ok {
		return nil, harnesses.ErrContinuationEvidenceUnavailable
	}
	prepared, err := continuation.PrepareContinuation(ctx, harnesses.ContinuationRequest{
		ParentSessionID: req.ParentSessionID,
		Request:         req.Request,
	})
	if err != nil {
		return nil, err
	}
	if prepared == nil {
		return nil, fmt.Errorf("prepare continuation: %w", harnesses.ErrContinuationEvidenceUnavailable)
	}
	return prepared, nil
}

// StartPreparedContinuation creates and leases one child before making the
// single Start call. A Start-time failure is terminal for that child: it is
// deliberately not returned as preparation failure, so a caller cannot treat
// it as permission to take a fresh fallback path.
func StartPreparedContinuation(ctx context.Context, child ContinuationChild, prepared harnesses.PreparedContinuation) (<-chan harnesses.Event, error) {
	if child == nil || prepared == nil {
		return nil, harnesses.ErrContinuationRequestInvalid
	}
	if err := child.Create(ctx); err != nil {
		return nil, err
	}
	if err := child.AcquireLease(ctx); err != nil {
		child.TerminalizeStartFailure(ctx, err)
		return nil, err
	}
	events, err := prepared.Start(ctx)
	if err != nil {
		// Start is the first point at which a route may touch its native
		// evidence. Do not project an adapter error verbatim: it can contain a
		// native token. The child still gets a durable failure, but only a
		// service-owned diagnostic crosses this seam.
		failure := errors.New("continuation start failed")
		child.TerminalizeStartFailure(ctx, failure)
		return nil, failure
	}
	if events == nil {
		err := errors.New("prepared continuation returned nil event stream")
		child.TerminalizeStartFailure(ctx, err)
		return nil, err
	}
	return events, nil
}
