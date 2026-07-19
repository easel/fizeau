package serviceimpl

import (
	"errors"
	"fmt"

	"github.com/easel/fizeau/internal/harnesses"
)

// ErrCompletedParentUnavailable reports that a Fizeau parent cannot be used
// for continuation. It intentionally coalesces locator and route-registration
// failures: neither exposes native continuation evidence or an inventory view
// to callers at this service seam.
var ErrCompletedParentUnavailable = errors.New("completed continuation parent unavailable")

// CompletedParentRoute is the service-private resolution of one completed
// Fizeau parent to the exact route runner that owns it. It contains no
// harness-native continuation evidence; route-specific evidence remains
// private until a later preparation step.
type CompletedParentRoute struct {
	ParentSessionID string
	Route           harnesses.RouteRunnerBinding
}

// ResolveCompletedParentRoute resolves one completed parent through its
// durable owner-only locator and the authority's exact registered-route view.
// It never scans logs, constructs a runner, invokes a runner capability, or
// creates a child. In particular, Lookup (rather than Bind) prevents a stale
// locator from causing factory construction for a route that was not actually
// registered by the active service instance.
func ResolveCompletedParentRoute(store *ContinuationLocatorStore, authority *harnesses.RouteRunnerAuthority, parentSessionID string) (CompletedParentRoute, error) {
	if store == nil || authority == nil || parentSessionID == "" {
		return CompletedParentRoute{}, ErrCompletedParentUnavailable
	}
	locator, err := store.ResolveCompleted(parentSessionID)
	if err != nil {
		return CompletedParentRoute{}, fmt.Errorf("resolve completed continuation parent: %w", ErrCompletedParentUnavailable)
	}
	binding, ok := authority.Lookup(locator.Route)
	if !ok || !binding.Valid() {
		return CompletedParentRoute{}, ErrCompletedParentUnavailable
	}
	return CompletedParentRoute{ParentSessionID: parentSessionID, Route: binding}, nil
}
