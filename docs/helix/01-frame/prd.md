---
ddx:
  id: helix.prd
  review:
    self_hash: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    deps: {}
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Product Requirements Document — Fizeau

## Summary

Fizeau exists for three reasons that build on each other:

1. **Facilitate agentic development.** A reusable, embeddable agent loop with
   the right primitives — tool-calling, planning, compaction, retries, sampling,
   reasoning, quotas, session logs — so building tools doesn't mean
   re-implementing the loop every time. Other tools embed `fizeau.New(...)`
   instead of writing their own agent harness.
2. **Make agentic work measurable.** Per-turn timing, prefill vs decode
   breakdown, cost-per-trial, subscription-quota accounting, route-attempt
   feedback — all first-class outputs, not bolted-on observability. You can't
   improve the prompts, agents, or providers you can't measure.
3. **Make local models a real option.** Local serving (vLLM, MLX, LM Studio,
   Ollama) on the same provider surface as cloud frontier models. The
   benchmarks compare them honestly. Self-hosted at the right quantization is
   often cheaper, sometimes faster, and rarely the right answer for everything
   — but you can pick per workload because the data is on the table.

The harness control implied by #1 enables #2 (we can instrument what we own)
and makes #3 viable (one provider surface that abstracts cloud and local
equally).

Concretely, Fizeau is a Go library that implements a tool-calling agent runtime
— an in-process loop with file read/write, shell execution, navigation
helpers, task tracking, structured I/O, compaction, retry, and per-turn
instrumentation — designed to be embedded in build orchestrators, benchmark
harnesses, CI systems, and any tool that needs an instrumented agent on its
critical path. Following the ghostty model — great library, proven by a usable
app — Fizeau ships as a Go package plus a thin standalone `fiz` CLI that
showcases the library and serves as an embeddable harness backend (see
CONTRACT-003). Fizeau also owns a reusable shared model catalog and
updateable manifest so callers can resolve canonical routing policies, model
metadata, provider surface strings, and deprecations without copying model
policy into each consumer. Every LLM interaction and tool call is logged and
replayable, with per-turn cost and timing built in. Success means tools embed
Fizeau instead of re-implementing the loop, every measurement surface produces
honest data without bolted-on observability, and local-vs-cloud is a per-workload
data question because the same provider-shaped surface covers both.

## Problem and Goals

### Problem

Tools that need a tool-calling agent on their critical path — build
orchestrators, benchmark harnesses, CI systems, evaluation pipelines, embedded
agent products — face three compounding problems:

1. **Each tool re-implements the loop.** Tool-calling protocol, retry, sampling,
   compaction, reasoning controls, quota handling, and session logging get
   rebuilt per project. The result is a fragmented surface where every consumer
   ships its own bugs and its own gaps.
2. **Measurement is bolted on.** Most agent stacks expose final tokens-and-cost
   if you're lucky. Per-turn timing, prefill vs decode, TTFT, throughput, known-
   vs-unknown cost, subscription quota accounting — the things you'd actually
   need to improve a prompt, an agent, or a provider — aren't first-class
   outputs. You can't compare what you can't measure.
3. **Local models live in a separate world.** Even when a tool theoretically
   supports a local backend, it usually means a different code path, a
   different observability surface, and different cost semantics — so
   comparing self-hosted against frontier cloud is never a straight delta. A
   significant fraction of agent work is mechanical and well within local-model
   capability, but tools route everything to cloud because the local path is
   second-class.

The legacy framing — "build orchestrators shell out to agent CLIs and we
should run the loop in-process instead" — describes one symptom. The deeper
problem is that the loop, the measurement chain, and the provider surface are
each being rebuilt per tool, with local backends as an afterthought.

### Goals

1. **Provide a reusable, embeddable agent loop.** Ship a Go library with a
   tool-calling loop, planning, compaction, retries, sampling, reasoning,
   quotas, and session logs as first-class primitives. Other tools embed
   `fizeau.New(...).Execute(ctx, request)` instead of writing their own agent
   harness.
