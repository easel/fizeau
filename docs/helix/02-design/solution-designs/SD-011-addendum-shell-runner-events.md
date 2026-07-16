---
ddx:
  id: SD-011-addendum
  depends_on:
    - SD-011
    - plan-2026-05-15-benchmark-runner-simplification
  review:
    self_hash: b3a1e5ff4ebdf111cd6d9da9eaa30dff154551686b9c0148978b2b42febe8136
    deps:
      SD-011: 3fee0eeae9b07811de5ebd2c630ef88f21f003bc4203b8e14026727491b1cb08
      plan-2026-05-15-benchmark-runner-simplification: 18c1a107428efab189b5fd298ff898d195c3f70130ee856660f54c1f0e97bd40
    reviewed_at: "2026-07-16T00:42:10Z"
---

# SD-011 Addendum: Shell-Runner Progress Event Taxonomy

**Related**: [SD-011 — Canonical Progress Events](./SD-011-canonical-progress-events.md),
[ADR-016 — Cells Are Self-Describing Evidence](../adr/ADR-016-cells-are-self-describing-evidence.md),
[plan-2026-05-15 — Benchmark Runner Simplification](./plan-2026-05-15-benchmark-runner-simplification.md)

## Problem

SD-011 defines a canonical progress event schema and sink boundary for native Fizeau
executions and LLM-provider harnesses (Claude, Codex, Gemini, Pi, Opencode). It does
not govern the progress format that the shell-based benchmark runner (`./benchmark`)
emits for operator visibility. The benchmark runner executes cells (Fizeau invocations)
at scale with its own orchestration concerns: resume logic, retry-on-transient-error,
concurrency-group locking, budget enforcement, and signal handling.

The contract needed before PR D (Node collector) lands is:

1. What shape do progress events take in the shell-runner context?
2. Where does formatting/structuring happen (runner vs collector vs website)?
3. How does the runner's event stream relate to the canonical taxonomy?

This addendum specifies the contract.

## Design

### Execution Model

The shell runner (`scripts/benchmark/benchmark`) operates as a **matrix orchestrator**:

1. Receives `--profile P --bench-set B` configuration.
2. Expands to a (profile, task, rep) cell matrix via `--plan`.
3. For each cell:
   - Checks resume state (does a terminal `report.json` already exist?).
   - If resume skips, logs skip event.
   - If executing, starts cell in a fresh session (setsid) with signal handling.
   - Monitors for transient errors and retries with exponential backoff.
   - Writes terminal `report.json` when complete.
4. Enforces per-concurrency-group rate limiting via flock.
5. On SIGTERM, interrupts in-flight cells and exits gracefully.

### Progress Event Stream

The shell runner emits progress events as **JSONL to a single progress log file** per
invocation. The log path is:

```
<out>/progress.jsonl
```

where `<out>` is the output root (default `bench/results/fiz-tools-v1`).

Each line is a JSON object conforming to the canonical `ProgressEvent` schema (from SD-011)
with shell-runner-specific constraints and extensions.

#### Event Shape

All events carry:
- `type` (required): event type enum (see taxonomy below).
- `source`: always `"shell-runner"`.
- `timestamp` (required): ISO-8601 UTC timestamp when the event was emitted.
- `task_id`: the task ID (e.g. `patch-build-script`).
- `cell_id`: the unique cell ID (timestamp + random suffix, e.g. `20260516T103045Z-a4c1`).
- `profile_id`: the profile ID used for the cell.
- `framework` / `dataset`: the framework and dataset (e.g. `terminal-bench` / `terminal-bench-2-1`).
- `message`: compact human-readable line (≤80 chars).
- `action` (optional): action being performed (e.g. `"resume_skip"`, `"cell_start"`, `"retry"`).
- `status` (optional): terminal status (e.g. `"completed"`, `"fail"`, `"transient_exhausted"`).
- Other canonical fields as applicable (see Taxonomy below).

Example:

```json
{"type":"cell.start","source":"shell-runner","timestamp":"2026-05-16T10:30:45Z","task_id":"patch-build-script","cell_id":"20260516T103045Z-a4c1","profile_id":"vidar-ds4","framework":"terminal-bench","dataset":"terminal-bench-2-1","message":"▶ patch-build-script (vidar-ds4) rep 1/2 starting","action":"cell_start"}
{"type":"cell.complete","source":"shell-runner","timestamp":"2026-05-16T10:38:22Z","task_id":"patch-build-script","cell_id":"20260516T103045Z-a4c1","profile_id":"vidar-ds4","framework":"terminal-bench","dataset":"terminal-bench-2-1","status":"completed","message":"✓ patch-build-script (vidar-ds4) rep 1/2 completed in 7m37s","action":"cell_complete","timing":{"duration_ms":457000}}
```

