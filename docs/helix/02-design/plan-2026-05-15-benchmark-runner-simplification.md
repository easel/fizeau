---
ddx:
  id: plan-2026-05-15-benchmark-runner-simplification
  depends_on:
    - ADR-016
  review:
    self_hash: 18c1a107428efab189b5fd298ff898d195c3f70130ee856660f54c1f0e97bd40
    deps:
      ADR-016: ac33068859c7d86a3e11b375abe0e2a1e37175db533918c48a449dd370b8df72
    reviewed_at: "2026-07-14T20:00:14Z"
---
# Plan: Benchmark Runner Simplification

**Date**: 2026-05-15 (filed) / 2026-05-16 (rewritten)
**Status**: ACTIVE
**Governs**: [ADR-016](./adr/ADR-016-cells-are-self-describing-evidence.md)
**Related**: [ADR-015](./adr/ADR-015-browser-analytical-benchmark-workbench.md), [SD-009](./solution-designs/SD-009-benchmark-mode.md), [SD-010](./solution-designs/SD-010-harness-matrix-benchmark.md), [SD-012](./solution-designs/SD-012-benchmark-evidence-ledger.md)
**Rough size**: 8 PRs, ~3–5 days serialized

> The original 2026-05-15 version of this plan proposed moving execution
> to a shell driver while keeping recipes and lanes as orthogonal data.
> ADR-016 (2026-05-16) supersedes that with a deeper simplification:
> cells are self-describing evidence, profiles are the only authoring
> unit, recipes/lanes/comparison-groups/resource-groups are deleted.
> The shell-execution direction is preserved; the data model collapses.

## Problem

The TerminalBench runner has too many overlapping control surfaces, and
the data model itself forces multi-place duplication. See ADR-016 for
the full case. The user-facing symptoms:

- Adding a model requires edits in 3–4 places (profile YAML, lane block,
  recipe enrollment, shell alias case arm).
- A simple URL change requires updating 6 places that the validator
  (`cmd/bench/sweep.go:910-919`) keeps in lockstep.
- Historical cells reference profiles that no longer exist (663 orphan
  cell directories across 4 deleted profile IDs); a `PROFILE_ALIASES`
  rescue table at `build-benchmark-data.py:150` maintains the joins.
- `comparison_groups:` is referenced by zero analytics code.
- `cmd/bench` has become a second execution product parallel to `fiz`,
  with its own routing/configuration model.
- `--dry-run` at the shell layer can still perform expensive preparation
  before printing the runnable plan.
- Output directories can be created before any real cell starts, leaving
  confusing empty structures after failed setup.

## Target Architecture

### Data model

```
scripts/benchmark/
  profiles/<id>.yaml          # provider config + metadata + harness/surface/concurrency
  bench-sets/<id>.yaml        # framework, dataset, task list, default reps
  concurrency-groups.yaml     # rate-limit shard caps
  machines.yaml               # hardware reference (unchanged)
```

A **profile** is everything needed to invoke `fiz` once: provider
(type/model/base_url/api_key_env), sampling, limits, pricing,
`agent_timeout_multiplier`, `harness` (anthropic/codex/pi/opencode/none),
`surface` (`fiz_provider_native`/`fiz_harness_anthropic`/`native_cli`/…),
`concurrency_group` (rate-limit shard key), metadata (model_family,
model_id, quant_label, runtime, server, backend, …), versioning
(resolved_at, snapshot, snapshot_notes).

A **bench-set** declares what to run: framework, dataset, task list,
default reps, optional per-task timeouts. Bench-sets are *admin metadata
only* — they do not appear in cell records or output paths. The same
cell can be produced by any bench-set that enumerates the same task.

**Concurrency groups** are keyed by rate-limit shard (e.g.
`openrouter`, `sindri-gpu`, `bragi-gpu`), not by host. Each entry has
`max_concurrency`. Profiles join via `concurrency_group:`.

### Cell layout

```
benchmark-results/<canonical>/cells/
  <framework>-<dataset>/
    <task>/
      <cell-id>/                  # 20260516T103045Z-a4c1
        report.json               # embeds full resolved profile
        fiz.txt                   # raw fiz log
        session/                  # trajectory artifacts
```

Cell IDs are ISO-timestamp + short random suffix. Monotonic, unique,
race-free, no counter to track. Chronological sort gives history.