2. **Make the loop measurable.** Per-turn timing availability and source,
   observable TTFT/prefill/decode/tool-latency windows, the four token streams
   (input, output, cache-read, cache-write), known-or-unknown cost-per-trial,
   subscription-quota accounting, and route-attempt feedback are first-class
   outputs of every `Execute` call, not optional observability. Unavailable
   timing windows are explicit unknowns, never synthesized numbers.
3. **Make local models a real option.** One provider surface across cloud
   (OpenAI, Anthropic, OpenRouter, Google) and local (vLLM, MLX, LM Studio,
   Ollama, native local) backends. Routing, billing, instrumentation, and
   session logs are uniform across both. Benchmarks can compare them honestly.
4. **Structured I/O.** Accept prompts and structured envelopes, return
   structured results with status, output, four-stream token usage, per-turn
   timing, and cost semantics.
5. **Prove it with an app.** Standalone `fiz` CLI that showcases the library
   and serves as an embeddable harness backend for callers like DDx and the
   benchmark runner, following the ghostty pattern.
6. **Own reusable model policy.** Provide a Fizeau-owned shared model catalog,
   publishable updateable manifest, canonical routing policies, and explicit
   refresh workflow so model metadata, provider surfaces, and deprecations are
   maintained once and consumed by any caller.

### Success Metrics

| Metric | Target | Measurement Method |
|--------|--------|--------------------|
| Embeddable adoption | ≥1 external tool (DDx, benchmark runner) consumes Fizeau as a library through CONTRACT-003 | Integration test + downstream consumer wiring |
| Measurement coverage | Every `llm.request → llm.response` chain reports timing availability/source, supported timing windows, four token streams, and known-or-unknown cost; no silent gaps | CONTRACT-001 conformance tests |
| Local-vs-cloud parity | Local serving runtimes (vLLM, MLX, LM Studio, Ollama) and cloud providers expose the same provider-shaped surface so benchmark deltas are honest | Benchmark catalog profile definitions |
| Local model completion rate | ≥70% of routine coding tasks succeed on local 7B+ under `cheap`/`default` policies | HELIX/build-pass logs |
| Cost per task (blended) | <$0.05 average for routine coding tasks | `fiz usage` report |
| Agent loop overhead | <10ms beyond model inference time | Benchmark suite |

### Non-Goals

- **TUI or interactive mode** — Fizeau is headless-only. Interactive use goes
  through pi, claude, or other standalone agents.
- **MCP server** — Fizeau provides tools directly, not via MCP protocol.
- **Prompt engineering** — Fizeau executes prompts; the caller owns
  prompt design and persona injection.
- **Harness orchestration policy** — Fizeau owns reusable model catalog data and
  policy, but callers choose harnesses/providers for a task and HELIX owns only
  stage intent.
- **Model hosting** — Fizeau connects to LM Studio/Ollama/cloud APIs. It does
  not run inference itself.
- **IDE integration** — Fizeau is a library, not an editor plugin.

## Users and Scope

### Primary Persona: Tool Builder / Embedder

**Role**: A tool that needs an instrumented tool-calling agent on its critical
path — build orchestrator, benchmark harness, CI system, evaluation pipeline,
embedded agent product. Software, not a human.
**Goals**: Embed an agent loop without re-implementing it; get first-class
per-turn measurement; treat local and cloud backends uniformly.
**Pain Points**: Re-implementing the loop, sampling, compaction, retries, and
session logging per project. Bolted-on observability. Local backends as
second-class code paths.

### Secondary Persona: Benchmark / Measurement Consumer

**Role**: A benchmark harness or research workflow that compares prompts,
agents, providers, or self-hosted vs cloud configurations.
**Goals**: Honest deltas. Holding either the model or the harness constant and
varying the other should produce data attributable to exactly that axis.
**Pain Points**: Provider-by-provider instrumentation drift; cost guessed from
stale tables; per-turn timing not exposed; local backends not on the same
surface as cloud frontier models.

