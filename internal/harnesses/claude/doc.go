// Package claude implements the CONTRACT-004 harness contracts for
// Anthropic's claude CLI: Harness, QuotaHarness, AccountHarness, and
// ModelDiscoveryHarness, and PortableRuntimeHarness. The portable capability
// applies only to the subprocess transport and delegates shared Claude Code
// binary/state ownership to internal/harnesses/anthropic; NativeMode remains an
// explicit non-subprocess transport and contributes no synthetic CLI binary.
// The subprocess portable form accepts only signed-manifest release digests
// installed as $HOME/.local/share/claude/versions/<x.y.z> dynamic Linux ELFs.
//
// SupportedLimitIDs (emitted by Windows[].LimitID on QuotaStatus):
//
//   - "session"        — current 5h session window
//   - "weekly-all"     — current week, all models
//   - "weekly-sonnet"  — current week, Sonnet-only window
//   - "extra"          — extra-usage / overage window
//
// SupportedAliases (recognized by ResolveModelAlias):
//
//   - "sonnet"
//   - "opus"
//   - "haiku"
//   - "fable"
//
// These sets are the stable public contract for this harness. The
// programmatic source of truth is (*Runner).SupportedLimitIDs() and
// (*Runner).SupportedAliases(); this doc comment mirrors them for
// human readers and must be kept in sync.
package claude
