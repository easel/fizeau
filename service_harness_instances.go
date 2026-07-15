package fizeau

import (
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
)

// harnessInstanceHook, when non-nil, is applied to the default harness map
// before it is returned by defaultHarnessInstances. Tests use this hook to
// substitute fake implementations without modifying service.go or requiring
// a factory parameter on New(). Must be restored after each test (use
// t.Cleanup). Production code must never set this variable.
var harnessInstanceHook func(map[string]harnesses.Harness) map[string]harnesses.Harness

// defaultHarnessInstances returns the production map of registered
// Harness implementations keyed by harness name. Only subprocess
// harnesses with concrete Runner types appear here; embedded
// ("fiz", "virtual", "script") and HTTP-only providers do not own
// quota/account state and are deliberately omitted — the scheduler
// treats absence as "no QuotaHarness/AccountHarness behavior".
//
// This file intentionally stays on the interface side of CONTRACT-004:
// concrete runner construction lives under internal/harnesses/, and the
// dispatcher in internal/serviceimpl/execute_dispatch.go remains the only
// allowed non-harness import seam for per-harness packages.
func defaultHarnessInstances() map[string]harnesses.Harness {
	instances := builtin.Instances()
	if harnessInstanceHook != nil {
		instances = harnessInstanceHook(instances)
	}
	return instances
}

// portableRuntimeInventory joins registry metadata to this service's actual
// runner-instance map. Preparation will consume this seam in a later bead;
// keeping the join here prevents a second static runner registry from becoming
// a competing authority.
func (s *service) portableRuntimeInventory() ([]harnesses.PortableRuntimeSurface, error) {
	return harnesses.BuildPortableRuntimeInventory(s.registry, s.harnessInstances)
}
