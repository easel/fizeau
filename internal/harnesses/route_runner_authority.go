package harnesses

import (
	"errors"
	"reflect"
	"sync"
)

// RouteRunnerFactory constructs one runner for one exact route identity from
// the authority-owned structural prototype for that harness. The prototype
// carries activated/configured launch state; a production factory clones it
// when endpoint-distinct instances are required.
type RouteRunnerFactory func(RouteRunnerKey, Harness) (Harness, error)

var (
	// ErrRouteRunnerAlreadyRegistered reports an attempted replacement. Exact
	// registrations are immutable for the lifetime of the authority.
	ErrRouteRunnerAlreadyRegistered = errors.New("route runner already registered")
	// ErrRouteRunnerUnavailable reports a factory result without a runner.
	ErrRouteRunnerUnavailable = errors.New("route runner unavailable")
	// ErrRouteRunnerIdentityConflict reports one runner object being assigned
	// to more than one exact route key.
	ErrRouteRunnerIdentityConflict = errors.New("route runner identity already bound to a different route")
)

type routeRunnerIdentity struct {
	typeOf reflect.Type
	ptr    uintptr
}

// RouteRunnerAuthority is the single service-owned authority for subprocess
// runner identity. It owns both the structural inventory representatives and
// exact per-route execution instances; the structural view is never used as a
// route lookup fallback.
type RouteRunnerAuthority struct {
	mu         sync.RWMutex
	structural map[string]Harness
	runners    map[RouteRunnerKey]Harness
	owners     map[routeRunnerIdentity]RouteRunnerKey
	factory    RouteRunnerFactory
}

// NewRouteRunnerAuthority creates an authority with a defensive copy of the
// structural inventory. The factory is invoked at most once for each
// successfully bound exact key.
func NewRouteRunnerAuthority(structural map[string]Harness, factory RouteRunnerFactory) *RouteRunnerAuthority {
	view := make(map[string]Harness, len(structural))
	for name, runner := range structural {
		view[name] = runner
	}
	return &RouteRunnerAuthority{
		structural: view,
		runners:    make(map[RouteRunnerKey]Harness),
		owners:     make(map[routeRunnerIdentity]RouteRunnerKey),
		factory:    factory,
	}
}

// Bind returns the immutable binding for key, constructing it atomically on
// first use. Factory failures and nil results are not cached, so a corrected
// environment can retry the same exact route.
func (a *RouteRunnerAuthority) Bind(key RouteRunnerKey) (RouteRunnerBinding, error) {
	if a == nil {
		return RouteRunnerBinding{}, ErrRouteRunnerUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if runner := a.runners[key]; runner != nil {
		return routeRunnerBinding(key, runner), nil
	}
	if a.factory == nil {
		return RouteRunnerBinding{}, ErrRouteRunnerUnavailable
	}
	runner, err := a.factory(key, a.structural[key.Harness])
	if err != nil {
		return RouteRunnerBinding{}, err
	}
	if runner == nil {
		return RouteRunnerBinding{}, ErrRouteRunnerUnavailable
	}
	identity, err := routeRunnerObjectIdentity(runner)
	if err != nil {
		return RouteRunnerBinding{}, err
	}
	if owner, exists := a.owners[identity]; exists && owner != key {
		return RouteRunnerBinding{}, ErrRouteRunnerIdentityConflict
	}
	a.runners[key] = runner
	a.owners[identity] = key
	return routeRunnerBinding(key, runner), nil
}

// Register installs runner for an exact key when no binding exists. It never
// replaces a prior registration.
func (a *RouteRunnerAuthority) Register(key RouteRunnerKey, runner Harness) (RouteRunnerBinding, error) {
	if a == nil || runner == nil {
		return RouteRunnerBinding{}, ErrRouteRunnerUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.runners[key]; exists {
		return RouteRunnerBinding{}, ErrRouteRunnerAlreadyRegistered
	}
	identity, err := routeRunnerObjectIdentity(runner)
	if err != nil {
		return RouteRunnerBinding{}, err
	}
	if owner, exists := a.owners[identity]; exists && owner != key {
		return RouteRunnerBinding{}, ErrRouteRunnerIdentityConflict
	}
	a.runners[key] = runner
	a.owners[identity] = key
	return routeRunnerBinding(key, runner), nil
}

// Lookup performs exact-key lookup only. There is deliberately no name-only,
// display-name, wildcard, or partial-key lookup path.
func (a *RouteRunnerAuthority) Lookup(key RouteRunnerKey) (RouteRunnerBinding, bool) {
	if a == nil {
		return RouteRunnerBinding{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	runner, ok := a.runners[key]
	if !ok || runner == nil {
		return RouteRunnerBinding{}, false
	}
	return routeRunnerBinding(key, runner), true
}

// StructuralInstance returns the inventory representative for a canonical
// harness name. It does not consult exact route registrations.
func (a *RouteRunnerAuthority) StructuralInstance(name string) Harness {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.structural[name]
}

// StructuralInstances returns a defensive snapshot for inventory joins.
func (a *RouteRunnerAuthority) StructuralInstances() map[string]Harness {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	view := make(map[string]Harness, len(a.structural))
	for name, runner := range a.structural {
		view[name] = runner
	}
	return view
}

func routeRunnerBinding(key RouteRunnerKey, runner Harness) RouteRunnerBinding {
	return RouteRunnerBinding{key: key, runner: runner}
}

func routeRunnerObjectIdentity(runner Harness) (routeRunnerIdentity, error) {
	value := reflect.ValueOf(runner)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return routeRunnerIdentity{}, ErrRouteRunnerUnavailable
	}
	return routeRunnerIdentity{typeOf: value.Type(), ptr: value.Pointer()}, nil
}
