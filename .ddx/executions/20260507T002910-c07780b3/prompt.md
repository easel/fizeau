<bead-review>
  <bead id="fizeau-1e090b7d" iter=1>
    <title>Rename native harness identity in code and CLI</title>
    <description>
Rename the native Fizeau harness identity from agent to fiz in service code, harness registration, route-status/list-models outputs, and CLI help strings. Remove native agent aliasing while preserving non-native historical references. Keep the implementation scoped to runtime behavior and any direct tests needed to prove it.
    </description>
    <acceptance>
1. Native harness registration and service routing use fiz as the public/native harness name. 2. Explicit Harness: "fiz" executes through the native loop. 3. Explicit Harness: "agent" no longer selects the native loop and returns the existing typed incompatible/unknown harness error. 4. CLI/list-models/route-status surfaces show fiz for the native harness.
    </acceptance>
    <labels>phase:build, area:contract, area:harness, area:rename, kind:breaking, downstream:ddx</labels>
  </bead>

  <governing>
    <ref id="CONTRACT-003" path="docs/helix/02-design/contracts/CONTRACT-003-fizeau-service.md" title="CONTRACT-003: FizeauService Service Interface">
      <content>
<untrusted-data>
---
ddx:
  id: CONTRACT-003
  depends_on:
    - helix.prd
---
# CONTRACT-003: FizeauService Service Interface

**Status:** Draft
**Owner:** Fizeau maintainers
**Replaces:** CONTRACT-002-ddx-harness-interface (deleted; entanglement-era contract)

## Purpose

This contract defines the public Go surface of the `fiz` module. The
service surface is the `FizeauService` interface plus the input/output struct types
referenced by its methods. The only public CLI embedding surface is the
`agentcli` mountable command tree described below. Anything else is internal
and may change without notice.

Consumers (DDx CLI, future HELIX/Dun integrations, the standalone `fiz`
binary, anything else) interact only through this surface. **They do not import
agent internal packages.** When new behavior is needed, consumers file an issue or
PR against this contract; agent maintainers decide whether the surface grows.

The native in-process Fizeau harness identity is `fiz`. The term `agent` is
reserved for real external AI-agent concepts or historical artifact names; it is
not a public alias for the native Fizeau route.

Implementation ownership and package boundaries are governed by
[ADR-008: Service Package and Transcript Boundaries](../adr/ADR-008-service-package-and-transcript-boundaries.md).
That ADR makes this contract the public facade, assigns transcript/progress
semantics to Fizeau, and treats downstream tools such as DDx as pass-through
consumers of public service events rather than owners of harness-native
transcript parsing.

## Module value proposition

`fiz` is the one stop shop for automatically routed one-shot
noninteractive agentic prompts. Two roles:

1. **Direct first-class agent** over native model providers (LM Studio, OpenRouter,
   Anthropic, etc.). Designed to be the high-performance choice for batch
   noninteractive tasks.
2. **Wrapper around other agents** — subprocess harnesses (claude, codex,
   opencode, pi, gemini) — used when their interactive features, vendor billing,
   specific capabilities, or comparison/fallback routing wants them in the
   candidate pool.

A single internal routing engine ranks `(harness, provider source, endpoint,
model)` candidates uniformly. Consumers see one surface; the internals decide
how to dispatch.

## Interface

```go
package agentlib

import (
    "context"
    "io"
    "time"
    "encoding/json"
)

// FizeauService is the entire public Go surface of the fiz module.
type FizeauService interface {
    // Execute runs an agent task in-process; emits Events on the channel until
    // the task terminates (channel closes). The final event (type=final) carries
    // status, normalized final_text, usage, cost, session log path, optional
    // message history, and routing_actual (the resolved fallback chain that fired).
    Execute(ctx context.Context, req ExecuteRequest) (<-chan Event, error)

    // TailSessionLog streams events from a previously-started or in-progress
    // session by ID. Used by clients (DDx workers, UIs) to subscribe to a run
    // started elsewhere — e.g., a server-managed worker that the CLI wants to
    // follow. Multi-subscriber-safe.
    TailSessionLog(ctx context.Context, sessionID string) (<-chan Event, error)

    // ListHarnesses returns metadata for every registered harness (native and
    // subprocess). HarnessInfo includes install state, supported permission
    // levels, supported reasoning values, and live quota when applicable.
    ListHarnesses(ctx context.Context) ([]HarnessInfo, error)

    // ListProviders returns providers known to the native `fiz` harness with
    // live status, configured-default markers, and cooldown state.
    ListProviders(ctx context.Context) ([]ProviderInfo, error)

    // ListModels returns models matching the filter. Implemented fields include
    // cost, perf signals, capabilities, context length, rank position, provider
    // source/type, and endpoint identity. Planned power-routing fields are
    // listed on ModelInfo below and land through agent-da67ebbe.
    ListModels(ctx context.Context, filter ModelFilter) ([]ModelInfo, error)

    // ListProfiles, ResolveProfile, and ProfileAliases describe named power
    // policies. A profile is shorthand for a point or bounded range on the
    // model power curve, plus optional ranking preferences; it is not a closed
    // list of concrete models.
    ListProfiles(ctx context.Context) ([]ProfileInfo, error)
    ResolveProfile(ctx context.Context, name string) (*ResolvedProfile, error)
    ProfileAliases(ctx context.Context) (map[string]string, error)

    // HealthCheck triggers a fresh probe and updates internal state.
    // Target.Type is "harness" or "provider".
    HealthCheck(ctx context.Context, target HealthTarget) error

    // ResolveRoute resolves a single under-specified request to a concrete
    // (Harness, ProviderSource/Endpoint, Model). The returned RouteDecision is
    // informational: operator dashboards, route-status displays, and debug
    // surfaces. RouteDecision includes the selected endpoint, sticky-lease
    // evidence (status only; never the raw key), and endpoint utilization
    // evidence when available so operators can explain why a worker stayed
    // pinned or why a fresh key picked a specific endpoint. It is not
    // re-injectable into Execute. Execute always re-resolves on its own inputs
    // (idempotent for the same caller intent, modulo health changes which is
    // the intended behavior).
    ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error)

    // RecordRouteAttempt records availability feedback about externally routed
    // work. Non-success statuses create a same-process cooldown keyed by
    // harness/provider/model/endpoint; success clears matching active failures.
    RecordRouteAttempt(ctx context.Context, attempt RouteAttempt) error

    // RouteStatus returns global routing state across live provider/model
    // candidates: cooldowns, recent decisions, sticky assignment status,
    // selected endpoint, and observation-derived per-(provider source,
    // endpoint, model) latency / utilization evidence.
    // Distinct from per-request ResolveRoute — this is the read-only operator
    // dashboard view.
    RouteStatus(ctx context.Context) (*RouteStatusReport, error)

    // UsageReport aggregates token, cost, and reliability totals across the
    // service-owned session-log directory. CLI subcommands such as
    // `fiz usage` consume this projection rather than re-reading
    // session-log JSONL records.
    UsageReport(ctx context.Context, opts UsageReportOptions) (*UsageReport, error)

    // ListSessionLogs returns the historical session-log entries known to the
    // service (session id, mod time, size). Consumers display these without
    // touching the on-disk session-log layout.
    ListSessionLogs(ctx context.Context) ([]SessionLogEntry, error)

    // WriteSessionLog renders every event in the named session log to w as
    // indented JSON, one event per object. The format is service-owned;
    // consumers do not parse it back into private session-log structs.
    WriteSessionLog(ctx context.Context, sessionID string, w io.Writer) error

    // ReplaySession renders a human-readable conversation transcript for the
    // named session log onto w. Used by `fiz replay <id>`.
    ReplaySession(ctx context.Context, sessionID string, w io.Writer) error
}

// ValidateUsageSince returns nil when spec is a usage window value accepted by
// UsageReport. CLI subcommands call this to surface validation errors with
// exit-code 2 before invoking the service.
func ValidateUsageSince(spec string) error

// New constructs a FizeauService. Options is intentionally minimal.
func New(opts Options) (FizeauService, error)
```

**Sixteen methods total.** `Execute` is the primary verb; `TailSessionLog`,
`ListHarnesses`, `ListProviders`, `ListModels`, `ListProfiles`,
`ResolveProfile`, `ProfileAliases`, `HealthCheck`, `ResolveRoute`,
`RecordRouteAttempt`, and `RouteStatus` are the supporting routing/status
surface; `UsageReport`, `ListSessionLogs`, `WriteSessionLog`, and
`ReplaySession` are the historical session-log projection used by `fiz log`,
`replay`, and `usage`.

## Mountable CLI Surface

The standalone `fiz` binary and embedding callers use the same public
Cobra command tree from package `agentcli`:

```go
package agentcli

type MountOption func(...)

func MountCLI(opts ...MountOption) *cobra.Command
func WithUse(use string) MountOption
func WithShort(short string) MountOption
func WithLong(long string) MountOption
func WithStdin(stdin io.Reader) MountOption
func WithStdout(stdout io.Writer) MountOption
func WithStderr(stderr io.Writer) MountOption
func WithVersion(version, buildTime, gitCommit string) MountOption

type ExitError struct { Code int }
func ExitCode(err error) (int, bool)

type Options struct { Args []string; Stdin io.Reader; Stdout io.Writer; Stderr io.Writer; Version, BuildTime, GitCommit string }
func Run(opts Options) int
```

Implementation references: `agentcli/mount.go` defines `MountCLI`, mount
options, `ExitError`, and `ExitCode`; `agentcli/run.go` defines the non-exiting
`Run` runner; `cmd/agent/main.go` mounts the command and is the only
`fiz` command path that converts returned errors into `os.Exit`.

Contract guarantees for embedders:

- `MountCLI` returns a fresh, unattached `*cobra.Command` on each call.
- `WithUse`, `WithShort`, and `WithLong` customize help metadata without
  changing execution behavior.
- `WithStdin`, `WithStdout`, and `WithStderr` inject the streams used by the
  mounted root and delegated runner. Native subcommands should preserve the
  standalone command behavior; when a legacy implementation still writes to
  process streams internally, that is an implementation debt, not a license for
  embedding callers to parse private state.
- `WithVersion` supplies the version/build metadata printed by `--version`.
- Mounted execution never calls `os.Exit`; non-zero CLI outcomes are returned
  as `*ExitError`, and callers may use `ExitCode` to recover the process-style
  code. The standalone binary owns process termination in `cmd/agent/main.go`.