### Event Taxonomy

#### Sweep-level events

**`sweep.start`**: emitted once at the beginning of a run.

```json
{
  "type": "sweep.start",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:00:00Z",
  "message": "sweep starting: 3 profiles × 1 bench-set → N cells",
  "action": "sweep_start",
  "profiles_count": 3,
  "bench_sets_count": 1
}
```

**`sweep.complete`**: emitted once at the end of a run (success).

```json
{
  "type": "sweep.complete",
  "source": "shell-runner",
  "timestamp": "2026-05-16T12:00:00Z",
  "message": "sweep complete: 48 cells submitted, 2 interrupted",
  "action": "sweep_complete",
  "total_cells": 48,
  "interrupted_cells": 2
}
```

**`sweep.interrupt`**: emitted when SIGTERM/SIGINT halts the sweep.

```json
{
  "type": "sweep.interrupt",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:15:00Z",
  "message": "sweep interrupted; waiting for in-flight cells",
  "action": "sweep_interrupt",
  "in_flight_count": 3
}
```

#### Resume and skip events

**`cell.resume_skip`**: cell already has terminal `report.json`, skipping.

```json
{
  "type": "cell.resume_skip",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:30:00Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T103000Z-x1y2",
  "profile_id": "vidar-ds4",
  "framework": "terminal-bench",
  "dataset": "terminal-bench-2-1",
  "message": "skip: patch-build-script (vidar-ds4) rep 2/2 [terminal report found]",
  "action": "resume_skip",
  "prior_status": "pass"
}
```

**`cell.invalid_skip`**: cell has invalid_class set; skipped unless `--retry-invalid`.

```json
{
  "type": "cell.invalid_skip",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:30:15Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T102900Z-abc1",
  "profile_id": "vidar-ds4",
  "message": "skip: patch-build-script (vidar-ds4) rep 1/2 [invalid: setup_failed]",
  "action": "invalid_skip",
  "invalid_class": "setup_failed"
}
```

#### Cell lifecycle events

**`cell.start`**: cell execution begins.

```json
{
  "type": "cell.start",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:30:45Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T103045Z-a4c1",
  "profile_id": "vidar-ds4",
  "framework": "terminal-bench",
  "dataset": "terminal-bench-2-1",
  "message": "▶ patch-build-script (vidar-ds4) rep 1/2 starting",
  "action": "cell_start"
}
```

**`cell.retry`**: transient error detected; retrying cell (up to BENCH_RETRY_MAX_ATTEMPTS).

```json
{
  "type": "cell.retry",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:35:22Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T103045Z-a4c1",
  "profile_id": "vidar-ds4",
  "message": "retry: patch-build-script (vidar-ds4) transient error (attempt 1/4); sleeping 1s",
  "action": "retry",
  "attempt": 1,
  "max_attempts": 4,
  "backoff_seconds": 1,
  "error_class": "http_5xx"
}
```

**`cell.complete`**: cell completed with terminal status (pass, fail, timeout, invalid, etc.).

```json
{
  "type": "cell.complete",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:38:22Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T103045Z-a4c1",
  "profile_id": "vidar-ds4",
  "framework": "terminal-bench",
  "dataset": "terminal-bench-2-1",
  "status": "completed",
  "message": "✓ patch-build-script (vidar-ds4) rep 1/2 completed in 7m37s",
  "action": "cell_complete",
  "timing": {"duration_ms": 457000}
}
```

**`cell.interrupt`**: cell interrupted by SIGTERM; execution halted.

```json
{
  "type": "cell.interrupt",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:15:10Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T103045Z-a4c1",
  "profile_id": "vidar-ds4",
  "message": "interrupt: patch-build-script (vidar-ds4) killed by signal",
  "action": "cell_interrupt",
  "status": "interrupted"
}
```

#### Budget events

**`budget.init`**: budget cap initialized at sweep start.

```json
{
  "type": "budget.init",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:00:00Z",
  "message": "budget cap set: $50.00 USD",
  "action": "budget_init",
  "max_cost_usd": 50.00
}
```

**`budget.halt`**: budget cap reached; subsequent cells are placeholders.

```json
{
  "type": "budget.halt",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:55:00Z",
  "message": "budget halted: cumulative cost $50.01 reached cap $50.00",
  "action": "budget_halt",
  "total_cost_usd": 50.01,
  "max_cost_usd": 50.00,
  "in_flight_count": 2
}
```

**`cell.budget_halted`**: placeholder cell written for post-halt slot.

