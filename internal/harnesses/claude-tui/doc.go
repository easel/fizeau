// Package claudetui provides the stub harness for the claude TUI.
//
// SCOPED: This package implements only the harness interface scaffolding
// and compile-time conformance assertions. Real PTY-driven execution and
// intermediate ProgressEvents are deferred to child beads.
//
// CONTRACT-004 invariant #2 forbids this package from importing
// internal/harnesses/claude; shared code must flow through
// internal/harnesses/anthropic.
package claudetui
