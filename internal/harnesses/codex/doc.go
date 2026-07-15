// Package codex implements the CONTRACT-004 harness contracts for
// OpenAI's Codex CLI: Harness, QuotaHarness, AccountHarness,
// ModelDiscoveryHarness, and PortableRuntimeHarness.
//
// The portable-runtime contributor recognizes owner-executable static Linux
// ELF binaries reached directly or through symlinks, including Homebrew
// launchers, and the official npm bin/codex.js shim with its matching
// platform-native package. It contributes a content-addressed static launch
// closure plus present Codex auth, config, model-cache, cache-tree, and Fizeau
// quota-cache state. Config, quota, and cache state are optional; a present
// auth file or non-empty configured credential environment value is required.
// Inherited environment is an explicit, sorted set of names from Codex API
// credentials and supported model-provider or remote-MCP config references.
// Values, path variables, local MCP commands, external config files, shell
// inheritance policies, and unrecognized launcher/state layouts are rejected
// rather than copied or inherited implicitly.
//
// SupportedLimitIDs (emitted by Windows[].LimitID on QuotaStatus):
//
//   - "codex"         — primary (5h) rolling quota window
//   - "codex-weekly"  — weekly rolling quota window
//
// SupportedAliases (recognized by ResolveModelAlias):
//
//   - "gpt"    — resolves to the highest-version concrete gpt-* model
//     in the discovery snapshot
//   - "gpt-5"  — resolves to the highest concrete gpt-5.x model in the
//     discovery snapshot
//
// These sets are the stable public contract for this harness. The
// programmatic source of truth is (*Runner).SupportedLimitIDs() and
// (*Runner).SupportedAliases(); this doc comment mirrors them for
// human readers and must be kept in sync.
package codex
