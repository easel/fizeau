---
ddx:
  id: SD-012
  depends_on:
    - FEAT-008
    - SD-009
  review:
    self_hash: bf30b7ba2939970dc4b6f94343403b37d72bc5626314a689682abcdd93d3acb2
    deps:
      FEAT-008: 7ccd6ca324a99e7e35a27f5cb3e7cdf9d591417327d101bf0f08dbe4e2e3d6f1
      SD-009: 7f518a10f93a3bee6ab4e09dadb24b7bfd43822db3c0bbfed43de0abb664b83a
    reviewed_at: "2026-07-14T05:16:22Z"
title: Benchmark Evidence Ledger and Derived Model Power
status: draft
updated: 2026-05-06
---

# Solution Design: SD-012 — Benchmark Evidence Ledger and Derived Model Power

> **Update 2026-05-16**: ADR-016 ("Cells Are Self-Describing Evidence")
> realizes the evidence-ledger principle below at the storage layer.
> Every cell embeds the full resolved profile snapshot at write time;
> ledger joins no longer require live profile YAML lookups. See
> `docs/helix/02-design/plan-2026-05-15-benchmark-runner-simplification.md`
> for the migration plan.

## Summary

Fizeau should treat benchmark observations as raw evidence, not as direct
catalog truth. Every benchmark runner should be able to emit normalized records
keyed by:

- model
- harness
- provider
- benchmark
- benchmark task or subset
- run environment and scoring metadata

Catalog `power` then becomes a derived projection over those records plus
cost, availability, recency, deployment class, and explicit override policy.
This preserves the distinction between model capability and model × harness ×
provider compatibility while still letting benchmark performance drive routing
strength over time.

## Problem

TerminalBench, MHI-style evals, SkillsBench, SWE-bench, HumanEval, MMLU,
TAU-bench, and project-local beadbench results all measure different things.
The concrete MHI source captured for this design is Rapid-MLX's Model-Harness
Index resource in `docs/resources/rapid-mlx-mhi-2026-05-06.md`; SkillsBench,
SWE-bench, and HumanEval are captured in sibling files under `docs/resources/`.
Some benchmarks are close to model-only capability. Others are explicitly model
× harness behavior. Current Fizeau catalog power stores a single integer with
provenance, but the raw evidence that led to the integer is not represented in a
common shape.

Without a raw evidence layer:

- TerminalBench leaderboard rows can be misread as model-only scores even when
  the row is really `Harness__Model`.
- Harness effects such as tool-call parsing, prompt discipline, retry behavior,
  and session logging get folded into model power without attribution.
- Cost, speed, quota, and availability signals are mixed with capability
  judgments in ad hoc ways.
- Recomputing model power after new benchmark runs requires reconstructing
  intent from benchmark-specific report files.

## Decision

Introduce a benchmark evidence ledger format. Existing benchmark-specific
outputs remain the source artifacts, but importers can project them into a
shared JSONL record type described by
`scripts/benchmark/benchmark-evidence.schema.json`.

The ledger is append-only evidence. It does not directly change routing.
Catalog generation or catalog-refresh tooling reads the ledger and computes
model-level derived power plus model × harness capability summaries.

## Record Identity

A single evidence record represents one aggregate or one atomic trial. Atomic
task-level records are preferred when available; aggregate records are allowed
when the upstream source only publishes aggregate scores.

Identity fields:

- `schema_version`: evidence schema version.
- `record_id`: stable content-derived identifier.
- `captured_at`: when Fizeau captured/imported the record.
- `source`: source runner or external publisher.
- `benchmark`: benchmark identity and version.
- `subject`: model, harness, provider, endpoint, and optional surface.
- `scope`: task, subset, split, repetition, and run identifiers.

Versioned identity is required for evidence that may feed FHI, catalog power,
or comparative claims. Records must capture, directly or through provenance:

- Fizeau version and git commit.
- Harness wrapper name/version and wrapped harness CLI/runtime version.
- Provider name, endpoint/API surface, deployment class, and provider version or
  capture timestamp.
- Model raw name, canonical catalog id when known, resolved snapshot/version
  when available, quantization/precision for local artifacts, and context limit.
- Benchmark name/version, scorer version, dataset commit, subset id/version, and
  runner/importer version.
- Local/self-hosted runtime version, model artifact id/checksum, OS/architecture,
  hardware class, accelerator backend, and utilization/capacity signals when
  available.

## Subject Semantics

`subject.model` is the canonical model identity if known. `subject.model_raw`
preserves the model string from the source. `subject.harness` and
`subject.provider` are never optional for agentic/tool benchmarks; if a public
source hides either value, it must be recorded as `unknown`, not omitted.

This prevents the common mistake of treating `ClaudeCode__GLM-4.7` as a GLM-only
score. It is a score for GLM-4.7 through Claude Code, on whatever provider and
version the source used.

## Score Semantics

The normalized `score` block carries the headline metric in a common form:

- `metric`: e.g. `pass_rate`, `reward_mean`, `accuracy`, `mhi`.
- `value`: normalized 0..1 score when possible.
- `raw_value`: original score if different, e.g. `92` for MHI on a 0..100 scale.
- `n`: number of trials or tasks represented.
- `passed`: count of passed trials when applicable.
- `failed`: count of failed trials when applicable.

Additional benchmark-specific dimensions live under `components`; examples:
`tool_call_success`, `terminalbench_pass_rate`, `humaneval_pass_rate`,
`mmlu_accuracy`, `median_wall_seconds`, `tool_calls_per_success`, and
`cost_per_success_usd`.

## Derived Power

Catalog `models.yaml` remains the compact routing surface. `power` is derived
from evidence, but the derivation is intentionally policy-owned:

1. Normalize each benchmark into capability dimensions.
2. Separate model-only evidence from model × harness evidence.
3. Weight reliable, harness-normalized agentic benchmarks heavily for coding
   agent routing. TerminalBench should become a major contributor once we have
   harness-normalized rows.
4. Apply deployment-class and availability guardrails. A local/community model
   should not receive managed-cloud frontier power from one benchmark alone.
5. Apply cost, quota, latency, and recency as routing utility signals or
   bounded power modifiers, not as silent replacements for capability.
6. Emit the resulting `power` and a compact `power_provenance` summary back into
   the catalog.

Power should therefore answer "how strong is this model for automatic routing?"
Model × harness evidence should answer "how strong is this combination in this
benchmark environment?"

## Derived Reports and Claim Grammar

The ledger is successful only if it can produce strong, reproducible claims from
raw evidence. Reports should support two families of statements:

1. Benchmark-specific comparative claims.
2. Cross-benchmark Fizeau Harness Intelligence claims.

Example benchmark-specific claim:

> fiz native with Opus 4.7 scores 81.0 on TerminalBench, 0.7 points below Claude
> Code with Opus 4.7 on the same subset.

Example FHI claim:

> Using fiz, the most effective model is Opus 4.7, with FHI 56.

Example local-vs-frontier FHI claim:

> With fiz 0.10 and oMLX 0.8.10, Qwen 3.6 27B 8-bit gets FHI 50, only 6
> points behind Opus 4.7.

These claims require more than headline scores. A claim generator must include
or be able to trace:

- exact Fizeau version or git commit
- exact harness name and harness version
- exact provider name, provider endpoint/API surface, and provider version or
  capture timestamp when no version is available
- exact model raw name, canonical model id when known, and resolved model
  snapshot/version when available
- deployment class, local runtime/server version, quantization, hardware
  profile, accelerator backend, context length, and reasoning/sampling controls
  for local or self-hosted stacks
- benchmark name, benchmark version, dataset commit, subset id/version, scorer,
  repetition count, and run timestamps
- score metric, normalized score, raw score, confidence interval or
  uncertainty note, and denominator
- invalid-run counts/classes and denominator handling
- source artifact paths and hashes, including session logs and upstream
  verifier outputs when available

### Benchmark-Specific Claims

Benchmark-specific reports compare rows only when the benchmark, scorer,
dataset/subset, model, and provider surface are intentionally controlled or when
the report states which axis is being varied.

For example, a valid TerminalBench harness comparison may vary only
`subject.harness` while holding model/provider/benchmark/subset constant:

```text
TerminalBench tb2-wide@<dataset_commit>, REPS=3
model=opus-4.7, provider=anthropic, benchmark=terminal-bench

fiz-native       81.0 ± 2.1
claude-code      81.7 ± 1.8
delta            -0.7
```

The report must not claim that a harness is better or worse when the rows also
vary model, provider, subset, scorer, or benchmark version unless the claim text
names those confounds explicitly.

### FHI Claims

Fizeau Harness Intelligence is a derived model × harness × provider metric. FHI
answers "how effective is this combination when driven through Fizeau's
execution surface?" It is separate from catalog model `power`, which answers
"how strong is this model for automatic routing?"

An FHI report may rank models within a fixed harness/provider surface:

```text
FHI for harness=fiz-native, provider=anthropic, evidence window=2026-Q2

opus-4.7         56
sonnet-4.6       49
gpt-5.4-mini     43
```

It may also rank harnesses within a fixed model/provider surface:

```text
FHI for model=opus-4.7, provider=anthropic, evidence window=2026-Q2

claude-code      57
fiz-native       56
fiz-claude       55
```

FHI derivation is policy-owned and must be versioned. Every FHI output includes
the FHI formula version, benchmark weights, included evidence window, included
benchmarks, excluded evidence with reasons, and confidence/coverage notes.

HumanEval, MMLU, and MHI-style components may contribute to FHI, but they must
not dominate long-horizon agentic evidence. TerminalBench, beadbench,
SkillsBench, and SWE-bench-style task outcomes should carry the primary weight
once enough harness-normalized rows exist.

### Deployment-Class Comparisons

