# AGENTS.md — agent coding conventions for AI agents

> Named for Hippolyte Fizeau (1819–1896), the French physicist who measured
> the speed of light with a rotating toothed wheel (1849) and the drag of
> light through moving water (1851). The project treats round-trip timing,
> per-turn measurement, and the difference between a moving medium and its
> rest frame as first-class observability — not afterthoughts. See
> `website/content/docs/about/the-name.md` for the full story.

## Package layout

- Root package (`fizeau`) — public facade: exported service interfaces,
  request/response/event types, constructors, errors, compatibility wrappers,
  and public contract tests. Keep concrete execution, transcript, routing
  health, quota, and aggregation mechanics behind `internal/` packages.
- `internal/serviceimpl/` — concrete service execution and session-log
  dispatch implementation used by the root facade.
- `internal/transcript/` — service-owned transcript, progress, session-log,
  and replay rendering helpers.
- `internal/routehealth/` — process-local route-attempt feedback, cooldown,
  TTL, and provider/model reliability signal implementation.
- `internal/quota/` — provider-level quota state machine and burn-rate
  prediction implementation.
- `internal/routingquality/` — routing-quality ring, aggregation, and
  override-class pivot implementation. Public metric structs stay in root.
- `internal/core/` — reusable agent loop, provider/tool contracts, core
  events, and stream consumption.
- `internal/provider/` — native provider backends and provider registries.
- `internal/harnesses/` — subprocess harness adapters and quota/account
  discovery.
- `internal/compactionctx/` — context helpers for non-persisted prefix token accounting during compaction.
- `internal/safefs/` — centralized wrappers for intentional filesystem reads/writes used to scope `gosec` suppressions.
- `agentcli/` — mountable CLI command tree backed by the public Fizeau
  service facade.
- `cmd/fiz/` — standalone CLI binary; keep it behind the service-boundary
  import allowlist.
- `telemetry/` — runtime telemetry scaffolding for invoke_agent/chat/execute_tool spans.
- `internal/tool/` — built-in tools (read, write, edit, bash).
- `internal/session/` — session log writer, replay renderer, and usage aggregation.
- `internal/config/` — multi-provider YAML config.

## Provider interface pattern

Providers implement `internal/core.Provider` (synchronous). Streaming is opt-in
via `internal/core.StreamingProvider`. The core loop detects streaming at
runtime with a type assertion:

```go
if sp, ok := req.Provider.(StreamingProvider); ok && !req.NoStream {
    resp, err = consumeStream(...)
} else {
    resp, err = req.Provider.Chat(...)
}
```

Do not add streaming logic to `Provider`. Do not change `ChatStream` signatures without updating both providers and `consumeStream`.

## Harness implementation pattern

Harness packages implement `harnesses.Harness` plus only the CONTRACT-004
sub-interfaces they actually support (`QuotaHarness`, `AccountHarness`,
`ModelDiscoveryHarness`). Service-side code consumes those interfaces; it
must not reach back into harness-specific exported helpers once an interface
surface exists.

Per-harness durable snapshot/cache structs stay package-private. Cross-package
consumers use the interface return types from `internal/harnesses/types.go`
instead of reading harness-owned snapshot fields directly.

Harness packages must not import each other. Shared helpers belong in neutral
packages under `internal/harnesses/` or another shared internal package. The
stable contract surface for human and programmatic consumers is
`SupportedLimitIDs()` and `SupportedAliases()`: package docs must mirror those
returns, and drift should fail tests.

## Event emission

Fizeau owns public `ServiceEvent` construction, progress text, transcript
semantics, and session-log projections. Downstream consumers should consume
public events/projections rather than parsing harness-native streams or private
session-log JSONL.

In `internal/core`, events flow through `emitCallback(req.Callback, Event{...})`.
Sequence numbers are tracked by the `seq *int` pointer threaded through the
call chain. Never emit core events outside the loop except inside stream
consumption for delta events.

