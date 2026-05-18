// Package anthropic contains shared Anthropic CLI/TUI harness helpers and types.
//
// CONTRACT-004 invariant #2 requires that both internal/harnesses/claude and
// internal/harnesses/claude-tui import shared Anthropic-specific code from this
// neutral package, forbidding cross-imports between claude and claude-tui.
//
// This package MUST NOT import internal/harnesses/claude or internal/harnesses/claude-tui.
package anthropic
