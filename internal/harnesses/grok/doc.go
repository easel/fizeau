// Package grok implements the CONTRACT-004 harness contracts for
// xAI's Grok Build CLI: Harness, QuotaHarness, AccountHarness, and
// ModelDiscoveryHarness.
//
// The grok CLI runs headlessly via `grok -p <prompt> --output-format
// streaming-json`, emitting one NDJSON event per stdout line
// (thought/text/end/error). Model discovery uses the non-interactive
// `grok models` subcommand with the on-disk ~/.grok/models_cache.json
// as fallback evidence. Quota evidence comes from the interactive TUI
// `/usage show` command driven over a PTY, which reports the shared
// weekly usage pool ("Weekly limit: N%" used plus "Next reset: ...").
//
// SupportedLimitIDs (emitted by Windows[].LimitID on QuotaStatus):
//
//   - "grok"         — primary quota window (mirrors the weekly pool)
//   - "grok-weekly"  — weekly rolling quota window
//
// SupportedAliases (recognized by ResolveModelAlias):
//
//   - "grok"    — resolves to the highest-version concrete grok-* model
//     in the discovery snapshot
//   - "grok-4"  — resolves to the highest concrete grok-4.x model in the
//     discovery snapshot
//
// These sets are the stable public contract for this harness. The
// programmatic source of truth is (*Runner).SupportedLimitIDs() and
// (*Runner).SupportedAliases(); this doc comment mirrors them for
// human readers and must be kept in sync.
package grok