Cells embed the full resolved `profile:` block. Profile is the cell's
sole identity-of-record; the path's profile component is gone.

### Operator contract

```bash
./benchmark --profile P --bench-set B
./benchmark --profile P --bench-set B --plan
./benchmark validate
./benchmark preflight --profile P
./benchmark profiles                    # list available profiles
./benchmark bench-sets                  # list available bench-sets
```

Rules:

1. `--plan` is pure. It validates configuration and prints the matrix.
   No file writes, no Docker pulls, no preflight probes, no
   `benchmark-results/` directory creation.
2. The runner creates `benchmark-results/<canonical>/cells/<framework>-<dataset>/<task>/<cell-id>/`
   *only* when the cell is about to start.
3. `--profile` and `--bench-set` are both required (no implicit defaults
   for paid or local-heavy runs). Comma-separated lists allowed.
4. `validate` is fast and offline. Schema checks, file existence,
   profile-vs-concurrency-group cross-reference, bench-set task file
   existence. No network calls.
5. `preflight` is the *only* command that probes endpoints. Operators
   run it explicitly before sweeps.
6. Benchmark execution uses `fiz`, not `fiz-bench`. The
   `fiz-bench` binary survives only as analytics tooling:
   `fiz-bench aggregate`, `fiz-bench import-evidence`,
   `fiz-bench validate`. It does not own the cell execution loop.
7. The benchmark manifest the shell reads is the union of
   `profiles/*.yaml` and `bench-sets/*.yaml`. Manifest parsing happens
   in one place: a small Go helper (`fiz-bench plan --json`) emits
   JSON, the shell consumes JSON, never YAML.
8. Output goes under `benchmark-results/` unless `--out` overrides.

### Cell schema

```yaml
# report.json (illustrative — actual format is JSON)
task_id:        patch-build-script
framework:      terminal-bench
dataset:        terminal-bench-2-1
cell_id:        20260516T103045Z-a4c1
started_at:     2026-05-16T10:30:45Z
finished_at:    2026-05-16T10:38:22Z
fiz_tools_version: …

profile:                              # embedded resolved snapshot
  id:              vidar-qwen3-6-27b
  provider:        {type, model, base_url, api_key_env}
  harness:         none
  surface:         fiz_provider_native
  concurrency_group: vidar-gpu
  sampling:        {temperature, reasoning, top_p, top_k}
  limits:          {max_output_tokens, context_tokens}
  pricing:         {input_usd_per_mtok, output_usd_per_mtok, cache_read_usd_per_mtok, cache_write_usd_per_mtok}
  agent_timeout_multiplier: 8.0
  metadata:        {model_family, model_id, quant_label, runtime, server, backend, …}
  versioning:      {resolved_at, snapshot, snapshot_notes}

# existing metrics
turns:           …
input_tokens:    …
output_tokens:   …
cost_usd:        …
wall_seconds:    …
grading_outcome: pass
final_status:    completed
# …
```

No `bench_set_id`, no `lane_id`, no `recipe_id`, no `profile_id`-as-only-reference.

## Functionality Inventory

### Preserve

- TerminalBench task selection from bench-set task lists.
- Repetition count per bench-set (overridable via `--reps`).
- Resume / force-rerun / retry-invalid behavior (re-implemented in
  shell driver as "cell exists with `final_status` in {pass, fail,
  timeout} → skip" or "cell `final_status == invalid` → optionally
  rerun" depending on flag).
- Per-concurrency-group concurrency limits (replaces per-resource-group
  caps).
- Harbor task execution and grading. Harbor invokes FizeauAgent →
  FizeauAgent invokes `fiz` with profile-derived env.
- FizeauAgent forwarding of profile env into `fiz`.
- Per-cell `report.json`, `fiz.txt`/session logs, trajectory artifacts,
  runtime props.
- Invalid setup/provider/quota/auth classification.
- Consecutive identical failure abort (re-implemented as per-(profile,
  task) circuit breaker in shell driver, or as a post-processing
  audit).
- Preflight endpoint probe as an explicit command.
- Report aggregation and evidence import compatibility.
- Stale Harbor container cleanup, if still needed after the shell
  process model is simplified.
- Atomic cell writes (`writeJSONAtomic` semantics).
- Process-tree reaping on SIGTERM.

### Replace

