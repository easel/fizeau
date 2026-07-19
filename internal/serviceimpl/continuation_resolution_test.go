package serviceimpl

import (
	"errors"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestResolveCompletedParentRouteUsesExactRegisteredRouteOnly(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()
	path := writeLocatorLog(t, root, "parent", route, 1)
	if err := store.WritePending("parent", path, route); err != nil {
		t.Fatal(err)
	}

	registered := &continuationTestRunner{}
	wrongEndpoint := &continuationTestRunner{}
	authority := harnesses.NewRouteRunnerAuthority(nil, func(harnesses.RouteRunnerKey, harnesses.Harness) (harnesses.Harness, error) {
		t.Fatal("resolution must not construct a route runner")
		return nil, nil
	})
	if _, err := authority.Register(route, registered); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Register(harnesses.RouteRunnerKey{
		Harness: route.Harness, Provider: route.Provider, Endpoint: "other", ServerInstance: route.ServerInstance, Model: route.Model,
	}, wrongEndpoint); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveCompletedParentRoute(store, authority, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentSessionID != "parent" || got.Route.Key() != route || got.Route.Runner() != registered {
		t.Fatalf("resolved parent = %#v, route = %#v, want exact registered route", got, got.Route.Key())
	}
	if registered.prepareCalls != 0 || registered.executeCalls != 0 || wrongEndpoint.prepareCalls != 0 || wrongEndpoint.executeCalls != 0 {
		t.Fatal("resolution invoked a route runner")
	}
}

func TestResolveCompletedParentRouteReturnsUnavailableWithoutEffects(t *testing.T) {
	root := t.TempDir()
	store, err := NewContinuationLocatorStore(root)
	if err != nil {
		t.Fatal(err)
	}
	route := locatorRoute()
	registered := &continuationTestRunner{}
	authority := harnesses.NewRouteRunnerAuthority(nil, func(harnesses.RouteRunnerKey, harnesses.Harness) (harnesses.Harness, error) {
		t.Fatal("resolution must not construct a route runner")
		return nil, nil
	})
	if _, err := authority.Register(route, registered); err != nil {
		t.Fatal(err)
	}

	invalidPath := writeLocatorEvents(t, root, "incomplete", nil)
	if err := store.WritePending("incomplete", invalidPath, route); err != nil {
		t.Fatal(err)
	}
	validPath := writeLocatorLog(t, root, "unregistered", route, 1)
	if err := store.WritePending("unregistered", validPath, route); err != nil {
		t.Fatal(err)
	}
	mismatchedPath := writeLocatorLog(t, root, "mismatched", route, 1)
	mismatchedRoute := route
	mismatchedRoute.Endpoint = "other"
	if err := store.WritePending("mismatched", mismatchedPath, mismatchedRoute); err != nil {
		t.Fatal(err)
	}
	otherAuthority := harnesses.NewRouteRunnerAuthority(nil, nil)

	for _, tc := range []struct {
		name      string
		store     *ContinuationLocatorStore
		authority *harnesses.RouteRunnerAuthority
		parent    string
	}{
		{name: "missing", store: store, authority: authority, parent: "missing"},
		{name: "incomplete", store: store, authority: authority, parent: "incomplete"},
		{name: "route mismatched", store: store, authority: authority, parent: "mismatched"},
		{name: "unregistered", store: store, authority: otherAuthority, parent: "unregistered"},
		{name: "empty parent", store: store, authority: authority},
		{name: "nil store", authority: authority, parent: "parent"},
		{name: "nil authority", store: store, parent: "parent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveCompletedParentRoute(tc.store, tc.authority, tc.parent)
			if !errors.Is(err, ErrCompletedParentUnavailable) {
				t.Fatalf("error = %v, want ErrCompletedParentUnavailable", err)
			}
			if got != (CompletedParentRoute{}) {
				t.Fatalf("unavailable resolution = %#v", got)
			}
		})
	}
	if registered.prepareCalls != 0 || registered.executeCalls != 0 {
		t.Fatal("unavailable resolution invoked the registered runner")
	}
}
