---
ddx:
  id: helix.arch
  depends_on:
    - helix.prd
  review:
    self_hash: 076e620580b77517a3f561f5ce842cf1c09e6cef625c13e0a1adb874ae0e19ef
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Architecture — Fizeau

## System Context

Fizeau is a library-first Go execution service. Embedders construct the root
package with `fizeau.New(...)`, submit a `ServiceExecuteRequest`, and consume
typed service events and projections. The `fiz` binary is the first-party
showcase of that public contract, not a second product or execution engine.

```text
┌────────────────┐  ┌────────────────┐  ┌────────────────┐
│ Go embedder    │  │ CI / worker    │  │ fiz CLI        │
│ (in process)   │  │ (in process)   │  │ via agentcli   │
└───────┬────────┘  └───────┬────────┘  └───────┬────────┘
        └────────────────────┼────────────────────┘
                             │ public root package
                    ┌────────▼─────────┐
                    │ FizeauService    │
                    │ Execute/Continue │
                    │ List/*           │
                    │ typed events     │
                    └────────┬─────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
      ┌───────▼────────┐           ┌────────▼────────┐
      │ native path    │           │ subprocess path │
      │ routing + core │           │ harness contract│
      │ providers/tools│           │ PTY / process   │
      └───────┬────────┘           └────────┬────────┘
              └──────────────┬──────────────┘
                             │
                    ┌────────▼─────────┐
                    │ normalized event│
                    │ + session log   │
                    └──────────────────┘
```

Fizeau owns route resolution, provider and harness dispatch, the native agent
loop, event normalization, transcript semantics, and session-log persistence.
Per ADR-017, one `Execute` call attempts one selected route. The caller owns
semantic evaluation and any task-level retry or escalation.

## Package Boundaries

### Public facade: root `fizeau` package

The root package is the only public execution boundary. It owns exported
service interfaces, request/response/event types, constructors, errors,
compatibility wrappers, and public contract tests. It delegates mechanics to
internal packages and must not expose their concrete types.

The executable root inventory is exact, not heuristic:
`TestRootFacadeSourceAllowlist` locks the production facade/adapters, and
`TestRootFacadeTestAllowlist` locks the remaining `package fizeau` composition
and structural tests. The literal lists and ownership rationale live in the
root-facade extraction inventory. External `package fizeau_test` tests exercise
the importable public contract and are not white-box implementation owners.

### Service implementation: `internal/serviceimpl`

`internal/serviceimpl` performs service execution and dispatch for the root
facade. It selects the native or subprocess path and returns service-owned
events. It does not give callers a provider instance or harness-native stream.

### Execution and provider internals

- `internal/core` owns the reusable agent loop and synchronous provider/tool
  contracts. Streaming is the opt-in `StreamingProvider` extension.
- `internal/provider` owns native provider adapters and registries.
- `internal/harnesses` owns subprocess adapters and the CONTRACT-004 capability
  interfaces. Harness packages do not import one another.
- `internal/tool` owns built-in tools used by native execution.

### Service-owned state and projections

- `internal/transcript` owns transcript, progress, replay, and session-log
  rendering semantics.
- `internal/routehealth` owns process-local attempt feedback, cooldown, TTL,
  provider/model reliability signals, aliveness endpoint/evidence projection,
  probe lifecycle concurrency, refresh single-flight state, and dispatch
  feedback transactions. Root service files supply API-neutral configuration,
  catalog, persistence, and harness callbacks only.
- `internal/quota` owns normalized quota state, burn-rate prediction, recovery
  scheduling and signal transitions, plus OpenRouter credit cache,
  single-flight, HTTP/decoding, failure-classification, threshold, TTL, and
  candidate-normalization mechanics. The root retains public quota/burn-rate
  compatibility types and narrow public-config/routing-evidence adapters.
- `internal/routingquality` owns routing-quality aggregation and override-class
  pivots; public metric structs remain in the root package.
- `internal/session` and `internal/sessionlog` support persisted service
  artifacts behind public projections.

Other focused packages under `internal/` support routing, catalogs, discovery,
configuration, compaction, safety, prompting, and runtime signals. They remain
implementation details even when the root facade projects their results.

### Thin CLI consumer: `agentcli` and `cmd/fiz`

`agentcli` provides the mountable Cobra command tree. Execution paths parse CLI
input, construct public service requests, consume typed public events, and
render output. Some non-execution catalog, discovery, and configuration
commands still use a narrow, test-enforced internal-package allowlist while
equivalent public projections are completed; this is transitional
implementation reference, not permission to bypass the service for execution.
The concrete transitional list is `approvedProductionInternalImports` in
`agentcli/service_boundary_test.go`.
`cmd/fiz` supplies version/process wiring and owns process termination. Neither
package constructs providers, invokes `internal/core`, implements failover,
parses harness-native output, or synthesizes session lifecycle records.

## Package View

```text
fizeau/
├── *.go                         # public facade and contract types
├── agentcli/                    # mountable Cobra consumer
├── cmd/
│   └── fiz/                     # thin standalone binary
├── telemetry/                   # public telemetry scaffolding
└── internal/
    ├── serviceimpl/             # execution and dispatch implementation
    ├── transcript/              # public-event/transcript semantics
    ├── routehealth/             # route reliability, probes, lifecycle, feedback
    ├── quota/                   # quota normalization and prediction
    ├── routingquality/          # routing-quality aggregation
    ├── core/                    # reusable native agent loop
    ├── provider/                # native provider backends
    ├── harnesses/               # subprocess harness adapters/contracts
    ├── tool/                    # built-in native tools
    ├── session/ + sessionlog/   # service artifact support
    └── ...                      # routing, catalog, config, compaction, etc.
```

