---
ddx:
  id: SD-002
  depends_on:
    - FEAT-006
    - SD-001
    - CONTRACT-003
    - ADR-008
  review:
    self_hash: 09861f7103db1731b221dde6bdf3283f5f2992ae08fc920da1f3f07f7e825e99
    deps:
      ADR-008: 3f36c9ae5997a72d2575876d739d110a7dd6950456a517695ed0d0cd8e118db3
      CONTRACT-003: e3da1c8ba3972a5d8af244b267fee8c20e03b5f221409484bf4dc1bb52709939
      FEAT-006: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
      SD-001: 7123b4d558d2ddd35289bf49390fde9e00b52081cbe90de37986d13fbbf36988
    reviewed_at: "2026-07-16T02:01:23Z"
---
# Solution Design: SD-002 — Mountable CLI and Standalone Binary

**Feature**: FEAT-006 (Standalone CLI)

## Scope

This design defines the thin command surface that demonstrates and embeds the
Fizeau library. `agentcli` is a reusable Cobra command tree. `cmd/fiz` is the
standalone process wrapper. Neither is an independent execution engine; the
root `fizeau` service facade remains the product and the only public execution
boundary.

## Requirements Mapping

| Requirement | Technical capability | Component |
|---|---|---|
| Prompt execution | positional prompt, `-p`, `run`, file, or stdin | `agentcli` |
| Output and exit behavior | text/JSON rendering, stderr progress, process-style status | `agentcli` + `cmd/fiz` |
| Embedding | fresh unattached `*cobra.Command`, injected streams and identity | `agentcli.MountCLI` |
| Configuration | config, environment, and flag inputs projected into service requests | `agentcli` |
| Inventory and status | models, providers, policies, harnesses, route status | `agentcli` over CONTRACT-003 |
| Session projections | log, replay, usage | `agentcli` over CONTRACT-003 |

## Public CLI Integration Surface

```go
package agentcli

func MountCLI(opts ...MountOption) *cobra.Command
func Run(opts Options) int
func ExitCode(err error) (int, bool)
```

`MountCLI` returns a fresh, unattached Cobra root on every call. Mount options
inject command identity, descriptions, streams, and version metadata. The
returned tree does not call `os.Exit`, so another Cobra application can attach
it safely. `ExitCode` lets a host translate command errors without parsing
text. `Run` remains the direct runner compatibility surface.

The command tree includes execution plus inventory, health, catalog,
route-status, session-log, and maintenance commands. Execution is service
backed. A narrow static allowlist currently permits non-execution catalog,
discovery, and config commands to consume internals that lack public
projections; that is transitional implementation reference and is not a model
for new commands. Legacy root flags may remain as explicit compatibility paths,
but new command behavior is native Cobra behavior in `agentcli`.

## Standalone Process

`cmd/fiz/main.go` mounts the same command tree, supplies build metadata and
standard streams, executes it, and is the only layer that terminates the
process. It contains no provider construction, routing algorithm, core loop,
harness parser, or session-log implementation.

```text
fiz run "prompt"
echo "prompt" | fiz run
fiz --json run "prompt"
fiz -p "prompt"                 # compatibility path

fiz models
fiz providers
fiz policies
fiz harnesses
fiz route-status
fiz log [session-id]
fiz replay <session-id>
fiz usage [--since=7d]
```

## Service Boundary

Execution commands construct `ServiceExecuteRequest` values and call
`FizeauService.Execute`. Inventory, status, and session commands use the
corresponding public methods and projections from CONTRACT-003 where those
surfaces exist; remaining non-execution direct imports are constrained by
`agentcli/service_boundary_test.go` and are migration targets. Event rendering
uses `ServiceEvent`, `DecodeServiceEvent`, or `DrainExecute` rather than
harness-native output or private session-log records.

