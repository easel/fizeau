// Package claudetui provides the claude TUI harness implementing
// harnesses.Harness over an in-process PTY with pooled session management,
// hook-based progress events, and transcript-derived final output. It proves
// subscription-mode billing for cost-based routing.
//
// CONTRACT-004 invariant #2 forbids this package from importing
// internal/harnesses/claude; shared code must flow through
// internal/harnesses/anthropic.
package claudetui