- Per-resource-group concurrency → per-concurrency-group concurrency
  (same caps, smaller config).
- `--phase`, `--subset`, `--all-recipes`, `--staged-recipes` flags →
  `--bench-set` flag.
- `fiz-bench sweep` / `fiz-bench matrix` execution commands →
  `./benchmark` shell driver.
- `fiz-bench lanes clone` → `cp scripts/benchmark/profiles/<src>.yaml
  scripts/benchmark/profiles/<dst>.yaml && $EDITOR
  scripts/benchmark/profiles/<dst>.yaml`. Profile is the unit.
- Lane aliases → drop. Profile filenames are stable identifiers.
- `PROFILE_ALIASES` / `EXCLUDED_PROFILES` in
  `scripts/website/build-benchmark-data.py` → cell-embedded metadata.
- Profile-ID join in analytics → embedded `cell.profile.*` reads.

### Retire

- `recipes:` block (replaced by `bench-sets/`).
- `lanes:` block (absorbed into profiles).
- `comparison_groups:` block (operators recover equivalence at query
  time via `profile.metadata.{model_family, surface, harness}`).
- `resource_groups:` block (replaced by `concurrency-groups.yaml`).
- `lane_aliases:` block (no aliases; profile filenames win).
- `profile_inventory:` block (one file per profile, glob the dir).
- `validateSweepLaneEnvMatch` and all lane-cross-validation in
  `cmd/bench/sweep.go:910-919`.
- `cmd/bench/lanes*.go` (entire subcommand family).
- Sweep YAML (`scripts/benchmark/terminalbench-2-1-sweep.yaml`)
  deleted entirely; its data redistributed across `profiles/` and
  `bench-sets/`.
- Budget-cap machinery in `cmd/bench/sweep.go:701-705` (yesterday's
  decision; preserved). The simplified runner is not responsible for
  spend caps.
- The `.lane_aborted/` marker convention (replaced by abort state
  scoped to (profile, task) or per-run state file).

## PR Sequence

PRs are sized to land independently; each leaves master runnable.

### PR 1a — Profile schema additions

- Add `harness`, `surface`, `concurrency_group` fields to
  `internal/benchmark/profile/profile.go` `Profile` struct (optional,
  `yaml:",omitempty"`).
- Populate values in every existing profile YAML based on current lane
  `lane_type` (→ `surface`) and current resource_group assignment (→
  `concurrency_group`). For the 6 wrapper-harness lanes
  (`fiz-harness-claude-sonnet-4-6`, etc.), the matching profile YAML
  already exists; populate `harness:` explicitly.
- No runner changes. Schema additions are silent at execution time.
- Tests: extend `internal/benchmark/profile/profile_test.go` for the
  new fields.

**Acceptance**: `go test ./internal/benchmark/profile/...` passes.
Every profile YAML loads with the new fields populated.

### PR 1b — bench-sets/ and concurrency-groups.yaml

- Add `scripts/benchmark/bench-sets/<id>.yaml` files, one per current
  recipe (`tb-2-1-canary`, `tb-2-1-full`, `tb-2-1-or-passing`,
  `tb-2-1-openai-cheap`, `tb-2-1-timing-baseline`, `tb-2-1-all`,
  `medium-comparison`). Each carries framework, dataset, task list
  (sourced from current `subsets:` block), default reps.
- Add `scripts/benchmark/concurrency-groups.yaml` translating each
  current `resource_groups:` entry to a `{id, max_concurrency}` pair.
- No code references yet. Pure data files.

**Acceptance**: Files load via a small validator (one-off, not
committed) that parses YAML and confirms task IDs exist in the harness
dataset.

### PR 1c — Runner writes self-describing cells

- Modify `cmd/bench/matrix.go` (around cell-write boundary at line
  ~751) to embed the full resolved profile snapshot into `report.json`
  instead of just `profile_id` + free-form `profile_snapshot` string.
- Add `framework`, `dataset`, `cell_id` fields to the cell record.
- `cell_id` generation: `time.Now().UTC().Format("20060102T150405Z") +
  "-" + shortRandom(4)`.
- Old `fiz-bench sweep` continues to work for the moment, but its
  cell writes embed metadata.
- Verify with smoke set: re-run a known cell, inspect the JSON, confirm
  embedded profile matches the source YAML.