FHI must support local-stack comparisons against frontier and non-frontier cloud
baselines. This is one of the primary product questions: whether local or
self-hosted deployments are close enough to frontier managed providers to be
useful under Fizeau.

Example valid claim:

```text
FHI formula=fhi/v1, evidence window=2026-Q2
benchmarks=terminalbench-wide, beadbench-v1, skillsbench-import

local stack:
  fiz=0.10
  harness=fiz-native
  provider=omlx
  provider_version=0.8.10
  model=Qwen 3.6 27B MLX 8-bit
  quantization=8-bit
  hardware=Mac Studio M3 Ultra 512GB

frontier baseline:
  harness=fiz-native
  provider=anthropic
  model=Opus 4.7

Qwen 3.6 27B 8-bit via oMLX    FHI 50
Opus 4.7 via Anthropic          FHI 56
delta                           -6
```

Deployment-class reports must group rows by at least:

- `managed_frontier`
- `managed_non_frontier`
- `local`
- `self_hosted`

Local/self-hosted rows require additional environment facts because those facts
are part of the capability surface:

- runtime/server name and version, e.g. `omlx 0.8.10`
- model artifact id/path or checksum when available
- quantization and precision
- hardware class, memory, accelerator backend, and OS/architecture
- endpoint type, e.g. OpenAI-compatible, Anthropic-compatible, native API
- context limit and reasoning/sampling controls actually applied
- utilization/capacity signals when available, such as active requests, queue
  depth, memory pressure, and tokens/s

The claim generator must not compare a local row to a frontier row unless the
included benchmark set, scoring formula, evidence window, and denominator rules
are identical. If one side lacks a benchmark component, the report either
computes a coverage-adjusted FHI with an explicit confidence penalty or refuses
the comparison.

## Initial Import Targets

Near-term importers should cover:

- `cmd/bench matrix` TerminalBench reports (`matrix.json`).
- Harbor job `result.json` and verifier reward files.
- beadbench `report.json`.
- public TerminalBench leaderboard reward cache.
- SkillsBench public rows or local SkillsBench reports.
- SWE-bench family leaderboard rows or task-level reports.
- HumanEval pass@k reports as low-cost coding/model-power components.
- MHI-style local eval reports when available.

The first MHI-style source to support is Rapid-MLX commit
`903487e82ad1998f0c20b721a7df66ec815ea673`, documented in
`docs/resources/rapid-mlx-mhi-2026-05-06.md`.

Benchmark-specific resource notes:

- `docs/resources/skillsbench-2026-05-06.md`
- `docs/resources/swebench-2026-05-06.md`
- `docs/resources/humaneval-2026-05-06.md`

Each importer should preserve source artifact paths and source hashes so a
ledger record can be traced back to the original run.

## Storage Policy

Raw benchmark output directories remain gitignored under `benchmark-results/`.
They may contain large logs, model outputs, tarballs, local auth-derived paths,
or upstream artifacts that should not be committed.

Curated evidence is stored separately:

- Local working ledgers: `benchmark-results/evidence/<run-id>.jsonl`.
- Checked-in small fixtures: `scripts/benchmark/testdata/evidence/`.
- Checked-in curated snapshots, when explicitly approved:
  `scripts/benchmark/evidence/<snapshot-id>.jsonl`.
- Human-readable summaries and claims: `docs/research/`.

Checked-in curated snapshots must contain normalized evidence records only, not
raw prompts, raw tool outputs, credentials, or large upstream artifacts. Every
record in a checked-in snapshot must include source artifact hashes or URLs so
the raw source can be audited outside git.

## Implementation Beads

The implementation work for this design is tracked under DDx epic
`fizeau-89e1e403` ("EPIC: Build FHI benchmark evidence pipeline"). Child beads
must stay scoped to one implementation layer each:

1. `fizeau-39b9669a` — extend and validate
   `benchmark-evidence.schema.json` for versioned axes, local-runtime metadata,
   session-log provenance, and invalid-run classes.
2. `fizeau-395d124d` — implement a ledger writer/importer CLI for appending
   validated JSONL records.
3. `fizeau-2c59b948` — import TerminalBench matrix outputs and link Fizeau
   session logs and Harbor verifier artifacts.
4. `fizeau-fde8c9bd` — import beadbench reports and DDx execute-bead evidence.
5. `fizeau-ff5fcc61` — import external benchmark rows/reports for Rapid-MLX
   MHI, SkillsBench, SWE-bench, and HumanEval.
6. `fizeau-d5e91885` — implement a versioned FHI formula and claim generator.
7. `fizeau-dcb621f8` — document one reproducible workflow from benchmark
   execution to ledger import to FHI/claim output.

Each child bead must name its in-scope files, out-of-scope files, and at least
one command-based acceptance criterion.

## Open Questions

- Exact power derivation weights. These should be data-driven once we have
  enough harness-normalized TerminalBench and local-model runs.
- Whether latency/cost should affect catalog `power` directly or only routing
  score. The current default should be conservative: keep power capability-led
  and use routing score for cost/latency preference.
