---
ddx:
  id: SD-009
  bead: agent-82042311
  created: 2026-04-08
  depends_on:
    - SD-008 # Terminal-Bench/Harbor integration audit
    - benchmark-baseline-2026-04-08
  review:
    self_hash: 7f518a10f93a3bee6ab4e09dadb24b7bfd43822db3c0bbfed43de0abb664b83a
    deps:
      SD-008: 38357271f3c5c7c3da364497f94615698ec68c53a43e407c71ddf14c01b90b1a
      benchmark-baseline-2026-04-08: 3d28ddc0759f2a3ff403ba888b2377b60e3efa8e91d702fce86db25ee849f827
    reviewed_at: "2026-07-16T07:25:15Z"
---

> **⚠️ IMPORTANT (2026-05-16)**: The benchmark runner architecture changed on 2026-05-16. 
> This document describes the original benchmark-mode framework and execution model.
> See [ADR-016: Cells Are Self-Describing Evidence](../adr/ADR-016-cells-are-self-describing-evidence.md) 
> and [plan-2026-05-15-benchmark-runner-simplification.md](../plan-2026-05-15-benchmark-runner-simplification.md) 
> for the current architecture: the shift from `fiz-bench` (Go harness) to `./benchmark` (bash orchestration),
> the elimination of lanes/recipes/comparison-groups, and the transition to profile-only authoring.

---

## Reference Implementations

- **ForgeCode** (patch-based editing): `https://github.com/antinomyhq/forgecode`
  - Core patch logic: `crates/forge_services/src/tool_services/fs_patch.rs`
  - Clone locally: `git clone --depth 1 https://github.com/antinomyhq/forgecode.git /tmp/forgecode-study`
  - Key approach: search-and-replace with exact match, line-ending normalization,
    multiple operation types (replace, prepend, append, swap), snapshot coordination.
  - Clone locally: `git clone https://github.com/antinomyhq/forgecode`
# Solution Design: SD-009 — Fizeau Benchmark Mode and Terminal-Bench Evaluation Plan

**Bead**: agent-82042311 (Specify fiz benchmark mode and Terminal-Bench evaluation plan)
**Type**: Design spec
**Status**: Complete — grounded in SD-008 (interface audit) and the 2026-04-08 baseline

> **Update 2026-05-16**: ADR-016 ("Cells Are Self-Describing Evidence")
> supersedes the benchmark-mode execution model below. The `lane:`,
> `recipe:`, `resource_groups:`, `comparison_groups:`, `lane_aliases:`,
> and `profile_inventory:` blocks are gone; profiles are the sole
> authoring unit, bench-sets declare task lists, and cells embed the full
> resolved profile snapshot at write time. The runtime/preset and
> Harbor-integration substance below remains accurate. See
> [ADR-016](../adr/ADR-016-cells-are-self-describing-evidence.md) and
> [`plan-2026-05-15-benchmark-runner-simplification.md`](../plan-2026-05-15-benchmark-runner-simplification.md)
> for the migration plan.

---

## Summary

This document specifies how fiz is evaluated under Terminal-Bench/Harbor,
defines the benchmark-mode runtime/preset, commits a fixed benchmark task subset,
defines a smoke-task workflow, and sets measurable metrics with thresholds grounded
in the baseline characterization captured in `benchmark-baseline-2026-04-08.md`.

---

## 1. Terminal-Bench / Harbor Integration Path for Fizeau

See SD-008 for the full audit. Summary for this spec:

**Integration type**: `BaseInstalledAgent` — fiz is installed as a static
`linux/amd64` binary inside each Terminal-Bench task container. A Python adapter
(tracked in bead `agent-a3ce467a`) handles install, invocation, and ATIF trajectory
conversion.

**Invocation in container**:
```bash
fiz --json --preset benchmark -p "<task_instruction>"
```

**Exit contract**:
- Exit code 0 = agent attempted the task (Harbor reads reward from test suite)
- Exit code non-zero = trial failure (Harbor marks task as failed)
- Terminal-Bench verifier runs `pytest` against the modified workspace after exit

