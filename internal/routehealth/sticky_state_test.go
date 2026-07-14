package routehealth

import (
	"testing"
	"time"

	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routing"
)

func TestStickyStateDefaultLeaseLifecycle(t *testing.T) {
	now := time.Date(2026, 5, 15, 14, 0, 0, 0, time.UTC)

	t.Run("normalizes keys and reuses a server across models", func(t *testing.T) {
		state := NewStickyState()
		first := state.ApplyStickyLease(now, StickyRequest{
			StickyKey: "  corr-id  ",
			Harness:   "fiz",
			Provider:  "local@desk-a",
			Model:     "model-a",
		})
		if first.Assignment != "acquired" || first.ServerInstance != "desk-a" {
			t.Fatalf("first decision=%+v, want acquired desk-a", first)
		}

		resolveServer := state.StickyServerInstanceResolver(now.Add(time.Second))
		if server, ok := resolveServer("corr-id"); !ok || server != "desk-a" {
			t.Fatalf("normalized-key lookup=(%q, %v), want (desk-a, true)", server, ok)
		}

		second := state.ApplyStickyLease(now.Add(time.Minute), StickyRequest{
			StickyKey:      "corr-id",
			Harness:        "fiz",
			Provider:       "local@desk-a",
			ServerInstance: "desk-a",
			Model:          "model-b",
		})
		if second.Assignment != "reused" || second.ServerInstance != "desk-a" {
			t.Fatalf("cross-model decision=%+v, want reused desk-a", second)
		}
		if second.Bonus != routing.StickyAffinityBonus {
			t.Fatalf("cross-model bonus=%v, want %v", second.Bonus, routing.StickyAffinityBonus)
		}

		oldModelLoads := state.EndpointLoadResolver(now.Add(time.Minute + time.Second))
		if _, ok := oldModelLoads("local", "desk-a", "model-a"); ok {
			t.Fatal("refreshed lease remained in old model scope")
		}
		if load, ok := oldModelLoads("local", "desk-a", "model-b"); !ok || load.LeaseCount != 1 {
			t.Fatalf("new model load=(%+v, %v), want one live lease", load, ok)
		}

		atOriginalBoundary := state.StickyServerInstanceResolver(now.Add(DefaultLeaseTTL))
		if server, ok := atOriginalBoundary("corr-id"); !ok || server != "desk-a" {
			t.Fatalf("original-boundary lookup=(%q, %v), want refreshed lease live", server, ok)
		}
		beforeRefreshedBoundary := state.StickyServerInstanceResolver(now.Add(time.Minute + DefaultLeaseTTL - time.Nanosecond))
		if server, ok := beforeRefreshedBoundary("corr-id"); !ok || server != "desk-a" {
			t.Fatalf("pre-refreshed-boundary lookup=(%q, %v), want live desk-a", server, ok)
		}
		atRefreshedBoundary := state.StickyServerInstanceResolver(now.Add(time.Minute + DefaultLeaseTTL))
		if server, ok := atRefreshedBoundary("corr-id"); ok || server != "" {
			t.Fatalf("refreshed-boundary lookup=(%q, %v), want expired", server, ok)
		}
	})

	t.Run("moves by replacing the selected server and keeps keys distinct", func(t *testing.T) {
		state := NewStickyState()
		state.ApplyStickyLease(now, StickyRequest{
			StickyKey: "key-a",
			Harness:   "fiz",
			Provider:  "local@desk-a",
			Model:     "model-a",
		})
		state.ApplyStickyLease(now, StickyRequest{
			StickyKey: "key-b",
			Harness:   "fiz",
			Provider:  "local@desk-a",
			Model:     "model-a",
		})

		moved := state.ApplyStickyLease(now.Add(time.Second), StickyRequest{
			StickyKey: "key-a",
			Harness:   "fiz",
			Provider:  "local@desk-b",
			Model:     "model-a",
		})
		if moved.Assignment != "moved" || moved.ServerInstance != "desk-b" || moved.Bonus != 0 {
			t.Fatalf("moved decision=%+v, want moved desk-b without affinity bonus", moved)
		}

		resolveServer := state.StickyServerInstanceResolver(now.Add(2 * time.Second))
		if server, ok := resolveServer("key-a"); !ok || server != "desk-b" {
			t.Fatalf("key-a lookup=(%q, %v), want (desk-b, true)", server, ok)
		}
		if server, ok := resolveServer("key-b"); !ok || server != "desk-a" {
			t.Fatalf("key-b lookup=(%q, %v), want (desk-a, true)", server, ok)
		}

		resolveLoad := state.EndpointLoadResolver(now.Add(2 * time.Second))
		if load, ok := resolveLoad("local", "desk-a", "model-a"); !ok || load.LeaseCount != 1 {
			t.Fatalf("desk-a load=(%+v, %v), want one distinct-key lease", load, ok)
		}
		if load, ok := resolveLoad("local", "desk-b", "model-a"); !ok || load.LeaseCount != 1 {
			t.Fatalf("desk-b load=(%+v, %v), want moved replacement lease", load, ok)
		}
	})

	t.Run("expires and reacquires at the exact default boundary", func(t *testing.T) {
		state := NewStickyState()
		req := StickyRequest{
			StickyKey: "boundary",
			Harness:   "fiz",
			Provider:  "local@desk-a",
			Model:     "model-a",
		}
		state.ApplyStickyLease(now, req)

		beforeBoundary := state.StickyServerInstanceResolver(now.Add(DefaultLeaseTTL - time.Nanosecond))
		if server, ok := beforeBoundary("boundary"); !ok || server != "desk-a" {
			t.Fatalf("pre-boundary lookup=(%q, %v), want live desk-a", server, ok)
		}
		atBoundary := state.StickyServerInstanceResolver(now.Add(DefaultLeaseTTL))
		if server, ok := atBoundary("boundary"); ok || server != "" {
			t.Fatalf("boundary lookup=(%q, %v), want expired", server, ok)
		}

		reacquired := state.ApplyStickyLease(now.Add(DefaultLeaseTTL), req)
		if reacquired.Assignment != "acquired" || reacquired.Bonus != 0 {
			t.Fatalf("boundary decision=%+v, want reacquired without affinity bonus", reacquired)
		}
	})
}