- Existing command paths are registered on the Cobra tree. Compatibility
  delegation may remain for the default prompt path and explicitly delegated
  commands, but migrated subcommands must not require callers to inject the
  `--` passthrough sentinel.

### Power-Routing Implementation Status

This contract distinguishes implemented surface from target power-routing
surface:

- Implemented today: `ListModels`, provider/endpoint identity, route decisions,
  route-attempt feedback, prompt-feature gates, and catalog reference methods.
- Planned through `agent-79e194aa`: profile power policy, `MinPower`, and
  `MaxPower` on `ServiceExecuteRequest`, `RouteRequest`, and `ModelFilter`.
- Planned through `agent-da67ebbe`: `ModelInfo` inventory fields for power,
  provider/deployment class or equivalent power provenance, availability,
  auto-routable, and exact-pin-only.
- Planned through `agent-7d537e4a`, `agent-005a0a30`, and `agent-de968c76`:
  power filtering, utility scoring, no-power best lowest-cost default, and
  statement-backed routing invariant tests.

## Public types

```go
type Options struct {
    ConfigPath string    // optional override; default $XDG_CONFIG_HOME/fizeau/config.yaml
    Logger     io.Writer // optional; agent writes structured session logs internally regardless

    // SessionLogDir overrides the directory used by the historical session-log
    // projections (UsageReport, ListSessionLogs, WriteSessionLog,
    // ReplaySession). Empty falls back to ServiceConfig.WorkDir() +
    // "/.fizeau/sessions". Per-Execute requests still set their own
    // ExecuteRequest.SessionLogDir.
    SessionLogDir string

    // Test-only injection seams. Each MUST be nil in production builds —
    // enforced by build tag `//go:build testseam`. Forming an Options with
    // any of these set in a non-test build is a compile error. Four seams
    // exist because consumers today inject at four different layers.
    FakeProvider            *FakeProvider
    PromptAssertionHook     PromptAssertionHook
    CompactionAssertionHook CompactionAssertionHook
    ToolWiringHook          ToolWiringHook
}

// Reasoning is the single public model-reasoning control. It is one scalar:
// named values such as auto/off/low/medium/high/minimal/xhigh/max, or numeric
// strings produced by ReasoningTokens for explicit token budgets.
//
// The root package may re-export this type, constants, and helper from a shared
// leaf package such as internal/reasoning. Internal packages such as
// internal/modelcatalog import the leaf package, not root agent, to avoid
// root-agent/internal-modelcatalog import cycles.
type Reasoning string

const (
    ReasoningAuto   Reasoning = "auto"
    ReasoningOff    Reasoning = "off"
    ReasoningLow    Reasoning = "low"
    ReasoningMedium Reasoning = "medium"
    ReasoningHigh   Reasoning = "high"
    ReasoningMinimal Reasoning = "minimal" // accepted only when advertised
    ReasoningXHigh  Reasoning = "xhigh"    // normalizes x-high
    ReasoningMax    Reasoning = "max"      // requires known model/provider max
)

func ReasoningTokens(n int) Reasoning

// Tool is the native agent tool interface. ExecuteRequest.Tools is only used
// by the in-process `fiz` harness; subprocess harnesses own their tool
// policy internally.
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    Execute(ctx context.Context, params json.RawMessage) (string, error)
    Parallel() bool
}

// Routing strength is power-owned. Callers set Profile, MinPower/MaxPower, or
// pin Model, Provider, or Harness directly. Power is numeric from 1..10; 0 means
// no bound when used on request filters and unknown/exact-pin-only when used on
// model inventory rows.

type ExecuteRequest struct {
    Prompt       string  // required
    SystemPrompt string  // optional; agent supplies a sane default if empty
    Model        string  // optional; resolved via ResolveRoute if empty
    Provider     string  // optional hard pin; empty = router decides
    Harness      string  // optional preference (hard); empty = router decides
    Profile      string  // optional named power policy; empty = no profile policy
    MinPower     int     // PLANNED agent-79e194aa; optional lower bound; 0 = no lower bound
    MaxPower     int     // PLANNED agent-79e194aa; optional upper bound; 0 = no upper bound
    ModelRef     string  // optional alias from the catalog; concrete refs are exact
    // Sampling controls. All five sampler fields use pointer types so that
    // nil means "unset — defer to lower layers / server defaults" and any
    // concrete value (including 0) means "send this on the wire". See ADR-007
    // for the full resolution chain (catalog sampling bundles → providers.*
    // .sampling → CLI). Direct callers may set these fields explicitly; the
    // CLI populates them from internal/sampling.Resolve before constructing
    // the request.
    Temperature       *float32 // model sampling temperature; nil = unset
    TopP              *float64 // nucleus sampling cutoff; nil = unset
    TopK              *int     // top-k sampling cutoff; nil = unset
    MinP              *float64 // min-p sampling cutoff; nil = unset
    RepetitionPenalty *float64 // repetition penalty (>1.0 penalizes); nil = unset
    Seed              *int64   // sampling seed; nil = unset/provider chooses
    Reasoning    Reasoning // optional; auto|off|low|medium|high|minimal|xhigh|max|<tokens>
    Permissions  string  // "safe" | "supervised" | "unrestricted"; default "safe"
    WorkDir      string  // required when the chosen harness uses tools
    Tools        []Tool  // optional native-agent override; nil = built-in tools
    ToolPreset   string  // optional native built-in selector; "benchmark" excludes task

    // Auto-selection inputs. When the caller pins no concrete model/provider,
    // Execute uses these to filter candidates by capability before scoring.
    // Explicit pins always win. Defaults: 0 / false skip the corresponding
    // filter. See ADR-005.
    EstimatedPromptTokens int  // when >0, filter candidates whose context window cannot hold the prompt
    RequiresTools         bool // when true, filter providers whose SupportsTools() is false

    // CachePolicy is the public prompt-caching opt-out. Valid values:
    //   ""        — same as "default"; per-provider default caching.
    //   "default" — explicit default; per-provider default caching.
    //   "off"     — disable caching for this request.
    // Any other value is rejected at the service boundary before dispatch.
    CachePolicy string

    // Three independent timeout knobs:
    //   Timeout         — wall-clock cap; the request fails after this duration
    //                     regardless of activity. 0 = no cap.
    //   IdleTimeout     — streaming-quiet cap; the request fails after this
    //                     duration of no events from the model. 0 = use harness
    //                     default (typically 60s).
    //   ProviderTimeout — per-HTTP-request cap to the provider; longer requests
    //                     fail the attempted route. Agent does not retry another
    //                     route. 0 = use provider default.
    Timeout         time.Duration
    IdleTimeout     time.Duration
    ProviderTimeout time.Duration

    // Optional stall policy. When non-nil, agent enforces and ends execution
    // with Status="stalled" if any limit hits. The agent also derives an
    // implicit MaxIterations ceiling from StallPolicy (typically 2× the
    // ReadOnly limit) — caller does not configure MaxIterations directly.
    StallPolicy *StallPolicy

    // SessionLogDir overrides the default session-log directory for this
    // request. Used by execute-bead to direct logs into a per-bundle evidence
    // directory. Empty = use Options.ConfigPath-derived default.
    SessionLogDir string

    // Metadata is bidirectional: echoed back in every Event via Event.Metadata,
    // AND stamped onto every line written to the session log (e.g., bead_id,
    // attempt_id) so external log consumers can correlate.
    //
    // Reserved cross-tool keys:
    //   produces_artifact — caller-declared artifact path or URI produced by the task
    //   media_type        — MIME/media type for produces_artifact
    //   role              — alias for top-level Role; top-level wins on collision
    //   correlation_id    — alias for top-level CorrelationID; top-level wins on collision
    //
    // The service currently treats metadata as an opaque string map and echoes
    // it; these keys are reserved so DDx/HELIX consumers can agree on artifact
    // semantics without parsing model output. Implemented echo path:
    // service.go ExecuteRequest.Metadata plus service_events.go Event.Metadata.
    //
    // When the caller sets a non-empty top-level Role or CorrelationID AND
    // the same reserved key in Metadata, the top-level field wins and a
    // MetadataKeyCollision warning is emitted on the final event
    // (ServiceFinalWarning.Code = "metadata_key_collision"). Future versions
    // may reject duplicate values outright.
    Metadata map[string]string

    // Role tags the kind of work this call performs, e.g. "implementer",
    // "reviewer", "decomposer", "summarizer". Role is echoed into the
    // routing_decision and final event Metadata, plus the session-log header.
    // It is not a hard constraint and does not affect routing eligibility.
    // Empty means unset.
    //
    // Normalization: lowercased, alphanumeric and hyphen only, max 64
    // characters. Invalid values are rejected pre-dispatch with a typed
    // *RoleNormalizationError (see routing_errors.go / role_correlation.go).
    Role string

    // CorrelationID joins calls that share work context — for example
    // "bead_123:attempt_4" — so reviewer + implementer + retry attempts
    // can be joined in logs and aggregations. It is echoed into
    // routing_decision and final event Metadata, plus the session-log header.
    // When validated and non-empty, it is also the sticky route key used to bias
    // related requests toward the same server instance. It is not a hard
    // constraint and does not affect eligibility. Empty means unset.
    //
    // Normalization: printable ASCII only, no control characters, no
    // whitespace except hyphen / colon / underscore (which are part of
    // the printable range), max 256 characters. Invalid values are
    // rejected pre-dispatch with a typed *CorrelationIDNormalizationError.
    CorrelationID string
}

type StallPolicy struct {
    MaxReadOnlyToolIterations int // 0 = disabled
}

### Selection precedence

> **Pin precedence is implementation reference, not the primary user surface.**
> Pinning `Harness`, `Provider`, or `Model` for a given prompt class indicates
> auto-routing is mis-deciding for that class — file a routing-quality issue.
> The rules below describe how the engine resolves conflicts when overrides
> are used; they are not how callers should normally drive `Execute`. See
> ADR-006 for override-tracking semantics.

When multiple selection inputs are set on `ExecuteRequest`, they resolve in
the following order, most-specific first. The list is also the precedence
the routing engine enforces:

1. **`Harness`** — hard pin. Routing never substitutes a different harness.
2. **`Provider`** — hard provider-source or endpoint pin, depending on the
   request surface. Routing never substitutes a different provider source or
   endpoint. A value that cannot be resolved fails pre-dispatch with a
   configuration error; no auto-substitution happens.
3. **`Model`** — hard pin. The request's `Model` field overrides any default
   model declared in the matching provider's config.
