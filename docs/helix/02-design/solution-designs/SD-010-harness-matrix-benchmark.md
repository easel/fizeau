---
ddx:
  id: SD-010
  bead: agent-fab7feae
  created: 2026-04-29
  depends_on:
    - SD-008   # Terminal-Bench / Harbor integration audit
    - SD-009   # fiz Benchmark Mode
    - harness-matrix-plan-2026-04-29   # plan v7 (codex peer review v6)
---

> **⚠️ IMPORTANT (2026-05-16)**: The benchmark runner architecture changed on 2026-05-16. 
> This document describes the original multi-harness matrix design framework.
> See [ADR-016: Cells Are Self-Describing Evidence](../adr/ADR-016-cells-are-self-describing-evidence.md) 
> and [plan-2026-05-15-benchmark-runner-simplification.md](../plan-2026-05-15-benchmark-runner-simplification.md) 
> for the current architecture: the shift from `fiz-bench` (Go harness) to `./benchmark` (bash orchestration),
> the elimination of lanes/recipes/comparison-groups, and the transition to profile-only authoring.

---

# Solution Design: SD-010 — Multi-Harness × Model Matrix Benchmark

**Bead**: agent-fab7feae (Author SD-010 — Multi-Harness × Model Matrix Benchmark)
**Type**: Design spec (normative)
**Status**: Draft — superseded for official fiz-wrapper comparisons by
`terminalbench-fiz-wrapper-comparison-2026-05-06`; retained as implementation
reference for matrix runner mechanics, telemetry, aggregation, and publication
rules.

---

## 1. Summary

This spec originally governed the **multi-harness × model matrix benchmark**:
TerminalBench-2 runs that compare fiz against other CLI agent harnesses
(initial tranche: `pi`, `opencode`) on a shared anchor model and a shared task
subset, and that extend in later tranches to a second model and to
frontier-reference cells (`codex`, `claude-code`).

The official comparison architecture changed on 2026-05-06. Official
TerminalBench cells now run through one Harbor installed agent,
`scripts/benchmark/harbor_agent.py:FizeauAgent`, and select the execution path
with fiz pins such as `FIZEAU_HARNESS=claude`, `FIZEAU_HARNESS=codex`,
`FIZEAU_HARNESS=pi`, `FIZEAU_HARNESS=opencode`, or
`FIZEAU_PROVIDER=openrouter`. Raw per-harness Harbor adapters may remain as
diagnostics, but they are not the official comparison path.

This document remains normative for reusable mechanics only:

- matrix run state machine
- failure taxonomy
- aggregation outputs
- cost reporting
- reproducibility metadata
- same-model-different-harness caveats

**Relation to existing specs.** SD-010 *extends*, it does not replace:

- **SD-008** (Terminal-Bench / Harbor integration audit) governs the in-container
  `BaseInstalledAgent` integration path. SD-010 is amended into SD-008 §6
  ("Multi-Harness Extension") and reuses the in-container layout for every
  harness in the matrix. Harnesses that cannot be installed in-container are
  documented and dropped — never run host-side.
- **SD-009** (fiz Benchmark Mode) governs single-harness fiz
  evaluation. Its §5 thresholds (resolved-task floor 0.55, target 0.7) are
  **scoped to fiz's own runs only**. Cross-harness cells use **mean reward
  ± SD over reps (minimum 3 reps per cell)** and are not gated by those
  thresholds. SD-009 §7.1 cross-references SD-010 for the multi-harness
  publication policy and §9 lifts the resumability / failure taxonomy normatively.
- **FEAT-005** (Logging and Cost) is amended to acknowledge the four token
  streams (input, output, cached-input, retried-input) tracked under SD-010,
  and to source $-per-Mtok numbers from the SD-010 profile schema rather than
  an ad-hoc per-runner constant.

The ground truth for the architecture and decisions in this spec is the planning
artifact `docs/research/harness-matrix-plan-2026-04-29.md` (v7, codex peer
reviewed v2 + v6). That plan closes once the epic bead is filed; subsequent
revisions live here in SD-010.

**Central caveat (must restate in every published memo).** "Same model,
different harness" is *not* a clean control of model capability. Each harness
ships its own system prompt, tool schema, retry policy, reasoning effort,
context compaction, and default sampling. Matrix results compare *(harness
scaffolding + policy) over a shared model API*, not pure harness skill, and
not pure model skill.

