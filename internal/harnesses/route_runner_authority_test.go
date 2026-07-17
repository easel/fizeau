package harnesses

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type routeRunnerFixture struct{ name string }

func (r *routeRunnerFixture) Info() HarnessInfo               { return HarnessInfo{Name: r.name} }
func (*routeRunnerFixture) HealthCheck(context.Context) error { return nil }
func (*routeRunnerFixture) Execute(context.Context, ExecuteRequest) (<-chan Event, error) {
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

func TestRouteRunnerKeyIsEndpointAware(t *testing.T) {
	base := RouteRunnerKey{
		Harness:        "codex",
		Provider:       "provider@west",
		Endpoint:       "west",
		ServerInstance: "server-1",
		Model:          "gpt-5",
	}
	changes := []struct {
		name string
		key  RouteRunnerKey
	}{
		{name: "harness", key: RouteRunnerKey{Harness: "claude", Provider: base.Provider, Endpoint: base.Endpoint, ServerInstance: base.ServerInstance, Model: base.Model}},
		{name: "provider", key: RouteRunnerKey{Harness: base.Harness, Provider: "provider", Endpoint: base.Endpoint, ServerInstance: base.ServerInstance, Model: base.Model}},
		{name: "endpoint", key: RouteRunnerKey{Harness: base.Harness, Provider: base.Provider, Endpoint: "east", ServerInstance: base.ServerInstance, Model: base.Model}},
		{name: "server instance", key: RouteRunnerKey{Harness: base.Harness, Provider: base.Provider, Endpoint: base.Endpoint, ServerInstance: "server-2", Model: base.Model}},
		{name: "model", key: RouteRunnerKey{Harness: base.Harness, Provider: base.Provider, Endpoint: base.Endpoint, ServerInstance: base.ServerInstance, Model: "gpt-5-mini"}},
		{name: "literal empty fields", key: RouteRunnerKey{}},
	}

	authority := NewRouteRunnerAuthority(nil, func(key RouteRunnerKey, _ Harness) (Harness, error) {
		return &routeRunnerFixture{name: key.Endpoint}, nil
	})
	baseBinding, err := authority.Bind(base)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []RouteRunnerBinding{baseBinding}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			if change.key == base {
				t.Fatalf("changed key aliases base: %#v", change.key)
			}
			binding, bindErr := authority.Bind(change.key)
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			if binding.Key() != change.key {
				t.Fatalf("binding key = %#v, want literal %#v", binding.Key(), change.key)
			}
			if found, ok := authority.Lookup(change.key); !ok || found.Runner() != binding.Runner() {
				t.Fatalf("exact lookup = (%#v, %v), want bound runner", found.Key(), ok)
			}
			for _, existing := range bindings {
				if binding.Runner() == existing.Runner() {
					t.Fatalf("key %#v aliased runner owned by %#v", change.key, existing.Key())
				}
			}
			bindings = append(bindings, binding)
		})
	}
	if _, ok := authority.Lookup(RouteRunnerKey{Harness: "codex"}); ok {
		t.Fatal("literal empty or populated fields acted as wildcards for a harness-only lookup")
	}

	eastKey := changes[2].key
	aliasing := NewRouteRunnerAuthority(nil, nil)
	shared := &routeRunnerFixture{name: "codex"}
	if _, err := aliasing.Register(base, shared); err != nil {
		t.Fatal(err)
	}
	if _, err := aliasing.Register(eastKey, shared); !errors.Is(err, ErrRouteRunnerIdentityConflict) {
		t.Fatalf("endpoint alias error = %v, want ErrRouteRunnerIdentityConflict", err)
	}
	factoryAliasing := NewRouteRunnerAuthority(nil, func(RouteRunnerKey, Harness) (Harness, error) {
		return shared, nil
	})
	if _, err := factoryAliasing.Bind(base); err != nil {
		t.Fatal(err)
	}
	if _, err := factoryAliasing.Bind(eastKey); !errors.Is(err, ErrRouteRunnerIdentityConflict) {
		t.Fatalf("factory endpoint alias error = %v, want ErrRouteRunnerIdentityConflict", err)
	}
}

func TestRouteRunnerAuthorityRejectsPartialLookup(t *testing.T) {
	exact := RouteRunnerKey{Harness: "codex", Provider: "openai", Endpoint: "west", ServerInstance: "one", Model: "gpt-5"}
	authority := NewRouteRunnerAuthority(nil, nil)
	runner := &routeRunnerFixture{name: "codex"}
	if _, err := authority.Register(exact, runner); err != nil {
		t.Fatal(err)
	}

	partial := []RouteRunnerKey{
		{Harness: exact.Harness},
		{Harness: exact.Harness, Provider: exact.Provider},
		{Harness: exact.Harness, Provider: exact.Provider, Model: exact.Model},
		{Harness: exact.Harness, Provider: exact.Provider, Endpoint: exact.Endpoint, Model: exact.Model},
		{Harness: "Codex", Provider: exact.Provider, Endpoint: exact.Endpoint, ServerInstance: exact.ServerInstance, Model: exact.Model},
	}
	for _, key := range partial {
		if binding, ok := authority.Lookup(key); ok {
			t.Fatalf("partial/display lookup %#v resolved binding %#v", key, binding.Key())
		}
	}
	if binding, ok := authority.Lookup(exact); !ok || binding.Runner() != runner {
		t.Fatalf("exact lookup = (%#v, %v), want registered runner", binding.Key(), ok)
	}
	if _, err := authority.Register(exact, &routeRunnerFixture{name: "replacement"}); !errors.Is(err, ErrRouteRunnerAlreadyRegistered) {
		t.Fatalf("replacement error = %v, want ErrRouteRunnerAlreadyRegistered", err)
	}
}

func TestRouteRunnerAuthorityConcurrentFirstBindAndInventorySnapshot(t *testing.T) {
	structural := &routeRunnerFixture{name: "codex"}
	var factoryCalls atomic.Int64
	authority := NewRouteRunnerAuthority(map[string]Harness{"codex": structural}, func(RouteRunnerKey, Harness) (Harness, error) {
		factoryCalls.Add(1)
		return &routeRunnerFixture{name: "codex"}, nil
	})
	key := RouteRunnerKey{Harness: "codex", Provider: "openai", Endpoint: "west", ServerInstance: "one", Model: "gpt-5"}

	const workers = 64
	bindings := make([]RouteRunnerBinding, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			binding, err := authority.Bind(key)
			if err != nil {
				t.Errorf("Bind() error = %v", err)
				return
			}
			bindings[index] = binding
		}(i)
		go func() {
			defer wg.Done()
			view := authority.StructuralInstances()
			if view["codex"] != structural {
				t.Errorf("inventory snapshot lost structural instance: %#v", view)
			}
			delete(view, "codex")
		}()
	}
	wg.Wait()
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls = %d, want 1", got)
	}
	for i := 1; i < len(bindings); i++ {
		if bindings[i].Runner() != bindings[0].Runner() {
			t.Fatalf("concurrent binding %d selected a different runner", i)
		}
	}
	if authority.StructuralInstance("codex") != structural {
		t.Fatal("caller mutation of inventory snapshot changed authority")
	}
}
