---
ddx:
  id: SD-014
  created: 2026-05-12
  depends_on:
    - FEAT-006 # standalone CLI (fiz) — provides the snapshot data via fiz-bench
    - SD-009 # benchmark-mode runtime + preset
    - SD-010 # harness matrix benchmark (data shape this site consumes)
    - SD-012 # benchmark evidence ledger (validity story)
    - ADR-010 # reasoning wire form (drives provenance pillar)
    - website/DESIGN.md # visual design system this spec consumes
  review:
    self_hash: 65cd79f7118efef8d5f05ed4a5dd067de2925394eff229f0af3f6aa73c7faff7
    deps:
      ADR-010: be383d4b64de9629cd84f094cbbfa902607dfff5343c643977d12899de12e553
      FEAT-006: 1c78778fcc8efa7fe750cf233719c21f1f6b07ce6b098c48f6d42855d57faa07
      SD-009: 7f518a10f93a3bee6ab4e09dadb24b7bfd43822db3c0bbfed43de0abb664b83a
      SD-010: 12f0b391c23a5a2b7e2abbbc6fec88ec6c7a57a7ff0a20e39c3a065a6c818e7a
      SD-012: bf30b7ba2939970dc4b6f94343403b37d72bc5626314a689682abcdd93d3acb2
      website/DESIGN.md: 920c3b6fb2b3daed4a4b409de13f57f3c07f7c5732e4a32bc1ad42f389aa842b
    reviewed_at: "2026-07-14T20:00:14Z"
---

# Solution Design: SD-014 — Benchmark Site Information Architecture

**Status:** Accepted
**Owner:** Fizeau Team

> **Update 2026-05-16**: ADR-016 ("Cells Are Self-Describing Evidence")
> supersedes the lane-identity / lane-join model used below. Cells now
> embed the full resolved profile snapshot; the site reads characteristic
> descriptors directly from `cell.profile.*` instead of joining to a
> separate lane catalog at build time. "Lane" wording below remains as a
> historical reference to the public benchmark-row identity concept that
> the §3 descriptor rules formalize; the underlying data path is now
> cell-embedded, not catalog-joined. See
> [ADR-016](../adr/ADR-016-cells-are-self-describing-evidence.md) and
> [`plan-2026-05-15-benchmark-runner-simplification.md`](../plan-2026-05-15-benchmark-runner-simplification.md).

## Summary

This spec defines the information architecture, data layer, and validity
rules for the `/benchmarks/` section of the Fizeau microsite (Hugo +
Hextra at `website/`). It is the governing artifact for implementation
beads under the `W-EPIC` thread.

The site serves two distinct stories with different audiences plus an
operator-facing data explorer and a provenance pillar that documents
"what it took to get here."

## 0. Visual contract (binding)

All pages and components introduced under this spec MUST adhere to
`website/DESIGN.md` ("Fizeau — Design System"). DESIGN.md is the
authority on:

- **Color palette** — light + dark are both first-class. Use the
  documented CSS custom properties (`--surface-page`, `--ink-primary`,
  `--accent-cyan`, `--accent-amber`, etc.). No new accent colors;
  no arbitrary hex codes in new templates or stylesheets.
- **Semantic chart palette** — assign colors by ROLE, not aesthetics
  (primary measurement = cyan; pass/positive = green; fail/negative =
  red; secondary = amber; external/comparator = violet; baseline =
  ink-tertiary). Documented in DESIGN.md "Semantic chart palette"
  table. SVG charts produced by the aggregator must consume these
  tokens.
- **Typography** — JetBrains Mono Variable for numerals, code,
  captions, eyebrow labels, hero headlines, brand mark; Inter
  Variable for prose/nav/body. No third family.
- **Brand grounding** — scientific instrument, not developer-tool
  landing page. The data is the headline; the medium matters
  (provenance always shown); differences over absolutes (deltas,
  ratios, bucketed comparisons preferred over lone numbers).

This spec consumes DESIGN.md; it does not modify it. Any visual
addition (new component class, new chart type) requires DESIGN.md
to grow first, then the consumer follows.