**Acceptance**: Cells written by post-PR runner contain
`cell.profile.*` block with all profile fields. Old cells remain
loadable (analytics handles both shapes in PR 3).

### PR 1d — `./benchmark` shell driver

- Add `scripts/benchmark/benchmark` (or `scripts/benchmark/run`) shell
  script implementing the operator contract above.
- Add `fiz-bench plan --json` Go helper that emits the resolved cell
  list (profile × bench-set × tasks × reps) as JSON for the shell to
  consume. No execution responsibility; pure manifest reader +
  validator.
- Add `fiz-bench validate` and `fiz-bench preflight --profile P` as
  Go helpers if the shell needs them; otherwise shell does the checks
  directly.
- Shell owns: matrix expansion (from `plan --json` output), output
  path creation per cell, resume check (does `report.json` already
  exist with terminal `final_status`?), `fiz` / Harbor invocation,
  signal handling, process-tree reaping.
- The existing `cmd/bench/sweep` execution path continues to work in
  parallel until PR 4.

**Acceptance**: `./benchmark --plan --profile P --bench-set B` prints
the matrix and creates no files. `./benchmark --profile P --bench-set
B` runs cells against a smoke set and produces self-describing cell
records.

### PR 2 — Backfill existing cells

- One-shot script (`scripts/benchmark/backfill-cell-metadata.py` or
  similar). Walks `benchmark-results/**/cells/`.
- For each cell:
  - Read `profile_id` (and `profile_snapshot`).
  - Resolve to a current profile YAML, applying `PROFILE_ALIASES`
    rules from `build-benchmark-data.py:150`.
  - For the 4 deleted profiles, recover the YAML from git history
    (the commit that deleted it):
    - `sindri-club-3090`
    - `vidar-qwen3-6-27b-openai-compat`
    - `sindri-club-3090-llamacpp`
    - `gpt-5-3-mini`
  - Embed the resolved profile into the cell's `report.json`.
  - Add `framework`, `dataset` (from path), `cell_id` (mint a synthetic
    one with the cell's `started_at` timestamp).
  - Move the cell to the new path (`framework/task/cell-id/`).
  - If profile is irrecoverable: delete the cell directory.
- Idempotent re-run safe: already-backfilled cells (with embedded
  `cell.profile.*`) are skipped.
- Delete the 4 `.lane_aborted/` marker directories at
  `benchmark-results/fiz-tools-v1/cells/.lane_aborted/`.
- Delete recipe-named operational subdirs (`tb21-all/`, `or-passing/`,
  `local-qwen/`, `timing-baseline/`) under
  `benchmark-results/fiz-tools-v1/` — they contain per-lane index
  files now superseded by self-describing cells.

**Acceptance**: Post-backfill, every cell under
`benchmark-results/**/cells/` carries an embedded `profile:` block.
Zero cells reference a profile via `profile_id` only.
`scripts/website/build-benchmark-data.py` runs successfully against
the backfilled cells using *only* embedded metadata (verified by
temporarily commenting out `PROFILE_ALIASES`).

### PR 3 — Analytics read embedded metadata

- `scripts/website/build-benchmark-data.py`: replace profile-YAML
  joins with embedded `cell.profile.*` reads. Delete
  `PROFILE_ALIASES` (~line 150) and `EXCLUDED_PROFILES`.
- `scripts/benchmark/generate-report.py`: same switch. Delete
  lane-block reads from sweep YAML.
- `cmd/bench/terminalbench_import.go`: read embedded
  `cell.profile.versioning.snapshot` instead of looking up the
  profile YAML separately.
- Comparison classes computed at query time: `GROUP BY
  cell.profile.metadata.model_family, cell.profile.surface,
  cell.profile.harness`. No standalone comparison-groups file.

**Acceptance**: `python scripts/website/build-benchmark-data.py`
produces byte-identical output (modulo embedded metadata being the
source) compared to a pre-PR baseline run on the same cells. All
existing benchmark-page tests pass.

### PR 4 — Delete lane scaffolding

Strict prerequisite: PRs 1c, 1d, 2, 3 all landed and verified.

- Delete entire files:
  - `scripts/benchmark/terminalbench-2-1-sweep.yaml`
  - `cmd/bench/lanes.go`
  - `cmd/bench/lanes_clone.go` and tests
  - Any `cmd/bench/testdata/` fixtures referencing lanes
- Delete code within surviving files:
  - `cmd/bench/sweep.go`: `validateSweepLaneEnvMatch` and callers;
    `Lane` struct's metadata duplicates (`FizeauEnv`, `ModelFamily`,
    `ModelID`, `QuantLabel`, `ProviderSurface`, `Runtime`,
    `HardwareLabel`, `Endpoint`); resource-group concurrency wiring
    (replaced by concurrency-group lookup); recipe-block reading;
    comparison-group reading; lane-alias parsing
  - `cmd/bench/matrix.go`: lane-id handling at the cell-write
    boundary
  - `cmd/bench/sweep_test.go`: all lane-validation and lane-env-match
    test cases