4. **`ModelRef`** — catalog reference. A ref to a concrete model is an exact
   model constraint. Catalog aliases are for exact model identity and migration,
   not target routing personas.
5. **`Profile`** — named power policy. The profile expands to effective power
   bounds and optional ranking preferences before candidate filtering. It never
   broadens or overrides hard harness/provider/model pins.
6. **`MinPower` / `MaxPower`** — routing strength bounds. Higher power means
   stronger for agent tasks. These bounds filter candidate models before
   scoring; they apply only to unpinned automatic routing and never override
   hard harness/provider/model pins. When a profile is also supplied, numeric
   bounds further constrain the profile's effective policy.

**Model constraint resolution.** When `Model` is set on `ExecuteRequest` or
`RouteRequest`, Fizeau owns the full resolution pipeline. DDx passes the raw
string unchanged. Fizeau owns normalization, catalog alias lookup, substring
matching, ambiguity handling, no-match handling, and final route selection for
that raw constraint. The service normalizes case, whitespace, vendor prefixes,
and delimiter characters, then matches against discovered/provider model IDs
and catalog-resolved concrete IDs. The service, not DDx, chooses the final
concrete model ID, reports ambiguity when multiple candidates survive
normalization, and returns typed no-match evidence when no concrete model can
be resolved.

`ModelRef` remains a separate exact catalog-reference surface. It is not a
profile. The contract above does not change `ModelRef` exactness; it only
clarifies how raw `Model` pins are resolved before route selection.

When no `MinPower`, `MaxPower`, exact model, provider-source/endpoint, or
harness constraint is supplied, automatic routing selects the best lowest-cost
viable auto-routable model from discovered inventory.

Auto-selection inputs (`EstimatedPromptTokens`, `RequiresTools`, `Reasoning`)
apply after hard pins and power bounds. They never override an explicit pin.

`Role` is observational and does not enter filtering, scoring, or
tiebreaking. `CorrelationID` does not enter eligibility filtering, but a valid
non-empty value is the sticky route key for ranking affinity. That affinity is
to the server instance, not the model. It is a score component, not a pin:
health, hard saturation, context fit, required capabilities, power policy, and
hard pins still decide eligibility first.

### Role and CorrelationID normalization

`Role` and `CorrelationID` are shared by `ServiceExecuteRequest` and
`RouteRequest`. The service validates them
pre-dispatch (before any session state is opened or any provider is
called) and rejects invalid values with typed errors. Validation is
identity-or-reject: the service does not silently rewrite caller input.

**Role rules.** Empty means unset. When non-empty, `Role` must be:

- lowercase ASCII letters (`a`–`z`), digits (`0`–`9`), or hyphen (`-`)
- 1 to 64 bytes inclusive

Any other byte (uppercase, whitespace, punctuation other than `-`,
non-ASCII) is rejected. Length over 64 bytes is rejected.

**CorrelationID rules.** Empty means unset. When non-empty,
`CorrelationID` must be:

- printable ASCII only (bytes `0x21`–`0x7E`); control characters
  (including `\t`, `\n`, `\r`) and the space byte (`0x20`) are rejected
- 1 to 256 bytes inclusive

Hyphen (`-`), colon (`:`), and underscore (`_`) are inside the printable
range and are therefore allowed. Non-ASCII bytes are rejected.

**Typed errors.** Validation failures return one of two exported error
types so callers can branch on cause without string matching:

- `*RoleNormalizationError{Role, Reason}` — returned when `Role` fails.
  `Error()` returns `invalid Role <quoted>: <reason>`.
- `*CorrelationIDNormalizationError{CorrelationID, Reason}` — returned
  when `CorrelationID` fails. `Error()` returns
  `invalid CorrelationID <quoted>: <reason>`.

`Reason` is a short human-readable phrase (for example
`"length 80 exceeds max 64"` or
`"character \"_\" at offset 3 is not lowercase alphanumeric or hyphen"`).
The error type, not the `Reason` string, is the stable contract.

**Where validation runs.** `Execute`, the streaming variant, and
`ResolveRoute` all validate `Role` and `CorrelationID` before dispatch.
The same rules apply to both request types so that `ResolveRoute` parity
holds (a request that `ResolveRoute` accepts is accepted by `Execute`,
and vice versa, with respect to these two fields).

**Validation surface.** The contract guarantees the typed error types
named above. Helper functions `ValidateRole(role string) error` and
`ValidateCorrelationID(id string) error` return `nil` for empty input
and otherwise return the corresponding typed error. They are exported
so DDx-side callers can validate user input early without round-tripping
through the service.

### Prompt-caching opt-out (`CachePolicy`)

`ServiceExecuteRequest.CachePolicy` (and the mirrored `RouteRequest.CachePolicy`)
is the single public surface callers use to opt out of provider-side prompt
caching. The accepted values are `""` (interpreted as `"default"`),
`"default"`, and `"off"`; any other value is rejected at the service boundary
before any session state is opened or any provider is dispatched. The default
empty value means "use the per-provider default caching behavior" — the
concrete cache markers are stamped (or not) by the provider in a follow-up
bead, not by callers. Set `CachePolicy = "off"` for:

- **Deterministic evals.** Cache hits change observed billing, latency, and
  occasionally the tokenizer-visible prefix; eval harnesses that need
  byte-for-byte reproducible attempts disable caching to remove that variable.
- **Privacy-sensitive prompts.** Some callers must guarantee that prompt
  prefixes are not retained on the provider's caching infrastructure beyond
  the lifetime of a single request.
- **One-shot benchmark runs.** Single-request benchmark scenarios pay the
  cache-write cost without ever realizing a hit, so disabling caching is the
  lower-cost and more honest measurement.

The field is plumbed end-to-end (request → routing → provider opts) so that
the Anthropic `cache_control` writer (bead C) and the cache-aware cost
attribution path (bead D) can land without further contract churn.

**Scope.** `CachePolicy` is request-scoped. In this contract version,
`ResolveRoute` MUST be observationally identical for `CachePolicy=""`,
`"default"`, and `"off"` — same `RouteDecision`, same ranked candidates.
`"off"` only suppresses provider-side cache writes (e.g. Anthropic
`cache_control` markers) and emits explicit-zero cache amounts in cost
attribution. Routing avoidance of caching-supporting vs. caching-only
providers is out of scope for this round.

### Cost attribution and cache pricing

When the configured-cost fallback prices an attempt from per-MTok rates
(rather than a provider-reported total), cache-read and cache-write tokens
are charged at the manifest's `cost_cache_read_per_m` and
`cost_cache_write_per_m` rates — sourced from the model entry in
`internal/modelcatalog/catalog/models.yaml` and projected onto
`telemetry.Cost.CacheReadPerM` / `CacheWritePerM`. The total
`CostAttribution.Amount` is the sum of input, output, cache-read, and
cache-write costs; the per-component cache amounts are surfaced on
`CostAttribution.CacheReadAmount` and `CacheWriteAmount` as `*float64`.

The pointer-vs-zero distinction matters:

- `nil` cache amounts mean the harness or provider did not report cache
  usage and no cache-rate pricing was available — the cost is unknown.
- An explicit zero (`*float64(0.0)`) means the caller opted out of caching
  via `CachePolicy = "off"`. The native loop emits explicit zero values in
  this case so downstream consumers can distinguish "no cache activity by
  design" from "we have no idea."

Historical session-log records are not retroactively re-priced; this
semantics applies to new runs going forward.

Native `fiz` permission modes are enforced by tool exposure at the service
boundary:

- `safe` (and empty/default) exposes only read-only built-ins: `read`, `find`,
  `grep`, and `ls`.
- `unrestricted` exposes the full native built-in tool set for the request's
  `ToolPreset`.
- `supervised` is rejected for the native `fiz` harness until an approval loop
  exists. Subprocess harnesses may still implement their own supervised modes.

type RouteRequest struct {
    Profile               string // named power policy; not a concrete model set
    Model                 string
    Provider              string
    Harness               string
    ModelRef              string
    MinPower              int // PLANNED agent-79e194aa; only for unpinned automatic routing
    MaxPower              int // PLANNED agent-79e194aa; only for unpinned automatic routing
    Reasoning             Reasoning
    Permissions           string
    EstimatedPromptTokens int  // when >0, filter candidates whose context window cannot hold the prompt
    RequiresTools         bool // when true, filter providers whose SupportsTools() is false
    CachePolicy           string // "" / "default" / "off"; mirrors ServiceExecuteRequest.CachePolicy
    Role                  string // mirrors ServiceExecuteRequest.Role for ResolveRoute parity
    CorrelationID         string // sticky route key when validated; also mirrors ServiceExecuteRequest.CorrelationID
}

type RouteDecision struct {
    Harness    string
    Provider   string  // provider source or endpoint selector; empty when not applicable
    Endpoint   string  // selected named endpoint when applicable
    ServerInstance string // normalized inference-server identity for sticky affinity
    Model      string
    Reason     string  // human-readable explanation
    Power      int     // catalog-projected power of the selected Model; 0 = unknown / exact-pin-only
    Sticky     *StickyRouteState
    Utilization *EndpointUtilization
    Candidates []Candidate  // full ranking, including rejected candidates
}

type StickyRouteState struct {
    Key        string // redacted or hashed in operator-facing JSON
    ServerInstance string
    Status     string // "new" | "reused" | "expired" | "invalidated" | "none"
    Reason     string // machine-readable explanation when not reused
    Bonus      float64
    ExpiresAt  time.Time
}

type EndpointUtilization struct {
    Provider       string
    Endpoint       string
    ServerInstance string
    Model          string
    ActiveRequests int
    QueuedRequests int
    MaxConcurrency int
    CacheUsageRatio float64
    Source         string // "vllm" | "llama_metrics" | "llama_slots" | "omlx" | "rapid_mlx" | "fizeau_leases" | "unknown"
    Fresh          bool
    ObservedAt     time.Time
}

type Candidate struct {
    Harness         string
    Provider        string // provider source or endpoint selector
    Endpoint        string
    ServerInstance  string
    Model           string
    Power           int // PLANNED agent-da67ebbe; 1..10, 0 unknown/exact-pin-only
    Score           float64
    ScoreComponents map[string]float64
    Eligible        bool
    Reason          string
    FilterReason    string // machine-readable rejection reason when Eligible=false
    EstimatedCost   CostEstimate
    PerfSignal      PerfSignal
    Sticky          *StickyRouteState
    Utilization     *EndpointUtilization
}