The visual conformance audit is bead **W0** (§10).

## 1. The two stories (top-level pillars)

**Story 1 — `/benchmarks/harness-comparison/`.** Audience: someone
evaluating which agent harness to use. They've heard of claude-code,
codex, opencode, pi, gemini-cli. The pillar answers "Is Fizeau a
viable agent for solving Terminal-Bench tasks, and how does it compare
to frontier harnesses?"

**Story 2 — `/benchmarks/model-landscape/`.** Audience: someone who's
chosen Fizeau and now needs to operationally tune. The pillar answers
"Given Fizeau, what model + provider + hardware combination should I
run? Local vs cloud? RTX 3090 Ti vs RTX 5090 mobile vs M2 Ultra vs M1
Max? oMLX vs Rapid-MLX vs llama.cpp vs ds4?"

These are different decision points and different visitors. The site
nav reflects them as distinct pillars; both are top-level and both
must have left-hand navigation populated by their child pages.

## 2. Information architecture

```
/benchmarks/                          PILLAR LANDING (frames both stories)
├── /harness-comparison/              STORY 1
│   ├── _index.md                     Headline + paired-comparison table
│   ├── vs-claude-code.md             Pair: fiz/anthropic-direct vs claude-code
│   ├── vs-codex.md                   Pair: fiz/openai-direct vs codex
│   ├── vs-opencode.md                Pair: fiz/X vs opencode/X
│   ├── vs-pi.md                      Pair: fiz/X vs pi/X
│   └── vs-gemini-cli.md              Pair: fiz/gemini-* vs gemini-cli
├── /model-landscape/                 STORY 2
│   ├── _index.md                     Headline matrix
│   ├── local-vs-cloud.md             Pareto frontier
│   ├── hardware.md                   GPU + Apple Silicon comparison
│   ├── backends.md                   Inference engine comparison
│   ├── reliability.md                invalid_class distribution per lane
│   └── reasoning-control.md          Wire-fix evidence + reasoning_tokens
├── /provenance/                      "WHAT IT TOOK TO GET HERE"
│   ├── _index.md                     Why this pillar exists
│   ├── changelog.md                  Date-ordered fixes + discoveries
│   ├── reasoning-control-saga.md     Wire-format dialect investigation
│   ├── timeout-calibration.md        QWEN_BASE × LANE_PENALTY framework
│   ├── token-accounting.md           reasoning_content fallback story
│   ├── classification.md             invalid_* taxonomy + evolution
│   └── stack-notes/                  PER-(engine, model) operator reference
│       ├── openrouter-qwen3.md       OR's effort flat-mapping; max_tokens honor
│       ├── llamacpp-qwen3.md         chat_template_kwargs envelope; REASONING_FORMAT=auto
│       ├── ds4-deepseek.md           alias collapse; flat reasoning_effort wire
│       └── ...                       (one per engine × model class, NOT physical machine)
├── /explorer/                        DYNAMIC raw-data workbench (DuckDB-WASM + Parquet + Perspective; see FEAT-008/ADR-015/SD-015)
├── /methodology/                     SHARED methodology (HOW we measure)
└── /reports/                         PER-RUN detail (raw audit trail)
    ├── /terminal-bench-2-1/
    └── /...
```

Hugo+Hextra renders left-hand nav per section automatically. Pillar
pages that route to children must populate nav from those children.

### 2.1 Existing structure to audit and migrate

The site already has content under
`website/content/benchmarks/terminal-bench-2-1/` with subdirs
`providers/`, `harnesses/`, `models/`, `charts/`, and `data/`. Several
of these overlap with the new pillars:

- `harnesses/*` — likely provides Story 1 raw material; audit and
  decide: extract per-pair pages (vs-claude-code, vs-codex, etc.) or
  reuse as detail-of-the-TB-2.1-report under `/reports/`
- `models/*` — likely provides Story 2 raw material; same call
- `providers/*` — provider-centric existing cuts; audit how they
  relate to the descriptor-first principle in §3