### Tertiary Persona: CLI User

**Role**: Developer using `fiz` (or a wrapper) from the command line.
**Goals**: Inspect providers/policies/models, run an agent task with policy
routing, replay session logs, and read usage/cost without standing up an
embedder.
**Pain Points**: Existing agent CLIs don't expose policy-routing, structured
session logs, or measurement as first-class outputs.

## Requirements

### Must Have (P0)

1. **Agent loop** — tool-calling LLM loop: send prompt → model responds with
   tool calls or text → execute tools → repeat until done or max iterations
2. **Tool set** — shipped built-ins include read, write, edit, bash, find,
   grep, ls, patch, and task
3. **OpenAI-compatible provider** — generic provider for any OpenAI-compatible
   endpoint. Covers LM Studio (localhost:1234), Ollama (localhost:11434),
   OpenAI, Azure, Groq, Together, OpenRouter. Single implementation, configure
   by base URL.
4. **Anthropic provider** — Claude API support for cloud use
5. **Structured I/O** — accept prompt as string or structured envelope, return
   structured result (status, output, tool calls made, tokens, duration, error)
6. **Go library API** — `fizeau.New(...).Execute(ctx, request)` through the
   public `FizeauService` contract is the primary interface. The library takes
   explicit configuration and has no global state.
7. **Token tracking** — count and report input/output tokens per invocation
8. **Iteration limit** — configurable max tool-call iterations to prevent
   runaway loops
9. **Working directory** — file operations scoped to a configurable root.
   Paths outside working dir are allowed (sandbox assumption) but logged.
10. **Session logging** — every LLM request/response and tool call recorded to
    JSONL log. Full prompt and response bodies stored. Logs must support replay —
    reading a session log reproduces the complete conversation including tool
    calls and results.
11. **Cost tracking** — preserve provider- or gateway-reported cost when
    available. If no reported cost exists, use only runtime-specific configured
    pricing for the exact provider system and resolved model; otherwise record
    cost as unknown. The public final projection preserves the numeric cost and
    whether its source is reported, configured, or unknown.
12. **Standalone CLI** — `fiz` binary wrapping the library. Proves the library
    works, serves as an embeddable harness backend (see CONTRACT-003). Reads its
    own config file. Accepts prompt via `-p` flag or stdin.

### Should Have (P1)

1. **System prompt composition** — base system prompt + caller-provided
   additions (persona, project context, conventions)
2. **Session continuity** — a dedicated public continuation operation keyed
   only by a completed Fizeau session ID. Each continuation creates a new child
   Fizeau session. `Execute` does not accept continuation or provider-native
   session identifiers; route-native evidence remains private behind the
   service boundary.
3. **Streaming callbacks** — caller can receive tool call events and partial
   responses in real time
4. **Timeout management** — per-invocation and per-tool-call timeouts
5. **Harness adapter** — implement the harness interface (CONTRACT-004) so Fizeau
   appears as a native harness alongside claude/codex/pi
6. **Usage reporting** — `fiz usage` command:
   per-provider/model token and cost summaries with time-window filtering
7. **Session replay** — `fiz replay <session-id>` reads a session log and
   prints the conversation in human-readable form (every turn, tool call,
   result, tokens, timing)
8. **OpenTelemetry observability** — emit OTel GenAI-aligned spans and metrics
   for agent, LLM, and tool activity while retaining JSONL session logs for
   replay. Use standard OTel token/timing fields where available and
   project-namespaced attributes for cost/runtime details not yet covered by
   the standard.
9. **Conversation compaction** — auto-summarize long conversation histories
   to fit within model context windows
10. **Shared model catalog** — a Fizeau-owned catalog and publishable
    updateable manifest for concrete model metadata, canonical routing
    policies (`cheap`, `default`, `smart`, `air-gapped`), provider surface
    strings, billing/default-inclusion metadata, and deprecation metadata, kept
    separate from prompt presets
