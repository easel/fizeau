// Package hooks provides a conflict-arbitration layer for managing Claude hooks.
//
// Per ADR-013 §3, user-defined hooks in ~/.claude/settings.json and Fizeau-injected
// hooks (PreToolUse/PostToolUse) must coexist without duplication. This package
// provides a Manager that routes hook events to all registered handlers.
package hooks
