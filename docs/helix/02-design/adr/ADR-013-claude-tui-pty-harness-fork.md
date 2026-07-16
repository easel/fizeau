---
ddx:
  id: ADR-013
  status: accepted
  evidence_id: billing-observation-subscription-mode-claude-tui
  depends_on:
    - ADR-002
    - ADR-004
    - ADR-011
    - ADR-014
    - CONTRACT-003
    - CONTRACT-004
  child_of: fizeau-67f2d585
  review:
    self_hash: 7b6760fa222d244517cf807e75414d2bf8282531ade62b9ec7ea961bd17b21c1
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-004: 0fcd10ef635933ba8c2c9bbbfca7fc7c91d117085ef161082e70c0da71d7c862
      ADR-011: 088af56c3f51ae0ba0bb0d71940195af827b2ec5b73768e11fd0d7427070f8d2
      ADR-014: 3924a8e18b73d81ecf5331627b78d255e38422db9d62393a6dd8ee3c10e5081d
      CONTRACT-003: e3da1c8ba3972a5d8af244b267fee8c20e03b5f221409484bf4dc1bb52709939
      CONTRACT-004: 37064b7b952d5027252aa31e2eb7d8fde0fb0139dd7195d5e72658a96c4b77e8
    reviewed_at: "2026-07-16T02:53:06Z"
---
# ADR-013: `claude-tui` PTY Harness as a Fork of `claude`

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-14 | Withdrawn pending CONTRACT-004 | Fizeau maintainers | ADR-002, ADR-004, ADR-011, ADR-014, CONTRACT-004 | Medium |
| 2026-05-17 | Re-proposed — CONTRACT-004 merged; awaiting empirical billing-observation evidence for acceptance | Fizeau maintainers | same | Medium |
| 2026-05-18 | **Accepted** — Empirical subscription-billing observation cassette recorded; primary-harness capability baseline extended with claude-tui row; capability matrix evidence-ID wiring implemented | Fizeau maintainers | same | High |
| 2026-06-04 | **Gate-E: Accept** — claude 2.1.160+ ships `--permission-mode bypassPermissions`, resolving the PermissionModes TUI-affordance gap; baseline promoted (12 of 14 capabilities `pass`, only `ListReasoning`/`SetReasoning` remain `gap`); amendments are documentation-only corrections requiring no code changes | Fizeau maintainers | same | High |
| 2026-07-14 | **Lifecycle amendment** — one containment boundary per accepted invocation; service-owned cleanup and terminalization aligned with CONTRACT-003 v0.15 | Fizeau maintainers | ADR-002, ADR-004, ADR-011, ADR-014, CONTRACT-003, CONTRACT-004 | High |

> **Acceptance note (2026-05-18):** Empirical subscription-mode billing observation
> is now recorded as `billing-observation-subscription-mode-claude-tui` in
> `testdata/harness-cassettes/claude-tui/billing-observation/`. The cassette
> demonstrates that invoking the Anthropic CLI through the direct PTY path
> (without batch flags) reports subscription quota and billing classification
> (not API metering). This fulfills constraint #8 from the re-proposal.
>
> The primary-harness capability baseline (`docs/helix/02-design/primary-harness-capability-baseline.md`)
> has been extended with a `claude-tui` row. All capabilities are initially marked
> as `gap` per ADR-002, pending live PTY record-mode cassette evidence.
>
> The machine-checkable capability matrix (`internal/harnesses/capability_matrix.json`)
> has been extended with claude-tui entries, each carrying an `evidence_id` field.
> The CI test `TestCapabilityMatrixEvidenceIDRequired` now validates that every
> `supported` capability row in the matrix carries a non-empty `evidence_id`.

> **Gate-E disposition (2026-06-04): ACCEPT.** Rationale: claude 2.1.160+ ships
> `--permission-mode bypassPermissions`, resolving the PermissionModes TUI
> affordance gap that was the last documented blocker at re-proposal. The
> `claude-tui` capability baseline is promoted from all-`gap` to 12 of 14
> capabilities `pass` (Run, FinalText, ProgressEvents, Cancel, WorkdirContext,
> PermissionModes[`safe`,`unrestricted`], ListModels, SetModel, TokenUsage,
> QuotaStatus, ErrorStatus, RequestMetadata) with live harness evidence
> (`harness.go`, `stream.go`, `contract004.go`, `launch_args_test.go`,
> validated against installed claude 2.1.162+). The remaining `ListReasoning`
> and `SetReasoning` rows stay `gap`: no documented Claude TUI slash command
> sets or lists per-turn reasoning. These amendments are documentation-only
> corrections requiring no code changes. Promotion is ready for routing
> integration once the ListReasoning/SetReasoning gaps are unblocked by a
> documented TUI affordance from Anthropic.

## Amendment — 2026-07-14: Per-Invocation Lifecycle Ownership

This amendment is the active lifecycle design for `claude-tui`. It aligns this
ADR with [CONTRACT-003 v0.15](../contracts/CONTRACT-003-fizeau-service.md) and
supersedes every conflicting pool, process-ownership, cancellation, cleanup,
and recovery statement retained below as historical context.

Each accepted `Execute` or `Continue` invocation that selects `claude-tui`
gets one dedicated Claude TUI PTY containment boundary. Fizeau registers that
boundary durably before releasing the Claude process, retains process-birth
identities for the supervisor and contained tree, and couples caller liveness
to the supervisor through a control channel. A direct-child parent-death signal
may supplement that channel; it does not replace descendant containment.

The boundary is never returned to a package-global or service-global live
session pool. A harness final payload, normal completion, failure, timeout, or
caller-signalled context cancellation begins service-owned cleanup immediately.
Cleanup requests graceful termination, escalates to forceful containment
termination, reaps owned children, and waits for boundary emptiness before the
public terminal event is emitted. `HarnessCleanupTimeout` bounds that wait. A
missed deadline or containment escape produces
`failed / cleanup_failed / cleanup` and preserves the primary execution tuple
as required by CONTRACT-003.

Stream abandonment means cancellation of the context passed to `Execute` or
`Continue`. Merely ceasing to receive from the returned channel is not
observable. Event delivery therefore cannot control cleanup or terminalization;
the service retains capacity for the terminal fact and closes the stream after
the cleanup decision. Caller death follows the same cleanup path through the
supervisor control channel, with best-effort terminal persistence and no live
delivery guarantee.

Recovery uses the durable lifecycle record and process-birth identities. It
must reject PID reuse and retain unresolved containment evidence; process-name
scanning, a bare PID or PGID, and startup-only orphan reaping are not ownership
proof. Startup stale recovery may supplement current-invocation cleanup after
`StaleHarnessReaperGrace`; it never delays or replaces cleanup at invocation
completion.

