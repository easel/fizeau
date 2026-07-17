package harnesses

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type portableBindingTestNamespaceRecipe struct{}

func (portableBindingTestNamespaceRecipe) PortableRuntimeNamespaceRecipe() {}

func TestPortableRuntimeRunnerBindingDiagnosticsRedactEnvironment(t *testing.T) {
	const secret = "portable-binding-secret-value"
	input := PortableRuntimeRunnerBindingInput{
		Structure: PortableRuntimeStructure{
			Name: "fixture", Transport: PortableRuntimeTransportSubprocess,
			Mode: PortableRuntimeStructuralUnpinned,
		},
		GuestRoot: "/opt/fizeau/runtime", ClosureClass: PortableRuntimeClosureStatic,
		Launch:      PortableRuntimeLaunch{EntrypointTarget: "fixture/runner"},
		Environment: map[string]string{"FIXTURE_TOKEN": secret}, NamespaceRecipe: portableBindingTestNamespaceRecipe{},
	}
	binding, err := NewPortableRuntimeRunnerBinding(input)
	if err != nil {
		t.Fatal(err)
	}
	child, err := binding.BuildCommand(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{input, binding, child, PortableRuntimeRunnerState{binding: binding}}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		diagnostic := fmt.Sprintf("%v %#v %s", value, value, encoded)
		if strings.Contains(diagnostic, secret) || strings.Contains(diagnostic, "FIXTURE_TOKEN=") {
			t.Fatalf("portable binding diagnostic leaked closed environment: %s", diagnostic)
		}
	}
}
