// Package anthropic contains shared Anthropic CLI/TUI harness helpers and types,
// including the single owner of Claude Code's normalized native-install and
// account/configuration portable assets. It owns release-evidence, executable,
// credential, quota, and cache discovery, but not route identity,
// transport-specific arguments, or transport-specific environment policy.
//
// CONTRACT-004 invariant #2 requires that both internal/harnesses/claude and
// internal/harnesses/claude-tui import shared Anthropic-specific code from this
// neutral package, forbidding cross-imports between claude and claude-tui.
//
// This package MUST NOT import internal/harnesses/claude or
// internal/harnesses/claude-tui. Both adapters delegate shared portable asset
// discovery here so normalized assets have identical targets and identities.
package anthropic