Defined event types (all must be emitted at the right time):
- `EventSessionStart` — once at start of `Run`
- `EventCompactionStart` — only when the compactor actually ran (produced a result or returned an error); pure no-ops are silent
- `EventCompactionEnd` — paired with Start; emitted together so callbacks stay balanced
- `EventLLMRequest` — before each provider call
- `EventLLMDelta` — per streaming chunk (inside `consumeStream`)
- `EventLLMResponse` — after each provider call
- `EventToolCall` — per tool execution
- `EventOverride` — mirrors service-stream override events for routing-quality metrics tracking (ADR-006 §5)
- `EventRejectedOverride` — mirrors service-stream rejected_override events for routing-quality metrics tracking
- `EventReasoningStall` — emitted when reasoning stall timeout is reached during streaming
- `EventPlanningTurn` — after the planning LLM call completes when `Request.PlanningMode` is enabled
- `EventToolCallLoopPivot` — when the agent detects an identical tool-call loop and has pivot budget available
- `EventSessionEnd` — once at end of `Run`

## Compaction

`internal/compaction.NewCompactor(cfg)` returns a `func(...)` matching the core
request compactor callback. The compactor is stateful (mutex-protected
`previousSummary` and `previousFileOps`). After compaction, the summary message
is placed **last** in the new message list so recent turns remain first in the
retained history.

`IsCompactionSummary` detects summary messages by checking for `<summary>` tags.

## Issue tracker

Use `ddx bead` commands. Common workflow:
```
ddx bead ready --json          # list available work
ddx bead show <id>             # inspect an issue
ddx bead update <id> --claim   # claim before starting
ddx bead close <id>            # close after verification
```

## Test conventions

- Unit tests live next to the code they test.
- Root facade tests that prove external import/source compatibility use
  `package fizeau_test`.
- Internal implementation tests live in their owning internal package.
- Provider packages may use local unit/conformance tests; virtual provider in
  `internal/provider/virtual/` serves as a test double.
- All tests must pass before committing: `go test ./...`

<!-- DDX-AGENTS:START -->
<!-- Managed by ddx init / ddx update. Edit outside these markers. -->

# DDx

