---
ddx:
  id: SD-015
  created: 2026-05-15
  depends_on:
    - FEAT-008
    - ADR-015
    - SD-014
    - website/DESIGN.md
  review:
    self_hash: 6191c30eab07fe4fa7de4b1c4b4ffe20b7fc9142f152f487bbaffb8a4bbfc735
    deps:
      ADR-015: eacf3702878a7101f78e029e8e8e1717a5789a60830748c5d493970cfb9c9ca1
      FEAT-008: 7ccd6ca324a99e7e35a27f5cb3e7cdf9d591417327d101bf0f08dbe4e2e3d6f1
      SD-014: 65cd79f7118efef8d5f05ed4a5dd067de2925394eff229f0af3f6aa73c7faff7
      website/DESIGN.md: 920c3b6fb2b3daed4a4b409de13f57f3c07f7c5732e4a32bc1ad42f389aa842b
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Solution Design: SD-015 - Benchmark Workbench

**Status:** Accepted
**Owner:** Fizeau Team

## ADR-016 Compatibility

> **ADR-016** (Cells Are Self-Describing Evidence) introduced changes that affect SD-015:
>
> - Cells now embed resolved profile snapshots at write time; profile YAMLs are no longer join targets
> - Profile-YAML joins and `PROFILE_ALIASES` are retired; historical renaming machinery is no longer needed
> - Existing Parquet artifacts remain backward-compatible; backfill ensures historical cells expose metadata identical to current output
>
> See [ADR-016: Cells Are Self-Describing Evidence](../../adr/ADR-016-cells-are-self-describing-evidence.md).

## Summary

SD-015 implements FEAT-008 as `/benchmarks/explorer/`: a static,
browser-side analytical workbench for benchmark cells. It replaces the
SD-014 DataTables explorer choice with a DuckDB-WASM + Parquet +
Perspective design while preserving the rest of the benchmark
information architecture.

## Architecture

```
scripts/website/build-benchmark-data.py
        |
        v
website/static/data/cells.parquet
website/static/data/task-combinations.parquet
        |
        v
/benchmarks/explorer/ Hugo page
        |
        v
benchmark-workbench.js
  - initializes DuckDB-WASM worker
  - registers Parquet files by URL
  - creates cells_enriched and workbench_cells SQL views
  - manages hash-addressable explorer panes
  - runs preset filters and aggregate SQL
  - runs pairwise model-family comparison SQL
  - loads workbench_cells into Perspective datagrid
```

The browser owns the analytical session. Static hosting only needs to
serve HTML, CSS, JS, WASM, worker files, and Parquet artifacts.

## Page Structure

`website/content/benchmarks/explorer/_index.md` contains a single
instrument-panel workbench with persistent local navigation. The
navigation entries are hash routes, not Hugo subpages:

- `#summary`
- `#data`
- `#compare`
- `#combinations`

`benchmark-workbench.js` owns this small router by reading
`window.location.hash`, toggling `[data-bw-pane]`, and marking the
matching `[data-bw-route]` link with `aria-current="page"`. The design
keeps the left navigation fixed on desktop and turns it into a compact
top local nav on narrow screens.

The panes are:

- Summary: result-bearing dataset explanation, unfiltered aggregate
  metrics, task-type/GPU distribution charts, and wall-time charts by
  GPU and model.
- Raw database: raw search, outcome/task/model/engine/GPU/RAM controls,
  enum filters, saved views, current aggregate readouts, and the
  Perspective datagrid with every cell column available.
- Comparison: pairwise model-family controls, comparison scope filters,
  and the sortable pairwise gap table.
- Combinations: task/model/GPU/pass controls and the sortable
  combination aggregate table.

The workbench lives outside the prose-width cap. It uses full available
viewport width and a stable grid height so the table is not trapped in a
small report table container.

## SQL Views

`cells_raw`

Reads `cells.parquet` directly. The builder filters this artifact to
result-bearing rows only:

- `result_state = "timeout"` when the run hit the explicit benchmark
  timeout path.
- `result_state = "passed"` when the run completed, was graded, and
  received reward greater than zero.
- `result_state = "failed"` when the run completed, was graded, and
  received reward zero.

Rows with `invalid_class` and rows that are neither timeout nor graded
are excluded from `cells.parquet`; their counts remain in the manifest
for audit.

The builder also enriches task category and difficulty from source
reports when present, then falls back to checked-in Terminal-Bench task
metadata and subset YAML metadata. This keeps pairwise "what kind of
task explains the gap?" views from collapsing into missing buckets when
older reports did not carry category fields.

`cells_enriched`

Selects all raw columns and adds:

- `search_text`: normalized concatenation of high-value descriptor
  fields for broad search.
- `effective_gpu_ram_gb`: first available VRAM/RAM value used by the
  max-GPU-RAM filter.
- `effective_gpu_model`: public GPU label fallback chain.
- `terminalbench_task_url`: canonical Terminal-Bench task detail URL for
  Terminal-Bench suite rows. The URL is used as the link target for task
  cells and is not part of the default visible grid column set.

`workbench_cells`

Current filtered view. Presets and controls recreate this view with a
deterministic `WHERE` clause, then the raw grid and raw aggregate cards
reload from the same view.

The comparison and combination panes do not consume the raw database
pane's transient filter state. Their controls are intentionally
separate so each pane owns the query scope for the table it displays.

## Default Columns

The grid initially shows:

- suite, task, task_subsets
- result_state, passed, grader_passed, final_status, invalid_class
- harness, harness_label, provider_type, provider_surface
- model_display_name, model, model_quant, quant_display, weight_bits
- kv_cache_quant, k_quant, v_quant, runtime_mtp_enabled
- engine, engine_version
- gpu_model, gpu_ram_gb, hardware_vram_gb, machine
- rep, turns, input_tokens, output_tokens, reasoning_tokens,
  total_tokens, cost_usd, wall_seconds
- started_at, finished_at

All other columns remain available through Perspective's column chooser.

## Raw Database Filters

The workbench promotes low/medium-cardinality fields into native select
controls so the common raw inspection questions do not require opening the
Perspective configuration panel first:

- model family, model quant, KV cache, K/V quant, MTP
- provider type/surface, harness, lane, deployment class, machine
- task category and difficulty
- GPU vendor, chip family, memory type, backend, KV disk
- reasoning, temperature, top-p, top-k, context, and output cap

Controls with no populated values hide themselves after DuckDB loads the
artifact.

The raw database pane creates `workbench_cells` from `cells_enriched`
using these controls. The Perspective datagrid then attaches to
`workbench_cells`, so native Perspective sorting, filtering, column
selection, grouping, and pivoting remain available inside the raw table.

## Presets

| Preset | Filter |
|--------|--------|
| All cells | no workbench filter other than current controls |
| Passing selected test | `result_state = 'passed' AND task = :task` |
| Passing selected test on selected GPU | passing selected test plus GPU match |
| Passing selected test under max RAM | passing selected test plus `effective_gpu_ram_gb < :max_ram` |
| Recent failures | `result_state <> 'passed'`, sorted by newest finished timestamp |

Controls are composable. Presets set intent; the grid's own filter,
sort, group, and pivot controls remain available for deeper analysis.

## Pairwise Gap Query

The pairwise comparison pane answers "where is the delta?" questions
such as Qwen versus Claude/Anthropic. It compares two selected
`model_family` values over the comparison pane's own filter scope. It
does not inherit raw database filters.

Supported group-by dimensions:

`task_category, task_difficulty, task, result_state, engine,
model_quant, deployment_class, gpu_vendor, effective_gpu_model,
sampling_reasoning, provider_type, harness_label`

For each bucket it reports:

- baseline pass rate and comparison pass rate over graded rows
- pass-rate gap in percentage points (`compare - baseline`)
- row counts, graded-fail counts, and timeout counts for each side
- token totals and p50 wall time for each side

When grouped by task, the task cell links to the canonical
Terminal-Bench task page.

The table renders its headers as buttons. Header clicks update the
client-side sort state and rerender the current result set without
rerunning unrelated raw database queries.

## Summary Queries

The Summary pane queries `cells_enriched` directly and ignores pane
filters. It renders:

- row count, pass rate, timeout count, distinct task/model/GPU counts,
  token total, and p50 wall time
- pie distributions for task category and effective GPU model
- bar charts for median wall time by effective GPU model and model
  family/display name

These charts are simple HTML/CSS renderings fed by DuckDB result rows;
no charting dependency is added.

## Aggregate Queries

Raw database aggregate cards query `workbench_cells`:

- `count(*)`
- pass count, fail count, and timeout count from `result_state`
- pass rate over completed graded rows (`passed / (passed + failed)`)
- `count(distinct model_display_name)`
- `count(distinct effective_gpu_model)`
- total tokens
- known cost total (`cost_usd IS NOT NULL`)
- `median(wall_seconds)`

Combination aggregate table groups by:

`task, model_display_name, model_quant, quant_display, kv_cache_quant,
k_quant, v_quant, runtime_mtp_enabled, engine, effective_gpu_model,
effective_gpu_ram_gb`

and reports rows, passes, failures, timeouts, pass rate, tokens, known
cost, and wall p50.

The Combinations pane queries `cells_enriched` with its own task, model,
GPU, and pass-only controls. Its table headers are buttons that sort the
aggregate rows in place.

## Design Constraints

- No marketing hero; the workbench is the first screen.
- No shadows. Surfaces are panels with hairline rules.
- Text and controls use DESIGN.md tokens and stay dense.
- Cards are only used as individual metric readouts; the page does not
  nest cards inside cards.
- The grid gets full available page width and at least viewport-height
  scale. It is not constrained by the docs prose width.
- Controls are colocated with the table or charts they affect.
- The local navigation remains stable while pane content changes.
- Existing static report tables keep their lightbox enhancer; the
  workbench is a separate component.

## Failure States

- Missing WASM/worker asset: show an error in the status strip and keep
  the page readable.
- Missing Parquet artifact: show artifact URL and failed phase.
- Empty filtered set: aggregate cards show zero/empty values and the
  grid loads an empty result.
- Browser without worker/WASM support: show unsupported-browser error.

## Build Integration

`npm run build:benchmark-workbench`:

1. Copies DuckDB-WASM browser assets into
   `website/static/vendor/duckdb/`.
2. Bundles `website/assets/js/benchmark-workbench.js` to
   `website/static/js/benchmark-workbench.js` with esbuild.

Hugo loads the module from `website/layouts/partials/custom/head-end.html`.
The module no-ops on pages without `[data-benchmark-workbench]`.

## Verification

- Run `npm run build:benchmark-workbench`.
- Run `hugo --source website`.
- Use a local Hugo server to open `/benchmarks/explorer/` and confirm:
  rows load, hash navigation works, all columns are available, saved
  views apply, aggregate cards update, charts render, task links point
  to Terminal-Bench, and pairwise/combination headers sort their tables.