`/clear` remains a Claude TUI protocol affordance, not a process-lifetime
primitive. It may reset state inside one owned invocation when a protocol flow
requires it, but it cannot justify reusing a live Claude process across Fizeau
invocations. Continuation is exposed through CONTRACT-003 `Continue`; any
route-specific resume support must use a fresh accepted invocation and report
its continuation disposition without retaining a pooled PTY.

**Key Points**: one containment boundary per accepted invocation | no shared
live process pool | cleanup before terminal delivery or at
`HarnessCleanupTimeout` | caller death through a supervisor control channel |
durable birth-identity recovery, never process-name scanning.

> **Historical status note (2026-05-14):** This ADR was withdrawn pending the
> universal harness interface refactor in
> [ADR-014](./ADR-014-universal-harness-interface.md) and
> [CONTRACT-004](../contracts/CONTRACT-004-harness-implementation.md).
> A 2026-05-14 inventory found 69 service-side call sites reaching into
> per-harness exports beyond the documented `Harness` interface;
> introducing `claude-tui` against that surface would either duplicate
> ~25 exports under a new prefix or wire service code through a fifth
> set of per-harness imports. Both outcomes are the leak pattern
> CONTRACT-004 exists to eliminate.
>
> The fork remains the right shape for accessing Anthropic subscription
> pricing through the TUI. Re-proposal happens after CONTRACT-004
> merges, at which point the implementation reduces to: a new
> `internal/harnesses/claude-tui/` package implementing
> `Harness` + `QuotaHarness` + `AccountHarness` +
> `ModelDiscoveryHarness`, sharing a snapshot type with `claude`
> through an `internal/harnesses/anthropic/` neutral subpackage. No
> service-side changes required.
>
> The companion implementation plan
> [`plan-2026-05-14-claude-tui-fork.md`](../plan-2026-05-14-claude-tui-fork.md)
> is superseded by
> [`plan-2026-05-14-harness-interface-refactor.md`](../plan-2026-05-14-harness-interface-refactor.md)
> for the prerequisite refactor work. A new claude-tui plan is written
> after re-proposal.
>
> The capability-baseline row added by this ADR
> (`claude-tui` in `primary-harness-capability-baseline.md`) and the
> recorder reference in `harness-golden-integration.md` are removed
> alongside this status flip.
>
> ---
>
> ### Prior art surveyed (2026-05-14)
>
> Two reference implementations exist for driving Claude Code outside
> the `claude --print` batch path. Future re-proposal does not have to
> redo this survey.
>
> | Project | Transport | Session lifetime | Notes |
> |---------|-----------|------------------|-------|
> | [smithersai/claude-p](https://github.com/smithersai/claude-p) | in-process PTY (zmux) + `--settings '<inline-json>'` to register `SessionStart`/`Stop` hooks | per-invocation, one-shot | A small ANSI scanner answers Ink's DA1/DA2/DSR/XTVERSION/window-size startup probes — no full terminal emulator. Final text + usage extracted by reading the JSONL transcript whose path is delivered in the `Stop` hook payload. README explicitly notes "client-side restrictions ... are fundamentally unenforceable"; no claim about subscription billing. |
> | [dexhorthy/shannon](https://github.com/dexhorthy/shannon) | tmux session + `tmux send-keys` | persistent across turns | Reads the same JSONL transcript by tailing `~/.claude/projects/`. Rejected as a transport choice for Fizeau per ADR-002 (tmux is not part of the core path), but informative as convergent evidence for the parsing seam. |
>
> The convergent insight from both projects: **the parsing seam is the
> on-disk JSONL transcript at `~/.claude/projects/<workdir>/<id>.jsonl`,
> not rendered TUI output, regardless of transport choice**.
> `internal/pty/terminal` frame derivation and screen pattern-matching
> are not required for normal prompt execution under either reference
> design; they remain required only for the `/usage` quota probe.
>
> ### Design direction for re-proposal
>
> The following decisions are recorded here so the future ADR-013
> re-proposer can adopt them without re-deriving the rationale. They
> are constraints, not interface contract — CONTRACT-004 and ADR-014
> remain unaffected and transport-agnostic.
>
> 1. **Transport**: in-process PTY via the existing `internal/pty/`
>    library, with hooks registered via `--settings '<inline-json>'`
>    (Anthropic-published extension point). The `--settings` flag is
>    explicitly distinguished from the previously-forbidden batch flags
>    (`--print`, `-p`, `--output-format`, `--stream-json`, `--effort`,
>    `--model`)
>    because it configures end-user-facing behavior the way a user's
>    `~/.claude/settings.json` would, not an automation/batch mode. The
>    batch-flag prohibition stands; the `--settings` carve-out is
>    additive.
>
> 2. **Output parsing**: read the JSONL transcript whose path is
>    delivered in the `Stop` hook payload. Do not parse rendered TUI
>    output. `internal/pty/terminal` (vt10x) is not required for
>    Execute; the PTY layer is reduced to "enough to keep Ink happy at
>    startup" — a small responder for DA1 / DA2 / DSR / XTVERSION /
>    window-size probes. A reusable startup-probe responder belongs in
>    `internal/pty/` so the quota probe path can consume it too.
>
> 3. **Streaming progress events**: use `PreToolUse` / `PostToolUse`
>    hooks (or whichever Claude Code hooks are documented for tool-call
>    boundaries at re-proposal time) to emit `tool_call` / `tool_result`
>    events during the turn. claude-p is batch (only `Stop`); Fizeau's
>    CONTRACT-004 requires intermediate `ProgressEvents` so this hook
>    set is load-bearing.
>
> **Historical implementation reference — superseded 2026-07-14:** Items 4–6
> below preserve the earlier pooled-session, no-PID-file, and startup-reaper
> proposal so the design history remains reviewable. They are not active
> lifecycle requirements. The amendment above and CONTRACT-003 v0.15 require
> one containment boundary per accepted invocation, pre-launch durable
> birth-identity registration, current-invocation cleanup before terminal
> delivery, and independent stale recovery.
>
> 4. **Session lifetime**: **pooled long-lived sessions with `/clear`
>    between turns, lifetime bounded by the fiz process**. Rationale:
>    Ink + auth startup is the expensive part (~50–200 ms per claude-p's
>    measurements); amortizing it across the many Execute calls within
>    a single fiz invocation is worth the pool-management cost.
>    `/clear` resets conversation state without dropping the warm
>    session. The pool dies when fiz dies — no PID files, no daemon, no
>    cross-invocation persistence.
>
>    Pool key default: **per `(harness, workdir)`**. Claude sessions
>    are bound to a working directory at startup; switching workdirs in
>    an existing session is not supported by `claude`. Per-(harness)
>    only would force serialization across all workdirs;
>    per-(harness, workdir, model) is overkill — model selection is
>    cheap enough to run via `/model` post-`/clear`.
>
>    Pool depth default: **1 per key**. Adequate for serial agent
>    loops, which is the standalone CLI's usage. Service-mode
>    concurrency can raise this with no contract change.
>
>    Pool placement: the pool lives at package scope (a singleton in
>    `internal/harnesses/claude-tui/`) or at service scope (a
>    constructor-injected dependency on the Runner), **not as a field
>    on the Runner struct**. Two `&claudetui.Runner{}` instances must
>    share the pool, otherwise the dispatcher's "construct a fresh
>    Runner per Execute" pattern defeats the amortization. CONTRACT-004
>    invariant #6 forbids mutable quota/account state on the Runner
>    but does not forbid shared transport state behind a singleton;
>    this is the right escape valve.
>
> 5. **Empirical `/clear` semantics gate** (pre-implementation): verify
>    against the installed Claude Code that `/clear`:
>    - resets conversation history (the point of the command);
>    - does NOT reset model selection (otherwise per-turn `/model` is
>      required, lengthening the per-turn ritual);
>    - does NOT reset permission mode;
>    - does NOT close the auth/session token;
>    - starts a new transcript file at a path observable from the
>      next turn's `Stop` hook payload.
>
>    If any of those don't hold, the per-turn ritual lengthens but
>    the pool model is still worthwhile. If `/clear` doesn't exist or
>    is unstable in the installed version, fall back to per-Execute
>    sessions (claude-p model) and accept the cold-start cost.
>
> 6. **Orphan reaper**: fiz crashes leave pooled `claude` processes
>    orphaned. A startup reaper analogous to the existing
>    `service_stale_harness_reaper*.go` kills `claude` processes whose
>    parent fiz PID is gone, before the new fiz instance constructs
>    its pool. No persistent state across fiz invocations — the
>    reaper inspects live process state only.
>
> 7. **Hook conflict handling**: a user's existing
>    `~/.claude/settings.json` may declare its own `SessionStart` /
>    `Stop` / `PreToolUse` / `PostToolUse` hooks. The `--settings
>    '<inline-json>'` mechanism's merge semantics (replace vs. layer)
>    are unspecified in claude-p's README and need explicit
>    verification before claude-tui can ship. If hooks are
>    replace-not-merge, Fizeau must compose with whatever the operator
>    already has, not stomp it.
>
> 8. **Subscription billing observation**: still required as a
>    promotion gate per ADR-014. Neither claude-p nor shannon claims
>    subscription billing; both route through the same `claude`
>    binary and inherit whatever billing classification that binary's
>    request paths produce. The re-proposal must include an empirical
>    measurement showing PTY+hooks-driven Claude moves the `/usage`
>    window, otherwise the fork's economic premise is unverified.
>
> ---
>
> The content below remains for historical reference of the original
> proposal.

## Context

The current `claude` harness (`internal/harnesses/claude/runner.go`) drives the
Claude CLI through `claude --print -p --output-format stream-json` (with a
legacy `--output-format json` fallback) via `os/exec`. That subprocess path
covers normal prompt execution today, but it is a different transport from the
direct PTY path Fizeau already uses for `/usage` quota probing
(`internal/harnesses/claude/quota_pty.go` over `internal/harnesses/ptyquota`,
`internal/pty/session`, `internal/pty/terminal`).

**The driver for forking is Anthropic subscription pricing.** Claude Pro/Max
subscription capacity is billed against an account quota that is exposed
through the interactive Claude CLI surface (TUI + `/usage`). The Anthropic
API, including request paths a `claude --print` invocation may resolve
against, is metered separately at per-token API pricing. Routing prompt
execution through the interactive TUI is how Fizeau accesses subsidized
subscription capacity (ADR-011 cost-based routing already assumes this is
possible). The existing `claude --print` transport cannot be relied on to
keep landing on subscription capacity as Anthropic evolves the boundary; the
TUI surface is the durable, documented entry point for subscription quota.

This is not about anti-automation fingerprinting or impersonating a human as
a security goal. It is about using the subscription product the way it is
priced, which means: drive the TUI, do not pass batch-API-shaped flags, and
do not introduce Fizeau-side signals (env vars, argv markers) that change
billing classification or invite policy enforcement against automated batch
use.

ADR-002 already commits Fizeau to direct PTY ownership and bans tmux from the
core path. ADR-004 caps build vs. buy for the PTY library boundary. ADR-011
treats subsidized Claude/Codex quota as a routing-preferred capacity pool.
None of those ADRs specify *how* normal prompt execution should adopt PTY
without breaking the existing `claude` evidence base (cassettes, runner
tests, capability baseline rows, auto-routing eligibility).

## Decision

Fizeau will add a **new, separate primary harness identity `claude-tui`** that
implements the existing `harnesses.Harness` interface
(`internal/harnesses/types.go`, `Info`/`HealthCheck`/`Execute`) and runs the
Claude CLI exclusively through the direct PTY transport. Each accepted run uses
one service-owned containment boundary and tears that boundary down before its
public terminal event, subject to `HarnessCleanupTimeout`. `claude-tui` is a
fork — a sibling package alongside `internal/harnesses/claude/` — not a mode,
flag, conditional branch in the existing runner, or pooled live session.

This decision does not change the `claude` harness contract, capability
evidence, or routing policy. Both identities may coexist; capability and
routing decisions are tracked per identity.

`claude-tui` is the identity through which subscription-billed Claude
capacity is routed once it earns auto-routing eligibility. Until then, the
existing `claude` harness continues to serve routed Claude traffic.

**Key Points**: separate package + registry identity | PTY only, no batch
flags, no Fizeau-side identification env/argv | one containment boundary per
accepted invocation | service-owned cleanup before terminal delivery | both
identities implement the same CONTRACT-004 interface and have independent
evidence rows.

## Harness Interface (Normative)

The new identity implements the existing, unchanged interface defined in
`internal/harnesses/types.go`:

```go
type Harness interface {
    // Info returns identity + capability metadata for this harness.
    Info() HarnessInfo

    // HealthCheck triggers a fresh probe (binary present, auth ok, etc.)
    // and returns nil if the harness is ready to execute.
    HealthCheck(ctx context.Context) error

    // Execute runs one resolved request. Events stream on the returned
    // channel; a single final event closes the stream. The first error
    // return is reserved for setup failures (binary missing, etc.) — once
    // the channel is returned, all per-run failures are reported via a
    // final event with Status != "success".
    Execute(ctx context.Context, req ExecuteRequest) (<-chan Event, error)
}
```

`HarnessInfo`, `ExecuteRequest`, `Event`, `FinalData`, `FinalUsage`,
`RoutingActual`, `ReasoningActual`, and the additive, unknown-preserving
`EventType` union are defined in the same file and are not extended by this
ADR. Service-owned additions such as `context_capacity` do not become
`claude-tui` parser responsibilities. CONTRACT-004 owns that internal adapter
interface. CONTRACT-003 owns the public service stream,
typed terminal fact, cleanup deadline, continuation, and process-lifecycle
requirements. This ADR adds no public event type, request field, or metadata
key.

The `Harness.Execute` final event is adapter output, not permission to emit the
public terminal fact immediately. `internal/serviceimpl` must hold that result
until the invocation's containment boundary is empty or
`HarnessCleanupTimeout` expires, then classify and emit exactly one public
terminal event. The runner receives the request context for caller-signalled
cancellation, while the service lifecycle supervisor performs cleanup under a
service-owned context that survives request cancellation.

`claude-tui` therefore differs from `claude` in:

- transport mechanism (PTY vs. `os/exec` batch),
- registry config (`HarnessConfig`) entry name, binary invocation, and
  baseline flags,
- `HarnessInfo.Name` returned by `Info()`,
- internal package layout and tests,
- its per-invocation PTY launch plan and protocol adapter,

not in interface, event shapes, or request semantics. A caller routing
through `harnesses.Harness` cannot tell the two apart except by the
`HarnessInfo.Name` it returns.

CONTRACT-003 `Continue` does not change this boundary. A continued session is a
new accepted Fizeau invocation. `claude-tui` may resume only through durable or
Claude-supported continuation state that does not keep a live PTY process
between invocations; otherwise the route reports continuation unsupported or
uses the caller-selected fresh-session policy.

A separate harness identity (rather than a `Transport` mode on a single
`HarnessConfig`) is required because:

- `HarnessInfo.Name` is the key the capability matrix, routing layer, and
  evidence store use to attribute rows. Collapsing two transports into one
  name makes evidence ambiguous.
- `HarnessConfig` carries `BaseArgs`, `PermissionArgs`, `ModelFlag`,
  `ReasoningFlag`, and `TUIQuotaCommand` fields that have transport-specific
  meaning. A PTY harness uses none of `BaseArgs`/`ModelFlag`/`ReasoningFlag`
  and uses `TUIQuotaCommand`-shaped flows for routine model/reasoning
  selection, not just for quota. Two configs is cheaper than a polymorphic
  one.
- `AutoRoutingEligible` is a per-row decision in the primary baseline. It
  must be expressible independently for the PTY transport, which has no
  live cassette evidence at introduction.

## Scope

### In Scope

| Aspect | Detail |
|--------|--------|
| Harness adapter | `internal/harnesses/claude-tui/` (Go package `claudetui`) must implement `harnesses.Harness` and only the CONTRACT-004 optional interfaces it supports. It owns Claude protocol driving, hook/transcript interpretation, model/reasoning discovery, and internal Final/Progress adapter events; it does not own public terminalization or live-process pooling. |
| Registry identity | `builtinHarnesses` (`internal/harnesses/registry.go`) must carry a distinct `claude-tui` identity with subscription billing classification and TUI-only invocation arguments. Batch/output-format arguments remain forbidden. Auto-routing promotion remains an evidence-backed spec decision independent of `claude`. |
| Lifecycle | `internal/serviceimpl` must acquire one `internal/processlifecycle` lease per accepted invocation before Claude starts. The lease owns durable birth-identity registration, the platform containment boundary, caller-liveness supervision, cleanup, and stale recovery. No live process crosses the invocation boundary. |
| Transport | Claude runs through `internal/pty/session` under the lifecycle lease. Normal Execute output uses documented Claude hooks and the JSONL transcript path delivered by the stop hook; a minimal PTY startup-probe responder keeps the TUI operational. Full terminal-frame parsing remains available for TUI-only probes such as `/usage`, not as a required normal-Execute parser. No `claude --print`/`-p`/`--output-format` invocation is permitted on this path. |
| Invocation profile | Start the user-facing `claude` TUI without batch/output-format flags. The workdir is fixed when the per-invocation containment boundary starts. Model, permission, and reasoning controls may use documented user-facing TUI arguments or affordances; absence of such an affordance remains a capability gap. |
| Prompt delivery | The prompt is sent into the TUI input area using the terminal's bracketed-paste sequence (`ESC[200~` … `ESC[201~`) followed by `Enter`. Paste arrives as a single burst inside the bracketed-paste boundaries, matching the byte shape a real paste produces; Fizeau does not insert artificial inter-byte delays inside a paste. Single keystrokes for slash commands and menu navigation are sent one logical key per event with a small fixed inter-key delay (default 25–75ms) to avoid super-human keystroke bursts that could trip TUI input handling. |
| Cancellation | Request-context cancellation is the caller's abandonment signal. The adapter may request graceful in-TUI cancellation, but the lifecycle lease must then terminate and reap the whole containment boundary under the service-owned cleanup context. Silent channel non-consumption is not a signal. |
| Capability surface | Same baseline rows as `claude`, evidenced independently: `Run`, `FinalText`, `ProgressEvents`, `Cancel`, `WorkdirContext`, `PermissionModes`, `ListModels`, `SetModel`, `ListReasoning`, `SetReasoning`, `TokenUsage`, `QuotaStatus`, `ErrorStatus`, `RequestMetadata`. |
| Cassettes | Record-mode and replay-mode follow ADR-002. Cassette `manifest.harness.name = "claude-tui"`; binary version, command, terminal, timing, and provenance fields are stamped from the PTY session as for `claude` quota cassettes. Live cassette evidence is required for promotion of any baseline row from `gap` to `pass`, identical to the `claude` rule. |
| Quota | Both Claude identities may share account-scoped Anthropic evidence through a neutral `internal/harnesses/anthropic/` store/probe seam. Each identity projects that evidence only through CONTRACT-004 `QuotaHarness`/`AccountHarness`; neither imports the other's package or concrete snapshot type. **Assumption**: both identities authenticate as the same Anthropic account. A multi-account design must key durable evidence by stable account identity. |
| Shared helpers | Pure Anthropic helpers, unexported durable cache/probe mechanics, account projection, quota-message classification, and model-name normalization belong in `internal/harnesses/anthropic/`. Runner-specific protocol state remains in its runner package. The neutral seam must not own live processes or bypass `internal/processlifecycle`. |
| Auto-routing | Promotion requires fresh account/quota evidence, accepted live record-mode evidence for every supported capability row, lifecycle conformance, and benchmark deltas. Promotion is a separate spec change; this ADR does not infer current eligibility from implementation state. |

### Out of Scope

| Aspect | Detail |
|--------|--------|
| Merging implementations | Sharing a runner with conditionals, build tags, or `if usePTY { ... } else { ... }` branching inside the `claude` package is explicitly rejected. The fork pays a one-time duplication cost so neither implementation contorts to fit the other. |
| Retiring `claude` | Not part of this ADR. The subprocess `claude` harness keeps its tests, cassettes, capability rows, and auto-routing eligibility. Any future retirement is a separate ADR with its own promotion/deprecation gates. |
| Multi-account support | Quota cache keying remains per-account-default. Operators with multiple Anthropic accounts continue to fall outside the supported routing model until a follow-up spec extends the cache key. |
| Cross-invocation live sessions | Package-global and service-global Claude process pools, `/clear`-based reuse, daemons, and detached PTY sessions are forbidden by CONTRACT-003. Durable continuation metadata and durable quota/account caches are allowed. |
| Process-name orphan reaping | Scanning for a process named `claude`, or trusting only a PID/PGID, is not a supported cleanup or recovery strategy. Recovery is scoped to a durable lifecycle record with process-birth identities. |
| Tmux | Explicitly rejected by ADR-002. Reaffirmed here: `claude-tui` must not depend on tmux, screen, or any external terminal multiplexer. |
| Operator UX changes | No new `fiz` flags or new operator commands. `claude-tui` is selectable through the same routing/identity mechanisms as any other primary harness. |
| `codex`/`gemini` PTY forks | Out of scope. If those harnesses need the same treatment for the same subscription-pricing reason, follow-up ADRs will mirror this structure rather than generalize prematurely. |
| Anti-fingerprinting / human impersonation as a goal | Out of scope. The constraints below exist to keep Fizeau's invocation looking like normal TUI usage so subscription-billing classification is preserved, not to defeat adversarial detection. |

## Invocation Constraints

These constraints derive from the subscription-pricing goal: invocation must
not look like batch-API automation, and Fizeau must not introduce side
channels that could be used to reclassify the request.

`claude-tui` **must not**:

- Pass any flag that signals batch/automation intent or selects a non-TUI
  output format: `--print`, `-p`, `--output-format`, `--stream-json`,
  `--effort`, or any future Anthropic flag in the same family. Where a TUI
  affordance for the same setting exists (e.g. a `/model` selector), use that
  affordance via PTY input bytes. Where no TUI affordance exists, the
  capability is `gap` for `claude-tui` until one does — do not silently fall
  back to a batch CLI flag.

  Model selection via `--model` and permission mode via
  `--permission-mode bypassPermissions` (claude 2.1.160+) are explicitly
  permitted: they configure end-user-facing TUI behavior (the same model and
  permission selections a user makes inside the TUI) and are correctly used by
  this harness, exactly as `--settings` is. They are not batch/automation
  signals.
- Introduce Fizeau-side environment variables that identify the caller as an
  agent: no `CLAUDE_*`, `ANTHROPIC_*`, `*_AGENT*`, `*_AUTOMATED*`, or similar
  Fizeau-introduced names on this path. Pre-existing variables already set
  in the operator's environment are passed through unchanged via the
  documented allowlist below.
- Send single-keystroke input bursts faster than the configured inter-key
  delay band (default 25–75ms per logical key). Pasted prompts go through
  bracketed paste and may arrive as one burst; that matches real paste
  behavior and is allowed.

`claude-tui` **may**:

- Use the bracketed-paste sequence to deliver a multi-line prompt in one
  event. Bracketed paste is the documented terminal mechanism for paste
  input and is what a real paste produces.
- Read raw PTY bytes and derive frames through `internal/pty/terminal` for
  TUI-only control flows. Normal Execute final text and tool-boundary evidence
  should use the documented hook/transcript seam. The adapter emits internal
  harness events; Fizeau constructs public service events above that boundary.
- Cancel a turn with the key combination the TUI documents for cancellation
  (Esc by default; Ctrl-C as a fallback). Regardless of the TUI response, the
  service lifecycle supervisor terminates and reaps the current containment
  boundary. A later request starts a new boundary; it does not restart or reuse
  the old live session.
- Reuse the existing `internal/pty/cassette` recorder and
  `internal/ptytest` assertion framework with no modifications beyond a new
  harness name and scenario fixtures.

### Environment Allowlist

The PTY session is started with the following environment, exactly:

- Pass-through from the operator environment: `HOME`, `PATH`, `USER`,
  `LOGNAME`, `SHELL`, `LANG`, `LC_ALL`, `TZ`, `XDG_*` (any present
  `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME`,
  `XDG_RUNTIME_DIR`), and any environment variables under the `CLAUDE_`
  prefix that the operator has already set in their shell (Fizeau itself
  must not set these — passing through what the operator already exported
  is acceptable because it represents the operator's normal shell state).
- Set by Fizeau if not already present: `TERM=xterm-256color` (default
  terminal type a modern shell would set), `LANG=C.UTF-8`, `LC_ALL=C.UTF-8`
  (locale defaults; existing operator values win).

Anything else is dropped. The cassette manifest's `env_allowlist` records
the exact set used per recording so future spec amendments can be reviewed
against captured evidence.

## Module Boundaries

Per ADR-002, no package below `internal/pty` may import
`internal/harnesses`. CONTRACT-003 adds the lifecycle boundary above both the
PTY and harness packages:

| Layer | Path | Owns |
|-------|------|------|
| Service orchestration | `internal/serviceimpl` | Accepts the invocation, acquires/releases the lifecycle lease, withholds the public terminal event until cleanup succeeds or times out, applies typed cleanup precedence, and projects adapter events onto CONTRACT-003. It must not parse Claude-native output. |
| Lifecycle supervisor | `internal/processlifecycle` | Pre-launch durable ownership record, process-birth identities, launch gate, Unix process group/session or Windows job object, caller-liveness control channel, graceful/forceful cleanup, reaping, timeout decision, and stale recovery. It must not interpret Claude protocol, quota, or terminal frames. |
| Raw PTY session | `internal/pty/session` | PTY descriptors, terminal sizing, raw input/output, resize, and wait behavior under a supplied lifecycle lease. It must not create a second ownership model, emit public service terminal facts, or retain sessions across invocations. |
| Startup-probe responder | `internal/pty/` neutral helper | Minimal DA1/DA2/DSR/XTVERSION/window-size responses needed to keep the Claude TUI operational. It has no Claude quota or transcript semantics. |
| Runner | `internal/harnesses/claude-tui/runner.go` | Implements CONTRACT-004 `Harness`; owns prompt delivery, hook registration, Claude commands/menus, request-local timeouts, and internal adapter events. Cancellation requests graceful protocol shutdown and then yields to the lifecycle supervisor. |
| Transcript/hook parser | `internal/harnesses/claude-tui/stream.go` | Resolves the JSONL transcript path from documented hook payloads, derives final text/tool boundaries/usage, and reports malformed or missing protocol evidence as adapter failure. Full frame parsing is reserved for TUI-only probe flows. |
| Model/reasoning discovery | `internal/harnesses/claude-tui/model_discovery.go` | TUI-driven model and reasoning enumeration and `ModelDiscoveryHarness` projection keyed by `claude-tui`. Discovery subprocesses use the same lifecycle boundary rules. |
| Shared Anthropic seam | `internal/harnesses/anthropic/` | Pure classification/normalization helpers plus account-scoped durable quota/account store and probe mechanics shared without exporting a harness-owned snapshot. It has no dependency on either runner and owns no live process pool. |
| Cassettes/tests | `internal/harnesses/claude-tui/testdata/` and `internal/ptytest` scenarios | Live record cassettes per capability row; replay-only tests for default CI; lifecycle tests use controlled subprocess fixtures rather than authenticated cassettes. |

The `claude-tui` runner must not import `internal/harnesses/claude`, and the
`claude` runner must not import `internal/harnesses/claude-tui`. The
`internal/harnesses/anthropic` neutral package is the only allowed sharing
seam for Anthropic-specific cache and parsing helpers. Both runners consume
process ownership through `internal/processlifecycle`; neither runner,
`internal/pty`, nor the neutral Anthropic seam may maintain a package-global or
service-global live process pool.

## Capability Evidence and Promotion Requirements

`primary-harness-capability-baseline.md` must carry a separate `claude-tui`
row. Capability and routing evidence for `claude` never promotes the sibling
identity implicitly. The lifecycle amendment adds a promotion requirement:
every supported live operation must also satisfy CONTRACT-003 containment,
cleanup, cancellation, and caller-death conformance.

The table below preserves the **historical 2026-06-04 Gate-E evidence record**.
It is not a claim about the current worktree or a substitute for current
baseline, cassette, conformance-test, and benchmark evidence:

| Row | Status | Evidence / known blocker |
|-----|--------|---------------|
| Run, FinalText, ProgressEvents, Cancel, WorkdirContext, ListModels, SetModel, TokenUsage, QuotaStatus, ErrorStatus, RequestMetadata | historical `pass` | Gate-E cited live harness evidence (`harness.go`, `stream.go`, `contract004.go`) against Claude 2.1.162+. |
| PermissionModes | historical `pass` | Gate-E cited Claude 2.1.160+ `--permission-mode bypassPermissions` evidence in `launch_args_test.go::TestBuildLaunchArgsBypassPermissions`. |
| ListReasoning | `gap`, no TUI affordance known | No documented Claude TUI slash command lists per-turn reasoning levels. |
| SetReasoning | `gap`, no TUI affordance known | The `claude --print` path uses `--effort`; no documented Claude TUI slash command sets per-turn reasoning. Until one ships, `claude-tui` does not set reasoning and treats requested non-default values as a routing rejection. |

Every current `pass` row must be accepted independently of `claude`. A `pass`
on one identity does not imply `pass` on the other. The current baseline is the
source of status; `harness-golden-integration.md` must require authenticated
record mode plus default-CI replay cassettes for each supported TUI capability.

## Performance and Cost Acknowledgement

The fork deliberately pays per-invocation lifecycle cost:

- **Cold start and cleanup**: every accepted invocation establishes a durable
  ownership record, starts a new PTY containment boundary, completes Claude TUI
  startup/auth negotiation, and reaps the boundary before terminal delivery.
  No unverified latency estimate justifies weakening that guarantee, and
  `/clear` cannot amortize it across invocations.
- **Parsing cost**: normal Execute uses hook payloads and the JSONL transcript
  seam instead of deriving a full terminal frame for every spinner redraw.
  TUI-only quota/model probes may still pay frame-rendering cost and must be
  measured separately.
- **Input cost**: documented menu navigation and inter-key delays add bounded
  latency when a request selects model, reasoning, or permission state.
  Bracketed-paste prompt delivery does not require artificial per-byte delay.
- **Code and lifecycle cost**: two harness adapters, a shared
  `internal/processlifecycle` supervisor, independent evidence, and separate
  cassette suites increase review surface. The design accepts this cost rather
  than hiding transport and ownership differences behind one runner.

The benchmark suite must compare `claude-tui` with `claude` across short runs,
long tool-using runs, cancellation, and cleanup. It must report startup,
request, transcript parsing, and teardown windows separately. Promotion to
auto-routing requires measured deltas and lifecycle conformance; this ADR does
not assert a current performance result.

These costs are accepted because subscription pricing is the load-bearing
economic constraint for primary-routing Claude capacity (ADR-011). If
`claude --print` ever lands on subscription capacity reliably and durably
without TUI-driven traffic, this ADR is open to revision.

## Alternatives

| Option | Pros | Cons | Evaluation |
|--------|------|------|------------|
| **Fork as `claude-tui` with per-invocation containment (selected)** | Implementations and capability evidence remain distinct; honors ADR-002 PTY transport and CONTRACT-003 lifecycle ownership; allows `claude` to coexist; lets routing distinguish subscription-billed and API-billed supply. | Duplicated adapter scaffolding plus cold-start and cleanup cost on every accepted invocation. | **Selected**: matches the subscription-pricing driver without weakening the public service lifecycle or hiding transport differences in one runner. |
| Add a `Transport` mode to existing `claude.Runner` | No new package; smaller code footprint at first. | Conditionals in every hot path (Execute, stream parsing, model discovery, error classification); capability evidence becomes ambiguous because the harness identity collapses two transports into one row; auto-routing eligibility decisions cannot be expressed cleanly; `HarnessConfig` fields like `BaseArgs` and `ModelFlag` have no meaning for half the configurations of a single name. | Rejected: explicitly the failure mode the user called out. |
| Pool live Claude TUI sessions and run `/clear` between invocations | Amortizes startup/auth negotiation and can reduce nominal latency. | Violates CONTRACT-003's per-invocation ownership and cleanup guarantee; turns `/clear` protocol behavior into a safety boundary; complicates cancellation, caller death, continuation, and stale recovery. | Rejected by the 2026-07-14 amendment. The earlier proposal is retained only as historical implementation reference. |
| Replace `claude` with PTY-only implementation now | Single canonical path. | Throws away existing `--print` cassettes, runner tests, and quota/auto-routing evidence before PTY parity is proven; high risk of regressing primary subscription capacity. | Rejected: parity is unproven; coexistence is cheaper and safer. |
| Drive PTY through tmux | Reuses well-trodden multiplexer patterns. | ADR-002 already rejected this for the core path; would inherit global tmux server state and weaken cassette determinism. | Rejected by ADR-002. |
| Build a generic "TUI harness" abstraction now, parameterized per CLI | One library covers Claude, Codex, Gemini. | Premature: only Claude needs this today; the right shape is unclear until at least two consumers exist; would slow `claude-tui` shipping. | Rejected for now; revisit if a second consumer emerges. |

## Consequences

| Type | Impact |
|------|--------|
| Positive | The design provides a TUI path for subscription-billed Claude capacity without conflating it with the API-shaped `claude` harness identity. |
| Positive | Capability, billing, and lifecycle evidence stays attributable to one harness identity and one accepted invocation. |
| Positive | Normal completion, cancellation, timeout, and caller death share one containment cleanup rule before terminal delivery. |
| Positive | Durable birth identities make stale recovery refuse PID reuse and diagnose containment escape without process-name scanning. |
| Negative | Every accepted `claude-tui` invocation pays PTY startup/auth and teardown cost; no live pool amortizes it. |
| Negative | The codebase carries two Claude adapters plus a shared lifecycle supervisor until a future retirement decision. |
| Negative | Authenticated record cassettes and caller-death/containment fixtures increase the evidence and CI surface. |
| Negative | Capabilities without a documented TUI affordance remain gaps; the adapter cannot substitute batch flags. |
| Neutral | Under the named single-account assumption, both identities may project the same account-scoped durable quota evidence through CONTRACT-004. Durable evidence sharing does not permit live process sharing. |

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| Cleanup misses descendants after normal completion or cancellation | M | H | Establish containment before launch, exercise stubborn-grandchild fixtures, and withhold the terminal fact until the boundary is empty or `HarnessCleanupTimeout` yields typed `cleanup_failed`. |
| Caller death bypasses request-context cancellation | M | H | Couple the trusted supervisor to caller liveness through a control channel; test with a separate caller process. Treat a direct-child parent-death signal only as secondary protection. |
| PID reuse makes stale recovery target an unrelated process | L | H | Persist owner and containment process-birth identities, validate them before signaling, and retain unresolved evidence instead of trusting a PID, PGID, or process name. |
| A harness daemonizes or escapes containment | L | H | Reject breakaway where the platform permits; classify a detected or indeterminate escape as `failed / cleanup_failed / cleanup`; never claim an escaped identity was reaped. |
| Shared Anthropic evidence diverges between harness identities | M | M | Keep store/probe mechanics in the neutral Anthropic seam and expose only CONTRACT-004 `QuotaStatus`/`AccountSnapshot`; conformance tests forbid concrete snapshot and cross-harness imports. |
| TUI or hook/transcript surfaces change between Claude releases | H | M | Pin cassette provenance, replay accepted fixtures, treat parser desynchronization as typed adapter failure, and demote affected capability rows rather than parsing rendered text heuristically. |
| Single-account quota assumption fails for multi-account operators | L | M | Require a follow-up contract to key durable evidence by stable account identity before claiming multi-account support. |
| Per-invocation cold start makes the route uncompetitive | M | M | Benchmark startup, request, and teardown separately. Let routing use measured cost/latency evidence; do not reintroduce a live pool as an optimization. |
| Anthropic changes the subscription/API billing boundary | L | H | Revalidate the billing observation and revise the routing decision explicitly; keep `claude` and `claude-tui` evidence and cost classes distinct. |

## Validation

| Success Metric | Review Trigger |
|----------------|----------------|
| Structural import checks keep `claude-tui` and `claude` as sibling packages with no cross-imports; shared Anthropic code is neutral and exposes no harness-owned concrete snapshot | Either runner imports the other, or service code consumes a runner-specific quota/account type |
| Every accepted `claude-tui` Execute/Continue or live probe acquires one lifecycle lease whose durable birth-identity record exists before the launch gate releases Claude | Claude or a probe subprocess can start before ownership registration, or one accepted invocation reuses another invocation's live boundary |
| Unix fixtures prove dedicated group/session containment plus caller-control-channel liveness; Windows fixtures prove non-inheritable kill-on-job-close containment; unsupported platforms reject before launch | A supported platform launches without a proved containment boundary |
| Normal completion, harness failure, timeout, and caller-signalled cancellation fixtures with a stubborn grandchild emit no public terminal event until cleanup succeeds or `HarnessCleanupTimeout` produces exactly one `cleanup_failed` terminal fact with the primary tuple | A terminal event races ahead of cleanup, cleanup uses the cancelled request context, or a cleanup failure loses the primary tuple |
| A separate-process caller-death fixture proves cleanup begins through supervisor liveness signaling and accepts best-effort persistence without requiring live terminal delivery | Caller death relies only on the Execute context or direct-child parent-death behavior |
| Recovery fixtures persist process-birth identities, refuse a reused PID, continue after per-invocation `cleanup_failed`, and never claim an escaped identity was reaped | Recovery scans by process name, trusts a bare PID/PGID, or startup recovery replaces current-invocation cleanup |
| Static and behavioral tests prove no package-global or service-global live Claude pool, daemon, detached PTY, or `/clear`-based cross-invocation reuse exists | A later invocation can observe or acquire a live process created for an earlier invocation |
| A non-draining caller that cancels its context still triggers cleanup and stream closure without consumer receive progress controlling lifecycle; silent non-consumption alone is never described as observable abandonment | Runner cleanup waits for channel reads or attempts to infer abandonment from absent reads |
| `claude-tui` quota/account behavior conforms to CONTRACT-004 and shares account-scoped durable evidence only through the neutral Anthropic seam | `claude-tui` reads a `claude` concrete snapshot/cache helper or a shared quota probe escapes lifecycle containment |
| Execute cassettes prove the hook/transcript seam; TUI-only probe cassettes prove frame-derived parsing; the environment allowlist and invocation arguments are recorded | Normal final text depends on rendered TUI scraping, or a recording adds batch flags or Fizeau-authored identity variables |
| The capability baseline keeps an independent `claude-tui` row and every current `pass` cites accepted live evidence plus default-CI replay | A cell inherits `claude` evidence or relies only on synthetic fixtures |
| Benchmarks report startup, request, parsing, and teardown deltas for short, long, cancellation, and cleanup scenarios | Auto-routing promotion is proposed without lifecycle conformance and measured deltas against `claude` |
| `AutoRoutingEligible=true` is set only by an explicit follow-up spec change citing current capability, billing, lifecycle, and benchmark evidence | Eligibility changes through implementation drift or historical Gate-E prose alone |

## Concern Impact

- **Resolves subscription-pricing access for Claude prompt execution**:
  Establishes a stable TUI-driven path so routed Claude traffic lands on
  subscribed capacity rather than per-token API pricing.
- **Uses ADR-002 transport**: Extends the direct PTY transport from the quota
  probe into normal prompt execution while leaving process-tree ownership with
  `internal/processlifecycle`.
- **Supports ADR-011**: Lets cost-based routing model subscription-billed
  and API-billed Claude as distinct supply pools.
- **Aligns with CONTRACT-003 lifecycle intent**: Keeps the public terminal fact
  behind per-invocation containment cleanup and preserves caller-death and
  stale-recovery obligations without introducing global process state.
- **Supports primary-harness capability baseline**: Adds a clean second
  primary identity for Claude rather than blurring evidence across transport
  modes.

## References

- [ADR-002 PTY Cassette Transport for Harness Golden Masters](./ADR-002-pty-cassette-transport.md)
- [ADR-004 Terminal Harness Build-vs-Buy Boundary](./ADR-004-terminal-harness-build-vs-buy.md)
- [ADR-011 Cost-Based Routing With Quota Pools](./ADR-011-cost-based-routing-with-quota-pools.md)
- [ADR-014 Universal Harness Interface](./ADR-014-universal-harness-interface.md)
- [CONTRACT-003 FizeauService Service Interface](../contracts/CONTRACT-003-fizeau-service.md)
- [CONTRACT-004 Harness Implementation Contract](../contracts/CONTRACT-004-harness-implementation.md)
- [Primary Harness Capability Baseline](../primary-harness-capability-baseline.md)
- [Harness Golden-Master Integration](../harness-golden-integration.md)
- [Implementation plan: claude-tui fork](../plan-2026-05-14-claude-tui-fork.md)
- `internal/harnesses/types.go` — `Harness` interface, `ExecuteRequest`, `HarnessInfo`, event types
- `internal/harnesses/harness.go` — `HarnessConfig` registry struct
- `internal/harnesses/registry.go` — `builtinHarnesses` map
- `internal/harnesses/claude/runner.go` — existing `--print` subprocess runner
- `internal/harnesses/claude/quota_pty.go` — existing direct-PTY `/usage` probe
- `internal/harnesses/ptyquota/probe.go` — shared PTY probe scaffold
- `internal/pty/session`, `internal/pty/terminal`, `internal/pty/cassette` — direct PTY library
- `internal/processlifecycle` — required shared lifecycle-supervisor boundary;
  this path names the design seam and does not assert current implementation
  status

## Review Checklist

- [x] Context names a specific problem
- [x] Decision statement is actionable
- [x] At least two alternatives were evaluated
- [x] Each alternative has concrete pros and cons
- [x] Selected option's rationale explains why it wins
- [x] Consequences include positive and negative impacts
- [x] Negative consequences have mitigations
- [x] Risks are specific with probability and impact assessments
- [x] Validation section defines review triggers
- [x] Concern impact is complete
- [x] ADR is consistent with governing feature spec and PRD requirements

## Amendment — 2026-07-14: Opaque Route-Owned Continuation

This amendment clarifies how CONTRACT-003 continuation applies to the accepted
`claude-tui` identity. It does not change the PTY transport decision or relax
the per-invocation lifecycle amendment above.

The public continuation reference is the completed parent Fizeau `SessionID`.
Claude-native conversation IDs, resume tokens, transcript identifiers, and
equivalent adapter evidence never cross the public service boundary. They are
not fields in a public request, response, event, terminal projection, or
service-owned session log, and they are never serialized there under a native
or generic metadata key. Only the `claude-tui` route may encode that evidence
inside its private durable evidence store. Fizeau treats the stored value as
opaque and cannot inspect, log, copy into public metadata, or translate it.

This version of continuation is harness-backed. `claude-tui` can opt in through
CONTRACT-004's `ContinuationHarness`; a native-provider route cannot. The
service reports a native-provider parent route as continuation-unsupported and
must not wrap, adapt, or force that provider through `Harness` to make resume
appear available. A provider-native continuation seam requires a later public
contract decision.

The service owns completed-parent lookup and actual-route resolution. It finds
the completed session even when the parent used a per-request session-log
directory, verifies the actual route recorded for that invocation, and asks
the registry for that route's runner. Resume support is determined by asserting
CONTRACT-004's optional `ContinuationHarness` on that registered runner
instance. It is not inferred from the `claude-tui` name, discovered on an
ad-hoc newly constructed runner, or redirected by a child request's routing
preferences.

Within one service process, the runner instance that owns the selected route
also owns access to its continuation evidence. After a service restart, the
same route identity is reconstructed from the completed parent record and is
registered against the same route-private durable evidence store. A different
route, another runner with the same display name, or a runner configured with
a different evidence store cannot resume the parent. The canonical route key
includes the normalized endpoint as well as the harness, provider, model, and
other route discriminators; two `claude-tui` endpoints never share continuation
authority merely because their harness and model names match.

Continuation is two-phase. `ContinuationHarness` first prepares an opaque,
route-bound continuation. Prepare validates the committed private evidence but
creates no child Fizeau session, event stream, lifecycle lease, containment, or
process. If prepare reports unavailable, `require_resume` returns unsupported
and `prefer_resume` may create an ordinary fresh child. Only after successful
prepare does the service create the child and acquire its fresh lifecycle
lease; the prepared continuation's `Start` operation may then spawn Claude
inside the new containment boundary. Evidence rejection or any other failure
after prepare is that child's terminal failure. It never changes disposition
to fresh and never starts a second fallback invocation.

Continuation evidence is durable, never an in-memory-only runner field. A
continuation-capable parent stores evidence atomically under its Fizeau session
ID and canonical route key before acknowledging evidence capture. The service
creates a durable pending completed-session locator before execution and
promotes it only after the terminal log and actual route are durable. Recovery
of a pending locator validates the exact terminal log and route, then consults
the same route-private store; it does not reconstruct evidence from transcript
text. A crash after evidence commit but before locator promotion leaves private
orphan evidence or a recoverable pending locator, never a public native ID. A
crash before evidence commit makes prepare unavailable after recovery.

Every accepted child is a fresh Fizeau session with its own session ID, service
event sequence, lifecycle lease, and containment boundary. This rule applies
to resumed children, `prefer_resume` fresh fallbacks, and `fresh_session`
children. A resumed Claude conversation may use durable native evidence, but
it never reuses the parent's Claude process, PTY, supervisor, process group,
Job Object, or containment lease. Parent cleanup completes before its terminal
fact; child startup establishes a new containment boundary before Claude
receives the continuation request.

The opacity rule is provenance-based, not a value redaction rule. It forbids
service- or harness-derived native evidence from being projected across the
boundary. Caller-supplied prompts or metadata that coincidentally contain the
same bytes retain their ordinary CONTRACT-003 behavior and are not silently
removed.

### Continuation validation additions

- Public-shape and serialization tests tag service- or harness-derived Claude
  IDs and tokens in the route-private store, continue by parent Fizeau
  `SessionID`, and prove that derived evidence is absent from public JSON,
  service events, terminal projections, metadata, and session-log JSONL. A
  control case proves equal caller-supplied metadata is preserved.
- A registry conformance test registers two distinct runner instances with
  the same names but different endpoint-bearing route keys and separate
  evidence stores. Only the runner for the parent's recorded actual route is
  asserted as `ContinuationHarness`; no replacement runner is constructed and
  the other store is never read.
- A restart test completes a parent using a per-request session-log directory,
  exercises crashes on both sides of evidence commit and locator promotion,
  rebuilds the service and route registry, and proves pending-locator recovery
  uses the same route-private durable store. No success path depends on an
  in-memory runner field.
- A two-phase test proves prepare starts no session, lease, containment, event
  stream, or process. It then proves the service creates the child and lease
  before `Start`, and that a post-prepare evidence failure produces one failed
  resumed child with no fresh fallback.
- A lifecycle conformance test continues the same parent twice and proves the
  children have distinct Fizeau session IDs, lifecycle leases, containment
  identities, supervisors, and PTYs. Fresh fallback and `fresh_session` cases
  prove the same new-lease rule; parent process handles cannot appear in any
  child.