- `charts/*` — existing SVGs; check token compliance per §0; reuse
  if compliant, regenerate if not
- `data/*` — pipeline already exists (e.g. `data/machines.json`);
  reconcile with the new aggregator in §5 (the new aggregator
  REPLACES this pipeline; existing data files removed)

W3 (Story 1) and W4 (Story 2) beads MUST audit the existing subdirs
before writing new pages. If existing content is reusable + token-
compliant, lift-and-link rather than rewrite. Per-suite reports stay
at `/reports/terminal-bench-2-1/` with a 301 redirect from the
current URL preserved (W8 handles redirect mapping).

**Methodology vs Provenance split:**
- **Methodology** = HOW we measure. Invariant. Reproducibility
  reference: schemas, validity rules, task subsets.
- **Provenance** = WHAT IT TOOK. Historical. Operator-facing
  war-stories, gotchas, per-(engine, model) setup notes.

## 3. Lane identity — characteristics, not internal names

**Invariant:** every public-facing surface (story pages, explorer
columns, charts, captions) describes a lane by its characteristic
descriptor, never by its internal slug. Hardware-coupled internal lane
names (`sindri-club-3090-llamacpp`, `vidar-ds4`, `bragi-qwen3-6-27b`,
etc.) survive only as URL anchors and as a default-hidden explorer
column for operator debugging.

**Required characteristic dimensions** for any lane that appears in
public documentation:

| Dimension | Source |
|---|---|
| Model | `metadata.model_display_name` |
| Quant | `metadata.quant_display` |
| Weight bits | `metadata.weight_bits` |
| KV cache config | `metadata.kv_cache_quant`, `metadata.kv_cache_disk` |
| Inference engine | `metadata.engine` (renamed from `runtime`) |
| Engine version | `metadata.engine_version` (from /props or build-time) |
| GPU | `machines.yaml.<host>.hardware_profile.chip` |
| Platform | `machines.yaml.<host>.hardware_profile.chip_family` |
| VRAM | `machines.yaml.<host>.hardware_profile.vram_gb` |
| Memory | `machines.yaml.<host>.hardware_profile.memory_gb` |
| TDP (operational) | `machines.yaml.<host>.tdp_watts_configured` (preferred) or `hardware_profile.tdp_watts_spec` |
| Sampling params | `sampling.{temperature, top_p, top_k}` |
| Reasoning | `sampling.reasoning` |

**Public descriptor format** (composed by aggregator):
```
Qwen3.6 27B · Q3_K_XL (Unsloth GGUF) · llama.cpp · RTX 3090 Ti (24 GB) · 225 W
DeepSeek V4 Flash · IQ2XXS-mixed-Q2K-Q8 · ds4-server · M2 Ultra (192 GB) · 200 W · KV on disk 128 GB
Qwen3.6 27B · cloud-hosted · OpenRouter
```

**Lanes excluded from public documentation** (per operator decision
2026-05-12): hardware-coupled internal lane names. They appear only as
internal IDs in URLs and the explorer's hidden column.

## 4. Validity rules — the noise filter

A lane qualifies for inclusion in story pages and the default
explorer view when ALL of:

1. **Coverage:** ≥3 reps on each task in the displayed task subset.
   Lane appears for tasks where it meets this bar; absent for those
   it doesn't.
2. **Reliability ceiling:** <20% of cells classified
   `invalid_class != null`. Above that, lane is infrastructure-broken,
   not model-failing; suppressed from public views.
3. **Reasoning-token coverage** (only for reasoning-capable models):
   ≥80% of cells have `reasoning_tokens > 0`. Catches lanes where the
   wire is wrong (silently no thinking).
4. **Valid-since gate:** Cells dated before the lane's `valid_since`
   timestamp are excluded. `valid_since` is per-lane, declared in
   the profile YAML, and reflects when fizeau started emitting the
   correct wire and capturing the correct telemetry for that lane.