---

## 2. Architectural Decisions D1–D6 (Normative)

The following decisions are lifted verbatim from `harness-matrix-plan-2026-04-29.md`
and are normative for every implementation bead under this spec.

### D1. In-container only

All harness cells run inside the upstream Harbor container. Mixing host-side
and in-container layouts contaminates the comparison. Harnesses that can't be
installed in-container are dropped from the matrix (documented in the OSS
harness install spike), not run host-side.

### D2. Single CLI entry: `fiz-bench matrix`

2026-05-06 amendment: the single entry still applies, but official cells should
pass only the `fiz` Harbor adapter/installed agent and vary the fiz target via
profile/env pins. Historical examples below that pass `--harnesses=fiz,pi,...`
are retained as diagnostic-run examples and must not be used for official
fiz-wrapper comparison claims.

A Go subcommand replaces the parallel-shell-scripts approach. It reuses the
existing TB subset loader and harness registry; it shells out to `harbor run`
once per run.

```
fiz-bench matrix \
  --subset=scripts/beadbench/external/termbench-subset-canary.json \
  --profiles=gpt-5-mini \
  --harnesses=fiz,pi,opencode \
  --reps=3 \
  --budget-usd=15 \
  --out=benchmark-results/matrix-<ts>/ \
  [--resume] [--force-rerun] [--retry-budget-halted]
```

No `--concurrency` flag in v1 — see D6.

### D3. Smoke vs matrix split

The existing `cmd/bench --external=termbench` path **survives** as
`--mode=smoke` — fast, ungraded, host tempdir, useful while iterating on
fiz code. The matrix subcommand is the **only** graded multi-harness
path. The smoke path MUST NOT be used to produce cross-harness comparison
memos.

### D4. Run state machine and telemetry schema

A run has three orthogonal axes; `final_status` is derived from them
deterministically. Adapters report the first two; the runner derives the
third.

```
process_outcome  ∈ {completed, timeout, harness_crash, install_failed, harness_refused, budget_halted}
grading_outcome  ∈ {graded, ungraded}                # ungraded = no verifier output
reward           ∈ {0, 1, null}                      # null iff ungraded

final_status (derived)
  = graded_pass            if grading_outcome=graded ∧ reward=1
  = graded_fail            if grading_outcome=graded ∧ reward=0
  = budget_halted          if process_outcome=budget_halted
  = install_fail_permanent if process_outcome=install_failed ∧ retriable=false
  = install_fail_transient if process_outcome=install_failed ∧ retriable=true
  = ran_ungraded           if grading_outcome=ungraded ∧ process_outcome=completed
  = <process_outcome>      otherwise                 # timeout, harness_crash, harness_refused
```

Telemetry schema each adapter MUST emit as part of its `report.json`:

```json
{
  "process_outcome": "completed|timeout|harness_crash|install_failed|harness_refused|budget_halted",
  "grading_outcome": "graded|ungraded",
  "reward": 0,
  "turns": 0, "tool_calls": 0, "tool_call_errors": 0,
  "input_tokens": 0, "output_tokens": 0,
  "cached_input_tokens": 0, "retried_input_tokens": 0,
  "wall_seconds": 0.0
}
```

`null` = unreported; the aggregator drops it from means (denominator shrinks),
reports `n/a` in `matrix.md`, and emits `n_reported` alongside. Adapters and
aggregator agree by construction — there is one source of truth (the two
outcome fields) and one derivation function.

### D5. Resumability

On `--resume`, a run is **skipped** when
`final_status ∈ {graded_pass, graded_fail, install_fail_permanent, budget_halted}`.
All other statuses retry on resume.

- `--force-rerun` overrides everything (rerun regardless of prior status).
- `--retry-budget-halted` retries only `budget_halted` cells (useful after
  raising a per-cell cost cap).

`budget_halted` is treated as a terminal status because retrying without
raising the cap is guaranteed to halt again; the operator must explicitly opt
in via `--retry-budget-halted` (or `--force-rerun`).

### D6. Concurrency = 1, hard

v1 of the matrix runner serializes runs. **No `--concurrency` flag.** Reasons:

(a) Docker resource pressure with multiple TB-2 containers running concurrently;
(b) provider rate limits hit faster than expected at concurrency > 1;
(c) the rate-limit-aware scheduler is its own design problem and shouldn't
    gate v1.