11. **Policy/provider/model routing** — request a canonical policy, numeric
    power bounds, or exact pin and let the embedded runtime choose among
    equivalent configured providers with recorded attribution. Routing treats
    local and cloud provider systems through the same eligibility and evidence
    model; callers own semantic retry and cross-harness escalation.

### Nice to Have (P2)

1. **Caching** — cache file reads within a session to reduce redundant I/O
2. **Multi-model consensus** — run same prompt on N models, return majority
   answer (multi-harness quorum is a caller concern)
3. **Model selection optimization** — choose model based on task
   characteristics (context length, complexity heuristics)

## Functional Requirements

The following eight product-level requirements are the canonical decomposition
of the product vision. Each subsystem maps to one approved feature spec; the
mechanics that follow elaborate these outcomes without creating additional
product authorities.

### Subsystem: Embedded Execution

**FR-1** — An embedder MUST be able to construct a public `FizeauService` with
`fizeau.New(...)` and execute a bounded, cancellable tool-calling run through
`Execute(ctx, request)`, receiving structured results and service-owned events
without importing concrete runtime internals.

### Subsystem: Workspace Tools

**FR-2** — An embedder MUST be able to supply or use Fizeau's workspace tools
through stable schemas and explicit working-directory semantics, with every
tool attempt represented in the execution result and measurement stream.

### Subsystem: Provider Parity

**FR-3** — Local serving runtimes, cloud APIs, and subprocess harnesses MUST
participate through provider-neutral contracts so the same request, event, and
usage surfaces apply regardless of where inference runs.

### Subsystem: Routing & Catalog

**FR-4** — An embedder MUST be able to select canonical policy intent, power
bounds, or exact pins against a Fizeau-owned model catalog and live inventory;
Fizeau selects and attributes one dispatch route while the caller retains
ownership of semantic retry and cross-harness escalation.

### Subsystem: Measurement & Replay

**FR-5** — Every execution MUST expose a replayable service-owned record of LLM
turns and tool attempts with four-stream token usage, timing, route identity,
and known-or-explicitly-unknown cost semantics.

### Subsystem: CLI Proof Surface

**FR-6** — The `fiz` command MUST remain a thin consumer of the same public
service facade, proving library behavior and offering machine-readable harness
and inspection surfaces without becoming a second execution architecture.

### Subsystem: Distribution

**FR-7** — Operators MUST be able to install, verify, and explicitly update the
proof CLI using versioned, platform-specific release artifacts without coupling library
execution to network update checks.

### Subsystem: Benchmark Evidence

**FR-8** — Benchmark consumers MUST be able to inspect self-describing result
cells and compare local and cloud runs without losing provenance, measurement
semantics, or the curated narrative views built from the same evidence.

## Detailed Product Mechanics

### Agent Loop

- The loop MUST send the prompt + conversation history to the LLM provider
- When the LLM responds with tool calls, Fizeau MUST execute each tool and
  append the results to the conversation
- When the LLM responds with text only (no tool calls), the loop MUST
  terminate and return the text as the result
- The loop MUST terminate after `max_iterations` tool-call rounds
- The loop MUST terminate if the context provides a cancellation signal
- Each tool call MUST be logged with inputs, outputs, duration, and any error

### Tools

- **read**: Accept absolute or relative path (resolved against working dir).
  Return file contents as string. Error if file doesn't exist or is binary.
  Support line range (offset + limit).
- **write**: Accept path and content. Create parent directories if needed.
  Overwrite existing file. Return bytes written.
- **edit**: Accept path, old_string, new_string. Fail if old_string not found
  or not unique. Return success/failure.
- **bash**: Accept command string and optional timeout. Execute in working dir.
  Capture stdout, stderr, exit code. Kill on timeout. Default timeout 120s.

### Providers

- Each provider MUST implement a common interface: `Chat(ctx, messages, tools,
  opts) (Response, error)`
- Provider selection MUST be configurable per-request
- Provider configuration MUST remain separate from the agent's canonical model
  catalog. Providers own transport/auth details; the catalog owns model policy.