func TestStickyStateLoadIsolationAndUtilization(t *testing.T) {
	state := NewStickyState()
	now := time.Date(2026, 5, 15, 14, 0, 0, 0, time.UTC)
	acquire := func(key, provider, server, model string) {
		t.Helper()
		decision := state.ApplyStickyLease(now, StickyRequest{
			StickyKey:      key,
			Harness:        "fiz",
			Provider:       provider,
			ServerInstance: server,
			Model:          model,
		})
		if decision.Assignment != "acquired" {
			t.Fatalf("acquire %q decision=%+v", key, decision)
		}
	}

	acquire("local-a-1", "local", "desk-a", "model-a")
	acquire("local-a-2", "local", "desk-a", "model-a")
	acquire("local-b", "local", "desk-b", "model-a")
	acquire("provider-wide", "local", "provider-wide-server", "model-a")
	acquire("other-model", "local", "model-b-server", "model-b")
	acquire("other-model-no-sample", "local", "model-b-no-sample", "model-b")
	acquire("other-provider", "remote", "remote-server", "model-a")
	acquire("other-provider-no-sample", "remote", "remote-no-sample", "model-a")

	state.RecordUtilization("local", "", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(1),
		QueuedRequests: utilization.Int(1),
		MaxConcurrency: utilization.Int(4),
		Source:         utilization.SourceVLLMMetrics,
		Freshness:      utilization.FreshnessFresh,
	})
	state.RecordUtilization("local", "desk-a", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(1),
		QueuedRequests: utilization.Int(1),
		MaxConcurrency: utilization.Int(4),
		Source:         utilization.SourceVLLMMetrics,
		Freshness:      utilization.FreshnessFresh,
	})
	state.RecordUtilization("local", "desk-b", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(9),
		QueuedRequests: utilization.Int(9),
		MaxConcurrency: utilization.Int(10),
		Source:         utilization.SourceLlamaMetrics,
		Freshness:      utilization.FreshnessStale,
	})
	state.RecordUtilization("local", "desk-c", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(2),
		MaxConcurrency: utilization.Int(2),
		Source:         utilization.SourceLlamaSlots,
		Freshness:      utilization.FreshnessFresh,
	})
	state.RecordUtilization("local", "model-b-server", "model-b", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(3),
		MaxConcurrency: utilization.Int(4),
		Freshness:      utilization.FreshnessFresh,
	})
	state.RecordUtilization("remote", "remote-server", "model-a", utilization.EndpointUtilization{
		ActiveRequests: utilization.Int(1),
		MaxConcurrency: utilization.Int(8),
		Freshness:      utilization.FreshnessFresh,
	})

	resolve := state.EndpointLoadResolver(now.Add(time.Second))
	if load, ok := resolve("local", "desk-a", "model-a"); !ok {
		t.Fatal("missing local/model-a desk-a load")
	} else if load.LeaseCount != 2 || load.NormalizedLoad != 2.5 || !load.UtilizationFresh || load.UtilizationSaturated {
		t.Fatalf("fresh desk-a load=%+v, want leases=2 load=2.5 fresh non-saturated", load)
	}
	if load, ok := resolve("local", "desk-b", "model-a"); !ok {
		t.Fatal("missing local/model-a desk-b load")
	} else if load.LeaseCount != 1 || load.NormalizedLoad != 1 || load.UtilizationFresh || load.UtilizationSaturated {
		t.Fatalf("stale desk-b load=%+v, want exact stale sample to suppress fresh provider-wide fallback", load)
	}
	if load, ok := resolve("local", "desk-c", "model-a"); !ok {
		t.Fatal("missing local/model-a desk-c load")
	} else if load.LeaseCount != 0 || load.NormalizedLoad != 1 || !load.UtilizationFresh || !load.UtilizationSaturated {
		t.Fatalf("saturated desk-c load=%+v, want fresh saturated capacity", load)
	}
	if load, ok := resolve("local", "provider-wide-server", "model-a"); !ok {
		t.Fatal("missing local/model-a provider-wide fallback load")
	} else if load.LeaseCount != 1 || load.NormalizedLoad != 1.5 || !load.UtilizationFresh || load.UtilizationSaturated {
		t.Fatalf("provider-wide fallback load=%+v, want leases=1 load=1.5 fresh non-saturated", load)
	}
	if _, ok := resolve("local", "model-b-server", "model-a"); ok {
		t.Fatal("model-b server leaked into model-a load scope")
	}
	if _, ok := resolve("local", "remote-server", "model-a"); ok {
		t.Fatal("remote provider server leaked into local load scope")
	}

	if load, ok := resolve("local", "model-b-server", "model-b"); !ok || load.LeaseCount != 1 {
		t.Fatalf("model-b scoped load=(%+v, %v), want one lease", load, ok)
	}
	if load, ok := resolve("local", "model-b-no-sample", "model-b"); !ok {
		t.Fatal("missing model-b lease-only load")
	} else if load.LeaseCount != 1 || load.NormalizedLoad != 1 || load.UtilizationFresh {
		t.Fatalf("model-b lease-only load=%+v, provider-wide model-a sample leaked", load)
	}
	if load, ok := resolve("remote", "remote-server", "model-a"); !ok || load.LeaseCount != 1 {
		t.Fatalf("remote scoped load=(%+v, %v), want one lease", load, ok)
	}
	if load, ok := resolve("remote", "remote-no-sample", "model-a"); !ok {
		t.Fatal("missing remote lease-only load")
	} else if load.LeaseCount != 1 || load.NormalizedLoad != 1 || load.UtilizationFresh {
		t.Fatalf("remote lease-only load=%+v, local provider-wide sample leaked", load)
	}

	sample, ok := state.UtilizationSample("local", "desk-a", "model-a")
	if !ok || sample.Source != utilization.SourceVLLMMetrics || sample.Freshness != utilization.FreshnessFresh {
		t.Fatalf("utilization sample=(%+v, %v), want exact fresh vLLM sample", sample, ok)
	}
	if sample, ok := state.UtilizationSample("local", "provider-wide-server", "model-a"); !ok || sample.Freshness != utilization.FreshnessFresh {
		t.Fatalf("provider-wide sample=(%+v, %v), want same-scope fallback", sample, ok)
	}
	if _, ok := state.UtilizationSample("local", "model-b-no-sample", "model-b"); ok {
		t.Fatal("provider-wide model-a sample leaked into model-b sample lookup")
	}
	if _, ok := state.UtilizationSample("remote", "remote-no-sample", "model-a"); ok {
		t.Fatal("local provider-wide sample leaked into remote sample lookup")
	}
}