Concurrent execution is a follow-up bead under a separate epic.

---

## 3. Profile Schema (Normative, v1)

Profiles live under `scripts/benchmark/profiles/<id>.yaml`. The v1 schema is
frozen by this spec; additive fields require a spec amendment.

```yaml
id: gpt-5-mini
provider:
  type: openai-compat        # anthropic | openai | openai-compat | google
  model: gpt-5-mini
  base_url: https://openrouter.ai/api/v1
  api_key_env: OPENROUTER_API_KEY
pricing:
  input_usd_per_mtok: 0.60
  output_usd_per_mtok: 2.00
  cached_input_usd_per_mtok: 0.30
limits:
  max_output_tokens: 8192
  context_tokens: 200000
  rate_limit_rpm: 500
  rate_limit_tpm: 200000
sampling: { temperature: 0.0, reasoning: medium }
versioning: { resolved_at: 2026-04-29, snapshot: "" }
```

Field semantics:

- `id` — stable identifier used in matrix CLI flags and output filenames.
- `provider.type` — controls which client path the harness adapter takes when
  translating the profile into harness-native config (D4 forbids silent
  override).
- `provider.api_key_env` — the runner injects this env var into the Harbor
  `--env` set; it is not the literal key.
- `pricing.*` — single source of truth for cost reconciliation; the cost guard
  package (D6 of plan, §9 here) reads these to compute per-cell $.
- `limits.rate_limit_*` — informational in v1 (D6 forbids concurrency > 1);
  reserved for the follow-up scheduler bead.
- `sampling` — opaque to the runner; passed through to the adapter's
  `apply_profile` step.
- `versioning.snapshot` — the resolved-at-run-time provider model snapshot ID;
  filled in by the adapter at `apply_profile` time and round-tripped into
  `report.json`.

The Go loader at `internal/benchmark/profile/` is the canonical reader. A unit
test in that package MUST load every shipped profile.

---

## 4. Adapter Contract (Diagnostic Reference)

2026-05-06 amendment: this section is diagnostic reference for raw harness
adapters. Official TerminalBench comparison adapters are governed by
`terminalbench-fiz-wrapper-comparison-2026-05-06`, which routes all official
Claude, Codex, pi, opencode, and provider-path cells through `FizeauAgent`.

Adapters live under `scripts/benchmark/harness_adapters/`. There is **one
adapter file per harness**; env-var-based dispatch is forbidden. The cell
invocation passes the adapter module path explicitly to Harbor:

```
harbor run \
  --agent scripts/benchmark/harness_adapters/fiz.py:Agent \
  --task <task-dir> \
  --output <cell-out>/<task-id>/ \
  --env DDX_BENCH_PROFILE_PATH=<profile.yaml>
```

### 4.1 Required Python protocol

Each adapter declares:

```python
class Agent(BaseInstalledAgent):
    def install(self) -> None: ...
    def command(self, task_instruction: str) -> List[str]: ...
    def apply_profile(self, profile: dict) -> dict: ...     # YAML → harness-native config
    def parse_telemetry(self, trial_dir: Path) -> dict: ... # → D4 schema
    def redact_secrets(self, payload: dict) -> dict: ...
```

Constraints on `command()`:

- Non-interactive (no TTY).
- `stdin` is `/dev/null`.
- Exit code 0 = attempted; non-zero = trial failure (per SD-008).

Constraints on `apply_profile()`:

- May translate the profile YAML into harness-native config (env, CLI flags,
  config file).
- MUST NOT silently override the profile's `provider.model`, `sampling`, or
  `limits.max_output_tokens`. Any unavoidable translation lossiness is
  recorded in the adapter's returned config and surfaced in the cell's
  `report.json` `adapter_translation_notes` field.

Constraints on `parse_telemetry()`:

- Output MUST conform to D4's schema. Fields the harness does not surface are
  emitted as `null`, not zero. The aggregator distinguishes "unreported" from
  "zero".

### 4.2 Adapter unit-test requirement

A `FakeProvider` test helper at
`scripts/benchmark/harness_adapters/_test/fake_provider.py` returns canned
responses; adapters under test point at it via `base_url`. The helper is
shared across adapter tests, not reimplemented per adapter.

Each adapter ships a pytest module that asserts, **without invoking the live
harness or any paid API**:

1. `command()` returns the expected non-interactive argv.
2. `apply_profile()` produces the expected harness-native config and never
   silently rewrites `provider.model` / `sampling` / `limits`.