type RouteAttempt struct {
    Harness      string
    Provider     string
    Model        string
    Endpoint     string
    Status       string // "success" clears active availability failures; other values record availability failure
    FailureClass string // setup/config|no-candidate|provider-transient|capability|cancelled|timeout
    Reason       string // machine-readable failure reason when available
    Error        string // human-readable failure detail
    Duration     time.Duration
    Timestamp    time.Time // zero = service clock
}

type HarnessInfo struct {
    Name                 string
    Type                 string   // "native" | "subprocess"
    Available            bool
    Path                 string   // for subprocess harnesses
    Error                string   // when Available=false
    IsLocal              bool
    IsSubscription       bool
    TestOnly             bool     // true for sentinel harnesses excluded from production routing
    ExactPinSupport      bool
    DefaultModel         string   // built-in default model when no override is supplied
    SupportedPermissions []string // subset of {"safe","supervised","unrestricted"}
    SupportedReasoning   []string // values such as {"off","low","medium","high","minimal","xhigh","max"}
    CostClass            string   // low/medium/high cost class; legacy values may exist
    Quota                *QuotaState // nil if not applicable; live field
    Account              *AccountStatus
    UsageWindows         []UsageWindow
    LastError            *StatusError
    CapabilityMatrix     HarnessCapabilityMatrix
}

type QuotaState struct {
    Windows    []QuotaWindow
    CapturedAt time.Time
    Fresh      bool
    Source     string
    Status     string // ok|blocked|stale|unavailable|unauthenticated|unknown
    LastError  *StatusError
}

type QuotaWindow struct {
    Name          string
    LimitID       string
    WindowMinutes int
    UsedPercent   float64
    ResetsAt      string
    ResetsAtUnix  int64
    State         string // ok|blocked|unknown
}

type AccountStatus struct {
    Authenticated   bool
    Unauthenticated bool
    Email           string
    PlanType        string
    OrgName         string
    Source          string
    CapturedAt      time.Time
    Fresh           bool
    Detail          string
}

type UsageWindow struct {
    Name         string
    Source       string
    CapturedAt   time.Time
    Fresh        bool
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    CostUSD      float64
}

type EndpointStatus struct {
    Name          string
    BaseURL       string
    ProbeURL      string
    Status        string // connected|unreachable|unauthenticated|error|unknown
    Source        string
    CapturedAt    time.Time
    Fresh         bool
    LastSuccessAt time.Time
    ModelCount    int
    LastError     *StatusError
}

type StatusError struct {
    Type      string // unavailable|unauthenticated|error
    Detail    string
    Source    string
    Timestamp time.Time
}

type HarnessCapabilityStatus string

const (
    HarnessCapabilityRequired      HarnessCapabilityStatus = "required"
    HarnessCapabilityOptional      HarnessCapabilityStatus = "optional"
    HarnessCapabilityUnsupported   HarnessCapabilityStatus = "unsupported"
    HarnessCapabilityNotApplicable HarnessCapabilityStatus = "not_applicable"
)

type HarnessCapability struct {
    Status HarnessCapabilityStatus
    Detail string // human-readable reason tied to the current implementation
}

type HarnessCapabilityMatrix struct {
    ExecutePrompt     HarnessCapability
    ModelDiscovery    HarnessCapability
    ModelPinning      HarnessCapability
    WorkdirContext    HarnessCapability
    ReasoningLevels   HarnessCapability
    PermissionModes   HarnessCapability
    ProgressEvents    HarnessCapability
    UsageCapture      HarnessCapability
    FinalText         HarnessCapability
    ToolEvents        HarnessCapability
    QuotaStatus       HarnessCapability
    RecordReplay      HarnessCapability
}

type ProviderInfo struct {
    Name          string
    Type          string  // "openai" | "openrouter" | "lmstudio" | "omlx" | "vllm" | "llama-server" | "ollama" | "anthropic" | "virtual"
    BaseURL       string
    ServerInstance string
    Status        string  // "connected" | "unreachable" | "error: <msg>"
    ModelCount    int
    Capabilities  []string  // {"tool_use","vision","json_mode","streaming"}
    IsDefault     bool      // matches the configured default_provider
    DefaultModel  string    // the per-provider configured default model, if any
    CooldownState *CooldownState  // nil if not in cooldown
    Auth          AccountStatus
    EndpointStatus []EndpointStatus
    Quota         *QuotaState
    Utilization   *EndpointUtilization
    UsageWindows  []UsageWindow
    LastError     *StatusError
}

type ModelInfo struct {
    ID            string
    Provider      string  // provider source or endpoint selector currently used by the service
    ProviderType  string  // concrete provider source type, e.g. "openrouter", "lmstudio", "vllm", "llama-server", or "omlx"
    Harness       string  // for subprocess-only models, the owning harness
    EndpointName  string  // configured endpoint name, "default", or host:port fallback
    EndpointBaseURL string // endpoint base URL used for discovery; empty when not applicable
    ServerInstance string  // normalized inference-server identity for sticky affinity
    ContextLength int     // resolved (provider API > catalog > default)
    ContextSource string    // provider_api|provider_config|catalog|default|unknown
    Power         int     // PLANNED agent-da67ebbe; catalog power 1..10; 0 unknown/exact-pin-only
    DeploymentClass string // PLANNED agent-da67ebbe; provider/deployment class or equivalent provenance
    PowerProvenance string // PLANNED agent-da67ebbe; compact source/method summary for power
    Capabilities  []string
    Cost          CostInfo
    PerfSignal    PerfSignal
    Available     bool
    AutoRoutable   bool   // PLANNED agent-da67ebbe; eligible for unpinned automatic routing
    ExactPinOnly   bool   // PLANNED agent-da67ebbe; available only when directly pinned
    IsDefault     bool    // matches the configured default model
    CatalogRef    string  // canonical catalog reference if recognized
    ReasoningDefault Reasoning // catalog/provider default for this model, if known
    ReasoningMaxTokens int     // 0 when unknown or not applicable
    RankPosition  int     // ordinal in the latest discovery rank for this provider; -1 if unranked
}

type ModelFilter struct {
    Harness  string  // empty = all harnesses
    Provider string  // empty = all provider sources/endpoints
    MinPower int     // PLANNED agent-79e194aa; 0 = no lower bound
    MaxPower int     // PLANNED agent-79e194aa; 0 = no upper bound
}

type ProfileInfo struct {
    Name               string // named power policy
    MinPower           int
    MaxPower           int
    Target             string // compatibility target when applicable
    AliasOf            string
    ProviderPreference string
    Deprecated         bool
    Replacement        string
    CatalogVersion     string
    ManifestSource     string
    ManifestVersion    int
}

type ResolvedProfile struct {
    Name            string // named power policy
    MinPower        int
    MaxPower        int
    Target          string // compatibility target when applicable
    Deprecated      bool
    Replacement     string
    CatalogVersion  string
    ManifestSource  string
    ManifestVersion int
    Surfaces        []ProfileSurface
}

type ProfileSurface struct {
    Name                    string
    Harness                 string
    ProviderSystem          string
    Model                   string
    Candidates              []string
    PlacementOrder          []string
    CostCeilingInputPerMTok *float64
    ReasoningDefault        Reasoning
    FailurePolicy           string
}

type HealthTarget struct {
    Type string  // "harness" | "provider"
    Name string
}

type CooldownState struct {
    Reason    string    // "consecutive_failures" | "manual" | etc.
    Until     time.Time
    FailCount int
    LastError string
    LastAttempt time.Time
}

type RouteStatusReport struct {
    Routes          []RouteStatusEntry
    GeneratedAt     time.Time
    GlobalCooldowns []CooldownState
}

type RouteStatusEntry struct {
    Model          string
    Strategy       string                  // informational, normally "auto"
    Candidates     []RouteCandidateStatus
    LastDecision   *RouteDecision          // most recent ResolveRoute result for this key (cached)
    LastDecisionAt time.Time
}

type RouteCandidateStatus struct {
    Provider          string
    Endpoint          string
    ServerInstance    string
    Model             string
    Priority          int
    Healthy           bool
    Cooldown          *CooldownState
    RecentLatencyMS   float64  // observation-derived
    RecentSuccessRate float64  // 0-1
    Utilization       *EndpointUtilization
    Sticky            *StickyRouteState
}

type UsageReportOptions struct {
    Since string     // "today", "7d", "30d", "YYYY-MM-DD", or "YYYY-MM-DD..YYYY-MM-DD"
    Now   time.Time  // zero = time.Now().UTC()
}

type UsageReport struct {
    Window *UsageReportWindow `json:"window,omitempty"`
    Rows   []UsageReportRow   `json:"rows"`
    Totals UsageReportRow     `json:"totals"`
}

type UsageReportWindow struct {
    Start time.Time `json:"start"`
    End   time.Time `json:"end"`
}

type UsageReportRow struct {
    Provider            string   `json:"provider"`
    Model               string   `json:"model"`
    Sessions            int      `json:"sessions"`
    SuccessSessions     int      `json:"success_sessions"`
    FailedSessions      int      `json:"failed_sessions"`
    InputTokens         int      `json:"input_tokens"`
    OutputTokens        int      `json:"output_tokens"`
    TotalTokens         int      `json:"total_tokens"`
    DurationMs          int64    `json:"duration_ms"`
    KnownCostUSD        *float64 `json:"known_cost_usd"`
    UnknownCostSessions int      `json:"unknown_cost_sessions"`
    CacheReadTokens     int      `json:"cache_read_tokens"`
    CacheWriteTokens    int      `json:"cache_write_tokens"`
}

// Derived helpers on UsageReportRow:
//   SuccessRate() float64           — successful sessions / total sessions
//   CostPerSuccess() *float64       — known cost / successful sessions, nil when unknown
//   InputTokensPerSecond() float64
//   OutputTokensPerSecond() float64
//   CacheHitRate() float64          — cache_read / (input + cache_read + cache_write)

type SessionLogEntry struct {
    SessionID string    `json:"session_id"`
    ModTime   time.Time `json:"mod_time"`
    Size      int64     `json:"size"`
}

type Event struct {
    Type     string          // see event types below
    Sequence int64
    Time     time.Time
    Metadata map[string]string  // echoed from ExecuteRequest.Metadata
    Data     json.RawMessage    // shape depends on Type; see schemas below
}

const (
    ServiceEventTypeTextDelta       = "text_delta"
    ServiceEventTypeToolCall        = "tool_call"
    ServiceEventTypeToolResult      = "tool_result"
    ServiceEventTypeCompaction      = "compaction"
    ServiceEventTypeRoutingDecision = "routing_decision"
    ServiceEventTypeStall           = "stall"
    ServiceEventTypeFinal           = "final"
)

