---
ddx:
  id: ADR-015
  created: 2026-05-15
  depends_on:
    - FEAT-008
    - SD-014
    - website/DESIGN.md
  review:
    self_hash: eacf3702878a7101f78e029e8e8e1717a5789a60830748c5d493970cfb9c9ca1
    deps:
      FEAT-008: 7ccd6ca324a99e7e35a27f5cb3e7cdf9d591417327d101bf0f08dbe4e2e3d6f1
      SD-014: 65cd79f7118efef8d5f05ed4a5dd067de2925394eff229f0af3f6aa73c7faff7
      website/DESIGN.md: 920c3b6fb2b3daed4a4b409de13f57f3c07f7c5732e4a32bc1ad42f389aa842b
    reviewed_at: "2026-07-14T05:16:22Z"
---
# ADR-015: Browser Analytical Benchmark Workbench

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-15 | Accepted | Fizeau maintainers | FEAT-008, SD-014, SD-015 | Medium-high |

## Context

The benchmark explorer needs to handle an analytical workload over a
wide raw datatable: thousands of benchmark cells today, likely more
later, with hundreds of columns once raw outcome, token, cost, runtime,
quantization, and hardware variables are retained.

The microsite is statically hosted. Introducing a server component would
make the explorer harder to publish, harder to reproduce locally, and
out of character with the rest of the Hugo benchmark artifacts. The
data pipeline already emits compressed Parquet, which is the right
browser transfer format for this workload.

The previous SD-014 explorer decision selected DataTables.js over JSON
and deferred Datasette-lite unless the dataset grew. That decision no
longer fits the current requirement: every collected field must be
available, row counts are expected to grow, and the user needs
aggregates as much as a table.

## Decision

Use **DuckDB-WASM over Parquet** as the browser analytical engine and
**Perspective** as the interactive grid/pivot UI for the benchmark
workbench.

The workbench is a static Hugo page that loads:

- `website/static/data/cells.parquet` as the valid benchmark outcome
  table: timeout rows plus completed graded pass/fail rows. Invalid and
  other ungraded source reports are excluded from the artifact and
  counted in the manifest.
- `website/static/data/task-combinations.parquet` for precomputed
  combination rows when needed by follow-up pages.
- Self-hosted DuckDB-WASM worker and WASM assets copied from npm.
- A bundled workbench module that initializes DuckDB, creates derived
  views, runs aggregate and pairwise comparison SQL, and loads a
  Perspective datagrid.

All data processing stays in the browser. No Datasette server, SQLite
service, DuckDB daemon, or custom API is introduced.

## Why DuckDB-WASM and Parquet

DuckDB-WASM matches the workload:

- It queries Parquet directly, avoiding a large JSON transfer and parse.
- It handles SQL filters and aggregates naturally.
- It keeps the architecture static-site compatible.
- It lets future story pages link into the same data model instead of
  inventing one-off JavaScript reducers.

Parquet is retained as the primary artifact. JSON remains a debug or
compatibility artifact, not the default explorer input.

## Why Perspective

Perspective supplies the prebuilt analytical UI that the benchmark page
needs: dense virtualized datagrid, column chooser, sorting, filtering,
grouping, pivots, and aggregate configuration. It avoids building and
maintaining a bespoke grid for hundreds of fields.

The workbench uses Perspective as the operator-facing grid and uses
DuckDB SQL for the page-level presets, summary aggregates, and pairwise
model-family gap analysis. If
Perspective's DuckDB virtual-table bridge proves too brittle across
versions, loading Arrow results from DuckDB into a local Perspective
table remains an acceptable implementation fallback without changing
the ADR: DuckDB and Parquet remain the source of truth.

## Alternatives Considered

| Alternative | Outcome | Reason |
|-------------|---------|--------|
| DataTables.js over JSON | Rejected | Good for small report tables, poor fit for 70MB JSON, hundreds of columns, and analytical aggregates. |
| Datasette server | Rejected | Adds a server component, contradicting the static microsite constraint. |
| Datasette Lite / SQLite WASM | Rejected for v1 | Strong for SQLite browsing, but the pipeline already emits Parquet and the workload is columnar/analytical. |
| WebSQL | Rejected | Deprecated browser API and unsuitable foundation for new work. |
| Duck-UI Embed | Deferred | Aligned with DuckDB, but less proven for a polished microsite workbench than Perspective. |
| Custom grid + SQL controls | Rejected | Too much UI surface to own: column management, virtualization, sorting, filtering, and pivots. |

## Consequences

Positive:

- Raw benchmark exploration remains serverless.
- The primary artifact is compressed and columnar.
- SQL aggregates are straightforward and reproducible.
- The UI can handle many rows and columns without a custom table
  implementation.

Negative:

- DuckDB-WASM and Perspective add substantial JavaScript/WASM assets.
- Browser support depends on WASM and workers.
- The page needs explicit loading/error states because initialization is
  heavier than ordinary report tables.

Neutral:

- Existing curated pages and generated static report tables remain.
  They are separate from the workbench and can keep using static HTML.

## Validation

- `npm run build:benchmark-workbench` bundles the workbench and copies
  DuckDB assets.
- `hugo --source website` renders the page with the workbench markup.
- Browser initialization loads `cells.parquet`, reports row counts, and
  populates the grid, aggregate cards, and pairwise gap table.
- No new server process is required to use the static build.