CONTRACT-003 v0.15 capacity reporting follows the same boundary. The CLI
renders the additive `context_capacity` event and typed terminal/final capacity
facts when present; it does not infer capacity from provider text or treat an
accepted-session capacity rejection as an `Execute` return error. Unknown
future event and terminal-cause values remain preservable in JSON output. The
selected route's resolved context value and source are authoritative for
execution and final routing attribution; a request-local compaction bound may
only tighten that value.

Routing intent uses the current ADR-009 vocabulary:

- `Policy`, `MinPower`, and `MaxPower` express automatic-routing intent;
- `Harness`, `Provider`, and `Model` are hard pins and override signals;
- reasoning, tool, context, and token inputs constrain or score candidates.

There is no normative `Profile`, `ModelRef`, or `PreResolved` CLI flow. The CLI
does not resolve a route and inject a private decision back into execution.
Route resolution and dispatch are service-owned.

## Configuration

The CLI accepts project/global Fizeau configuration, environment variables,
and flags, then projects those values into service construction and public
request fields. Canonical paths use the `fizeau` namespace:

```text
$XDG_CONFIG_HOME/fizeau/config.yaml
./.fizeau/config.yaml
```

Later request-local inputs override broader defaults. Loading configuration in
the first-party CLI does not transfer provider construction or routing
ownership out of the service.

## Exit Semantics

| Code | Meaning |
|---|---|
| 0 | command or execution completed successfully |
| 1 | execution or operational failure |
| 2 | invalid CLI usage where the command path defines a usage error |

Mounted callers receive errors, including `ExitError`, rather than process
termination. Only `cmd/fiz` maps them to `os.Exit`.

## Boundary Rules

1. `agentcli` consumes the root `fizeau` facade for all execution behavior;
   its temporary non-execution internal allowlist is explicit and
   test-enforced.
2. `cmd/fiz` is process wiring around `agentcli.MountCLI`.
3. Neither layer imports or invokes `internal/core` or provider adapters.
4. Fizeau owns provider/harness dispatch, transport retry and
   routing-infeasibility handling within a request, event normalization, and
   session-log persistence. It does not perform semantic cross-route failover.
5. Per ADR-017, a semantic retry or stronger-route escalation is initiated by
   the embedding caller as a new request, not hidden inside the CLI or service.
   Eligibility-time context rejection filters a candidate before selection and
   may leave a better eligible survivor. Once a route is selected,
   accepted-session capacity exhaustion never dispatches the next ranked
   candidate in the same `Execute` call.
6. Static boundary tests prevent the CLI from bypassing the public facade.
7. The v0.15 Go migration requires keyed literals for public Fizeau structs
   that gained fields. The CLI and embedding examples use keyed literals; JSON
   capacity events and fields remain additive for tolerant consumers.

## Test Strategy

- Mount two command trees in one process and prove their streams, identity, and
  flags do not leak across instances.
- Exercise standalone and mounted execution through the public service seam.
- Verify native Cobra inventory/status/session commands render public
  projections without importing internal execution packages.
- Verify `cmd/fiz` maps `ExitError` to process status and owns all `os.Exit`
  calls.
- Verify JSON and text rendering preserve the selected context value/source,
  decode additive capacity events, and use the typed terminal fact for a main
  capacity rejection without synthesizing a next-route retry.
- Compile public CLI/embedding fixtures with keyed v0.15 request and projection
  literals, and accept unknown future event and terminal-cause values.
- Preserve explicit regression tests for supported compatibility flags.

## Technology Rationale

| Layer | Choice | Rationale |
|---|---|---|
| Command framework | Cobra | mountable command composition and native subcommands |
| Public execution API | CONTRACT-003 root facade | one boundary for embedders and first-party CLI |
| Standalone wrapper | `cmd/fiz` | minimal process and build-metadata ownership |

## Traceability

| Authority | Design element |
|---|---|
| FEAT-006 | prompt, output, config, inventory, session, and process behavior |
| ADR-008 | public facade and service/transcript boundaries |
| ADR-009 | policy/power/pin routing vocabulary |
| ADR-017 | caller-owned semantic escalation |
| CONTRACT-003 | service methods, events, projections, and mountable CLI surface |
