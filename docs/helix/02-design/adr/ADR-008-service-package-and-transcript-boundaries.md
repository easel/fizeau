---
ddx:
  id: ADR-008
  depends_on:
    - FEAT-001
    - FEAT-005
    - FEAT-006
  review:
    self_hash: 3f36c9ae5997a72d2575876d739d110a7dd6950456a517695ed0d0cd8e118db3
    deps:
      FEAT-001: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
      FEAT-005: 0a963abf9f30cb7551a30302fa853525e417f03cd1611603aec221d0159998e0
      FEAT-006: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
    reviewed_at: "2026-07-15T05:49:18Z"
---
# ADR-008: Service Package and Transcript Boundaries

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-05 | Accepted | Fizeau maintainers | `CONTRACT-003`, `ADR-005`, `ADR-006` | High |

## Context

The repository root is the public Go package:

```go
github.com/easel/fizeau
```

That package is currently carrying more than the public contract. It contains
the exported service API, concrete service execution, routing/status support,
provider quota state, transcript/progress rendering, session-log mechanics,
test seams, and a large test surface. This makes the package hard to reason
about and makes downstream consumers more likely to treat implementation
details as public behavior.

The same boundary problem exists at runtime. Fizeau owns native providers and
subprocess harness adapters, but downstream consumers such as DDx have grown
compensating code that parses harness-native streams, tails Fizeau session-log
files, renders human progress lines, and infers runtime status from internal
telemetry. That duplicates Fizeau concerns and creates drift between harnesses.

Pure no-op compactions are already silent in the agent loop: when the compactor
returns the no-op signal, Fizeau emits no compaction start/end event. Any
downstream policy that watches for repeated no-op compaction telemetry is
therefore observing a stale or invalid signal.

## Decision

### 1. Root package is the public facade

The root `package fizeau` is the compatibility boundary for external
consumers. It should contain exported service interfaces, request/response
types, event types, constructors, errors, compatibility aliases when needed,
and public contract tests.

Concrete service implementation code should move behind internal packages.
The likely end state is:

```text
internal/serviceimpl
internal/transcript
internal/routehealth
internal/quota
```

This follows the official Go module layout guidance: an importable library
package can live at the module root, commands live under `cmd/`, and private
implementation packages live under `internal/`. This is a package-boundary
change, not a module split. The module import path stays stable as
`github.com/easel/fizeau`.

Do not create a nested `fizeau/` package under this module. With the existing
module path, that would either create the stutter import path
`github.com/easel/fizeau/fizeau` or force the repository into a
nested-module/workspace shape. Neither tradeoff is justified by this decision.
Introducing additional `go.mod` modules is deferred until the internal
boundaries have proven stable and a real multi-module need exists.

### 2. Public service types stay public

Public contract types should remain in the root package unless there is a
separate, deliberate decision to create a public subpackage. Root aliases to
an `internal` package are avoided because they make API identity and
documentation harder to understand and can leak unimportable implementation
paths into tooling.

Internal packages may depend on root public types only through narrow seams
that avoid import cycles. If an implementation move exposes a cycle, the fix
is to introduce smaller implementation-local interfaces or adapter functions,
not to make the root package absorb implementation code again.

For route health, `internal/routehealth` owns endpoint/evidence normalization,
probe signal reads, startup and route-time probing, the periodic refresh
lifecycle, refresh single-flight state, route-attempt/final transactions,
dispatch-feedback ordering, endpoint URL selection, and probe-store mutation.
It accepts API-neutral projections and callbacks; it does not import the root
package or `internal/serviceimpl`. The root `service_aliveness.go` retains the
public `ProviderAlivenessProber` identity and ServiceConfig/ServiceOptions
projection, while `service_dispatch_feedback.go` retains the public
`RecordRouteAttempt` method, harness/public conversions, catalog-key callbacks,
and narrow store/persistence adapters. Diagnostic reachability classification
remains in `internal/serviceimpl` and is injected into the route-health
transaction; stable failure-class extraction lives in `internal/routehealth`.

This boundary is executable, not conventional: the root structural analyzer
locks the exact adapters and protected internal entrypoint sites, rejects
aliases and wrappers around those protected symbols, forbids the named legacy
mechanic declarations and root-owned probe primitives, and permits lexical
local shadows and unrelated package selectors. Newly named code that does not
cross a locked declaration, entrypoint, import, timer, or concurrency boundary
still requires ordinary review. `internal/routehealth` separately proves that
its dependency graph cannot reach the root package.

### 3. Fizeau owns transcript and progress semantics

Fizeau is the owner of:

- harness-native stream parsing;
- native-provider event normalization;
- public service event construction;
- tool call/result pairing;
- payload-size accounting;
- compact progress/status line rendering;
- session-log lifecycle and replay projections.

The canonical human-readable live line is the Fizeau-owned progress payload,
currently `ServiceProgressData.Message`. It must remain bounded and suitable
for downstream display without downstream parsing of prompts, tool input, tool
output, or harness-native JSON.

Harnesses may differ internally, but once an interaction crosses the
`FizeauService` boundary, downstream consumers should see the same public event
vocabulary and required fields.

### 4. Downstream consumers are pass-through clients

DDx and other consumers may:

- call public `FizeauService` methods;
- forward or store public `ServiceEvent` values;
- display Fizeau-provided progress text;
- link or copy Fizeau-owned session artifacts;
- decode final/routing fields needed for their own bookkeeping.

They must not:

- parse harness-native streams such as Claude `stream-json`;
- synthesize Fizeau internal session events;
- tail and render Fizeau session-log JSONL as policy input;
- reconstruct tool transcripts for routing/retry decisions;
- infer runtime failure from private or historical Fizeau telemetry.

DDx still owns its own bead/worker lifecycle events: bead claimed, attempt
started, review running, landed, preserved, failed, and similar outer workflow
state. Those events are separate from Fizeau's agent transcript.

### 5. No-op compaction is silent

Pure no-op compaction probes are not progress and are not telemetry for
downstream policy. They remain silent at the public service boundary. Real
compaction work and real compaction failures remain Fizeau runtime concerns.

Any stale API or downstream code that models a no-op compaction stall breaker
must be removed in follow-on implementation beads.

## Consequences

- The root package becomes smaller and easier to audit for public API
  compatibility.
- Implementation tests move with the packages that own the behavior.
- Transcript rendering becomes consistent across native, Claude, Codex,
  Gemini, Pi, and Opencode paths.
- DDx changes are blocked until Fizeau ships the public surface needed for
  pass-through consumption.
- Some refactors will be mechanically large; each implementation bead must
  keep source compatibility and run `go test ./...`.

## Implementation Implications

1. Delete stale no-op compaction stall policy from Fizeau.
2. Move concrete service implementation behind `internal/serviceimpl`.
3. Introduce `internal/transcript` for public event/progress construction.
4. Migrate native and subprocess harness event paths onto the transcript
   package.
5. Add conformance tests across supported execution paths.
6. Keep route-health lifecycle, state transitions, attempt/final transactions,
   and dispatch-feedback mechanics in `internal/routehealth`; root retains only
   public identities and neutral configuration/callback adapters.
7. Audit the root package with import-path and lexical-scope-aware structural
   tests, keeping public/composition integrations at root and pure white-box
   mechanics beside their internal owners.
8. Tag a Fizeau release before downstream DDx changes consume the new surface.

## Non-Goals

- No split into multiple Go modules in this decision.
- No public import path change.
- No change to routing policy or provider scoring.
- No removal of real compaction failure handling.
- No DDx implementation changes in this repository.