There is no public `agent` package and no `cmd/agent` execution path.

## Execution Flow

### Native providers

```text
caller / agentcli
  -> fizeau.New(...).Execute(req)
  -> internal/serviceimpl dispatch
  -> service route resolution and provider construction
  -> internal/core loop
  -> tools, compaction, telemetry
  -> normalized ServiceEvent stream and service-owned session log
```

### Subprocess harnesses

```text
caller / agentcli
  -> fizeau.New(...).Execute(req)
  -> internal/serviceimpl dispatch
  -> CONTRACT-004 harness selection
  -> PTY / subprocess execution
  -> Fizeau event and transcript normalization
  -> normalized ServiceEvent stream and service-owned session log
```

Both paths end at the same public event and session-log contract. Downstream
consumers never need harness-native stream parsing or private JSONL schemas.

### Session continuation

```text
caller / agentcli
  -> FizeauService.Continue(parent Fizeau SessionID)
  -> stable completed-session locator and exact endpoint-aware route key
  -> authoritative registered route instance
  -> optional CONTRACT-004 continuation preparation (no child or spawn)
  -> new child Fizeau SessionID and fresh lifecycle lease
  -> prepared continuation start
  -> normalized events and public lineage without native references
```

Continuation never changes the public identifier boundary: callers supply a
completed Fizeau session ID, while a supporting harness privately resolves any
route-native conversation evidence. A resumed continuation stays on the
parent's exact terminal route and uses that route's registered instance. After
restart, the canonical replacement for the same route may resume from durable
private evidence. A fresh-policy fallback uses ordinary routing. Both outcomes
are new invocations with new containment and lifecycle leases; live processes,
PTYs, and leases never cross an invocation boundary. This contract version
supports resume only through subprocess routes implementing CONTRACT-004;
native-provider routes report resume unsupported and are not forced through the
harness abstraction.

## Escalation Ownership

Fizeau dispatches exactly one selected route per `Execute` call and reports the
selection, rejected candidates, timing, cost, quota, and terminal outcome.
Fizeau may perform transport-level retries that are part of one provider call,
but it does not interpret task semantics and choose a stronger route. A caller
that concludes the result is inadequate creates a new request with revised
intent or explicit constraints. This keeps retry budgets and escalation policy
with the component that understands the task, as required by ADR-017.

## Architectural Rules

1. The root `fizeau` package is the sole public execution boundary.
2. Concrete execution, transcript, routing-health, quota, and aggregation
   mechanics remain behind `internal/`.
3. Fizeau owns provider/harness dispatch, event normalization, and session-log
   persistence for both native and subprocess paths.
4. `agentcli` and `cmd/fiz` consume the public service for execution. New
   inspection behavior uses public projections; the existing non-execution
   allowlist is transitional and may only shrink without an architecture
   amendment.
5. Provider streaming remains an optional `internal/core.StreamingProvider`
   capability; it is not added to the synchronous `Provider` interface.
6. Harness implementations expose only the CONTRACT-004 sub-interfaces they
   support. Service code consumes those interfaces rather than harness-specific
   snapshot fields or helpers.
7. Callers own semantic retry and escalation; Fizeau reports evidence for one
   route attempt.
8. New consumer-visible execution or status behavior is specified in
   CONTRACT-003 before a consumer reaches into an internal package.
9. Public continuation carries only a completed Fizeau session ID. Native
   conversation references remain owned and stored by the implementing route,
   and every child continuation acquires a fresh lifecycle lease.

## Caching

Native provider prompt caching is an adapter concern behind the service
boundary.

- **Prefix order invariant.** Where a provider uses prefix caching, tools,
  system instructions, and retained history keep a deterministic order ahead
  of the turn-specific user message.
- **Two-marker placement.** The Anthropic adapter places explicit ephemeral
  breakpoints at the end of the tool definitions and at the end of the system
  blocks, preserving the two stable reusable prefixes.
- **Compaction caveat.** Compaction intentionally changes conversation bytes
  and may cause the next turn to repopulate the cache.
- **Tool-mutation caveat.** Tool definitions must remain deterministic across
  turns; per-turn state belongs in tool input, not mutable descriptions.

`ServiceExecuteRequest.CachePolicy` is the public request control. Empty and
`default` use normal adapter caching; `off` disables explicit cache markers and
preserves the distinction between a known zero cache amount and unavailable
cache accounting.

## Key Design Decisions

| Decision | Choice | Authority |
|---|---|---|
| Product surface | embeddable root `fizeau` service facade | Product vision, ADR-008, CONTRACT-003 |
| Native execution | service-owned dispatch into `internal/core` | ADR-008, CONTRACT-003 |
| Harness execution | service-owned CONTRACT-004 adapters | ADR-014, CONTRACT-004 |
| Session continuation | exact terminal route, optional private capability, new child session and lease | CONTRACT-003, CONTRACT-004, ADR-013, ADR-014 |
| Event/transcript ownership | Fizeau normalization for every execution path | ADR-008, CONTRACT-003 |
| Escalation | caller-owned semantic retry, one Fizeau route per request | ADR-017 |
| CLI | mountable Cobra tree in `agentcli`; thin `cmd/fiz` wrapper | SD-002, CONTRACT-003 |