```json
{
  "type": "cell.budget_halted",
  "source": "shell-runner",
  "timestamp": "2026-05-16T10:55:15Z",
  "task_id": "patch-build-script",
  "cell_id": "20260516T105515Z-xyzw",
  "profile_id": "vidar-ds4",
  "message": "halted: patch-build-script (vidar-ds4) rep 3/4 [budget cap reached]",
  "action": "cell_budget_halted",
  "status": "budget_halted"
}
```

### Relationship to Canonical SD-011 Schema

The shell-runner progress events **are** canonical `ProgressEvent` instances with these constraints and extensions:

1. **Constraint: no LLM/Tool fields**: Since the shell runner orchestrates cell execution
   (not LLM turns or tool calls), it does not populate `ProgressEvent.llm`, `ProgressEvent.tool`,
   or `ProgressEvent.usage`. Those fields remain empty or omitted.
   
   *Rationale*: LLM and tool-level progress belongs in the cell's Fizeau-owned
   canonical service event/session projection, not in the runner's sweep-level
   events. Harness-native streams remain private input evidence and are not a
   collector contract.

2. **Extension: `action` field required**: The canonical schema marks `action` as optional.
   For shell-runner events, `action` is required and provides the operator-facing intent
   (e.g. `"resume_skip"`, `"retry"`, `"cell_complete"`).

3. **Extension: sweep-level events**: The canonical schema focuses on turn-level granularity.
   Shell-runner adds sweep-level events (`sweep.start`, `sweep.complete`, `budget.halt`)
   that have no LLM turn or tool counterpart. These follow the same JSON structure,
   omitting cell-specific fields.

4. **Output shape**: Fizeau-owned canonical service events for native and
   subprocess routes are consumed through the service callback/sink boundary
   (SD-011 §Progress Sink Boundary). Shell-runner events are **always** emitted
   as JSONL to a file on disk (`progress.jsonl`), not via that callback. This is
   practical for long-running batch operations where no live callback consumer
   exists.

5. **Service control-event boundary**: `context_capacity` is a sibling Fizeau
   service event governed by CONTRACT-003/CONTRACT-004, not a shell-runner
   `ProgressEvent` type. The runner and collector MUST NOT infer or synthesize
   it from prompt size, provider errors, `report.json`, or harness-native
   streams. When a cell's Fizeau event stream is retained, its service-owned
   clamp/skip/reject event and terminal ordering remain unchanged and separate
   from the shell orchestration log.

### Collector Contract

The Node collector (PR D) reads both progress events and cell reports:

1. **Live operator viewing**: Tail `<out>/progress.jsonl` to stream events in real time.
   Parse each line as JSON and format for display (see Formatter Contract below).

2. **Retroactive reconstruction**: After the sweep completes, cells carry terminal
   `report.json` with full metrics and grading. The collector reads these and
   maps them only to shell-owned `cell.complete` / `cell.interrupt` events for
   storage or replay. It never reconstructs a Fizeau `context_capacity` event.
   (This allows operator viewing even if progress.jsonl was lost or truncated.)

3. **Resume auditing**: The collector cross-checks `cell.resume_skip` events against
   actual `report.json` files to detect inconsistencies.

### Formatter Contract

The formatter consumes progress events from two sources:

1. **Progress events (preferred)**: When progress.jsonl is present, read and format
   canonical events directly. Use the `message` field as-is when it fits the
   operator's display context; derive `(action, target, timing)` from structured fields
   for more sophisticated rendering (e.g. progress bars, cost meters).

2. **Fallback to report.json**: When progress.jsonl is absent (historical runs, manual
   invocation outside this framework), reconstruct progress events from cell
   `report.json` using the SD-011 legacy-normalization helpers. Map `started_at` and
   `finished_at` to synthetic `cell.start` and `cell.complete` events. Preserve
   unknown recorded event and status values; do not fabricate service control
   events or successful terminal facts.

### Logging on Disk

**File**: `<out>/progress.jsonl`

- One event per line, JSONL format.
- Lines are appended in chronological order.
- File may be read concurrently (tail, grep, jq) while cells are still running.
- Not compressed or rotated (simplicity; post-processing can compress).
- Idempotent re-run: `--plan` does not create the file; `run_sweep` initializes it
  empty at sweep start (or truncates on restart).

### Example Sweep Session