**No interface changes needed to the fiz CLI** for the basic installed-agent path.
The `--preset benchmark` preset is a new addition (bead `agent-5f35fdeb`) that suppresses
interactive behavior and reduces shell anti-patterns.

---

## 2. Benchmark-Mode Runtime / Preset Decision

### Decision: Add a `benchmark` preset; no separate binary

**Rationale**: The 2026-04-08 baseline (T6) showed two categories of behavior that
need tuning for unattended evaluation:

1. **Shell anti-patterns** (ls, find) for directory exploration when structured tools exist
2. **Edit tool format confusion** once in 6 tasks

These are prompt-level issues, not architectural ones. A dedicated `benchmark` system
prompt preset addresses both without introducing a separate binary or build variant.

**What the `benchmark` preset adds over `cheap`**:

| Behavior | cheap | benchmark |
|----------|-------|-----------|
| Discourages ls/find/cat for navigation | No | Yes — explicit rule |
| Edit tool format reminder | Implicit | Explicit example |
| No clarification questions | Not stated | Stated explicitly |
| Non-interactive completion | Implied | Stated: never ask, always attempt |
| Tool-first navigation | Implied | Explicit preference order |

**What stays the same**: Tool set, iteration limit, provider config, JSON output format.
The `--json` flag is still required separately (harness flag, not preset behavior).

**Implementation bead**: `agent-5f35fdeb` (Add benchmark-mode preset and non-interactive
completion behavior)

---

## 3. Fixed Benchmark Task Subset

### Subset Commitment

The benchmark subset is versioned and committed to the repo as a YAML manifest at:
`scripts/benchmark/task-subset-v1.yaml`

This file pins specific Terminal-Bench task IDs. The set is small enough to run in CI
or manually, but representative enough to detect regressions.

