package anthropic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeClaudeAuthUsability_EnvTokenUsable(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-not-real")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	got := ProbeClaudeAuthUsability()
	if got.Class != "" {
		t.Fatalf("env token should be usable, got class=%q diagnostic=%q", got.Class, got.Diagnostic)
	}
}

func TestProbeClaudeAuthUsability_InvalidCredentialsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	path := filepath.Join(root, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ProbeClaudeAuthUsability()
	if got.Class != AuthUsabilityInvalid {
		t.Fatalf("empty oauth material: class=%q want %q diagnostic=%q", got.Class, AuthUsabilityInvalid, got.Diagnostic)
	}
}

func TestProbeClaudeAuthUsability_ValidCredentialsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	path := filepath.Join(root, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"tok","refreshToken":"ref"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ProbeClaudeAuthUsability()
	if got.Class != "" {
		t.Fatalf("valid oauth: class=%q diagnostic=%q", got.Class, got.Diagnostic)
	}
}

func TestProbeClaudeAuthUsabilityStrict_Missing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	got := ProbeClaudeAuthUsabilityStrict()
	if got.Class != AuthUsabilityMissing {
		t.Fatalf("strict missing: class=%q want %q diagnostic=%q", got.Class, AuthUsabilityMissing, got.Diagnostic)
	}
	// Soft default must not fail CI hosts without credentials.
	soft := ProbeClaudeAuthUsability()
	if soft.Class != "" {
		t.Fatalf("soft missing should be usable, got class=%q", soft.Class)
	}
}