3. `parse_telemetry()` emits a D4-conformant object on a fixture trial dir.
4. `redact_secrets()` removes any value drawn from `provider.api_key_env`.

Adapter unit tests run on every PR (CI bead NEW17). New adapters are not
mergeable until their test module passes.

---

## 5. Failure Taxonomy and State Machine (Normative)

### 5.1 Eleven persisted statuses

The complete enumeration of values that may appear as the persisted status in
a cell's `report.json`:

```
install_fail_permanent | install_fail_transient | auth_fail | provider_refusal |
timeout | malformed_command | verifier_fail | harness_crash | budget_halted |
ran | graded_pass | graded_fail
```

`graded_pass` and `graded_fail` are the two graded terminal statuses; `ran`
denotes a completed-but-ungraded run; the remaining nine cover process and
harness failure modes.

### 5.2 (process_outcome, grading_outcome, reward) → final_status

The state machine is a pure function from the three D4 axes to `final_status`:

| process_outcome | grading_outcome | reward | final_status            |
|-----------------|-----------------|--------|-------------------------|
| completed       | graded          | 1      | graded_pass             |
| completed       | graded          | 0      | graded_fail             |
| completed       | ungraded        | null   | ran (= ran_ungraded)    |
| timeout         | *               | *      | timeout                 |
| harness_crash   | *               | *      | harness_crash           |
| install_failed  | *               | *      | install_fail_permanent  |
|                 |                 |        | / install_fail_transient (per `retriable` flag) |
| harness_refused | *               | *      | harness_refused         |
| budget_halted   | *               | *      | budget_halted           |

Two statuses in §5.1 — `auth_fail`, `provider_refusal`, `malformed_command`,
`verifier_fail` — are **specializations** that an adapter MAY emit instead of
the broader `harness_crash` / `harness_refused` / `ran_ungraded` when it can
discriminate the cause. They are persisted as-is in `report.json`. The
aggregator treats every non-`graded_*` status uniformly: dropped from mean
reward, itemized in `n_reported`, surfaced in `matrix.md`.

### 5.3 Retriability

`retriable` is set by the adapter on `install_failed`. Examples:
network-flaky package install → retriable; license/ToS rejection → permanent.
The default is `retriable=false` if the adapter does not set it.

### 5.4 Resumability policy (cross-reference D5)

See §2 D5. Restated normatively:

- Skip on resume: `graded_pass`, `graded_fail`, `install_fail_permanent`,
  `budget_halted`.
- Retry on resume: every other status.
- Override flags: `--force-rerun` (everything), `--retry-budget-halted`
  (`budget_halted` only).

---

## 6. Aggregator Output Format (Normative)

`fiz-bench matrix-aggregate <out>/` produces three files in the matrix
output directory.

### 6.1 `matrix.json`

A complete, lossless dump: every cell, every rep, every task, every D4 field.
Schema is the union of:

- Run-level objects keyed by `(harness, profile_id, rep, task_id)`, each
  carrying the full D4 telemetry plus `final_status` and `adapter_translation_notes`.
- Cell-level objects keyed by `(harness, profile_id)`, carrying derived
  `mean_reward`, `sd_reward`, `n_runs`, `n_reported`, and `cost_usd`.
- Matrix-level metadata: pinned harness commits, harness CLI versions, profile
  YAML hashes, resolved model snapshot IDs, dataset commit, Harbor commit,
  Docker image digests, runner commit, timestamp.

`matrix.json` is the source-of-truth artifact. Memos cite it directly.

### 6.2 `matrix.md`

Human-readable summary in this exact shape:

```markdown
## Reward (mean ± SD across N reps)

| Harness     | gpt-5-mini      | claude-code-anchor |
|-------------|-----------------|--------------------|
| fiz   | 0.67 ± 0.33     | —                  |
| pi          | 0.33 ± 0.47     | —                  |
| opencode    | 0.50 ± 0.41     | —                  |
| claude-code | —               | 0.83 ± 0.24        |

## Per-task pass count (out of N reps)

| Task                       | fiz / gpt-5-mini | pi / gpt-5-mini | opencode / gpt-5-mini |
|----------------------------|------------------------|-----------------|------------------------|
| hello-world                | 3/3                    | 3/3             | 3/3                    |
| log-summary-date-ranges    | 2/3                    | 0/3             | 1/3                    |
| git-leak-recovery          | 1/3                    | 0/3             | 0/3                    |

## Costs

| Cell                       | Input tok | Output tok | Cached tok | Cost ($) |
|----------------------------|-----------|------------|------------|----------|
| fiz / gpt-5-mini     | 1.2M      | 0.4M       | 0.3M       | 1.42     |

## Non-graded runs

| Cell / rep / task          | final_status        | cause                         |
|----------------------------|---------------------|-------------------------------|
| pi / gpt-5-mini / 2 / git-leak-recovery | budget_halted | per-run cap $1.00 hit        |
```

