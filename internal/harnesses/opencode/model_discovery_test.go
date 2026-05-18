package opencode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultOpenCodeModelDiscovery(t *testing.T) {
	snapshot := testOpenCodeModelDiscovery()
	snapshot.Models = []string{"opencode/gpt-5.4", "opencode/claude-sonnet-4-6"}
	if len(snapshot.Models) == 0 {
		t.Fatal("test discovery should include model IDs")
	}
	assertContainsString(t, snapshot.ReasoningLevels, "high", "reasoning")
	if snapshot.FreshnessWindow != openCodeModelDiscoveryFreshnessWindow.String() {
		t.Fatalf("FreshnessWindow = %q, want %q", snapshot.FreshnessWindow, openCodeModelDiscoveryFreshnessWindow.String())
	}
}

func TestParseOpenCodeModels(t *testing.T) {
	input := `
opencode/gpt-5.4
opencode/claude-sonnet-4-6
opencode/gpt-5.4
lm-studio/*
Name Provider Context
`
	models := parseOpenCodeModels(input)
	want := []string{"opencode/gpt-5.4", "opencode/claude-sonnet-4-6", "lm-studio/*"}
	if len(models) != len(want) {
		t.Fatalf("models = %#v, want %#v", models, want)
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("models = %#v, want %#v", models, want)
		}
	}
}

func TestReadOpenCodeModelDiscovery(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
opencode/claude-sonnet-4-6
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := readOpenCodeModelDiscovery(ctx, script)
	if err != nil {
		t.Fatalf("readOpenCodeModelDiscovery: %v", err)
	}
	if snapshot.Source != "cli:opencode models" {
		t.Fatalf("Source = %q, want cli:opencode models", snapshot.Source)
	}
	assertContainsString(t, snapshot.Models, "opencode/gpt-5.4", "models")
	assertContainsString(t, snapshot.Models, "opencode/claude-sonnet-4-6", "models")
	assertContainsString(t, snapshot.ReasoningLevels, "max", "reasoning")
}

func TestReadOpenCodeModelDiscoveryVerboseEvidence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
{
  "id": "gpt-5.4",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 2.5,
    "output": 15,
    "cache": {
      "read": 0.25,
      "write": 0
    }
  },
  "limit": {
    "context": 1050000,
    "output": 128000
  },
  "capabilities": {
    "reasoning": true,
    "attachment": true,
    "toolcall": true
  },
  "variants": {
    "none": {},
    "low": {},
    "medium": {},
    "high": {},
    "xhigh": {}
  }
}
opencode/minimax-m2.5-free
{
  "id": "minimax-m2.5-free",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 0,
    "output": 0,
    "cache": {
      "read": 0,
      "write": 0
    }
  },
  "limit": {
    "context": 204800,
    "output": 131072
  },
  "capabilities": {
    "reasoning": true,
    "attachment": false,
    "toolcall": true
  },
  "variants": {
    "low": {},
    "medium": {},
    "high": {}
  }
}
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := readOpenCodeModelDiscovery(ctx, script, "models", "--verbose")
	if err != nil {
		t.Fatalf("readOpenCodeModelDiscovery: %v", err)
	}
	if snapshot.Source != "cli:opencode models --verbose" {
		t.Fatalf("Source = %q, want cli:opencode models --verbose", snapshot.Source)
	}
	assertContainsString(t, snapshot.Models, "opencode/gpt-5.4", "models")
	assertContainsString(t, snapshot.Models, "opencode/minimax-m2.5-free", "models")
	assertContainsString(t, snapshot.ReasoningLevels, "none", "reasoning")
	assertContainsString(t, snapshot.ReasoningLevels, "xhigh", "reasoning")
	if !strings.Contains(snapshot.Detail, "per-model costs are present for 2 records") {
		t.Fatalf("Detail = %q, want cost evidence", snapshot.Detail)
	}
}

func TestReadOpenCodeVerboseModelEvidence(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat <<'EOF'
opencode/gpt-5.4
{
  "id": "gpt-5.4",
  "providerID": "opencode",
  "status": "active",
  "cost": {
    "input": 2.5,
    "output": 15,
    "cache": {
      "read": 0.25,
      "write": 0
    }
  },
  "limit": {
    "context": 1050000,
    "output": 128000
  },
  "capabilities": {
    "reasoning": true,
    "attachment": true,
    "toolcall": true
  },
  "variants": {
    "low": {},
    "high": {},
    "xhigh": {}
  }
}
EOF
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evidence, err := readOpenCodeVerboseModelEvidence(ctx, script)
	if err != nil {
		t.Fatalf("readOpenCodeVerboseModelEvidence: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %#v, want one record", evidence)
	}
	got := evidence[0]
	if got.Model != "opencode/gpt-5.4" || got.ProviderID != "opencode" || got.ModelID != "gpt-5.4" {
		t.Fatalf("unexpected model identity: %#v", got)
	}
	if got.Cost == nil {
		t.Fatal("expected cost evidence")
	}
	if got.Cost.InputUSDPerMTok != 2.5 || got.Cost.OutputUSDPerMTok != 15 || got.Cost.CacheReadUSDPerMTok != 0.25 {
		t.Fatalf("unexpected cost evidence: %#v", got.Cost)
	}
	if got.ContextLimit != 1050000 || got.OutputLimit != 128000 {
		t.Fatalf("unexpected limits: %#v", got)
	}
	if !got.Reasoning || !got.ToolCall || !got.Attachment {
		t.Fatalf("unexpected capabilities: %#v", got)
	}
	wantVariants := []string{"low", "high", "xhigh"}
	if len(got.Variants) != len(wantVariants) {
		t.Fatalf("variants = %#v, want %#v", got.Variants, wantVariants)
	}
	for i := range wantVariants {
		if got.Variants[i] != wantVariants[i] {
			t.Fatalf("variants = %#v, want %#v", got.Variants, wantVariants)
		}
	}
}

func TestReadOpenCodeModelDiscoveryRejectsEmptyOutput(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-opencode")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf 'no models here\n'
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := readOpenCodeModelDiscovery(ctx, script); err == nil {
		t.Fatal("expected empty model output to fail discovery")
	}
}

func TestParseOpenCodeVerboseModelEvidenceRejectsMalformedJSON(t *testing.T) {
	_, err := parseOpenCodeVerboseModelEvidence(`opencode/gpt-5.4
{
  "id": "gpt-5.4"
`)
	if err == nil {
		t.Fatal("expected malformed verbose output to fail")
	}
}

func assertContainsString(t *testing.T, values []string, want, label string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%s missing %q in %#v", label, want, values)
}