- Providers MUST report token usage when the upstream API provides it
- Providers MUST support tool/function calling in the format the model expects
- The LM Studio provider MUST connect to `http://localhost:1234/v1` by default
  with configurable host/port
- The Ollama provider MUST connect to `http://localhost:11434` by default

### Model Catalog

- Fizeau MUST define a reusable shared model catalog for model families,
  canonical routing policies, provider surface strings, billing/default
  inclusion, and deprecation/stale metadata
- The shared catalog MUST be distinct from system prompt presets and use its
  own naming/config surface
- Fizeau MUST ship an embedded release snapshot of the catalog and support an
  updateable external manifest for faster policy/data refresh where practical
- Fizeau MUST support publishing catalog manifests outside normal binary
  releases and an explicit local refresh/install flow that does not introduce
  network access into ordinary request execution
- Callers MUST be able to select routing policies and exact model pins through
  the Fizeau-owned catalog without duplicating model policy in their own repos
- HELIX stage intent MUST remain above this layer; HELIX selects intent, callers
  express policy or pin constraints, and Fizeau resolves harness/provider/model
  details using its catalog and live evidence

### Structured I/O

- Input: plain string prompt, or structured envelope (JSON with kind, id,
  prompt, inputs, response_schema fields)
- Output: `DrainExecuteResult` with: status (success/failure/timeout/cancelled),
  output (final text), tool_calls (log of all tool calls), four token streams
  (input/output/cache-read/cache-write, plus any derived total), duration_ms,
  error (if any), and the actual harness/provider/model route

## Acceptance Test Sketches

Detailed, authoritative acceptance criteria live with the feature specs in
`docs/helix/01-frame/features/FEAT-00X-*.md`. The sketches below remain the
product-level smoke matrix; feature-level AC should be used when creating
tests, review findings, or execution beads.

| Requirement | Scenario | Input | Expected Output |
|-------------|----------|-------|-----------------|
| Agent loop basics | Simple file read task | "Read main.go and tell me the package name" | `DrainExecuteResult` with status=success and output containing the package name |
| Tool: edit | Find-replace in file | Prompt to rename a function | File on disk is modified, result shows edit tool call |
| Tool: bash | Run tests | "Run `go test ./...` and report results" | Result includes test output, exit code |
| LM Studio provider | Connect to local model | Prompt with LM Studio running Qwen 3.5 | Successful completion with token count |
| Iteration limit | Prevent runaway | Max 3 iterations, task needs 10 | `DrainExecuteResult` has failure status after 3 rounds |
| Structured I/O | Structured envelope | JSON envelope with prompt and response_schema | Drained final projection matches schema |
| Token tracking | Count tokens | Any successful completion | Drained final usage has non-zero input and output counts |
| Session logging | Run any task | Any successful completion | JSONL log entry with full prompt, response, tool calls, tokens, timing |
| Session replay | Read logged session | `fiz replay <id>` on a completed session | Human-readable dump of every turn, tool call, and result |
| Cost tracking | Run cloud task with billed cost returned | Claude or gateway completion with reported billing | `DrainExecuteResult.CostUSD` is present, matches the reported cost, and carries `CostSourceReported` |
| Cost tracking | Run task without pricing data | Unconfigured runtime or provider with no reported billing | `DrainExecuteResult.CostUSD` is absent and carries `CostSourceUnknown` (unknown, not guessed) |
| Standalone CLI | End-to-end | `fiz -p "Read main.go"` with config file | Successful completion, session logged |
| Shared model catalog | Select policy and route | `fiz run --policy smart "hi"` | Concrete route selected from the catalog and live inventory for the requested policy |
| Harness path | Structured harness invocation | Prompt envelope or harness execution path (CONTRACT-003) | Machine-readable JSON output includes tokens, session ID, and cost semantics |
| Self-update check | Scripted version check | `fiz update --check-only` | Exit code reflects update availability and output shows current/latest versions |

