package fizeau

import (
	"context"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

type portableRuntimeSentinel struct{}

func (*portableRuntimeSentinel) Info() harnesses.HarnessInfo {
	panic("portable inventory must not call Info")
}
func (*portableRuntimeSentinel) HealthCheck(context.Context) error {
	panic("portable inventory must not call HealthCheck")
}
func (*portableRuntimeSentinel) Execute(context.Context, harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	panic("portable inventory must not call Execute")
}
func (*portableRuntimeSentinel) PortableRuntimeStructure() harnesses.PortableRuntimeStructure {
	return harnesses.PortableRuntimeStructure{
		Name:      "codex",
		Transport: harnesses.PortableRuntimeTransportSubprocess,
		Mode:      harnesses.PortableRuntimeStructuralUnpinned,
	}
}

func TestPortableRuntimeInventoryUsesConfiguredServiceInstances(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	sentinel := &portableRuntimeSentinel{}
	previousHook := harnessInstanceHook
	harnessInstanceHook = func(instances map[string]harnesses.Harness) map[string]harnesses.Harness {
		instances["codex"] = sentinel
		return instances
	}
	t.Cleanup(func() { harnessInstanceHook = previousHook })

	svc := newTestService(t, ServiceOptions{})
	rows, err := svc.portableRuntimeInventory()
	if err != nil {
		t.Fatalf("portableRuntimeInventory() error = %v", err)
	}

	required := 0
	for _, row := range rows {
		if row.Inclusion != harnesses.PortableRuntimeInclusionRequired {
			continue
		}
		required++
		if row.Instance != svc.harnessInstances[row.Name] {
			t.Errorf("row %q did not retain the configured service runner instance", row.Name)
		}
		if row.Name == "codex" && row.Instance != sentinel {
			t.Error("codex inventory row did not retain the hook-substituted service instance")
		}
	}
	if required != len(svc.harnessInstances) {
		t.Fatalf("required subprocess rows = %d, configured service instances = %d", required, len(svc.harnessInstances))
	}
	if svc.harnessByName("codex") != sentinel {
		t.Fatal("service lookup and portable inventory do not share the same configured instance authority")
	}
}

func TestPortableRuntimeInventoryUsesGeminiAndPiDispatchInstances(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	svc := newTestService(t, ServiceOptions{})
	rows, err := svc.portableRuntimeInventory()
	if err != nil {
		t.Fatalf("portableRuntimeInventory() error = %v", err)
	}

	wanted := map[string]bool{"gemini": true, "pi": true}
	seen := make(map[string]bool, len(wanted))
	for _, row := range rows {
		if !wanted[row.Name] {
			continue
		}
		seen[row.Name] = true
		configured := svc.harnessInstances[row.Name]
		if row.Instance != configured || svc.harnessByName(row.Name) != configured {
			t.Fatalf("%s inventory and dispatch do not share the configured service instance", row.Name)
		}
		coordinatorRequest := svc.executeCoordinatorRequest(ServiceExecuteRequest{}, RouteDecision{Harness: row.Name}, "fixture-session", nil)
		if coordinatorRequest.ConfiguredHarness != configured {
			t.Fatalf("%s execute coordinator did not receive the inventory-owned instance", row.Name)
		}
		if configured.Info().Name != row.Name {
			t.Fatalf("%s configured service instance reports identity %q", row.Name, configured.Info().Name)
		}
		if _, ok := configured.(harnesses.PortableRuntimeHarness); !ok {
			t.Fatalf("%s configured service instance lacks PortableRuntimeHarness", row.Name)
		}
	}
	for name := range wanted {
		if !seen[name] {
			t.Fatalf("portable inventory lacks configured %s dispatch instance", name)
		}
	}
}