- Rewrite `fiz-bench sweep` (if kept at all) as a profile × bench-set
  cross-product loop wrapping the shell driver. Process-tree reaping
  and resume logic that lived in `sweep.go` migrate to the shell
  driver.
- Drop CLI flags: `--phase`, `--subset`, `--all-recipes`,
  `--staged-recipes`, `--snapshot`, `--snapshot-suffix`.

**Acceptance**: `go test ./...` passes. `go build ./cmd/bench` works.
`./benchmark --profile P --bench-set B` runs cells equivalent to
the pre-PR sweep invocation.

### PR 4b — Zero-reference verification

- `rg -n 'lane_id|validateSweepLaneEnvMatch|FizeauEnv|PROFILE_ALIASES|EXCLUDED_PROFILES|resource_groups|comparison_groups|lane_aliases|recipes:'`
  across non-archival paths.
- Expected zero hits outside `.ddx/`, `docs/research/archive/`, and
  frozen `benchmark-results/` snapshots.
- Any hit is a deletion bug; fix in PR 4b or follow-up.

**Acceptance**: documented grep produces zero non-archival hits.

### PR 5 — Docs + shell cleanup

- Rewrite `scripts/benchmark/README.md` for new operator workflow.
- Delete `fiz-bench lanes clone` documentation references.
- Delete or rewrite `scripts/benchmark/run_terminalbench_2_1_sweep.sh`
  (its alias case arms, phase logic, Python YAML-scraping snippets).
  Replace with thin `./benchmark` driver.
- Update `scripts/benchmark/run_medium_model_terminalbench_comparison.sh`
  similarly.
- Update `docs/benchmarks/sections/{02,03,05}-*.md`,
  `docs/benchmarks/runtime-props.md`, and other public-facing
  benchmark docs for any stale lane/recipe vocabulary.
- Update ADR-015 if its workbench joins assumed profile-YAML lookups.
- Audit `docs/helix/02-design/solution-designs/SD-{009,010,012}` for
  benchmark-mode/matrix/evidence-ledger references to lane/recipe
  concepts; file follow-up amendments if needed.

**Acceptance**: `rg -i 'lane|recipe|comparison.group|resource.group|lane.alias'`
in `docs/` returns only intentional historical references (e.g.
ADR-016 itself explaining what was removed).

## Regression Guardrails

Before any deletion (PR 4), capture golden fixtures:

- **Plan parity**: `scripts/benchmark/testdata/plans/` directory with
  golden expected `--plan --json` outputs for representative
  invocations:
  - `--profile vidar-ds4 --bench-set tb-2-1-timing-baseline`
  - `--profile sindri-llamacpp,vidar-ds4 --bench-set tb-2-1-or-passing`
  - `--profile openai-gpt-5-5 --bench-set tb-2-1-all`
- **Command parity**: capture the effective `fiz` / Harbor command
  inputs for one local provider lane, one managed provider lane, one
  harness lane. Old runner and new runner must emit the same env +
  args for the same (profile, task).
- **Resume parity**: existing completed cells skip on rerun; invalid
  cells rerun only with `--retry-invalid`; `--force-rerun` ignores
  terminal reports.
- **Concurrency parity**: concurrency-group caps serialize endpoints
  the way resource-group caps did. Sanity: enumerate every current
  `resource_groups[]` entry, confirm the new `concurrency_group`
  equivalent has the same `max_concurrency`.
- **Telemetry parity**: cell directories still contain artifacts
  required by report generation and evidence import (`report.json`,
  `fiz.txt`, `session/`).
- **Signal parity**: SIGTERM to `./benchmark` reaps active child
  processes and leaves resumable cell state.