> **Amended 2026-05-14 (sweep schema v2, fizeau-596ff006):** the v1 subset is now one entry in the `subsets:` block of `scripts/benchmark/terminalbench-2-1-sweep.yaml`. The thresholds in §5 below remain scoped to the fiz-only run — formally `(subset=terminalbench-2-1-full, lane=fiz-only)` under the v2 schema. Subsets are now first-class and decoupled from lane/recipe membership; see [terminalbench-2-1-sweep-plan-2026-05-07.md §Subsets](../../../research/terminalbench-2-1-sweep-plan-2026-05-07.md#subsets).

**v1 subset** (15 tasks, grouped by capability area):

```yaml
# scripts/benchmark/task-subset-v1.yaml
# Fixed benchmark subset for fiz — v1 (2026-04-08)
# Do not modify without updating the version and filing a bead.
version: "1"
captured: "2026-04-08"
dataset: terminal-bench/terminal-bench-2

tasks:
  # File navigation and read (2 tasks)
  - id: tb2-read-and-describe
    category: navigation
    rationale: Baseline read capability; should always pass
  - id: tb2-find-and-summarize
    category: navigation
    rationale: Multi-file reading without bash anti-patterns

  # Targeted edits (3 tasks)
  - id: tb2-rename-symbol
    category: edit
    rationale: Covers edit tool single-occurrence correctness
  - id: tb2-add-function-signature
    category: edit
    rationale: Edit with import addition (multi-edit batching)
  - id: tb2-fix-type-error
    category: edit
    rationale: Edit with type signature mismatch

  # Error handling and guards (2 tasks)
  - id: tb2-add-error-guard
    category: edit
    rationale: Add guard clauses (mirrors baseline T3)
  - id: tb2-propagate-error
    category: edit
    rationale: Multi-site edit: add error return through call chain

  # Test writing (2 tasks)
  - id: tb2-write-unit-test
    category: test
    rationale: Write a test case from scratch
  - id: tb2-fix-failing-test
    category: test
    rationale: Diagnose + fix a failing test (multi-round)

  # Shell / bash (2 tasks)
  - id: tb2-rewrite-script
    category: bash
    rationale: Bash refactoring; shell anti-pattern detection
  - id: tb2-automate-task
    category: bash
    rationale: Write a new shell script from spec

  # Multi-file navigation (2 tasks)
  - id: tb2-cross-file-refactor
    category: navigation
    rationale: Read multiple files, apply consistent edits
  - id: tb2-trace-call-chain
    category: navigation
    rationale: Navigate a call chain without bash exploration

  # Compound (2 tasks)
  - id: tb2-implement-feature
    category: compound
    rationale: Read + edit + test in one task (hardest category)
  - id: tb2-debug-and-fix
    category: compound
    rationale: Identify and fix a bug via bash + edit
```

**Note on task IDs**: The IDs above are representative placeholders that map to the
actual Terminal-Bench v2 task catalog. The `agent-a3ce467a` bead that implements the
adapter will validate and pin the real task IDs from the live dataset.

**Evidence-grade comparison subset**: The first real-ID manifest is
`scripts/benchmark/task-subset-v2.yaml`. It is the correct subset for any
before/after claim. `task-subset-v1.yaml` is retained only as a historical design
artifact from the initial benchmark plan.

### Subset Versioning Policy

- Subset is frozen once pinned. Threshold regressions must be investigated, not
  worked around by swapping tasks.
- New subset versions (`v2`, `v3`) may be created when tasks are deprecated by Terminal-Bench,
  but are treated as a new baseline (fresh characterization run required).
- Expanding the subset requires a new bead and threshold calibration.

---

## 4. Smoke-Task Workflow

The smoke workflow validates that the fiz adapter runs to completion on a single
task before a full benchmark run. It should take under 2 minutes.

### Smoke-Run Steps

```bash
# Step 1: Build linux/amd64 binary
GOOS=linux GOARCH=amd64 go build -o dist/fiz-linux-amd64 ./cmd/fiz

# Step 2: Run one task from the fixed subset
harbor run \
  --dataset terminal-bench/terminal-bench-2 \
  --agent fiz \
  --task-id tb2-read-and-describe \
  --runtime docker \
  --env ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY}"

# Step 3: Verify
TRIAL=$(ls -t ~/.harbor/jobs/*/trials/ | head -1 | xargs dirname)
cat "${TRIAL}/verifier/reward.txt"         # expect: 0 or 1 (both are valid; 1 is pass)
cat "${TRIAL}/agent/trajectory.json" | python3 -m json.tool > /dev/null  # valid JSON
echo "Smoke run complete"
```

**Passing criterion for smoke run**:
- fiz exits with code 0 (no harness crash)
- `trajectory.json` is valid JSON with at least 1 step
- `reward.txt` exists (contains `0` or `1`)

A smoke run that produces `reward.txt = 0` is not a failure of the smoke workflow —
it means the agent didn't solve the task, which is separate from the harness working.

### Smoke Task Registration (scripts/benchmark/)

The adapter and smoke workflow scripts live in `scripts/benchmark/`:

```
scripts/benchmark/
├── harbor_agent.py          # BaseInstalledAgent adapter (agent-a3ce467a)
├── task-subset-v1.yaml      # Historical placeholder subset from initial design
├── task-subset-v2.yaml      # Real-ID evidence-grade comparison subset
├── smoke_run.sh             # Smoke run script
└── README.md                # How to run benchmarks
```

---

## 5. Metrics and Thresholds (Grounded in Baseline)

### Primary Metrics

| Metric | Definition | Collection |
|--------|------------|------------|
| Resolved-task rate | Fraction of subset tasks where `reward.txt = 1` | Harbor job results |
| Clarification-question rate | Fraction of trials where agent output contains a question before making tool calls | Trajectory analysis |
| Shell anti-pattern rate | Fraction of bash tool calls that are navigation anti-patterns (ls, find, cat) | Trajectory analysis |
| Structured-edit success rate | Fraction of edit tool invocations that succeed (non-error) | Trajectory analysis |

### Thresholds (Grounded in Baseline)

**Scope of thresholds.** The floor and target values in the table below
apply **only to fiz's own runs** under one fixed harness, runtime,
profile, and dataset. They were calibrated against a single-arm baseline
(fiz on `claude-haiku-4-5` via OpenRouter, 2026-04-08) and are not
valid as pass/fail gates for any other harness.

For **cross-harness cells** (e.g. fiz vs. pi vs. opencode under the
matrix benchmark in SD-010), reporting uses **mean reward + SD over reps
(minimum 3 reps per cell)**, not the floor/target. Reusing a single-arm
floor as a multi-arm pass criterion is an apples-to-oranges error: each
harness has different priors over the subset, and the threshold was never
calibrated against them. Cross-harness comparison rules are normative in
SD-010 §7 and the multi-harness extension in §7 below.

The 2026-04-08 baseline on 6 tasks with `claude-haiku-4-5` via OpenRouter:

| Metric | Baseline (6-task pilot) | v1 Regression Floor | Aspirational Target |
|--------|------------------------|--------------------|--------------------|
| Resolved-task rate (fiz only) | 100% (6/6 simple tasks) | ≥ 55% on v1 subset | ≥ 70% |
| Clarification-question rate | 0% | < 10% | < 5% |
| Shell anti-pattern rate | 50% of bash calls (T6 only) | < 30% of bash calls | < 10% |
| Structured-edit success rate | 75% (3/4 attempts) | ≥ 70% | ≥ 90% |

**Why these thresholds**:

- **55% resolved-task floor**: The pilot used simple tasks; Terminal-Bench v2 is harder.
  The PRD target is 70% for routine tasks on local models. 55% is the "something is
  severely broken" floor; 70% is the goal.
- **< 10% clarification rate**: Terminal-Bench tasks are non-interactive. The pilot
  showed 0%; the 10% floor accounts for edge cases on ambiguous instructions.
- **< 30% shell anti-pattern rate**: The pilot showed 50% (2/4 bash calls in T6 were
  anti-patterns), but all were navigation patterns that a benchmark-mode preset
  eliminates. After the preset is in place, this should reach < 10%. 30% is a floor
  for detecting regression before the preset is implemented.
- **≥ 70% edit success rate**: The 75% pilot value included one format-confusion failure.
  With the edit tool description clarified in the benchmark preset, this should reliably
  exceed 85%. 70% is the regression floor.

### Secondary Metrics (tracked but not thresholds)

| Metric | Collection | Purpose |
|--------|------------|---------|
| Avg wall-clock time per task | Harbor trial_result.json | Detect runaway loops |
| Avg tool calls per resolved task | Trajectory analysis | Efficiency tracking |
| Avg input tokens per task | Trajectory analysis | Cost projection |
| Task timeout rate | Harbor | Detect iteration limit issues |

---

## 6. Micro-Evals That Gate Regressions

These unit-level evals run in CI without Harbor and catch common failure modes:

| Micro-eval | What it tests | Passing criterion |
|------------|--------------|-------------------|
| Edit format correctness | Agent uses `old_string`/`new_string` not `edits[]` | edit tool succeeds on first attempt for simple rename |
| No-bash file read | Agent uses read tool, not bash ls/cat, to inspect a known file | 0 bash calls on pure read task |
| Error recovery | Agent recovers from an edit tool returning "not found" error | Task resolves within 2 extra turns |
| Clarification gate | Agent does not ask a question when the task is unambiguous | No `?` in first output on simple task |

Micro-evals are run via the existing virtual/dictionary provider (bead `agent-483477c7`)
to avoid live model costs.

---

## 7. Evidence-Grade Comparative Protocol

The original SD-009 deliverables established the benchmark harness, fixed subset
shape, baseline characterization, and benchmark-critical tools. A credible claim
that ForgeCode-inspired changes improved fiz's Terminal-Bench standing
requires a stricter comparative protocol than the original pilot baseline.

### Comparative claim

The claim under test is:

> ForgeCode-inspired harness and tooling changes improved fiz's measured
> performance on a fixed Terminal-Bench subset under one fixed harness and one
> fixed runtime/model configuration.

### Required controls

Any before/after comparison MUST satisfy all of the following:

1. **Pinned task subset**: comparison runs use a real-ID subset manifest
   (`task-subset-v2.yaml` or later). Placeholder-ID manifests are not valid for
   evidentiary comparison.
2. **Pinned SHAs**: the exact `before_sha` and `after_sha` are chosen before the
   run and recorded in the benchmark artifact.
3. **One harness for both sides**: the benchmark runner, Harbor adapter, scoring
   code, and report schema are identical across the before/after runs. Do not
   compare "old agent + old runner" versus "new agent + new runner".
4. **Pinned runtime configuration**: provider route, exact model, preset/system
   prompt, tool surface, Harbor runtime, and dataset version are fixed across
   both sides and captured in a checked-in config artifact.
5. **Predeclared scoring rules**: metric definitions are written before the run,
   not inferred after inspecting results.

### Evidence-grade execution order

1. Create a real-ID benchmark subset manifest (`task-subset-v2.yaml`) and record
   the task selection rule.
2. Upgrade the benchmark harness so one runner can execute arbitrary fiz
   binaries from different SHAs while preserving identical reporting and scoring.
3. Extend the report and scoring pipeline to capture actual runtime metadata and
   compute the declared comparison metrics.
4. Pin the comparison SHAs and checked-in benchmark config.
5. Run the benchmark on the `before_sha`.
6. Run the benchmark on the `after_sha`.
7. Compare the two reports and publish a memo from the recorded artifacts.

### Metric definitions for comparison runs

The comparison memo MUST report these metrics using predeclared scoring rules:

| Metric | Operational definition |
|--------|------------------------|
| Resolved-task rate | Fraction of tasks where Harbor reward is exactly `1` |
| Clarification-question rate | Fraction of trials where the first agent message before tool use asks the user for clarification or defers pending user input |
| Shell anti-pattern rate | Fraction of bash tool calls used for repository/file navigation when structured tools should suffice (`ls`, `find`, `cat`, ad hoc `grep`/`rg` discovery, similar shell-only exploration) |
| Structured-edit success rate | Fraction of structured edit/patch tool calls that return success rather than error |

The implementation MUST encode these definitions in scoring code or fixtures.
They are not left to memo-time interpretation.

### Validity notes

- Creating `task-subset-v2.yaml` is required because `task-subset-v1.yaml`
  contains representative placeholder IDs and is therefore not a valid
  benchmark subset for before/after evidence.
- If repeated runs are not feasible, the comparison memo MUST explicitly state
  that the results are single-run and therefore subject to model variance.
- If a subset version changes, the run establishes a new baseline and MUST NOT
  be compared numerically against older subset versions without that caveat.

### 7.1 Multi-harness extension (cross-reference SD-010)

The protocol above governs **single-harness** before/after claims about
fiz. The multi-harness × model matrix benchmark — comparing
fiz to other CLIs (pi, opencode, claude-code, codex) on the same
model and subset — is specified in
`docs/helix/02-design/solution-designs/SD-010-harness-matrix-benchmark.md`.
SD-010 is normative for any cell that involves more than one harness;
the rules in this subsection are a summary of the obligations SD-009
inherits when the matrix runner is invoked.

**Same-model, different-harness comparison rules.** When publishing a
matrix cell that compares two or more harnesses on the same model and
profile:

1. **One harness binary per row, one model snapshot per column.** Each
   cell pins (harness commit, harness CLI version, profile YAML hash,
   resolved model snapshot ID, dataset commit, Harbor commit, Docker
   image digests). Mixing harness commits within a row, or model
   snapshots within a column, is not a valid comparison.
2. **Identical adapter contract.** Every harness uses the SD-010 adapter
   protocol (`install`, `command`, `apply_profile`, `parse_telemetry`,
   `redact_secrets`) and the same telemetry schema (D4 in SD-010 / §9
   below). Cross-harness numbers obtained under different scoring or
   telemetry pipelines are not comparable.
3. **Minimum 3 reps per cell.** Cross-harness cells report **mean
   reward + SD across at least 3 reps**, not the SD-009 §5 floor/target.
   SD is reported, not gated; cells with high SD are discussed in the
   memo rather than masked.
4. **Identical profile, no harness-side overrides.** A harness adapter
   may translate the profile YAML into harness-native config but MUST
   NOT silently override sampling, model, or limits. Any unavoidable
   translation lossiness is recorded in the cell's `report.json` and
   carried into the memo's caveat block.
5. **Required caveat block.** Every cross-harness memo MUST include the
   caveat block defined in SD-010 §7: same-model-different-harness is a
   *harness ergonomics* comparison, not a *model capability*
   comparison; differences in scaffolding, prompt template, tool
   surface, and turn budget account for an unknown share of the delta.
   Memos that omit the caveat block are not evidence-grade.

**Failure handling.** Cross-harness cells use the failure taxonomy and
state machine in §9 below (lifted from SD-010). A cell whose
`final_status` is not `graded_*` is **reported with cause and excluded
from mean reward**; the memo states `n_reported` per cell so the reader
can see how many reps actually scored.

**Pointer for the running runner.** The `fiz-bench matrix`
subcommand specified in SD-010 is the only graded multi-harness path.
The legacy `cmd/bench --external=termbench` path remains as a
single-harness smoke (SD-010 D3) and MUST NOT be used to produce
cross-harness comparison memos.

---

## 8. Implementation Order

| Bead | Dependency | What it delivers |
|------|-----------|-----------------|
| `agent-a3ce467a` | SD-008 | Harbor Python adapter + smoke-run script |
| `agent-5f35fdeb` | This doc (§2) | `benchmark` preset |
| `agent-4dde1671` | This doc (§6) | Navigation tools + micro-evals |
| `agent-78c86322` | adapter + baseline | Automated baseline capture in CI |
| `agent-8e46e7e2` | This doc (§6) | Structured patch / exact-match edit evals |
| `agent-77d95bdc` | This doc (§6) | Task-tracking tools and planning evals |

---

## 9. Resumability and Failure Taxonomy (Normative)

This section is **lifted verbatim from SD-010 (D4, D5, and the failure
taxonomy)** and is normative for every benchmark run produced under
SD-009 — single-harness or multi-harness. Any deviation requires a
spec change to both SD-009 and SD-010.

### 9.1 Run state machine

A run has three orthogonal axes; `final_status` is derived from them
deterministically. Adapters report the first two; the runner derives
the third.

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

### 9.2 Telemetry schema (per-run, mandatory)

Every adapter MUST emit the following JSON object as part of its
`report.json`:

```json
{
  "process_outcome": "completed|timeout|harness_crash|install_failed|harness_refused|budget_halted",
  "grading_outcome": "graded|ungraded",
  "reward": 0,
  "turns": 0, "tool_calls": 0, "tool_call_errors": 0,
  "input_tokens": 0, "output_tokens": 0,
  "cache_read_tokens": 0, "cache_write_tokens": 0,
  "retry_accounting": {"retried_input_tokens": 0},
  "wall_seconds": 0.0
}
```

`null` = unreported; the aggregator drops it from means (denominator
shrinks), reports `n/a` in `matrix.md`, and emits `n_reported`
alongside. Adapters and aggregator agree by construction — there is one
source of truth (the two outcome fields) and one derivation function.

### 9.3 Failure taxonomy (11 statuses)

The complete enumeration of values that may appear as the persisted
status in a cell's `report.json`:

```
install_fail_permanent | install_fail_transient | auth_fail | provider_refusal |
timeout | malformed_command | verifier_fail | harness_crash | budget_halted |
ran | graded_pass | graded_fail
```

(`graded_pass` and `graded_fail` are the two graded terminal statuses;
`ran` denotes a completed-but-ungraded run; the remaining nine cover
process and harness failure modes.)

### 9.4 Resumability policy

On `--resume`, a run is **skipped** when
`final_status ∈ {graded_pass, graded_fail, install_fail_permanent, budget_halted}`.
All other statuses retry on resume.

- `--force-rerun` overrides everything (rerun regardless of prior
  status).
- `--retry-budget-halted` retries only `budget_halted` cells (useful
  after raising a per-cell cost cap).

`budget_halted` is treated as a terminal status because retrying without
raising the cap is guaranteed to halt again; the operator must
explicitly opt in via `--retry-budget-halted` (or `--force-rerun`).

### 9.5 Memo acceptance

A benchmark memo (single- or multi-harness) is **acceptance-grade only
if**:

1. Every run reaches a terminal `final_status` (one of the values
   listed in §9.3).
2. Non-`graded_*` runs are itemized in the memo with cause.
3. SD per cell is **reported, not gated** — high SD is discussed, not
   used to reject results.
4. Cost is reconciled to the observed runtime token streams (input, output,
   cache-read, cache-write) per FEAT-005. Retried-input accounting, when
   present, remains a separate derived benchmark metric.
5. The cross-harness caveat block from SD-010 §7 is included whenever
   more than one harness appears in the result set.

---

## 10. Failure Modes (Benchmark Preset)

The `benchmark` preset addresses four recurrent failure classes observed when
running fiz against terminal-bench-2. Each is named, given a detection
signal, and paired with the recovery rule the preset bakes in. Single-source
of truth for which guideline addresses which failure.

### FM-1 — Tool-Call Loop

- **Symptom**: Agent issues the same bash (or other tool) command three times
  in a row without adapting in response to the result.
- **Detection**: Existing fingerprint comparator in `internal/core/loop.go`
  raises `ErrToolCallLoop` at three identical consecutive tool calls (see
  AC-FEAT-001-09). The pivot-recovery semantics of FEAT-001 may inject a
  redirect message before the hard abort.
- **Recovery (preset rule)**: `ERROR RECOVERY` guideline — *if a tool call
  fails or returns an error, change your approach before retrying; never
  issue the same failing command twice in a row.*

### FM-2 — Missing Output File

- **Symptom**: Agent reasons through a solution inside `<think>` blocks,
  declares success, and never invokes `write` to materialize the required
  output file. The verifier scores `reward=0`.
- **Detection**: No in-loop signal — only the verifier sees it. The preset
  pre-empts the failure by enforcing a verification step.
- **Recovery (preset rule)**: `OUTPUT VERIFICATION` guideline — *before
  reporting task complete, confirm the required output file exists by
  reading it or running bash to check; do not declare success until the file
  is on disk.*

### FM-3 — Reasoning Stall

- **Symptom**: Agent fills context with extended thinking turns and never
  emits a tool call, exhausting either the iteration budget or the stall
  guard.
- **Detection**: `ErrReasoningStall` (AC-FEAT-001-08) and the byte-limit
  variant `ErrReasoningOverflow` (AC-FEAT-001-07) catch the runaway
  reasoning case at the streaming layer; the existing tool-call counter is
  blind to pure-thinking turns.
- **Recovery (preset rule)**: `ANTI-STALL` and `TOOL FIRST` guidelines —
  *if you have been thinking for more than one turn without calling a tool,
  call a tool immediately in your next response*; *think through your plan
  in at most one reasoning paragraph, then call tools.*

### FM-4 — No Verification

- **Symptom**: Agent writes the file but exits without running the provided
  test script, missing failures the test would catch.
- **Detection**: No in-loop signal; verifier-side. Preset bakes in the
  habit so the test gets run before exit.
- **Recovery (preset rule)**: `RUN THE TEST` guideline — *if a test script
  is mentioned or visible (e.g. `test_outputs.py`, `test.sh`), run it with
  bash as your final verification step before reporting done.*

### Source provenance

Failure-mode taxonomy and recovery guidelines extracted from
`docs/research/benchmark-preset-enhancement-2026-05-01.md` (Background and
Diagnosis section + the five preset guideline additions).

---

## 11. Planning Mode

Planning mode is a single pre-execution decomposition pass that runs **before**
the main tool loop. It is a pure quality knob — opt-in per request, auto-on
for the `benchmark` preset — and is degradation-safe: failures fall through to
normal execution.

### Trigger

Planning fires when either condition is true on the inbound `core.Request`:

```
planning := req.ToolPreset == "benchmark" || req.PlanningMode
```

The benchmark preset auto-enables planning because terminal-bench-2 tasks
benefit measurably from the decomposition pass. Ad-hoc callers opt in via the
`--plan` CLI flag (or `PlanningMode: true` on the service request).

### Behavior

One lightweight LLM call, no tools available, before the main `for iteration`
loop:

1. The agent issues a single `Provider.Chat` call with `tools = nil` and the
   task prompt wrapped in the planning template.
2. The plan response is appended to the conversation history as a
   `RoleAssistant` message wrapped in `<plan>` tags so subsequent turns and
   log replay can recognise it.
3. The standard `EventLLMRequest` / `EventLLMResponse` pair is emitted around
   the planning call (preserving the AGENTS.md convention that every provider
   call has a paired request/response event), followed by an
   `EventPlanningTurn` carrying the plan text, token usage, and resolved
   model name.
4. Plan tokens accumulate into `result.Tokens` for full cost accounting.
5. The main tool loop then runs normally with the plan already in context.

### Failure handling

If the planning call errors, the loop logs at `WARN` level and continues
without injecting a plan. Planning failures are never fatal — a degraded run
is preferable to a crashed run when the planning step is the only thing that
broke.

### Wire constraints

- `tools = nil` is what forces the no-tool decomposition. The provider sees
  no tool schema and the model has no apparatus to emit tool calls.
- The planning call MUST receive the same `req.SystemPrompt` so the model
  reasons inside the same behavioural framing as the main loop.
- The planning prompt asks for ≤150 words covering: files to read, changes
  required, failure modes and recovery, verification steps. It explicitly
  forbids questions and forbids beginning implementation.

### Telemetry

`EventPlanningTurn` (`type: "planning.turn"`) is the planning-specific log
event. Its `data` payload carries `plan` (string), `usage` (TokenUsage), and
`model` (string). The preceding `EventLLMRequest` carries `tools: null`,
which lets log-replay tools distinguish planning from normal turns without
parsing the prompt.

### Source provenance

Trigger condition, no-tools call shape, `<plan>`-tag injection, and
`EventPlanningTurn` schema extracted from
`docs/research/planning-mode-2026-05-01.md` (Recommendation, What Planning
Mode Does, Exact Code Changes, Session Log Representation).

---

## Open Questions

- [ ] **Real Terminal-Bench task IDs**: The `task-subset-v1.yaml` uses placeholder IDs.
  An evidence-grade comparison requires a new real-ID manifest (`task-subset-v2.yaml`)
  with a documented task selection rule.
- [ ] **Cloud runtime vs local Docker**: Harbor's cloud runtimes (Modal, Daytona) add
  latency. Initial evaluation should use local Docker (`--runtime docker`).
- [ ] **Local model evaluation**: The baseline used `claude-haiku-4-5` (cloud). A local
  model baseline (qwen3.5-27b via LM Studio) is needed to measure the "70% of routine
  tasks succeed on local 7B+" PRD success metric. This is a separate baseline run.
- [ ] **Commit-independent harness**: the benchmark runner and scoring path currently
  live in the agent repo and must be made commit-independent before a before/after
  comparison can be treated as evidence rather than mixed harness+agent drift.