The "Non-graded runs" section is mandatory whenever any cell has
`n_reported < n_runs`.

### 6.3 `costs.json`

Per-cell breakdown:

```json
{
  "cells": [
    {
      "harness": "fiz",
      "profile_id": "gpt-5-mini",
      "input_tokens": 1234567,
      "output_tokens": 456789,
      "cached_input_tokens": 345678,
      "retried_input_tokens": 12345,
      "cost_usd": 1.42,
      "pricing_source": "scripts/benchmark/profiles/gpt-5-mini.yaml#sha256=..."
    }
  ],
  "matrix_total_usd": 8.13,
  "per_run_cap_usd": 1.00,
  "per_matrix_cap_usd": 32.40,
  "cap_derivation": "p95(observation_runs) × 2.0 safety; floor=$1.00, ceiling=$5.00"
}
```

The `pricing_source` field pins the profile YAML hash so the cost trail is
reproducible if profile prices are later edited.

---

## 7. Same-Model-Different-Harness Caveat (Publication Policy)

### 7.1 The required caveat block

Every memo that publishes any cell from a multi-harness matrix MUST include
the following caveat block, near the top of the memo body, under a heading
named "Caveat: same-model-different-harness comparison":

> Cells in this matrix that share a model column and differ only by harness
> row are **not a clean control of model capability**. Each harness ships its
> own system prompt, tool schema, retry policy, reasoning effort, context
> compaction strategy, and default sampling. The numbers below compare
> *(harness scaffolding + policy) over a shared model API*, not pure harness
> skill, and not pure model skill. Differences in scaffolding, prompt
> template, tool surface, and turn budget account for an unknown share of any
> observed delta. See SD-010 §2 D4 (telemetry schema), §5 (failure taxonomy),
> and §7 for the full obligations.

### 7.2 Cell-level rules

For every cross-harness cell in the memo:

1. **One harness binary per row, one model snapshot per column.** Each cell
   pins (harness commit, harness CLI version, profile YAML hash, resolved
   model snapshot ID, dataset commit, Harbor commit, Docker image digests).
   Mixing harness commits within a row, or model snapshots within a column,
   is not a valid comparison and the memo is not acceptance-grade.
2. **Identical adapter contract.** Every harness uses the §4 protocol and the
   §2 D4 telemetry schema. Numbers obtained under different scoring or
   telemetry pipelines are not comparable across harnesses.
3. **Minimum 3 reps per cell.** Cross-harness cells report **mean reward + SD
   across at least 3 reps**. SD is reported, not gated; cells with high SD
   are discussed in the memo rather than masked.
4. **Identical profile, no harness-side overrides.** See §4.1 — silent
   overrides forbidden.
5. **Failure handling.** Cells with `final_status` not in `{graded_pass,
   graded_fail}` are reported with cause and excluded from mean reward; the
   memo states `n_reported` per cell.

### 7.3 Acceptance-grade memo checklist

A memo is acceptance-grade only if:

1. Every run reaches a terminal `final_status` (one of the values in §5.1).
2. Non-`graded_*` runs are itemized in the memo with cause.
3. SD per cell is reported, not gated — high SD is discussed, not used to
   reject results.
4. Cost is reconciled to the observed token streams (input, output,
   cached-input, retried-input) per FEAT-005.
5. The §7.1 caveat block appears verbatim.

---

## 8. Frontier-Reference ToS / Fair-Use Policy

Frontier-reference cells (`codex`, `claude-code`) are **not OSS competitors**;
they are reference points to anchor harness ergonomics against the
vendor-native experience. This carries publication constraints that OSS
harnesses do not.

### 8.1 Internal vs public memos