type ServiceTextDeltaData struct { Text string }
type ServiceToolCallData struct { ID, Name string; Input json.RawMessage }
type ServiceToolResultData struct { ID, Output, Error string; DurationMS int64 }
type ServiceCompactionData struct { MessagesBefore, MessagesAfter, TokensFreed int }
type ServiceRoutingDecisionData struct {
    Harness, Provider, Model, Reason string
    // RequestedHarness echoes the caller's hard harness pin, when one was
    // supplied. Empty means the service routed automatically.
    RequestedHarness string
    // HarnessSource is "request_harness" for explicit caller pins and
    // "auto_route" for service-owned routing.
    HarnessSource string
    FallbackChain []string
    SessionID string
}
type ServiceStallData struct { Reason string; Count int64 }
type ServiceFinalData struct {
    Status, Error, FinalText, SessionLogPath string
    ExitCode int
    DurationMS int64
    Usage *ServiceFinalUsage
    Warnings []ServiceFinalWarning
    CostUSD float64
    RoutingActual *ServiceRoutingActual
}
type ServiceFinalUsage struct {
    InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens *int
    CacheTokens, ReasoningTokens, TotalTokens *int
    Source string
    Fresh *bool
    CapturedAt string
    Sources []ServiceUsageSourceEvidence
}
type ServiceFinalWarning struct {
    Code, Message string
    Sources []ServiceUsageSourceEvidence
}
type ServiceUsageSourceEvidence struct {
    Source string
    Fresh *bool
    CapturedAt string
    Usage *ServiceUsageTokenCounts
    Warning string
}
type ServiceUsageTokenCounts struct {
    InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens *int
    CacheTokens, ReasoningTokens, TotalTokens *int
}
type ServiceRoutingActual struct {
    Harness, Provider, Model string
    FallbackChainFired []string
    Power int // catalog-projected power of the actually-dispatched Model; 0 means unknown/exact-pin-only
}

type ServiceDecodedEvent struct {
    Type string
    Sequence int64
    Time time.Time
    Metadata map[string]string
    TextDelta *ServiceTextDeltaData
    ToolCall *ServiceToolCallData
    ToolResult *ServiceToolResultData
    Compaction *ServiceCompactionData
    RoutingDecision *ServiceRoutingDecisionData
    Stall *ServiceStallData
    Final *ServiceFinalData
}

func DecodeServiceEvent(ev Event) (ServiceDecodedEvent, error)

type DrainExecuteResult struct {
    Events []ServiceDecodedEvent
    TextDeltas []ServiceTextDeltaData
    ToolCalls []ServiceToolCallData
    ToolResults []ServiceToolResultData
    Compactions []ServiceCompactionData
    Stalls []ServiceStallData
    RoutingDecision *ServiceRoutingDecisionData
    Final *ServiceFinalData
    FinalStatus string
    FinalText string
    Usage *ServiceFinalUsage
    CostUSD float64
    SessionLogPath string
    RoutingActual *ServiceRoutingActual
    TerminalError string
}

