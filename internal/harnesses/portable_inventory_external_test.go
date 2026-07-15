package harnesses_test

import (
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/builtin"
)

func TestPortableRuntimeInventoryCoversEveryEligibleRegisteredHarness(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistry(), instances)
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}

	wantNames := []string{
		"claude", "claude-tui", "codex", "fiz", "gemini", "lmstudio", "lucebox",
		"omlx", "opencode", "openrouter", "pi", "script", "virtual", "vllm",
	}
	wantClassification := map[string]struct {
		transport harnesses.PortableRuntimeTransport
		inclusion harnesses.PortableRuntimeInclusion
	}{
		"claude":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"claude-tui": {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"codex":      {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"gemini":     {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"opencode":   {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"pi":         {harnesses.PortableRuntimeTransportSubprocess, harnesses.PortableRuntimeInclusionRequired},
		"fiz":        {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionNonSubprocess},
		"lmstudio":   {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"lucebox":    {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"omlx":       {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"openrouter": {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"vllm":       {harnesses.PortableRuntimeTransportHTTP, harnesses.PortableRuntimeInclusionNonSubprocess},
		"script":     {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionTestOnly},
		"virtual":    {harnesses.PortableRuntimeTransportEmbedded, harnesses.PortableRuntimeInclusionTestOnly},
	}
	gotNames := make([]string, 0, len(rows))
	for _, row := range rows {
		gotNames = append(gotNames, row.Name)
		want, ok := wantClassification[row.Name]
		if !ok {
			t.Errorf("unexpected inventory row %q", row.Name)
			continue
		}
		if row.Transport != want.transport || row.Inclusion != want.inclusion {
			t.Errorf("row %q classification = (%q, %q), want (%q, %q)", row.Name, row.Transport, row.Inclusion, want.transport, want.inclusion)
		}
		if row.Inclusion == harnesses.PortableRuntimeInclusionRequired {
			if row.Instance != instances[row.Name] {
				t.Errorf("required row %q does not retain the actual runner instance", row.Name)
			}
		}
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("inventory names = %v, want %v", gotNames, wantNames)
	}
}

func TestPortableRuntimeInventoryUsesActualNativeTransport(t *testing.T) {
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "native")
	instances := builtin.Instances()
	rows, err := harnesses.BuildPortableRuntimeInventory(harnesses.NewRegistryForTest("claude"), map[string]harnesses.Harness{
		"claude": instances["claude"],
	})
	if err != nil {
		t.Fatalf("BuildPortableRuntimeInventory() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Transport != harnesses.PortableRuntimeTransportNative || rows[0].Inclusion != harnesses.PortableRuntimeInclusionNonSubprocess {
		t.Fatalf("native Claude row = %#v", rows[0])
	}
	if rows[0].Instance != instances["claude"] {
		t.Fatal("native Claude row does not retain the actual runner instance")
	}
}
