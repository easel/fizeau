---
ddx:
  id: benchmark-baseline-tb2-qwen-2026-05-01
  captured: 2026-05-01
  model: Qwen3.6-27B-MLX-8bit (via oMLX @ vidar:1235)
  provider: omlx
  fizeau-version: 2e87466
  status: FINAL
  supersedes_for: terminal-bench-2 local-model baseline
  sibling_of: benchmark-baseline-2026-04-08.md
  review:
    self_hash: 7b641932d66e783dec26836d3af25e0b1f3f7373c75e2b4eb6a628c5992b3f2d
    deps: {}
    reviewed_at: "2026-07-16T07:25:15Z"
---
# TB-2 Baseline: Qwen3.6-27B via fizeau — 2026-05-01

This is the v1 benchmark baseline for fizeau on terminal-bench-2 under a
**local model** (Qwen3.6-27B via oMLX). It is the floor against which the
PIVOT, PRESET, PLAN, SKILL, and ANC feature beads are gated. It complements,
rather than replaces, `benchmark-baseline-2026-04-08.md` (cloud Haiku via
OpenRouter on a 6-task pilot at 100% pass) — the two baselines exercise
different harness × model paths and are not directly comparable.

## Run conditions

| Field | Value |
|---|---|
| Matrix dir | `benchmark-results/matrix-20260501T035821Z` |
| Agent | fizeau (`fiz`) at commit `2e87466` |
| Preset | `benchmark` |
| Model | `Qwen3.6-27B-MLX-8bit` via oMLX @ vidar:1235 |
| Profile | `vidar-qwen3-6-27b` |
| Tasks | 89 TB-2 tasks × 3 reps = 267 cells |
| TB-2 pin | `53ff2b87d621bdb97b455671f2bd9728b7d86c11` |
| Sampling | T=0.6, top_p=0.95, top_k=20, reasoning=medium |
| Concurrency | `--jobs=1` (sequential) |
| Date | 2026-05-01 |

## Results

| Metric | Value |
|---|---|
| **Pass rate** | **0 / 267 (0%)** |
| graded_pass | 0 |
| graded_fail | 265 |
| harness_crash | 1 |
| ran (ungraded) | 1 |

### By difficulty

| Difficulty | Pass | Total | Rate |
|---|---|---|---|
| easy | 0 | 12 | 0% |
| medium | 0 | 165 | 0% |
| hard | 0 | 90 | 0% |

## Agent failure modes

Of the 265 graded_fail cells, fiz.txt was present in ~80; the remainder had
no fiz.txt (Docker pull failures that were later cleaned up and re-run).
Agent-level outcomes:

| fiz status | Count | % |
|---|---|---|
| failed | 246 | 96% |
| stalled | 4 | 2% |
| success (but verifier failed) | 5 | 2% |

### Error breakdown within "failed"

| Error | Count | % of failed |
|---|---|---|
| `agent: identical tool calls repeated, aborting loop` | 156 | 63% |
| `agent: reasoning stall: model produced only reasoning tokens` | 87 | 35% |
| `no_progress_tools_exceeded` | 4 | 2% |
| provider connectivity errors | 3 | 1% |

### "Success but verifier failed" analysis

Five cells where fiz reported `status: success` but reward = 0:

- `mteb-leaderboard` (3 reps): agent ran to completion but never wrote
  `/app/result.txt`.
- `polyglot-c-py` (2 reps): agent ran to completion but output was incorrect.

## Excluded run: bragi (LM Studio, Qwen3.6-27B Q4_K_M)

The companion run `matrix-20260501T040121Z` (bragi via LM Studio,
Qwen3.6-27B Q4_K_M) also produced 0/267 passes, but **83% of its failures
were provider connectivity errors** (`POST` network failures). LM Studio was
crashing under even one concurrent request. That run measures LM Studio
server stability, not agent quality, and is **excluded from this baseline**
until bragi is re-tested with a stable server.

## Interpretation

The 0% pass rate is real, not a harness bug. The agent reaches the verifier
on the correct model — it produces wrong or missing output. Two root causes
account for 98% of failures:

1. **Tool call loop (63%)**: model issues the same failing bash command
   repeatedly. The current loop detector aborts after three identical calls
   with no recovery. → Addressed by FEAT-001 PIVOT semantics
   (AC-FEAT-001-09 pivot path).
2. **Reasoning stall (35%)**: model enters extended thinking and never
   calls a tool. The stall detector fires but emits no recovery prompt. →
   Addressed by SD-009 §10 PRESET guidelines (`ANTI-STALL`, `TOOL FIRST`)
   and SD-009 §11 Planning Mode.
3. **No output written (2%)**: agent reports success internally but never
   calls `write`. → Addressed by SD-009 §10 PRESET guideline `OUTPUT
   VERIFICATION`.

## Improvement targets (gates for feature beads)

| After shipping | Target pass rate | Rationale |
|---|---|---|
| PIVOT + PRESET only | ≥ 5 % (13/267) | Recovery from loops; should rescue at least the easy tasks |
| + PLAN | ≥ 10 % (27/267) | Planning pass reduces stalls on medium tasks |
| + SKILL | ≥ 15 % | Progressive context reduces mid-session context rot |
| Full stack | ≥ 20 % (53/267) | Still below Dirac (65 %) — model quality is the ceiling |

These are minimum thresholds. If a batch of changes does not lift pass
rate, the changes must be investigated before filing the next batch.

## Rerun procedure

```bash
cd /home/erik/Projects/agent
OMLX_API_KEY=local HARBOR_AGENT_ARTIFACT=$(pwd)/fiz-linux-amd64 \
  /tmp/bench-fixed matrix \
  --profiles=vidar-qwen3-6-27b --harnesses=ddx-agent --reps=3 \
  --subset=scripts/beadbench/external/termbench-full.json \
  --tasks-dir=scripts/benchmark/external/terminal-bench-2 \
  --out=benchmark-results/matrix-<ts>/ --jobs=1
```

## Source provenance

Run conditions, results, failure-mode breakdown, bragi exclusion, and
improvement targets extracted from
`docs/research/tb2-baseline-qwen3-27b-2026-05-01.md` (FINAL, 2026-05-01).
Filed as a sibling to `benchmark-baseline-2026-04-08.md` because the
2026-04-08 cloud-Haiku 6-task pilot and the 2026-05-01 local-Qwen
89-task × 3-rep run measure different harness × model paths and are not
directly comparable; folding them into one document would conflate the
baselines.