```
# Initial invocation
$ ./benchmark --profile vidar-ds4 --bench-set tb-2-1-canary --out ~/bench-run-1

# Events written to ~/bench-run-1/progress.jsonl:

{"type":"sweep.start","source":"shell-runner","timestamp":"2026-05-16T10:00:00Z","message":"sweep starting","action":"sweep_start","profiles_count":1,"bench_sets_count":1}

{"type":"budget.init","source":"shell-runner","timestamp":"2026-05-16T10:00:05Z","message":"budget cap set: $100.00 USD","action":"budget_init","max_cost_usd":100}

# Cell 1: completed on first try
{"type":"cell.start","source":"shell-runner","timestamp":"2026-05-16T10:00:10Z","task_id":"patch-build-script","cell_id":"20260516T100010Z-abcd","profile_id":"vidar-ds4","message":"▶ patch-build-script (vidar-ds4) rep 1/2 starting","action":"cell_start"}

{"type":"cell.complete","source":"shell-runner","timestamp":"2026-05-16T10:07:47Z","task_id":"patch-build-script","cell_id":"20260516T100010Z-abcd","profile_id":"vidar-ds4","status":"pass","message":"✓ patch-build-script (vidar-ds4) rep 1/2 completed in 7m37s","action":"cell_complete","timing":{"duration_ms":457000}}

# Cell 2: transient error and retry
{"type":"cell.start","source":"shell-runner","timestamp":"2026-05-16T10:08:00Z","task_id":"patch-build-script","cell_id":"20260516T100800Z-efgh","profile_id":"vidar-ds4","message":"▶ patch-build-script (vidar-ds4) rep 2/2 starting","action":"cell_start"}

{"type":"cell.retry","source":"shell-runner","timestamp":"2026-05-16T10:12:15Z","task_id":"patch-build-script","cell_id":"20260516T100800Z-efgh","profile_id":"vidar-ds4","message":"retry: patch-build-script (vidar-ds4) transient error (attempt 1/4); sleeping 1s","action":"retry","attempt":1,"max_attempts":4,"backoff_seconds":1,"error_class":"http_5xx"}

{"type":"cell.complete","source":"shell-runner","timestamp":"2026-05-16T10:20:22Z","task_id":"patch-build-script","cell_id":"20260516T100800Z-efgh","profile_id":"vidar-ds4","status":"completed","message":"✓ patch-build-script (vidar-ds4) rep 2/2 completed in 12m22s (1 retry)","action":"cell_complete","timing":{"duration_ms":742000}}

{"type":"sweep.complete","source":"shell-runner","timestamp":"2026-05-16T10:20:30Z","message":"sweep complete: 2 cells submitted, 0 interrupted","action":"sweep_complete","total_cells":2,"interrupted_cells":0}
```

## Implications

### For Operators

- `tail -f <out>/progress.jsonl | jq -r '.message'` gives live human-readable output.
- `jq 'select(.type=="cell.retry")' <out>/progress.jsonl` filters for transient errors.
- `jq '.timing.duration_ms' <out>/progress.jsonl | jq -s 'add'` computes total wall time.

### For Collectors and Analytics

- Progress events are **structurally uniform** across Fizeau-owned native and
  subprocess projections plus shell orchestration. A single progress formatter
  handles those canonical sources; it does not parse harness-native streams.
- Shell-runner events provide **sweep-level metadata** (budget, concurrency, retry attempts)
  that cell reports alone do not capture, improving observability and auditability.
- Progress.jsonl is **optional for correctness**: cells are self-describing (report.json
  has all terminal state). Progress.jsonl is advisory for live monitoring and
  forensics.

### For Future Extensions

Shell-runner progress events may be extended with:
- Per-concurrency-group lock-wait time (currently silent).
- Per-cell resource usage snapshots (CPU, memory peak).
- Harbor container image digest log events for reproducibility.
These would be added as new event types without changing the existing taxonomy.
Consumers preserve unknown additive event and status values. The shell schema
does not weaken SD-011's v0.15 keyed-literal migration or its additive
`context_capacity` JSON/event compatibility rules.

## Verification

- `./benchmark --plan --profile P --bench-set B` produces no progress.jsonl
  (--plan is pure, no execution).
- `./benchmark --profile P --bench-set B` creates progress.jsonl in the output
  root and appends events in temporal order.
- Parsing `progress.jsonl` as JSONL produces valid `ProgressEvent` instances (one per line).
- For each cell, there is at least one corresponding event (resume_skip, interrupt, or complete).
- For cells with retries, there are ordered (cell.start, cell.retry[1..N], cell.complete)
  events.
- Collector fixtures prove `report.json` fallback never synthesizes
  `context_capacity`; retained Fizeau event streams preserve their original
  service-owned capacity and terminal ordering.
- Unknown additive shell and retained Fizeau event values survive collection
  and formatting.
- No `progress.jsonl` means the run was interrupted mid-setup; cells may be incomplete.
  Operator should inspect `report.json` directly to resume.