5. **Required metadata complete:** profile YAML declares every
   characteristic dimension in §3. Profiles missing any required
   field are excluded with a structured "metadata incomplete" reason.
6. **Profile exists:** every cell record must reference a profile YAML
   that exists at aggregator-run time. Cells with no matching profile
   (e.g., the historical `vidar-qwen3-6-27b-openai-compat` lane —
   276 cells, profile YAML deleted operator-side 2026-05-12) are
   dropped and counted under `aggregates-rejected.json` with reason
   `profile_missing`.

Lanes that fail any criterion appear in `aggregates-rejected.json`
with the reason; the reliability page surfaces the rejected list with
explanations so the absence is honest, not hidden.

## 5. Data layer

### Build-time aggregator

`scripts/website/build-benchmark-data.py` walks
`benchmark-results/fiz-tools-v1/cells/<suite>/<task>/<lane>/rep-N/report.json`
and produces:

- `website/static/data/cells.{json,parquet}` — normalized
  result-bearing cells only: explicit timeouts, completed graded
  passes, and completed graded failures
- `website/static/data/task-combinations.{json,parquet}` —
  precomputed per-task/model/runtime/hardware summaries
- `website/static/data/cells.schema.json` — public feed schema
- `website/static/data/benchmark-data-manifest.json` — generation
  metadata, row counts, result-state counts, and exclusion counts for
  invalid or non-result-bearing source reports

### Cell record schema

```json
{
  "schema_version": 1,
  "task": "fix-ocaml-gc",
  "suite": "terminal-bench-2-1",
  "harness": "fiz",
  "internal_lane_id": "sindri-club-3090-llamacpp",
  "descriptor": "Qwen3.6 27B · Q3_K_XL (Unsloth GGUF) · llama.cpp · RTX 3090 Ti (24 GB) · 225 W",
  "model": "Qwen3.6-27B-UD-Q3_K_XL.gguf",
  "model_display_name": "Qwen3.6 27B",
  "model_family": "qwen3-6-27b",
  "tier": "smart",
  "quant_display": "Q3_K_XL (Unsloth GGUF)",
  "weight_bits": 3,
  "kv_cache_quant": "default",
  "kv_cache_disk": false,
  "engine": "llama.cpp",
  "engine_version": "b9014-d4b0c22f9",
  "hardware_chip": "nvidia-rtx-3090-ti",
  "hardware_chip_family": "nvidia-cuda",
  "hardware_vram_gb": 24,
  "hardware_memory_gb": 96,
  "hardware_tdp_watts": 225,
  "hardware_tdp_source": "configured",
  "rep": 3,
  "final_status": "graded_pass",
  "invalid_class": null,
  "wall_seconds": 1989,
  "reasoning_tokens": 10734,
  "reasoning_tokens_approx": false,
  "input_tokens": 78000,
  "output_tokens": 3200,
  "cost_usd": 0,
  "turns": 28,
  "started_at": "...",
  "finished_at": "...",
  "valid_lane": true,
  "lane_validity_reasons": []
}
```

Hardware fields (`hardware_*`) come from joining the cell's profile
YAML against `scripts/benchmark/machines.yaml`. Internal lane id stays
in the record but is NOT a public column.

### Aggregate record schema

