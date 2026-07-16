---
ddx:
  id: helix.product-vision
  review:
    self_hash: eb5af3663734d35e7b42963ce12e39adc19147aa2df25fe9bd3887793217836c
    deps: {}
    reviewed_at: "2026-07-16T07:25:15Z"
---
# Product Vision

## Mission Statement

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

The harness control implied by #1 is what enables #2 (we can instrument what
we own) and what makes #3 viable (one provider surface that abstracts cloud and
local equally). Tools built on fizeau inherit that without writing their own
loop, sampling controls, performance instrumentation, cost tracking, or
subscription accounting.

## Positioning

For **build-loop orchestrators, CI systems, benchmark harnesses, and any
tool that needs an instrumented tool-calling agent on its critical path**,
**Fizeau** is an **embeddable Go agent runtime library** with a measured loop
and a unified provider surface across cloud and local backends.

Unlike Claude Code, Codex, Pi, OpenCode, or Aider — which are standalone
processes invoked via subprocess — Fizeau ships as a Go package that runs
in-process, with one provider surface (OpenAI, Anthropic, OpenRouter, vLLM,
MLX, LM Studio, Ollama), first-class per-turn timing/cost/quota instrumentation,
and policy-based routing that picks the cheapest capable model for each task.

Fizeau can also wrap other agent CLIs through the `fiz-harness-*` lanes,
holding the model constant while varying the harness so callers can isolate
"is the loop hurting?" from "is the model hurting?"

## Vision

Tools that need an agent loop embed `fizeau.New(...)` instead of re-implementing
the loop, sampling controls, retry policy, compaction, session logging, cost
attribution, and subscription accounting. Every turn reports timing
availability and source; observable prefill, decode, TTFT, and tool-latency
windows are first-class values, while unavailable windows are explicitly
unknown. Prompts, agents, and providers can therefore be improved against real
data without invented measurements. Local models
sit on the same provider surface as cloud frontier models, so picking
self-hosted vs. cloud is a per-workload data question, not a religious one.

**North Star**: A benchmark harness, a build orchestrator, and a research
notebook all consume Fizeau as a library. Each one gets the same measured
loop, the same provider surface, the same JSONL session logs, and the same
cost/quota accounting — without anyone having to re-implement that
infrastructure. A delta between two runs that share a model but differ in
harness is unambiguously *harness loss*; a delta between two runs that share a
harness but differ in provider is unambiguously *provider/runtime loss*.

## Design Philosophy

Fizeau follows the ghostty model: build a great library, then prove it works
with a usable standalone app. The library (the `fizeau` Go package) is the
product. The CLI (`fiz` binary) is the showcase — a thin porcelain that
demonstrates the library works end-to-end and serves as an embeddable harness
backend for callers like DDx and the benchmark runner.

The runtime owns the harness. That ownership is the lever for the other two
pillars: we can instrument what we own (measurement), and we can hide
provider/runtime differences behind one surface (local-as-real-option).

## User Experience

A benchmark harness opens a lane definition that pins a particular model on a
particular provider, calls Fizeau in-process, and gets back per-turn timing,
known cost, token streams, and a JSONL session log it can replay. A build
orchestrator dispatches a coding task, Fizeau picks a `cheap` policy candidate,
runs it on a local Qwen via LM Studio, and returns structured results in 90s
at near-zero marginal cost — with the same instrumentation as the benchmark
run. A research notebook compares the same prompt across vLLM, oMLX, and
Anthropic, reading throughput and prefill/decode breakdowns out of the session
logs without bolting on a separate observability layer.

## Target Market

| Attribute | Description |
|-----------|-------------|
| Who | Tool builders who need an instrumented tool-calling agent on their critical path: benchmark harnesses, build orchestrators (DDx/HELIX-style), CI systems, evaluation pipelines, and embedded agent products |
| Pain | Re-implementing the agent loop, sampling, compaction, retry, session logging, cost attribution, and subscription accounting per project. Provider-by-provider integration drift. Observability bolted on after the fact. No honest path to compare local vs cloud. |
| Current Solution | Subprocess-spawn an existing CLI (claude, codex, pi, opencode), parse its output, accept whatever instrumentation it happens to expose. Or write a bespoke loop and rebuild observability from scratch. |
| Why They Switch | One embeddable loop with first-class measurement and one provider surface across cloud and local. They stop reinventing the harness. |

## Key Value Propositions

| Value Proposition | Customer Benefit |
|-------------------|------------------|
| Embeddable Go library — `fizeau.New(...).Execute(ctx, request)` | Zero subprocess overhead, direct state sharing, no output parsing |
| One provider surface, many backends (OpenAI, Anthropic, OpenRouter, vLLM, MLX, LM Studio, Ollama, native local) | Cloud and local on equal footing; deltas reflect provider/runtime, not harness drift |
| Per-turn timing, prefill vs decode, cost-per-trial as first-class outputs | Prompts, agents, and providers can be improved against real measurement |
| Subscription-quota accounting and route-attempt feedback | Honest cost/quota reporting across pay-per-token, fixed, and subscription billing |
| Harness-as-wrapper (`fiz-harness-*`) for Claude Code, Codex, Pi, OpenCode | Hold model constant, vary the harness — isolate loop loss from model loss |
| JSONL session logs, fully replayable | Every turn, tool call, and cost figure on disk; debuggable and re-renderable |
| Standalone `fiz` CLI proving the library | Usable porcelain validates the library end-to-end and serves DDx/CI as a harness backend |

## Success Definition

| Metric | Target |
|--------|--------|
| Embeddable adoption | At least one external tool (e.g., DDx, the benchmark runner) consumes Fizeau as a library through `CONTRACT-003` |
| Measurement coverage | Every `llm.request → llm.response` chain reports timing availability/source, emits supported TTFT/prefill/decode windows, and preserves known-or-unknown cost; no silent gaps |
| Local-vs-cloud parity | Local serving runtimes (vLLM, MLX, LM Studio, Ollama) and cloud frontier providers expose the same provider-shaped surface so benchmark deltas are honest |
| Loop overhead | <10ms beyond model inference time (no process spawn) |

## Why Now

Local models crossed the capability threshold for routine coding tasks in
2025-2026 — Qwen 3.5, Llama 3.x, and GLM-4.7 reliably handle file edits,
test fixes, and scaffolding with tool calling. Local serving runtimes (vLLM,
MLX, LM Studio, Ollama) are stable platform services. At the same time, every
agentic-development tool is rebuilding the same loop infrastructure
(tool-calling, retry, compaction, sampling, session logs, cost), and almost
none of them produce the per-turn measurement needed to honestly compare
prompts, agents, providers, or self-hosted vs cloud. Fizeau exists to be the
shared, measured, provider-agnostic loop those tools build on top of, instead
of each one re-implementing it.