This project uses [DDx](https://github.com/DocumentDrivenDX/ddx) for
document-driven development. Use the `ddx` skill for beads, work,
review, agents, and status. DDx and HELIX are runtime plugins installed from
their published marketplaces; do not vendor plugin source into this repository.

Canonical installs:

```bash
claude plugin marketplace add DocumentDrivenDX/ddx-library
claude plugin install ddx@ddx-library --scope user
claude plugin marketplace add DocumentDrivenDX/helix
claude plugin install helix@helix --scope user
codex plugin marketplace add DocumentDrivenDX/helix
codex plugin add helix@helix
ddx install ddx --global
```

The repository ignores `.agents/skills/`, `.claude/skills/`, and
`.ddx/plugins/` so runtime updates cannot dirty the worktree or shadow the
marketplace versions.

## Default Interactive Mode

Broad conversational DDx prompts — queue orientation, planning, review,
guidance folding, spec alignment, and bead breakdown — use
`interactive-steward` / `queue_steward`. Explicit worker commands
(`ddx work`, `ddx try <id>`, "execute bead `<id>`") route to
`bead_execution`. Explicit code/doc edit requests route to
`direct_user_implementation`. Explicit review-only requests route to
`review`.

`DDX_MODE=bead_execution` overrides only the interactive queue-steward default.
It **never** overrides tracker, merge, commit, safety, or verification policy —
those apply in every mode.

### Mutation policy

- **read / plan / fresh-eyes review / fold guidance / align specs** — non-mutating
  by default; no tracker writes, no code edits.
- **Tracker mutation** (e.g. `ddx bead create`, `ddx bead update`) requires an
  explicit durable-output verb: "create a bead", "file this as work",
  "break down into beads".
- **Code edits** require explicit implementation intent ("fix this",
  "implement X") or `bead_execution` mode.

## Files to commit

After modifying any of these paths, stage and commit them:

- `.ddx/beads.jsonl` — work item tracker
- `docs/` — project documentation and artifacts

## Conventions

- Use `ddx bead` for work tracking (not custom issue files).
- Documents with `ddx:` frontmatter are tracked in the document graph.
- Run `ddx doctor` to check environment health.
- Run `ddx doc stale` to find documents needing review.

## Merge Policy

Branches containing `ddx try` or `ddx work` commits
carry a per-attempt execution audit trail:

- `chore: update tracker (execute-bead <TIMESTAMP>)` — attempt heartbeats
- `Merge bead <bead-id> attempt <TIMESTAMP>- into <branch>` — successful lands
- `feat|fix|...: ... [ddx-<id>]` — substantive bead work

Bead records store `closing_commit_sha` pointers into this history. Any
SHA rewrite breaks the trail. **Never squash, rebase, or filter** these
branches. Use only:

- `git merge --ff-only` when the target is a strict ancestor, or
- `git merge --no-ff` when divergence exists

Forbidden on execute-bead branches: `gh pr merge --squash`,
`gh pr merge --rebase`, `git rebase -i` with fixup/squash/drop,
`git filter-branch`, `git filter-repo`, and `git commit --amend` on
any commit already in the trail.
<!-- DDX-AGENTS:END -->

## Review and Verification Discipline

Worker close states (`success`, `already_satisfied`, `review_request_changes`,
`review_block`) are not authoritative. Always run the bead's structural
acceptance criteria locally before accepting a close.

Two failure modes seen repeatedly:

- **gpt-5.5 reviewers BLOCK on environmental test failures** —
  `httptest.NewServer` cannot bind ports in the reviewer sandbox, so any
  test using it fails the reviewer's local re-run even when the code is
  correct. Verify locally; if the structural ACs pass and the reviewer's
  cited failures are env-only (port binding, missing auth, etc.), the BLOCK
  is a false positive. Close manually with a commit noting the false
  positive.
- **The harness's `already_satisfied` heuristic accepts "regression tests
  pass" as the close condition** even when structural ACs aren't met.
  Examples seen: AC promised "internal/routing.Candidate has typed
  FilterReason field at the rejection site," regression tests passed,
  bead closed — but the field had been added without changing the
  rejection-site call. Reopen with `--append-notes` listing the specific
  defects against the original AC.

If structural ACs name specific test functions, those tests must run and
pass. If they name a structural property (no field X, no import Y, file Z
deleted), grep/AST-check the property holds. Don't trust outcome
classifications; trust the property checks.

## Spec Amendment Discipline

Specs represent the desired design of the system. They are the benchmark for
implementation and testing, not a ledger of implementation gaps. If the code
does not yet match the spec, track that gap in beads with concrete acceptance
criteria; do not dilute the spec with caveats about every missing or partially
implemented piece.

Spec changes should describe designs the project intends to build and has a
credible belief it can build. Do not land speculative designs that the project
does not intend to implement, or designs that contradict known constraints
without resolving them. Unknown gaps are expected: discover them during
implementation or review, create beads for them, and close those beads until
the implementation and tests conform to the design.

When amending a spec to describe existing mechanics as implementation
reference, verify the description against the code. Codex flagged this pattern
in this repo's spec history multiple times: a draft amendment claiming
"Provider is a hard pin" when the code only hard-pinned under
`Harness+Provider`; a draft "pre-dispatch HealthCheck re-validation" for
behavior that wasn't implemented. Those belonged either as corrected design
specs plus implementation beads, or as accurate implementation-reference prose.

When a surface is the wrong primary user-facing one but the underlying
mechanics are accurate, **demote rather than rewrite**: add an
"implementation reference" disclaimer paragraph above the existing prose,
linking to the ADR that explains the right primary surface. ADR-006 is the
canonical example — pin precedence stays accurate as implementation
reference; policy-as-cost-vs-time is the new primary user surface.

When in doubt about whether a spec is coherent design or unsupported
speculation, run a code-review pass
(`ddx agent run --harness codex --model gpt-5.5 --prompt review-amendments.md`)
before merging spec changes. Codex with gpt-5.5 has been reliable at
catching code-vs-spec mismatch.

## Bead Sizing and Cross-Repo Triage

**Sizing.** A bead whose scope crosses CLI ↔ service ↔ engine boundaries,
or one whose AC names ≥ 3 prescribed test files with rigor expectations,
is too broad. The worker will correctly refuse to land partial work and
return `already_satisfied` or `no_changes` with an analysis of the split.

When that happens — or proactively, when filing — split into 3-4
deps-chained sub-beads with sharper scope. Each sub-bead should land
cleanly with a focused commit and reviewable AC. Recurring patterns:
service-side change first, then CLI consumption, then test rigor; or
public surface change → wiring → cleanup.

**Cross-repo triage.** When a bug's actual fix surface lives in a sibling
repo, don't let it sit half-owned. The pattern:

1. File an explicit upstream bead in that repo's tracker with a sharp
   principle in the description.
2. Close the local bead as `tracked upstream` via
   `ddx bead update --notes "Tracked upstream as <repo-id> ..."`.
3. Cross-reference the upstream bead from the local close note.

Common cross-repo surfaces from this repo: the `ddx` CLI (execute-loop,
review pipeline, retry-pin propagation), upstream provider repos for
catalog updates.