`aggregates.json` is keyed by `(engine, model_display_name,
hardware_chip)` (the public descriptor's load-bearing dimensions) with:

- counts: `n_cells`, `n_pass`, `n_fail`, `n_invalid`
- rates: `pass_rate`, `invalid_rate`
- walls: `wall_p50`, `wall_p95`, `wall_max`
- cost: `cost_p50`, `cost_total`
- reasoning: `reasoning_tokens_p50`
- coverage: `tasks_covered`, `tasks_with_min_3_reps`
- validity: `valid_lane` (bool), `validity_reasons`

## 6. Explorer

The explorer implementation is superseded by FEAT-008, ADR-015, and
SD-015. The binding architecture is now a browser-side analytical
workbench over generated Parquet:

- `cells.parquet` is the primary result-bearing cell artifact.
- DuckDB-WASM loads Parquet and runs preset filters plus aggregates.
- Perspective renders the interactive datagrid/pivot UI.
- The page remains static-site deployable; no Datasette, SQLite, DuckDB
  server, or custom API is introduced.

The SD-014 information-architecture role is unchanged: `/explorer/` is
the operator-facing raw-data surface. The technical decision changed
because the required table is no longer a small JSON feed; it must
support thousands of rows, hundreds of columns, and analytical
aggregates over every collected benchmark variable.

## 7. Methodology page

`/benchmarks/methodology/` documents:

- TerminalBench-2.1 overview: tasks, what they test, grading method
- Harbor wrapper: per-cell invocation contract
- Cell schema: every field documented
- Task-base wall budget rationale (QWEN_BASE × LANE_PENALTY framework)
- Validity rules from §4, with rationale per
- Reproducibility: how to reproduce a single cell, how to reproduce a
  lane sweep
- Versioning: cells include `fiz_tools_version` and
  `profile_snapshot` so historical comparisons are tractable
- ADR cross-references: ADR-010 reasoning wire, ADR-007 sampling
  catalog, ADR-009 routing surface, ADR-012 cache

## 8. Provenance pillar

`/benchmarks/provenance/` documents the historical record. Distinct
from methodology: methodology is the formal contract; provenance is
the war-stories and operator gotchas.

- **changelog.md** — date-ordered fixes + discoveries with cell-level
  evidence
- **reasoning-control-saga.md** — the wire-format dialect investigation
  (OR Qwen3 effort flat-mapping → kwargs envelope → ds4 alias collapse
  → introspection moment → L1/L2/L3 architecture per ADR-010)
- **timeout-calibration.md** — QWEN_BASE × LANE_PENALTY framework
  derivation, terminated_mid_work classification
- **token-accounting.md** — usage path vs reasoning_content fallback;
  why `_approx=true` is honest
- **classification.md** — invalid_* taxonomy + evolution
- **stack-notes/** — per-(engine, model-class) operator reference
  (NOT per physical machine). Each entry documents wire quirks, known
  limitations, operator setup checklist, current status.

## 8.5 Visual components and Hugo shortcodes

The site already has a shortcode catalog under
`website/layouts/_shortcodes/` and a custom stylesheet at
`website/assets/css/custom.css`. New pages MUST extend these rather
than introduce parallel infrastructure:

- **Reuse first.** Every story page should be expressible via existing
  shortcodes. If a page would require a new visual element, propose
  it as a new shortcode under `_shortcodes/` and a CSS rule in
  `custom.css` that consumes DESIGN.md tokens.
- **Required new shortcodes** (file under W3/W4 or a shared
  components bead):
  - `descriptor-pill` — renders a lane characteristic descriptor
    (e.g. "Qwen3.6 27B · Q3_K_XL · llama.cpp · RTX 3090 Ti · 225 W")
    with semantic role coloring.
  - `pass-rate-bar` — horizontal segmented bar (pass / fail / invalid)
    using DESIGN.md role colors.
  - `paired-comparison-row` — Story 1 table row showing two lanes
    side-by-side with a delta.
  - `headline-table` — Story 2 sortable table; consumes
    `aggregates.json`.
  - `validity-badge` — chip indicating a lane's validity status with
    tooltip showing reasons (consumes `aggregates-rejected.json`).
  - `provenance-callout` — used by methodology + provenance pages to
    cite ADRs/commits inline.

- **No new accent colors, no inline `style="..."` with hex codes** in
  new templates or shortcodes. Everything goes through DESIGN.md
  tokens defined in `website/assets/css/custom.css` (or its successor).

## 9. Per-page data flow

| Page | Consumes | Build-time |
|---|---|---|
| Pillar landings (`_index.md`) | `aggregates.json` headline numbers | Hugo template + `getJSON` |
| Story 1 sub-pages | `aggregates.json` filtered to harness pairs | Hugo template + Python-generated SVG charts |
| Story 2 sub-pages | `aggregates.json` filtered to engine/hardware/backend cuts | Hugo template + SVG charts |
| Reliability page | `aggregates.json` + `aggregates-rejected.json` | Hugo template (transparent rejection list) |
| Methodology + provenance | Static markdown | None (pure docs) |
| Explorer | `cells.parquet` + `task-combinations.parquet` | DuckDB-WASM + Perspective client-side workbench (see SD-015) |
| Reports per-suite | Existing per-run reports | None — preserved as-is |

## 10. Implementation phasing

| # | Title | Scope |
|---|---|---|
| **W-EPIC** | Benchmark site redesign — two stories + provenance + dynamic explorer | parent |
| **W0** | Visual conformance audit + DESIGN.md token coverage in `custom.css` | verifies §0 contract; baseline before content beads land |
| **W1** | Build-time aggregator + validity rules + cell schema | foundation; produces `website/static/data/{cells,cells-valid,aggregates,aggregates-rejected,machines}.json` and removes the deprecated content-tree JSON pipelines |
| **W2** | Methodology page | documents §4 validity, §5 schemas, reproducibility |
| **W3** | Story 1 pages — harness comparison pillar + sub-pages | depends on W1; audits existing `website/content/benchmarks/terminal-bench-2-1/harnesses/*` (per §2.1) — reuse where token-compliant, rewrite otherwise |
| **W4** | Story 2 pages — model landscape pillar + sub-pages | depends on W1; same audit treatment for `models/*` and `providers/*` |
| **W5** | Explorer workbench with DuckDB-WASM + Parquet + Perspective | depends on W1/FEAT-008/ADR-015/SD-015; no server component |
| **W6** | Provenance pillar + stack-notes | self-contained docs; can land first |
| **W7** | Profile YAML metadata backfill (per §3 required dimensions) | prerequisite to W1's aggregator passing the validity gate for all worth-including lanes |
| **W8** | URL migration + 301 redirects from current per-(provider/harness/model) lane-name URLs | preserves bookmark/SEO continuity when descriptor-first replaces the old structure; runs after W3+W4 land |

Dependency graph:
- W0 standalone (visual baseline)
- W7 → W1 → (W2, W3, W4, W5) → W8
- W6 standalone (documentation-heavy)

## 11. Decisions captured (operator review 2026-05-12)

1. **Page granularity:** flat URLs under `/benchmarks/`; pillar pages
   must have left-hand nav.
2. **Methodology placement:** dedicated page (not inline-only).
3. **Comparison fairness lenses:** all measured lenses get coverage
   (model + tool surface + harness behavior + retry semantics).
4. **Explorer scope:** "valid data" only by default (per §4); toggle
   to show all.
5. **Update cadence:** manual `make site` rebuild for v1; CI later.
6. **Charts:** SVG for story-page headlines (server-rendered);
   interactive table for the explorer.
7. **Lane identity:** characteristic descriptors only in public
   surfaces (per §3); hardware-coupled internal lane names suppressed.
8. **Power tracking:** `tdp_watts_configured` per machine (operational)
   plus `tdp_watts_spec` per hardware profile (chip max). Aggregator
   uses configured if present, else spec.
9. **Future deferred:** per-run TDP capture via nvidia-smi /
   powermetrics — filed as a future bead.

## 12. Out of scope

- **Modifying DESIGN.md.** This spec consumes the design system; it
  does not change it. Any visual addition (new component, new chart
  type) requires DESIGN.md to grow first, then SD-014's consumers
  follow.
- Real-time updates (operator triggers `make site`).
- Multi-machine cache sharing for the data layer (per-host build).
- OR sub-provider (`@<sub_provider>`) expansion in lane identity —
  deferred to the M5 bead in the model-snapshot epic; site uses the
  parent OR descriptor for now.
- Pre-rendered chart artifacts in git — charts are build-time
  generated, with `make site-charts` for local review.
- Authentication / private-data publication — TB-2.1 is public, this
  spec assumes public publication. Future suites with sensitive
  prompts get separate review.
