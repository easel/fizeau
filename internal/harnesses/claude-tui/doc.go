// Package claudetui provides the claude TUI harness implementing
// harnesses.Harness over one lifecycle-contained PTY per invocation, with
// hook-based progress events and transcript-derived final output. It proves
// subscription-mode billing for cost-based routing without retaining live
// Claude state between invocations.
//
// CONTRACT-004 invariant #2 forbids this package from importing
// internal/harnesses/claude; shared code must flow through
// internal/harnesses/anthropic.
package claudetui