- **Internal memos** (`docs/research/`, internal Slack/Linear, internal
  presentations): no ToS gating. Frontier-reference cells may be included
  freely once the adapter and run produce a `graded_*` outcome under the §4
  contract.
- **Public memos** (blog posts, papers, externally-shared decks, social
  posts, vendor-comparison marketing): require **prior ToS / fair-use
  review** for every frontier-reference harness in the result set, completed
  before the memo is shared outside the organization. The review records:
  - The harness's current Terms of Service URL and the version reviewed.
  - Whether benchmarking and public publication of results are permitted.
  - Any required attribution, disclaimer, or notification of the vendor.
  - The reviewer (named human) and date.

The review record is checked into `docs/research/` alongside the memo.

### 8.2 Attribution and naming

Public memos that cite frontier-reference cells use the vendor-canonical
product name and version (e.g. "Claude Code 1.x", "Codex CLI N.y") and link
the harness's release notes. fiz's own results are not branded as
"beating Claude Code" or similar; the central caveat (§1, §7.1) makes that
framing invalid.

### 8.3 Implementation gating

The frontier-reference adapter implementation work is intentionally **moved
to a follow-up tranche** (per the plan v7 codex-v6 cut). The initial tranche
under this spec covers fiz + pi + opencode only. ToS approval for
public memos is gated to the follow-up tranche where the adapters land; it
does not block the initial epic.

---

## 8.5 In-container install recipes (pi, opencode)

The Step 7 OSS harness install spike confirmed in-container installability,
non-interactive flags, license, and profile mapping for the initial OSS
tranche. These recipes are operationally load-bearing for any matrix run
involving `pi` or `opencode` and are normative for the adapter Dockerfiles
under `scripts/benchmark/harness_adapters/<harness>/`.

### pi (`@mariozechner/pi-coding-agent`)

| Field | Value |
|---|---|
| Pinned version | `0.67.1` |
| Install command | `npm install -g @mariozechner/pi-coding-agent@0.67.1` |
| Binary on PATH | `pi` (npm `bin` entry → `dist/cli.js`) |
| License | MIT — benchmarking permitted; public memos OK with repo + version cite |
| In-container `--help` exit | 0 in `node:22-slim` (verified 2026-04-29); same flow on `python:3.12-bookworm` |

**Non-interactive invocation**:

```sh
pi --mode json --print --no-session \
   --no-extensions --no-skills --no-prompt-templates --no-themes \
   --provider <provider> --model <model> \
   [--thinking <off|minimal|low|medium|high|xhigh>] \
   "<prompt>"   # prompt as last positional arg
# stdin: /dev/null
```

`--print` is the explicit non-interactive switch (no `isatty()` gate in
this mode). `--no-session` prevents writing session files into the task
workdir, which would otherwise leak across reps and trip TB-2 verifiers
that snapshot the workdir. The `--no-extensions --no-skills
--no-prompt-templates --no-themes` quartet keeps reproducibility across
developer machines and the matrix container.

**Custom base URL** is **not** wired as a CLI flag. The `openai-compat`
provider accepts `PI_OPENAI_COMPAT_BASE_URL` and `PI_OPENAI_COMPAT_API_KEY`
env vars; the adapter sets these from the profile's
`provider.{base_url, api_key_env}`.

**Drop-rule status**: KEPT — proceed to Step 8 adapter (matrix bead NEW15).

**Known limitations**: `sampling.temperature` and `limits.max_output_tokens`
are not exposed as CLI flags (provider default applies); the default model
is `google/gemini-2.5-flash` so the adapter MUST set `--provider` +
`--model` explicitly.

### opencode (`sst/opencode`)

| Field | Value |
|---|---|
| Pinned version | `1.3.17` |
| Install command (spike) | `curl -fsSL https://opencode.ai/install \| VERSION=1.3.17 bash` |
| Install command (adapter Dockerfile) | `curl -fsSL` the release archive directly from `github.com/sst/opencode/releases/download/v1.3.17/` and verify against the upstream-published `*.sha256` |
| Binary on PATH | `/root/.opencode/bin/opencode` |
| License | MIT — benchmarking permitted; public memos OK with repo + version cite |
| In-container `--help` exit | 0 in `node:22-slim` (verified 2026-04-29) |

**Non-interactive invocation**:

```sh
opencode run --format json --pure --mdns false \
   --hostname 127.0.0.1 --port 0 \
   --dir <task-workdir> \
   -m <provider>/<model> \
   [--variant <reasoning>] \
   --   "<prompt>"
# stdin: /dev/null
# env: OPENCODE_DISABLE_AUTOUPDATE=1, OPENCODE_DATA_DIR=<cell-tmp>, OPENCODE_CONFIG_DIR=<cell-tmp>
```

`run --format json` disables the spinner / TUI rendering paths; `run`
auto-approves all tool permissions so no extra permission flags are
needed. `--pure` bypasses external plugin discovery. `--mdns false` and
explicit hostname/port avoid surprising network exposure inside Harbor.
`OPENCODE_DISABLE_AUTOUPDATE=1` blocks the launch-time HTTPS update check.
`OPENCODE_DATA_DIR=<cell-tmp>` ensures rep-2 does not start with rep-1's
session cache (otherwise the matrix is contaminated).

**Custom base URL** is configured via `$OPENCODE_CONFIG_DIR/opencode.json`
(no CLI flag). The adapter `apply_profile()` writes a minimal config per
run; for OpenRouter the entry is the `@openrouter/ai-sdk-provider` plugin
with `apiKey: "{env:OPENROUTER_API_KEY}"`. Sampling temperature and
`max_output_tokens` are also config-only (no CLI), via
`provider.<id>.options.{temperature, maxTokens}`.

**Drop-rule status**: KEPT — proceed to Step 8 adapter (matrix bead NEW16).

### Drop rule (D1 restated)

Per §2 D1 ("In-container only"), harnesses that cannot be installed
in-container under Harbor are dropped from the matrix and documented —
not run host-side. Both `pi` and `opencode` clear this gate. `forge`,
`codex`, and `claude-code` are out of scope for the initial tranche per
the plan v7 codex-v6 cut and live in the follow-up epic; the same
in-container `--help` gate is re-applied to each before adapter beads in
that epic are claimed.

### Source provenance

In-container install commands, non-interactive flags, profile mapping,
custom-base-url plumbing, license terms, and known limitations extracted
from `docs/research/oss-harness-install-2026-04-29.md` (sections "pi",
"opencode", "Dropped harnesses", and "Acceptance evidence", 2026-04-29).
Folded into SD-010 because the install recipes are operationally
load-bearing for the matrix runner and belong with the adapter contract
(§4); a sibling note would split the operational story across two files.

## 9. Open Questions

### 9.1 Anchor model selection (deferred to NEW6 census)

The Phase A.1 anchor model is **not pinned by this spec**. The selection is
deferred to the model-census deliverable (`docs/research/model-census-2026-04-29.md`,
bead NEW6 of the matrix epic) and the user's sign-off on the (anchor,
second-model) pair surfaced by that census.

The census applies the plan v7 selection-fallback hierarchy:

1. **Tool-use viable** — supports tool / function calling at a quality bar
   usable for TB-2.
2. **In bracket** — output ≤ $3/Mtok.
3. **Recency** — released or refreshed ≥ 2025-11-01.

Failures cascade: rows that fail tool-use are excluded outright; rows that
fail bracket or recency are excluded unless a signed-off exception is
recorded. If after the filter no candidate exists for a vendor, that vendor
axis is dropped from Phase A — substitution with an out-of-bracket or
year-old model is forbidden.

The plan v7 working candidates (April 2026, **all subject to census
verification**):

- GPT-5.3-mini (likely Phase A.1 anchor pending census)
- Gemini 3 Flash (axis-diverse Phase A.2 second-model candidate)
- Refreshed Haiku (Phase A.2 candidate if a 2026 refresh exists in bracket)
- Qwen 3.6-plus / DeepSeek V3.2+ — reserved for Phase B (lesser-model pass)

#### Census exclusion rationale (lifted from `docs/research/model-census-2026-04-29.md` §3)

The hierarchy is applied in order **tool-use → bracket → recency**; the
first failing rule short-circuits the row. Failures cascade in order, so a
row that fails tool-use is reported as `tool-use: fail` and remaining gates
are not evaluated. The per-row outcomes from the 2026-04-29 census:

