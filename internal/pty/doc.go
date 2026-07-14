// Package pty documents the internal direct-PTY substrate boundary.
//
// This subtree is introduced for bead agent-949a5ba4 under the decisions in:
// ADR-002 (direct PTY cassette transport), ADR-004 (build-vs-buy boundary),
// SPIKE-001 (creack/pty plus vt10x top rendering proof), SPIKE-002 (terminal
// driver/recorder alternatives), and the harness-golden TUI-only checklist in
// docs/helix/02-design/harness-golden-integration.md.
//
// The package boundary is intentionally generic. Code below internal/pty owns
// PTY descriptors, raw/input/resize/signal event timing, terminal rendering,
// and frame snapshots. Process-tree containment, caller-death supervision,
// cleanup, and recovery belong to internal/processlifecycle. This subtree must
// not import upper-layer harness packages or contain provider-specific policy.
package pty
