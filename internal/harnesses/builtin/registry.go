package builtin

import (
	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	claudetui "github.com/easel/fizeau/internal/harnesses/claude-tui"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
	opencodeharness "github.com/easel/fizeau/internal/harnesses/opencode"
	piharness "github.com/easel/fizeau/internal/harnesses/pi"
)

// New returns a fresh built-in subprocess harness runner by canonical name.
func New(name string) harnesses.Harness {
	switch name {
	case "claude":
		return &claudeharness.Runner{NativeMode: claudeharness.NativeTransportSelected()}
	case "claude-tui":
		return &claudetui.Harness{}
	case "codex":
		return &codexharness.Runner{}
	case "gemini":
		return &geminiharness.Runner{}
	case "opencode":
		return &opencodeharness.Runner{}
	case "pi":
		return &piharness.Runner{}
	default:
		return nil
	}
}

// Instances returns the production map of built-in subprocess harnesses keyed
// by canonical harness name.
func Instances() map[string]harnesses.Harness {
	return map[string]harnesses.Harness{
		"claude":     New("claude"),
		"claude-tui": New("claude-tui"),
		"codex":      New("codex"),
		"gemini":     New("gemini"),
		"opencode":   New("opencode"),
		"pi":         New("pi"),
	}
}