| Model | Tool-use viable | In bracket (≤ $3/Mtok output) | Recency (≥ 2025-11-01) | Outcome |
|---|---|---|---|---|
| GPT-5.3-mini | pass (strong) | pass (~$2.00) | pass (2026-Q1) | **KEEP** — eligible for Phase A |
| Gemini 3 Flash | pass (strong; verify parallel-call quality on OpenAI-compat path) | pass (~$0.50) | pass (2026-Q1) | **KEEP** — eligible for Phase A |
| Haiku 4.5 (refreshed 2026 snapshot) | pass | pass (pending verify; expected ≤ $3) | conditional — pass *iff* a 2026-Q1 refresh exists; fails recency if only 2025-10 snapshot exists | **KEEP-conditional** — Phase A only if NEW18 confirms the refresh snapshot |
| Qwen 3.6-plus (OpenRouter) | pass (acceptable; weaker than GPT-5.3-mini) | pass (~$0.50–1.00) | pass (2026-Q1) | **KEEP for Phase B** — reserved for the lesser-model pass; not a Phase A candidate |
| DeepSeek V3.2+ (OpenRouter) | pass (acceptable) | pass (~$0.30–1.00) | conditional (verify V3.2+ refresh date ≥ 2025-11-01) | **KEEP for Phase B** — alternate / reserve to Qwen |
| Sonnet 4.6 | pass | **fail** (~$15.00 ≫ $3) | pass | **EXCLUDE** — out of bracket; no signed-off exception requested |
| Opus 4.5 / 4.6 / 4.7 | pass | **fail** (≥ $15.00) | pass | **EXCLUDE** — out of bracket |
| GPT-5 (full) | pass | **fail** (> $5.00) | pass | **EXCLUDE** — out of bracket |
| GPT-4o | pass | fail (~$10.00) [short-circuited at bracket; recency would also fail] | n/e | **EXCLUDE** — out of bracket; also out by recency |
| GPT-4.1-mini | pass | pass (~$1.60) | **fail** (2025-04) | **EXCLUDE** — out by recency |
| Sonnet 4.5 (original) | pass | pass (~$3.00 boundary) | **fail** (2025-09) | **EXCLUDE** — out by recency |
| Haiku 4.5 (original 2025-10) | pass | pass (verify) | **fail** (2025-10 predates 2025-11-01) | **EXCLUDE** — out by recency unless a 2026 refresh is confirmed |
| Gemini 2.5 Flash | pass | pass | **fail** (2025-Q1) | **EXCLUDE** — out by recency |
| Qwen 3.5 / earlier | conditional pass (weaker tool-use) | pass | **fail** (2025-Q3 and earlier) | **EXCLUDE** — out by recency |

**Audit summary**: no row failed gate (1) tool-use outright. Exclusions are
entirely driven by gates (2) bracket and (3) recency. The recommended pair
from §5 of the census is `(GPT-5.3-mini, Gemini 3 Flash)` for vendor-axis
diversity; the alternate Phase A.2 is the refreshed-Haiku snapshot if the
NEW18 profile commit confirms the 2026-Q1 refresh, otherwise the Anthropic
axis is dropped from Phase A entirely (no substitution).

**Source provenance**: row-by-row exclusion rationale extracted from
`docs/research/model-census-2026-04-29.md` (§3 hierarchy outcomes + §4
audit trail + §5 recommended pair, 2026-04-29).

The smoke model (used to keep the rig green during Steps 1–8) is **exempt
from the recency rule**: its job is to make the path runnable, not to publish
numbers. It is committed alongside the implementation profile YAMLs; the
anchor and second-model profiles land separately under bead NEW18 after the
census closes.

### 9.2 Forge installability (re-spike under follow-up tranche)

The plan v7 codex-v6 cut **drops `forge` from the initial tranche** until the
OSS install spike (NEW14) confirms in-container installability and license /
benchmarking permission. Forge is not a pre-committed competitor under this
spec; it lives in the follow-up epic.

---

## Acceptance for SD-010 (this bead)

- Spec lands at `docs/helix/02-design/solution-designs/SD-010-harness-matrix-benchmark.md`.
- Sections 1–9 above are present and cover: summary + relation to SD-008/SD-009,
  D1–D6 architecture decisions, profile schema, adapter contract, failure
  taxonomy + state machine, aggregator output format, same-model-different-harness
  publication policy, frontier-reference ToS / fair-use policy, open questions
  (anchor model deferred to NEW6 census).
- Status flips to `Approved` after codex peer review and user sign-off on the
  open question in §9.1 (anchor model selection).
- Per HELIX evolve practice: **no implementation bead from the matrix epic is
  claimed before this bead closes.**
