// Package claude implements the CONTRACT-004 harness contracts for
// Anthropic's claude CLI: Harness, QuotaHarness, AccountHarness, and
// ModelDiscoveryHarness.
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
