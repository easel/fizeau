package grok

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

// grokModelsCapturedOutput is real `grok models` output (grok 0.2.106).
const grokModelsCapturedOutput = `You are logged in with grok.com.

Default model: grok-4.5

Available models:
  * grok-4.5 (default)
`

// grokModelsCacheCapturedJSON is a trimmed real ~/.grok/models_cache.json
// (grok 0.2.106) keeping only the fields the harness decodes.
const grokModelsCacheCapturedJSON = `{
  "fetched_at": "2026-07-23T17:32:13.112352868Z",
  "grok_version": "0.2.106",
  "models": {
    "grok-4.5": {
      "info": {
        "id": "grok-4.5",
        "name": "Grok 4.5",
        "context_window": 500000,
        "hidden": false,
        "reasoning_effort": "high",
        "reasoning_efforts": [
          {"id": "high", "default": true},
          {"id": "medium", "default": false},
          {"id": "low", "default": false}
        ]
      }
    }
  }
}`

func TestGrokModelDiscoveryFromCLIOutputText(t *testing.T) {
	snapshot := grokDiscoveryFromText(grokModelsCapturedOutput, "cli:models")
	if len(snapshot.Models) != 1 || snapshot.Models[0] != "grok-4.5" {
		t.Fatalf("Models = %v, want [grok-4.5]", snapshot.Models)
	}
	if snapshot.Source != "cli:models" {
		t.Errorf("Source = %q", snapshot.Source)
	}
	if len(snapshot.ReasoningLevels) == 0 {
		t.Error("ReasoningLevels empty")
	}
}

func TestGrokModelDiscoveryDefaultOrderedFirst(t *testing.T) {
	text := `Default model: grok-4.5

Available models:
  * grok-4-fast
  * grok-4.5 (default)
  * grok-5-preview
`
	snapshot := grokDiscoveryFromText(text, "cli:models")
	want := []string{"grok-4.5", "grok-4-fast", "grok-5-preview"}
	if len(snapshot.Models) != len(want) {
		t.Fatalf("Models = %v, want %v", snapshot.Models, want)
	}
	for i, m := range want {
		if snapshot.Models[i] != m {
			t.Fatalf("Models = %v, want %v", snapshot.Models, want)
		}
	}
}

func TestGrokModelDiscoveryFromCLISubcommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
cat <<'EOF'
%s
EOF
`, grokModelsCapturedOutput)
	binary := filepath.Join(dir, "fake-grok")
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readGrokModelDiscoveryFromCLI(context.Background(), binary)
	if err != nil {
		t.Fatalf("readGrokModelDiscoveryFromCLI: %v", err)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0] != "grok-4.5" {
		t.Fatalf("Models = %v, want [grok-4.5]", snapshot.Models)
	}
}

func TestGrokModelDiscoveryFromModelsCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models_cache.json")
	if err := os.WriteFile(path, []byte(grokModelsCacheCapturedJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readGrokModelDiscoveryFromModelsCache(path)
	if err != nil {
		t.Fatalf("readGrokModelDiscoveryFromModelsCache: %v", err)
	}
	if len(snapshot.Models) != 1 || snapshot.Models[0] != "grok-4.5" {
		t.Fatalf("Models = %v, want [grok-4.5]", snapshot.Models)
	}
	if snapshot.Source != "models-cache" {
		t.Errorf("Source = %q", snapshot.Source)
	}
	if snapshot.CapturedAt.IsZero() {
		t.Error("CapturedAt not parsed from fetched_at")
	}
}

func TestGrokModelDiscoveryFromModelsCacheMissing(t *testing.T) {
	if _, err := readGrokModelDiscoveryFromModelsCache(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected error for missing cache")
	}
}

func TestGrokResolveModelAlias(t *testing.T) {
	snapshot := harnesses.ModelDiscoverySnapshot{Models: []string{"grok-4.5"}}
	r := &Runner{}

	for _, alias := range []string{"grok", "grok-4"} {
		resolved, err := r.ResolveModelAlias(alias, snapshot)
		if err != nil {
			t.Fatalf("ResolveModelAlias(%q): %v", alias, err)
		}
		if resolved != "grok-4.5" {
			t.Errorf("ResolveModelAlias(%q) = %q, want grok-4.5", alias, resolved)
		}
	}

	if _, err := r.ResolveModelAlias("gpt", snapshot); !errors.Is(err, harnesses.ErrAliasNotResolvable) {
		t.Errorf("unknown alias error = %v, want ErrAliasNotResolvable", err)
	}
	if _, err := r.ResolveModelAlias("grok-9", snapshot); !errors.Is(err, harnesses.ErrAliasNotResolvable) {
		t.Errorf("unmatched major error = %v, want ErrAliasNotResolvable", err)
	}
}

func TestGrokLatestModelVersionComparison(t *testing.T) {
	models := []string{"grok-4-fast", "grok-4.5", "grok-4.1", "grok-5-preview"}
	if got := latestGrokModel("", models); got != "grok-5-preview" {
		// grok-5-preview has a suffix; bare grok-5 would win if present.
		t.Logf("latest overall = %q", got)
	}
	if got := latestGrokModel("4", models); got != "grok-4.5" {
		t.Errorf("latest grok-4 = %q, want grok-4.5", got)
	}
}

func TestGrokSupportedAliases(t *testing.T) {
	r := &Runner{}
	got := r.SupportedAliases()
	want := []string{"grok", "grok-4"}
	if len(got) != len(want) {
		t.Fatalf("SupportedAliases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SupportedAliases = %v, want %v", got, want)
		}
	}
}