Golden fixtures live in `scripts/benchmark/testdata/plans/` and are
tested via `go test ./cmd/bench/...` after PR 1d.

## Migration Open Questions

These need answers before specific PRs start:

1. **Recipe → bench-set name mapping.** Current recipes:
   `canary`, `local-qwen`, `timing-baseline`, `or-passing`, `tb21-all`,
   `openai-cheap`, `sonnet-comparison`, `gpt-comparison`,
   `medium-model-canary`, `medium-model`. Bench-set IDs should keep the
   recipe ID where possible (`tb-2-1-canary`, `tb-2-1-or-passing`,
   etc.). Confirm any exceptions.

2. **Orphan profile recovery.** Pull from git log:
   - `sindri-club-3090` — last present at which commit?
   - `vidar-qwen3-6-27b-openai-compat` — same.
   - `sindri-club-3090-llamacpp` — same.
   - `gpt-5-3-mini` — same.
   PR 2 needs operator confirmation on the recovered YAML for each
   before backfill writes them into ~660 cells.

3. **Wrapper-harness profile naming.** Six lanes today carry
   `FIZEAU_HARNESS=<claude|codex|pi|opencode>` on top of a base
   profile. The matching profile YAMLs already exist
   (`fiz-harness-claude-sonnet-4-6.yaml`, etc.) but they don't
   currently declare `harness:` because the harness was a lane-env
   override. PR 1a explicitly adds `harness:` to those profiles.
   Confirm none are missing — `grep -l 'FIZEAU_HARNESS' terminalbench-2-1-sweep.yaml`
   should match an existing profile for every lane.

4. **DS4 MTP wiring.** Per the original 2026-05-15 plan, `vidar-ds4-mtp`
   is the test case for "is MTP a profile attribute, a request
   parameter, or a server startup mode?". Today the profile exists with
   `mtp: enabled` in its metadata block and a corresponding lane sets
   `FIZEAU_DS4_MTP=true`. PR 1a should confirm whether `mtp:` belongs
   in profile metadata, profile-level config, or something else.

## Non-Goals

- Do not change matrix report format beyond adding embedded `profile:`,
  `framework`, `dataset`, `cell_id`.
- Do not change evidence import contract.
- Do not run full TerminalBench as part of this cleanup.
- Do not add another lane-authoring layer before the runner contract is
  simple.
- Do not retain or add budget-cap machinery. The simplified runner is
  not responsible for spend caps.
- Do not leave two blessed benchmark execution paths after PR 4. During
  PRs 1c–3 there is a dual-run period; PR 4 ends it.
- Do not introduce presets, recipes, or comparison-groups as data files
  pending operator demand. Cell-embedded metadata + bash one-liners
  cover the use cases today.

## Implementation Order Summary

1. **PR 1a** — profile schema additions
2. **PR 1b** — bench-sets/ and concurrency-groups.yaml
3. **PR 1c** — runner writes self-describing cells
4. **PR 1d** — `./benchmark` shell driver
5. **PR 2** — backfill existing cells
6. **PR 3** — analytics read embedded metadata
7. **PR 4** — delete lane scaffolding (+ 4b zero-reference verification)
8. **PR 5** — docs + shell cleanup

PRs 1a, 1b are pure additions. PRs 1c, 1d, 2, 3 are reversible (old
path still works). PR 4 is the cliff edge — by then parity fixtures
guard the new path.

## Bead Breakdown Plan

This plan should be filed as one EPIC bead with eight child beads, one
per PR. Suggested IDs (assigned at filing):

- `EPIC: bench — cells are self-describing evidence` (this plan)
  - `bench: add harness/surface/concurrency_group to profile schema` (PR 1a)
  - `bench: add bench-sets/ and concurrency-groups.yaml` (PR 1b)
  - `bench: runner embeds full profile snapshot in cells` (PR 1c)
  - `bench: ./benchmark shell driver` (PR 1d)
  - `bench: backfill existing cells with embedded metadata` (PR 2)
  - `bench: analytics read embedded cell metadata` (PR 3)
  - `bench: delete lane scaffolding` (PR 4 + 4b)
  - `bench: docs and shell cleanup` (PR 5)

Each child bead's acceptance criteria match the PR's "Acceptance"
section above. Parity fixtures land as part of PR 1d.