func DrainExecute(ctx context.Context, events <-chan Event) (*DrainExecuteResult, error)
```

## Status Signal Semantics

`ListHarnesses`, `ListProviders`, and `RouteStatus` are the status API for
doctor-style consumers. Consumers must not read provider-native files, auth
stores, quota caches, or config files directly to build routing diagnostics.

Every normalized status datum carries:

- `Source`: the endpoint, cache, config, or probe path that produced it.
- `CapturedAt`: when the service captured or read the datum.
- `Fresh`: whether the value is inside the service's freshness window.
- `LastError`: normalized `unavailable`, `unauthenticated`, or `error`
  information when the datum could not be captured successfully.

Provider endpoint probes report `EndpointStatus` with reachability,
`ModelCount`, and `LastSuccessAt` when connected. Provider authentication is
reported through `ProviderInfo.Auth`; missing API keys or 401/403-style probe
failures are `Unauthenticated=true` and do not require consumers to know the
provider's native auth file format.

`ListModels` is the public model-listing surface for configured native
providers. For `openrouter`, `lmstudio`, and `omlx`, it must query each
configured endpoint's OpenAI-compatible models endpoint (`<base_url>/models`,
where `base_url` normally ends in `/v1`) and return one `ModelInfo` per
discovered `(provider, endpoint, model)` tuple. Results carry the configured
provider name in `Provider`, the concrete backend type in `ProviderType`, and
the endpoint identity in `EndpointName` / `EndpointBaseURL`; consumers must not
infer these from URLs or internal config. If an endpoint is unreachable or
returns a non-OK response, that endpoint contributes no models and the method
continues listing other endpoints/providers. Missing credentials for cloud
providers surface through the same empty-result behavior here and through
`ListProviders`/`HealthCheck` status for diagnostics.

The CLI projection for this service method is `fiz --list-models`. Its
JSON output must be a rendering of `ModelInfo`, including planned power-routing
fields as they land.

Claude Code and Codex subscription quotas are read from durable service-owned
caches by `ListHarnesses`; `HealthCheck` may refresh stale caches by invoking
the authenticated direct PTY probe. Existing tmux-backed quota probes are legacy
diagnostics and must not be treated as final capability evidence. Live record
mode must fail immediately with a clear unavailable or unauthenticated status when the
target binary, credentials, or direct PTY transport dependency is missing.
Replay mode reads committed/generated cassette data or quota cache fixtures and
must not require credentials.

`UsageWindows` are the normalized historical-usage projection. An empty slice
means no service-owned usage source is available for that harness/provider yet;
consumers should display that as unavailable rather than reading native logs
directly.

## CLI Projection Boundary

The standalone `cmd/fiz` binary is a first-party consumer of this service
contract. Its job is to translate user input into public service requests and
render public service results. The CLI boundary is strict:

- execution goes through `FizeauService.Execute`;
- session replay/follow goes through `TailSessionLog`;
- output decoding uses `DecodeServiceEvent` or `DrainExecute`, not local copies
  of private payload structs;
- historical session-log projections (`fiz log`, `replay`, `usage`) go
  through `ListSessionLogs`, `WriteSessionLog`, `ReplaySession`, and
  `UsageReport`; CLI subcommands do not parse session-log JSONL records
  directly;
- harness capabilities, model inventory, route feedback, quota/status, and
  test-only harness dispatch are consumed through public service methods.

The CLI must not:

- construct native providers or retry/failover wrappers;
- call `internal/core` loop entry points directly;
- synthesize service session lifecycle records into the internal session-log
  schema;
- rebuild `RouteDecision` candidate lists from config as a substitute for
  calling `ResolveRoute` or passing routing intent to `Execute`.

In practice this means `cmd/fiz` must not depend on
`internal/core`, `internal/provider/*`, `internal/tool`, `internal/session`,
`internal/compaction`, `internal/harnesses`, or `internal/routing`.

If a CLI-visible behavior cannot be expressed through `FizeauService` methods or the
public request/event/result types, the contract must grow first. Internal
package reach-through from `cmd/fiz` is architecture debt and must not be
normalized as a permanent compatibility layer.

## Catalog Power Projection

Catalog power is service data, not consumer configuration. Consumers that need
to present, validate, or route by strength call:

- `ListModels` for actual available models and their catalog `Power`,
  provider/deployment class, cost, speed/perf signal, context, availability,
  catalog reference, auto-routable state, and exact-pin-only state. The power
  and eligibility fields are planned in agent-da67ebbe.
- `Execute` / `ResolveRoute` with `MinPower` and `MaxPower` for route intent.
  Those request/filter fields are planned in agent-79e194aa.

Migration rule: any consumer currently reading `~/.config/agent/models.yaml`,
model-catalog manifests, or hard-coded surface strings to discover power,
placement, or candidate policy must switch to the service methods above. Direct
YAML reads are allowed only inside the agent service and model-catalog
implementation.

Catalog power is required for automatic routing. The current embedded v4
manifest does not yet expose a `power` field; adding it is prerequisite work
tracked by agent-735c591e and agent-91b8aa19. Initial catalog power is
synthesized from normalized benchmarks such as SWE-bench and
terminal/TypeScript task benchmarks when available, plus model capabilities,
recency, cost, and provider/deployment class. In the absence of direct
benchmark coverage, cost times recency is the default proxy within a
provider/model family: the newest and most expensive model is presumed
strongest unless the catalog contains an explicit override. Older family
members are not auto-routable unless directly pinned or explicitly marked as
useful cost/power exceptions.

## Harness Capability Matrix

`ListHarnesses` exposes `HarnessInfo.CapabilityMatrix` so consumers can decide
which harnesses are eligible without reading internal registry structs. Status
semantics:

- `required`: the service contract relies on this capability for that harness.
- `optional`: the harness supports the capability, but callers must tolerate its
  absence on other harnesses.
- `unsupported`: the capability is meaningful for the harness class but is not
  currently available.
- `not_applicable`: the capability does not apply to that harness class.

The broad matrix below is a compatibility view across subprocess harnesses,
test-only harnesses, and current provider-backend rows. It is not the
authoritative health signal for the primary harnesses. Primary harness health is
specified separately in
`docs/helix/02-design/primary-harness-capability-baseline.md` and covers only
`fiz`, `codex`, and `claude`.

Primary-harness baseline capabilities are strict: `Run`, `FinalText`,
`ProgressEvents`, `Cancel`, `WorkdirContext`, `PermissionModes`, `ListModels`,
`SetModel`, `ListReasoning`, `SetReasoning`, `TokenUsage`, `QuotaStatus` for
primary subscription harnesses, `ErrorStatus`, and `RequestMetadata`. These capabilities
must not be reported as `optional` in the primary baseline. In particular,
`ListModels` is required for `codex` and `claude`; if model choices are only
available through their interactive TUI surfaces and no headless collector is
implemented yet, the primary baseline reports a visible `gap` or `blocked`
state rather than treating model listing as unsupported or optional.

Current builtin matrix:

| Harness | ExecutePrompt | ModelDiscovery | ModelPinning | WorkdirContext | ReasoningLevels | PermissionModes | ProgressEvents | UsageCapture | FinalText | ToolEvents | QuotaStatus | RecordReplay |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| codex | required | unsupported | optional | optional | optional | optional | required | optional | optional | optional | optional | unsupported |
| claude | required | unsupported | optional | optional | optional | optional | required | optional | optional | optional | optional | unsupported |
| gemini | required | optional | optional | optional | unsupported | optional | required | optional | optional | unsupported | unsupported | optional |
| opencode | required | unsupported | optional | optional | optional | optional | required | optional | optional | unsupported | unsupported | unsupported |
| fiz | required | optional | optional | optional | optional | optional | required | optional | optional | optional | not_applicable | unsupported |
| pi | required | unsupported | optional | optional | optional | unsupported | required | optional | optional | unsupported | unsupported | unsupported |
| virtual | required | not_applicable | not_applicable | not_applicable | not_applicable | not_applicable | required | optional | optional | not_applicable | not_applicable | required |
| script | required | not_applicable | not_applicable | not_applicable | not_applicable | not_applicable | required | optional | optional | not_applicable | not_applicable | required |
| openrouter | required | required | unsupported | unsupported | unsupported | unsupported | required | optional | optional | unsupported | unsupported | unsupported |
| lmstudio | required | required | unsupported | unsupported | unsupported | unsupported | required | optional | optional | unsupported | not_applicable | unsupported |
| omlx | required | required | unsupported | unsupported | unsupported | unsupported | required | optional | optional | unsupported | not_applicable | unsupported |

Notes:

- `ExecutePrompt=required` means `Service.Execute` has a wired dispatch path
  today. Registered subprocess runners that are not wired through
  `Service.Execute` remain `unsupported` in that row even when lower-level
  runner code exists.
- `FinalText=optional` means final events populate `final_text` when the
  harness or native provider produced user-facing response text. During the
  migration window, `text_delta` remains available for consumers that still
  stream output incrementally, but final verdict parsers should prefer
  `final_text` and avoid parsing raw harness stream frames.
- `RecordReplay=required` only for test-only harnesses (`virtual`, `script`).
  Production harnesses do not currently expose deterministic record/replay
  through this service contract.

## Test-Only Execute Harnesses

`virtual` and `script` are explicit test-only harnesses. The router never
chooses them implicitly; callers must set `ExecuteRequest.Harness`
explicitly to opt in.

`Harness="virtual"` accepts either:

- `Metadata["virtual.response"]`: an inline deterministic final response.
  Optional keys: `virtual.prompt_match`, `virtual.input_tokens`,
  `virtual.output_tokens`, `virtual.total_tokens`, `virtual.delay_ms`,
  `virtual.model`.
- `Metadata["virtual.dict_dir"]`: a virtual-provider dictionary directory keyed
  by normalized prompt hash.

`Harness="script"` accepts a pinned script definition through metadata:
`script.stdout` is required; optional keys are `script.stderr`,
`script.exit_code`, and `script.delay_ms`. This is intentionally data-driven and
does not require fake `claude`, `codex`, `opencode`, `gemini`, or `pi` binaries.
Both harnesses emit the normal `routing_decision` → progress/text → `final`
sequence and can be consumed through `DrainExecute`.

## Event JSON shapes

Closed union of event types. Every harness backend emits these identically.

```jsonc
// type=text_delta
{"text": "..."}

// type=tool_call
{"id": "...", "name": "find", "input": {...}}

// type=tool_result
{"id": "...", "output": "...", "error": "...", "duration_ms": 123}

// type=compaction
// (Emitted ONLY when actual compaction work was performed. No-op compactions
// emit nothing — the compactor was asked, decided no work needed, returned silently.)
{"messages_before": 30, "messages_after": 12, "tokens_freed": 4521}

// type=routing_decision
// (Emitted at start of execution.)
{
  "harness": "agent",
  "provider": "lmstudio",
  "endpoint": "bragi",
  "model": "qwen/qwen3.6-35b-a3b",
  "reason": "power 7 match; endpoint reachable; 256K context",
  "candidates": [
    {
      "harness": "agent",
      "provider": "lmstudio",
      "endpoint": "http://bragi:1234/v1",
      "model": "qwen/qwen3.6-35b-a3b",
      "power": 7,
      "eligible": true,
      "score": 0.82,
      "score_components": {"capability": 0.38, "cost": 0.24, "latency": 0.2}
    }
  ]
}

// type=stall
// (Emitted just before final when StallPolicy triggers.)
{"reason": "no_compactions_exceeded", "count": 50}

// type=final
// (Emitted last; channel closes after.)
{
  "status": "success" | "failed" | "stalled" | "timed_out" | "cancelled",
  "exit_code": 0,
  "error": "",
  "routing_failure": null,
  // on routed failure:
  // "routing_failure": {
  //   "failure_class": "provider-transient",
  //   "attempted": {"harness": "agent", "provider": "lmstudio", "endpoint": "bragi", "model": "qwen/qwen3.6-35b-a3b", "power": 7}
  // },
  "final_text": "user-facing final response text, stripped of harness stream envelopes",
  "duration_ms": 12345,
  "usage": {
    "input_tokens": 7996,
    "output_tokens": 267,
    "cache_read_tokens": 1200,
    "reasoning_tokens": 41,
    "total_tokens": 8263,
    "source": "native_stream",
    "fresh": true,
    "sources": [
      {"source": "native_stream", "fresh": true, "usage": {"input_tokens": 7996, "output_tokens": 267, "total_tokens": 8263}}
    ]
  },
  "warnings": [
    {
      "code": "usage_source_disagreement",
      "message": "token usage sources disagree; selected source by documented precedence",
      "sources": [
        {"source": "native_stream", "usage": {"input_tokens": 7996, "output_tokens": 267, "total_tokens": 8263}},
        {"source": "transcript", "usage": {"input_tokens": 7990, "output_tokens": 267, "total_tokens": 8257}}
      ]
    }
  ],
  "cost_usd": 0.0042,
  "session_log_path": "/path/to/session.jsonl",
  "messages": [...],   // optional history continuation
  "routing_actual": {
    "harness": "agent",
    "provider": "lmstudio",
    "endpoint": "bragi",
    "model": "qwen/qwen3.6",
    "fallback_chain_fired": []  // legacy field; agent does not retry candidate 2
  }
}
```

### Final Text Empty Outcome

A `success` final event with empty `final_text` is a valid outcome. Legitimate
cases include: the model declined to answer, returned a refusal, produced a
structured-output-only response (e.g. tool calls with no final assistant
message), or completed under a system policy that suppresses user-visible
text. Consumers MUST NOT treat empty `final_text` as a transient failure and
MUST NOT trigger a retry on empty text alone. The terminal classification is
carried by `status` (and `error` when non-success); `final_text` length is
not a liveness or success signal. Reviewers and orchestrators that need to
distinguish "model produced nothing useful" from "model produced something
useful" should consult tool-call/tool-result events plus `status`, not the
length of `final_text`.

### Final Usage Source Policy

Final-event `usage` is optional. A missing `usage` object means per-run token
usage was unavailable, not zero. When a harness explicitly reports zero tokens,
the relevant fields are present with value `0` — this preserves upstream
provenance: nil means "harness did not emit this dimension" while a present
zero means "upstream provider explicitly reported zero". Harness emitters MUST
NOT silently substitute zero for unknown. Token dimensions that the harness
did not expose are omitted rather than serialized as fabricated zeros.

The normalized token vocabulary is `input_tokens`, `output_tokens`,
`cache_read_tokens`, `cache_write_tokens`, `cache_tokens`,
`reasoning_tokens`, and `total_tokens`. Harness-specific terms are normalized
into this vocabulary when exposed by Claude, Codex, or native provider streams.

When more than one source is available, precedence is:

1. `native_stream`
2. `transcript`
3. `status_output`
4. `fallback`

The selected source is copied to `usage.source`; every valid source considered
is listed in `usage.sources` with its `fresh`/`captured_at` metadata when
available. If multiple sources report different overlapping token fields, the
service still selects by precedence but records a final warning with
`code=usage_source_disagreement` and the source values. Malformed or changed
usage shapes are recorded as `code=usage_malformed` warnings and do not cause
missing fields to be filled with zero.

## Typed Event Decoding

Consumers should not redefine local copies of final/tool/routing payload
structs. `DecodeServiceEvent` returns a typed view for one event, and
`DrainExecute` consumes an `Execute` channel into a `DrainExecuteResult` with
the terminal fields consumers usually need: final status, normalized final text,
usage, cost, routing actual, tool calls/results, session log path, and terminal
error text.

Before:

```go
type serviceFinalData struct {
    Status string `json:"status"`
    FinalText string `json:"final_text"`
}

for ev := range events {
    if ev.Type != "final" {
        continue
    }
    var final serviceFinalData
    _ = json.Unmarshal(ev.Data, &final)
}
```

After:

```go
result, err := agent.DrainExecute(ctx, events)
if err != nil {
    return err
}
verdictText := result.FinalText
status := result.FinalStatus
actualModel := result.RoutingActual.Model
```

## Test seam types

```go
// FakeProvider supports three patterns:
//   - Static script: sequence of pre-recorded responses, consumed in order.
//   - Dynamic callback: function called per request returning a response.
//   - Error injection: per-call status override.
type FakeProvider struct {
    Static      []FakeResponse                            // for static script pattern
    Dynamic     func(req FakeRequest) (FakeResponse, error)  // for dynamic per-call pattern
    InjectError func(callIndex int) error                 // for error injection pattern
}

type FakeRequest struct {
    Messages []Message
    Tools    []string
    Model    string
}

type FakeResponse struct {
    Text      string
    ToolCalls []ToolCall
    Usage     TokenUsage
    Status    string  // "success" by default
}

// PromptAssertionHook is called once per Execute, with the system+user prompt
// the agent actually sent to the model. Used by tests that verify prompt
// construction/compaction without running a real provider.
type PromptAssertionHook func(systemPrompt, userPrompt string, contextFiles []string)

// CompactionAssertionHook is called whenever a real compaction runs. No-op
// compactions are NOT delivered (they don't fire compaction events either).
type CompactionAssertionHook func(messagesBefore, messagesAfter int, tokensFreed int)

// ToolWiringHook is called once per Execute, with the resolved tool list and
// the harness that received it. Used by tests that verify the right tools
// land at the right harness given the request's permission level.
type ToolWiringHook func(harness string, toolNames []string)
```

## Reasoning contract

`Reasoning` is the only preferred public control for model-side reasoning.
Consumers do not set separate public thinking, effort, level, or budget fields.
The scalar accepts named values (`auto`, `off`, `low`, `medium`, `high`) and
provider/harness-supported extended values such as `minimal`, `xhigh` /
`x-high`, and `max`. It also accepts numeric values through
`ReasoningTokens(n)`, where `0` means explicit off and positive integers mean
an explicit max reasoning-token budget or documented provider-equivalent
numeric value.

Normalization is tri-state:

- Empty means no caller preference.
- `auto` means resolve model, catalog, or provider defaults.
- `off`, `none`, `false`, and `0` mean explicit reasoning off.
- Positive integers mean an explicit numeric request.

Default portable named-to-token budgets are `low=2048`, `medium=8192`, and
`high=32768` only when a selected provider/model does not publish a more
specific map. Providers and subprocess harnesses may map resolved reasoning to
wire or CLI knobs named `reasoning`, `thinking`, `effort`, `variant`, or a
numeric budget. They may also drop auto/default reasoning controls for models
that do not support explicit reasoning control. Explicit unsupported values,
unknown extended values, and over-limit numeric values fail clearly.

Catalog reasoning defaults are model metadata. Lower-power coding models may
default to `reasoning=off`; higher-power frontier models may default to
`reasoning=high`. Any explicit caller `Reasoning` value wins over catalog
defaults, including supported values above high such as `xhigh` or `max`, and
numeric values.

## Sampling contract

`ExecuteRequest` carries six pointer-typed sampling fields: `Temperature`,
`TopP`, `TopK`, `MinP`, `RepetitionPenalty`, and `Seed`. Each field uses a
pointer so `nil` is a first-class "unset — let lower layers or the server
decide" state distinct from any concrete value (notably distinct from `0`,
which is a meaningful greedy-decoding request).

Per ADR-007, sampling values are catalog policy and resolve through a
precedence chain before the request reaches the service:

1. **Catalog sampling bundles** (manifest top level, named bundles).
2. **`providers.<name>.sampling`** in user config (per-provider override).
3. **CLI flags** (deferred — not in v1).

Higher layers stomp lower layers **per field, not per bundle**. Any field nil
at every layer is omitted from the wire and the server's own default applies.

Native OpenAI-compatible providers honor all six fields. Anthropic Messages
honors `Temperature`/`TopP`/`TopK` only; other fields are silently dropped at
the provider seam. Subprocess harnesses (pi, codex, claude-code) do not honor
catalog sampling — they pin samplers internally; the catalog's
`ModelEntry.sampling_control` records this with `harness_pinned`. Callers
requiring strict deterministic parity should note that the oMLX server
silently ignores `Seed` (empirical, 2026-04-27).

The catalog is a versioned data artifact published independently of binary
releases (see
[`plan-2026-04-10-catalog-distribution-and-refresh.md`](../plan-2026-04-10-catalog-distribution-and-refresh.md)).
A new sampling bundle reaches existing users when they run
`fiz catalog update` against the published channel. New code reading an
old installed manifest degrades gracefully — the resolver's L1 lookup returns
nothing, lower layers and server defaults apply, and a single first-use
warning points at the refresh command. See ADR-007 §7 for the schema-evolution
rules that govern future additions to this contract surface.

## Bead Execution Policy

DDx bead implementation owns retry policy. The agent service selects the best
candidate inside one request's power bounds, hard pins, and auto-selection
inputs; it does not decide that a failed low-power attempt should become a
high-power attempt. When DDx wants to escalate, it issues a new `Execute`
request with a higher `MinPower`, preserving the same bead context, logs, and
execution budget.

DDx should normally try a low or medium `MinPower` with `reasoning=off` first,
then escalate only when caller-owned evidence shows that a stronger model is
likely to help. Exact `Model`, `Provider`, or `Harness` pins remain hard
constraints and are not widened by escalation.

Power retry is eligible when the first pass produced semantic evidence outside
agent routing: model capability looked insufficient, reasoning quality was
poor, post-implementation tests failed, or review blocked after the agent had a
valid checkout and attempted the bead. This evidence belongs to DDx and may be
submitted to the catalog/power-maintenance process, but it does not flow through
agent route-health feedback and does not cause the agent to retry.

Power retry is not eligible for deterministic setup failures: dirty-worktree or
merge conflicts, missing repository checkout, invalid bead metadata, unresolved
dependencies, config parse errors, missing harness binaries, authentication
setup failures, or command-not-found/toolchain setup failures. These failures
should stop with actionable evidence instead of spending a stronger attempt.

Cost caps, timeout limits, permission policy, and determinism controls apply
across both passes as one execution budget. The agent-side contract defines the
fields and semantics; the DDx execute-loop implementation is tracked in the
paired DDx repo bead `ddx-785d02f7`.

## Route Attempt Feedback

The agent service owns provider availability feedback for model selection.
`Execute` records only the selected candidate's service-observed dispatch
result: transport errors, auth/quota/rate limits, 5xx responses, stream loss,
subprocess exit, timeout, malformed protocol output, and capability mismatch.
These are direct signals about availability of the provider, endpoint, harness,
or pinned model route.

`RecordRouteAttempt` is the public feedback API for external routed work that
needs to report the same availability outcomes. It is not the semantic task
success/failure channel. The minimum implementation is deterministic,
process-local routing feedback. The active TTL is `ServiceConfig`
`HealthCooldown`; when that is unset the default is 30 seconds.

Candidate keying uses the tuple `(Harness, ProviderSource, Endpoint, Model)`.
Consumers should provide every field they know. A non-success `Status` with a
routing-relevant `FailureClass` records an active failure and future
`ResolveRoute` calls demote matching candidates inside the same process until
the TTL expires. `Status="success"` clears matching active failures so a
recovered candidate is eligible without waiting for TTL expiry. `RouteStatus`
reports active route-attempt cooldowns on matching candidates with `Reason`,
`LastError`, `LastAttempt`, and `Until` timestamps.

Failure classes control what gets penalized:

- `provider-transient` and `timeout` demote the provider/endpoint/model tuple
  for future selections.
- `capability` marks the candidate ineligible for requests needing that missing
  context/tool/reasoning capability.
- `setup/config`, `no-candidate`, and `cancelled` are returned to the caller as
  actionable evidence but do not poison model quality. Auth/config failures may
  mark a provider unusable until configuration changes, but they are not proof
  that the model itself is weak.

## Provider Quota State

The agent owns a per-provider **quota state machine** that is distinct from the
per-request route-attempt feedback above. Route-attempt feedback demotes one
`(Harness, ProviderSource, Endpoint, Model)` candidate inside a process-local
TTL window. Quota state operates one level up — at the **provider** granularity
— and is the basis for telling the caller "this provider is offline until
`retry_after`, do not bother retrying sooner."

### States and transitions

Each configured provider is in exactly one of two states at any instant:

- `available` — the provider has no known quota signal that would prevent it
  from serving a routing decision. This is the initial state.
- `quota_exhausted` — the provider has reported (or is predicted to report) a
  daily / monthly / window quota signal and carries a `retry_after` instant.
  The provider is filtered out of every `ResolveRoute` and `Execute` candidate
  set until the state machine returns it to `available`.

Allowed transitions:

```
available        --MarkQuotaExhausted(retry_after)-->  quota_exhausted
quota_exhausted  --MarkAvailable-->                    available
quota_exhausted  --(now >= retry_after, observed)-->   available  (auto-decay)
```

`MarkQuotaExhausted` is called from three independent triggers; whichever
fires first wins:

1. **Upstream rate-limit headers.** Provider response-header parsers
   (`internal/provider/quotaheaders`) extract a structured signal —
   `RemainingTokens`, `RemainingRequests`, `ResetTime`, `RetryAfter` — from
   Anthropic, OpenAI, and OpenRouter responses. When `remaining_tokens == 0`,
   `remaining_requests == 0`, or `Retry-After` is set, the originating provider
   is moved to `quota_exhausted` with `retry_after` taken from the parsed
   `ResetTime` / `RetryAfter`. Header parsers are tolerant of missing fields.
2. **Local burn-rate prediction.** When `providers.<name>.daily_token_budget`
   is set, `ProviderBurnRateTracker` maintains a UTC-daily rolling window of
   recorded request+response token usage. If the projected end-of-window usage
   exceeds the configured budget, the provider is preemptively transitioned to
   `quota_exhausted` with `retry_after` set to the next UTC midnight, before
   the upstream signal arrives.
3. **Recovery probe.** A periodic background loop sweeps the
   `quota_exhausted` set; on a successful re-probe the provider returns to
   `available`, and on continued failure `retry_after` is extended with bounded
   exponential backoff (initial 5m, cap 1h).

The state machine is process-local. It is not persisted across service
restarts; the upstream signal and burn-rate tracker re-establish state on next
use.

### NoViableProviderForNow

When every otherwise-eligible routing candidate is excluded **solely** because
its provider is in `quota_exhausted`, `ResolveRoute` and `Execute` return a
typed `*NoViableProviderForNow` error:

```go
type NoViableProviderForNow struct {
    // RetryAfter is the earliest expected provider-recovery time across the
    // exhausted set. Callers should not retry before this instant.
    RetryAfter time.Time
    // ExhaustedProviders is the set of provider names currently in the
    // quota_exhausted state that would otherwise have served the request.
    ExhaustedProviders []string
}
```

Semantics:

- **Distinct from `ErrNoLiveProvider`**, which means the entire ladder lacks
  any live provider regardless of quota — typically a configuration or
  connectivity problem that will not resolve itself.
- **Distinct from configuration errors** (`ErrUnknownProvider`,
  `ErrUnknownProfile`, `ErrHarnessModelIncompatible`), which are operator
  mistakes.
- **Transient.** Callers should pause and resume on or after `RetryAfter`
  rather than treating the request as a permanent failure.
- `errors.Is(err, &NoViableProviderForNow{})` is the supported discrimination.

DDx is the canonical downstream consumer: when its drain loop receives
`*NoViableProviderForNow` from `Execute`, it pauses the queue, sleeps until
`RetryAfter`, and resumes — rather than counting the bead as failed and
escalating power. `ExhaustedProviders` is informational evidence for the
operator-facing log; it does not affect the pause duration.

### Operator config knobs

- **`providers.<name>.daily_token_budget`** *(int, optional)*. Maximum total
  tokens (request + response) the provider may consume per UTC daily window.
  Zero or absent disables predictive exhaustion for this provider; the
  upstream quota signal is still respected.

  ```yaml
  providers:
    anthropic:
      type: anthropic
      api_key: ${ANTHROPIC_API_KEY}
      daily_token_budget: 5_000_000   # preempt exhaustion at 5M tokens/day
    openrouter:
      type: openrouter
      api_key: ${OPENROUTER_API_KEY}
      # no budget → upstream signal is the only exhaustion trigger
  ```

- **Recovery probe interval.** The recovery probe loop is enabled by setting
  `ServiceOptions.QuotaRefreshContext` (when `nil` the loop does not start).
  Cadence is self-pacing: each pass sleeps until the soonest known
  `retry_after`, bounded above by a 5-minute fallback. After a probe failure,
  that provider's `retry_after` is extended with bounded exponential backoff
  starting at 5 minutes and capping at 1 hour. There is no separate
  user-facing interval knob; operators tune cadence by choosing whether to
  enable the loop at all.

### Out of scope: per-request rate limiting

Sub-daily rate limiting — HTTP `429 Too Many Requests` with `Retry-After` on
the order of seconds or minutes — is **not** part of the quota state machine.
Those signals stay in the per-request feedback path: the failing dispatch is
reported on the final event with the attempted route, route-attempt feedback
demotes the offending candidate inside its TTL window, and the caller decides
whether to retry. Promoting a provider to `quota_exhausted` for a brief 429 is
explicitly avoided because it would block routing across all models served by
that provider for the duration of `retry_after`.

The boundary is qualitative: signals indicating the operator's daily / monthly
/ subscription window is spent (zero remaining tokens, zero remaining
requests, or a `Retry-After` long enough that recovery probing is the right
strategy) flow through the quota state machine; signals indicating short-term
throttling flow through the per-request path.

## Routing Policy Test Contract

Routing tests must be statement-backed: each policy-invariant case includes a
human-readable `policy_statement` so a failing assertion maps directly to a
contract sentence. agent-de968c76 owns this suite. Minimum statements:

- If local/free candidates are available and satisfy requested power, tools,
  context, health, and hard constraints, route to local/free before paid cloud.
- Local/free preference never overrides `MinPower`, `MaxPower`, exact model
  pins, provider-source/endpoint pins, harness pins, or required capabilities.
- A no-power request selects the best lowest-cost viable auto-routable model
  from discovered inventory.
- Unknown-power and exact-pin-only models are inspectable but excluded from
  unpinned automatic routing.
- Provider/deployment class prevents local/community benchmark ties from
  equaling managed cloud frontier power solely because one benchmark is high.
- `Execute` dispatches one candidate and returns route evidence; it does not
  retry another candidate inside the same request.
- `Role` and `CorrelationID` are echoed into `routing_decision` and `final`
  event `Metadata`.
- `Role` and `CorrelationID` are echoed into the session-log header (one line
  per session, on `session.start`).
- `Role` and `CorrelationID` are NOT echoed into `text_delta` event metadata
  (per-event bloat avoidance); the existing Metadata echo path on `text_delta`
  still applies for caller-supplied metadata under non-reserved keys.
- `Role` never affects eligibility filtering or scoring.
- `CorrelationID` never affects eligibility filtering, but a valid non-empty
  value contributes sticky server-instance affinity during scoring.
- Without a `CorrelationID`, sticky affinity is absent and routing uses the
  normal candidate score.
- `ResolveRoute` and `Execute` observe identical routing policy for the same
  correlation-aware request.
- Invalid `Role` and `CorrelationID` values are rejected pre-dispatch with the
  typed `*RoleNormalizationError` and `*CorrelationIDNormalizationError`.
- `ServiceRoutingActual.Power` reflects the catalog-projected power of the
  actually-dispatched Model.
- When the caller sets both top-level `Role` and `Metadata["role"]`, the
  top-level field wins and a `MetadataKeyCollision` warning
  (`ServiceFinalWarning.Code = "metadata_key_collision"`) is emitted on the
  final event. Same rule applies for `CorrelationID` /
  `Metadata["correlation_id"]`.

## Harness Integration Testing

Real subprocess harness support uses versioned PTY cassettes as golden-master
evidence. The transport decision is [ADR-002: PTY Cassette Transport for
Harness Golden Masters](/Users/erik/Projects/agent/docs/helix/02-design/adr/ADR-002-pty-cassette-transport.md).
The runnable replay/record workflow is documented in
[Harness Golden-Master Integration](/Users/erik/Projects/agent/docs/helix/02-design/harness-golden-integration.md).

ADR-002 selects direct PTY ownership inside Fizeau as the canonical service
and cassette transport for live execution, record mode, replay mode,
cancellation, quota probes, model-list probes, and inspection. tmux is not part
of the core harness/cassette design, and tmux-only evidence must not promote a
capability to final `supported` status. Replay-mode tests can prove parser,
event, cleanup, timing, and transport behavior, but a harness capability is not
promoted to or retained as `supported` without fresh record-mode evidence from
the real authenticated harness when that capability depends on an external
binary or subscription. PTY cassette record/replay is part of the `internal/pty`
library boundary, with version-1 cassette timestamps quantized to 100ms by
default and replay supporting realtime, scaled, and collapsed timing modes.

## Behaviors the contract guarantees

The agent owns these execution-time behaviors. Callers do not opt in or out.

- **Explicit model resolution.** `Execute` and `ResolveRoute` use the same
  raw-`Model` resolution semantics. A unique normalized match becomes the
  concrete model ID used for route selection; ambiguous or missing matches fail
  with typed evidence instead of falling through to an unrelated model.

- **Orphan-model validation.** When raw `Model` resolution finds no concrete
  candidate, `Execute` and `ResolveRoute` fail before dispatch with typed
  no-match evidence rather than silently picking the wrong provider.

- **Discovery-backed model pins.** When `Model` is pinned and no catalog entry
  exists, `Execute` resolves it through provider discovery: if `Provider` is
  also pinned, only that provider's discovery is consulted; otherwise discovery
  runs across all configured discovery-capable providers. If any returns the
  model, the request proceeds with the catalog's no-entry cost
  (`cost_source: "unknown"`) and the **provider's** default reasoning. If
  discovery does not return it, the orphan-model rule above fires before any
  session log is opened. Automatic routing scoring does not apply —
  discovery-only models cannot be auto-selected by power bounds.

- **Provider request deadline wrapping.** Every HTTP call to a provider is
  wrapped with `ProviderTimeout`. Per-request failures classified as
  transport/auth/upstream are reported on the final event with the attempted
  route; the agent does not retry another candidate.

- **Service-owned native routing and provider construction.** For the embedded
  `fiz` harness, `Execute` resolves configured provider candidates,
  constructs the concrete provider adapter, and dispatches one candidate.
  Callers express intent with `Harness`, `Provider`, `Model`, `ModelRef`,
  `MinPower`, or `MaxPower`, plus the optional `EstimatedPromptTokens`/
  `RequiresTools` auto-selection inputs; they do not pass provider instances,
  private candidate tables, or pre-resolved `RouteDecision` values.
  `ResolveRoute` results are informational only — `Execute` always re-resolves
  on its own inputs (idempotent for the same caller intent, modulo health
  changes).

- **Route-reason attribution.** The start-event `routing_decision` and
  final-event `routing_actual` together capture why each candidate was
  tried/picked. `routing_decision.requested_harness`,
  `routing_decision.harness_source`, and the matching session-log lifecycle
  fields distinguish caller hard pins from service-owned automatic routing.

- **Stall detection.** Per `StallPolicy`. Default policy (when caller passes
  `nil`) uses conservative limits matching today's circuit-breaker thresholds.

- **No-op compaction silence.** Pure no-op compaction probes are not progress
  and are not externally stall-detected. They emit no public compaction events.
  Real compaction failures such as `ErrCompactionStuck` and
  `ErrCompactionNoFit` remain fatal execution errors.

- **OS-level subprocess cleanup.** On `ctx.Done()`, agent reaps PTY and
  orphan processes for subprocess harnesses. Tested and guaranteed.

- **Session-log persistence ownership.** When session logging is enabled,
  service-owned execution writes the lifecycle and terminal records for that
  session. Consumers may choose where logs are stored via `SessionLogDir`, but
  they do not recreate internal session start/end records from the event
  stream.

- **No-op compaction telemetry suppression.** Compaction events fire ONLY
  when actual work was performed. The compactor's pre-/post-turn checkpoint
  probes that decide "no compaction needed" emit nothing at default verbosity.

## Extensions and stability

This contract is the boundary. Internal packages (`compaction`, `prompt`,
`tool`, `session`, `observations`, `modelcatalog`, `provider/*`) live under
`internal/` and the Go compiler blocks external imports.

When a consumer needs new contract behavior, file a PR against this document
proposing the addition. Maintainers decide whether the surface grows. Do not
work around the boundary by importing internals (impossible after `internal/`
enforcement) or by forking the module.
</untrusted-data>
      </content>
    </ref>
  </governing>

  <diff rev="ebedafd89cc9bd71f01aaf7490ae4e4846b071d9">
<untrusted-data>
commit ebedafd89cc9bd71f01aaf7490ae4e4846b071d9
Merge: 84e42322 2eef98a5
Author: ddx-land-coordinator <coordinator@ddx.local>
Date:   Wed May 6 20:29:07 2026 -0400

    Merge bead fizeau-1e090b7d attempt 20260507T002039- into master
</untrusted-data>
  </diff>

  <instructions>
You are reviewing a bead implementation against its acceptance criteria.

For each acceptance-criteria (AC) item, decide whether it is implemented correctly, then assign one overall verdict:

- APPROVE — every AC item is fully and correctly implemented.
- REQUEST_CHANGES — some AC items are partial or have fixable minor issues.
- BLOCK — at least one AC item is not implemented or incorrectly implemented; or the diff is insufficient to evaluate.

## Required output format (schema_version: 1)

Respond with EXACTLY one JSON object as your final response, fenced as a single ```json … ``` code block. Do not include any prose outside the fenced block. The JSON must match this schema:

```json
{
  "schema_version": 1,
  "verdict": "APPROVE",
  "summary": "≤300 char human-readable verdict justification",
  "findings": [
    { "severity": "info", "summary": "what is wrong or notable", "location": "path/to/file.go:42" }
  ]
}
```

Rules:
- "verdict" must be exactly one of "APPROVE", "REQUEST_CHANGES", "BLOCK".
- "severity" must be exactly one of "info", "warn", "block".
- Output the JSON object inside ONE fenced ```json … ``` block. No additional prose, no extra fences, no markdown headings.
- Do not echo this template back. Do not write the words APPROVE, REQUEST_CHANGES, or BLOCK anywhere except as the JSON value of the verdict field.
  </instructions>
</bead-review>