## Technical Context

- **Language/Runtime**: Go 1.23+
- **Key Libraries**:
  - `github.com/openai/openai-go` — OpenAI-compatible API client (LM Studio,
    Ollama, OpenAI, Azure)
  - `github.com/anthropics/anthropic-sdk-go` — Anthropic Claude API client
  - Standard library for HTTP, JSON, process execution
- **Build**: `go build`, Makefile
- **Platform Targets**: Linux (primary — CI and build servers), macOS
  (development). Windows is not a priority.
- **Integration Point**: Callers embed the Go module and interact via CONTRACT-003

## Constraints, Assumptions, Dependencies

### Constraints

- **No CGo** — pure Go for easy cross-compilation and embedding
- **No TUI dependencies** — headless only, no terminal UI libraries
- **Minimal dependency footprint** — avoid large frameworks; prefer standard
  library + provider SDKs
- **Must be embeddable** — no global state, no init() side effects, all
  configuration via explicit parameters

### Assumptions

- LM Studio or Ollama is running locally when local models are requested
- Local models with tool-calling support (Qwen 2.5+, Llama 3.1+) are
  available and loaded
- The harness interface (CONTRACT-003) is stable enough to target

### Dependencies

- LM Studio daemon (`lms daemon up`) or Ollama service for local models
- Cloud API keys (Anthropic, OpenAI) for cloud provider support
- Go toolchain 1.23+

## Risks

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Local model tool calling unreliable | Medium | High | Test against specific model versions (Qwen 3.5, Llama 3.2); return route and failure evidence so the caller can retry or escalate with task context |
| LM Studio API breaks compatibility | Low | Medium | Pin to known-good LM Studio version; test in CI against local instance |
| openai-go SDK doesn't handle LM Studio edge cases | Medium | Medium | Thin adapter layer that can work around SDK limitations |
| Harness interface changes during development | Low | Low | Fizeau defines its own API first; the adapter is a thin shim |
| Local model context window too small for large tasks | Medium | Medium | Routing rejects ineligible contexts clearly; the caller may retry or escalate using its own task semantics |

## Resolved Decisions

- **Model loading**: Assume models are pre-loaded. Fizeau does not manage
  `lms load` / `ollama pull`. Model selection optimization is P2.
- **Routing**: Callers own semantic retry and cross-harness orchestration.
  Within the embedded harness, Fizeau owns policy/provider/model routing keyed
  by `Policy`, numeric power bounds, and exact hard pins.
- **Config**: Library takes a `Config` struct that any embedder provides. The
  standalone CLI has its own config reader. Library has no config file opinions.
- **File paths**: Allow paths outside working directory. Expectation is the
  agent runs in a sandbox. Log all file operations regardless.
- **Architecture**: Ghostty model — great library, proven by usable app.
- **Observability**: JSONL remains the replay artifact, while OTel is the
  canonical analytics surface. Report provider/gateway cost when available,
  otherwise use runtime-specific configured pricing or record cost as unknown.
  Do not guess cost from generic stale price tables.

## Open Questions

None at this time. JSONL is the local replay artifact, and OTel is the
canonical analytics surface per ADR-001.

## Success Criteria

- Fizeau library compiles with `go build` and has no CGo dependencies
- `fizeau.New(...).Execute(...)` can complete a file-read-and-edit task using
  LM Studio (or any local serving runtime) locally
- The same call can complete the same task against Anthropic, OpenRouter, or
  any cloud provider on the shared provider surface
- A caller (DDx, benchmark runner, or any embedder) can use Fizeau as an
  in-process harness via CONTRACT-003 without parsing private internals
- Per-turn timing (TTFT, prefill, decode), four-stream token usage, and
  known-or-unknown cost are reported for every successful execution on both
  local and cloud backends, conforming to CONTRACT-001
- Benchmark profiles that share a model but differ in harness produce a
  measurable delta attributable to harness; profiles that share a harness but
  differ in provider produce a measurable delta attributable to provider/runtime
