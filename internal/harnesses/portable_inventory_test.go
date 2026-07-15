package harnesses

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

type portableInventoryHarness struct {
	transport PortableRuntimeTransport
	name      string
	mode      PortableRuntimeStructuralMode
	called    *bool
}

type portableNoStructureHarness struct{}

func (portableNoStructureHarness) Info() HarnessInfo                 { return HarnessInfo{} }
func (portableNoStructureHarness) HealthCheck(context.Context) error { return nil }
func (portableNoStructureHarness) Execute(context.Context, ExecuteRequest) (<-chan Event, error) {
	return nil, nil
}

func (h portableInventoryHarness) Info() HarnessInfo {
	*h.called = true
	panic("Info must not be called while joining portable runtime inventory")
}

func (h portableInventoryHarness) HealthCheck(context.Context) error {
	*h.called = true
	panic("HealthCheck must not be called while joining portable runtime inventory")
}

func (h portableInventoryHarness) Execute(context.Context, ExecuteRequest) (<-chan Event, error) {
	*h.called = true
	panic("Execute must not be called while joining portable runtime inventory")
}

func (h portableInventoryHarness) PortableRuntimeStructure() PortableRuntimeStructure {
	return PortableRuntimeStructure{
		Name:      h.name,
		Transport: h.transport,
		Mode:      h.mode,
	}
}

func TestPortableRuntimeInventoryHasNoSideEffects(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate inventory test")
	}
	inventoryFile := filepath.Join(filepath.Dir(currentFile), "portable_inventory.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), inventoryFile, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse portable inventory imports: %v", err)
	}
	if len(parsed.Imports) != 2 || parsed.Imports[0].Path.Value != `"fmt"` || parsed.Imports[1].Path.Value != `"sort"` {
		t.Fatalf("portable inventory imports = %#v, want only fmt and sort", parsed.Imports)
	}

	parsed, err = parser.ParseFile(token.NewFileSet(), inventoryFile, nil, 0)
	if err != nil {
		t.Fatalf("parse portable inventory implementation: %v", err)
	}
	forbiddenCalls := map[string]bool{
		"Discover": true, "Execute": true, "HealthCheck": true,
		"Info": true, "LookPath": true, "PortableRuntimeAssets": true,
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && forbiddenCalls[selector.Sel.Name] {
			t.Errorf("portable inventory calls forbidden side-effect surface %s", selector.Sel.Name)
		}
		return true
	})

	called := false
	registry := &Registry{
		LookPath: func(string) (string, error) {
			called = true
			panic("LookPath must not be called while joining portable runtime inventory")
		},
		harnesses: map[string]HarnessConfig{
			"subprocess": {
				Name:         "subprocess",
				Binary:       "portable-fixture",
				DefaultModel: "fixture-model",
			},
		},
	}

	rows, err := BuildPortableRuntimeInventory(registry, map[string]Harness{
		"subprocess": portableInventoryHarness{
			name: "subprocess", transport: PortableRuntimeTransportSubprocess,
			mode: PortableRuntimeStructuralUnpinned, called: &called,
		},
	})
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if called {
		t.Fatal("portable inventory join invoked a runner or path lookup")
	}
	if len(rows) != 1 || rows[0].Inclusion != PortableRuntimeInclusionRequired {
		t.Fatalf("rows = %#v, want one required subprocess row", rows)
	}
}

func assertPortableRuntimeInventoryRejectsUnclassifiedRecords(t *testing.T) {
	t.Helper()
	tests := map[string]struct {
		registry  *Registry
		instances map[string]Harness
	}{
		"missing instance": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"missing": {Name: "missing", Binary: "missing", DefaultModel: "model"},
			}},
		},
		"unknown transport": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"unknown": {Name: "unknown", Binary: "unknown", DefaultModel: "model"},
			}},
			instances: map[string]Harness{
				"unknown": portableInventoryHarness{
					name: "unknown", transport: "telepathy",
					mode: PortableRuntimeStructuralUnpinned, called: new(bool),
				},
			},
		},
		"missing descriptor": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"opaque": {Name: "opaque", Binary: "opaque", DefaultModel: "model"},
			}},
			instances: map[string]Harness{"opaque": portableNoStructureHarness{}},
		},
		"descriptor identity mismatch": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"expected": {Name: "expected", Binary: "expected", DefaultModel: "model"},
			}},
			instances: map[string]Harness{
				"expected": portableInventoryHarness{
					name: "other", transport: PortableRuntimeTransportSubprocess,
					mode: PortableRuntimeStructuralUnpinned, called: new(bool),
				},
			},
		},
		"unknown structural mode": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"unknown": {Name: "unknown", Binary: "unknown", DefaultModel: "model"},
			}},
			instances: map[string]Harness{
				"unknown": portableInventoryHarness{
					name: "unknown", transport: PortableRuntimeTransportSubprocess,
					mode: "maybe", called: new(bool),
				},
			},
		},
		"native with subprocess mode": {
			registry: &Registry{harnesses: map[string]HarnessConfig{
				"native": {Name: "native", Binary: "native", DefaultModel: "model"},
			}},
			instances: map[string]Harness{
				"native": portableInventoryHarness{
					name: "native", transport: PortableRuntimeTransportNative,
					mode: PortableRuntimeStructuralUnpinned, called: new(bool),
				},
			},
		},
		"orphan instance": {
			registry: &Registry{harnesses: map[string]HarnessConfig{}},
			instances: map[string]Harness{
				"orphan": portableInventoryHarness{
					name: "orphan", transport: PortableRuntimeTransportSubprocess,
					mode: PortableRuntimeStructuralUnpinned, called: new(bool),
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildPortableRuntimeInventory(test.registry, test.instances); err == nil {
				t.Fatal("BuildPortableRuntimeInventory() error = nil, want invalid inventory")
			}
		})
	}
}

func TestPortableRuntimeInventoryClassifiesExactPinOnly(t *testing.T) {
	called := false
	registry := &Registry{harnesses: map[string]HarnessConfig{
		"pinned": {Name: "pinned", Binary: "pinned", ExactPinSupport: true},
	}}
	pinned := portableInventoryHarness{
		name: "pinned", transport: PortableRuntimeTransportSubprocess,
		mode: PortableRuntimeStructuralExactPinOnly, called: &called,
	}
	rows, err := BuildPortableRuntimeInventory(registry, map[string]Harness{
		"pinned": pinned,
	})
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Inclusion != PortableRuntimeInclusionExactPinOnly {
		t.Fatalf("rows = %#v, want exact-pin-only classification", rows)
	}
}
