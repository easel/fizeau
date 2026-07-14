---
ddx:
  id: website/DESIGN.md
  depends_on:
    - helix.product-vision
  review:
    self_hash: 920c3b6fb2b3daed4a4b409de13f57f3c07f7c5732e4a32bc1ad42f389aa842b
    deps:
      helix.product-vision: eb5af3663734d35e7b42963ce12e39adc19147aa2df25fe9bd3887793217836c
    reviewed_at: "2026-07-14T05:16:22Z"
---
# Fizeau — Design System

The visual language for the Fizeau microsite and any first-party Fizeau surface (CLI banners, generated reports, embedded widgets). The aesthetic is **scientific instrument**, not developer-tool landing page.

## Brand grounding

The project is named for Hippolyte Fizeau, who built rotating-toothed-wheel apparatus to measure the speed of light in 1849, then measured the drag of light through moving water in 1851. The visual language is the language of his lab notebooks and the instruments that descend from them: chronographs, oscilloscopes, spectrum analyzers, photometric tables.

**Three principles fall out:**

1. **The data is the headline.** Numbers, charts, and timing readouts are first-class citizens, not illustrations to a marketing page. Layout serves the measurement.
2. **The medium matters.** A precision instrument shows you what its substrate is doing — current draw, internal temperature, sample rate. Our surfaces always show provenance: snapshot date, sample size, what was filtered out, why.
3. **Differences are easier to read than absolutes.** Charts emphasize per-run deltas, ratios, and bucketed comparisons. We avoid lone numbers without context.

## Color palette

Two-mode: **light** is the default for documentation; **dark** is the canonical mode for instrument readouts (it's where measurements look right, the way oscilloscope traces always do). Both modes are first-class — neither is an afterthought.

### Light mode

| token | hex | role |
|---|---|---|
| `--surface-page` | `#fbfaf6` | page background — warm bone-white, like printed tape |
| `--surface-panel` | `#ffffff` | cards, tables, instrument panels |
| `--surface-sunken` | `#f1eee6` | section dividers, code blocks, inset readouts |
| `--ink-primary` | `#0e1116` | body text, headings |
| `--ink-secondary` | `#454a52` | metadata, labels, captions |
| `--ink-tertiary` | `#7d838c` | timestamps, low-emphasis annotation |
| `--rule` | `#d6d3ca` | borders, dividers, table grid |
| `--rule-strong` | `#b3afa2` | section underlines, table head |
| `--accent-cyan` | `#0a8e8e` | primary signal — links, active states, primary chart series |
| `--accent-amber` | `#c46a00` | live data, "now" markers, warning-but-not-error |
| `--accent-green` | `#1f7a3f` | pass, success, positive deltas |
| `--accent-red` | `#b22b2b` | fail, error, negative deltas |
| `--accent-violet` | `#5e3aa8` | external/comparator series, neutral third |

### Dark mode

| token | hex | role |
|---|---|---|
| `--surface-page` | `#0a0e14` | page background — deep instrument-bezel black with a navy shift |
| `--surface-panel` | `#10151d` | cards, tables, instrument panels |
| `--surface-sunken` | `#070a0f` | inset readouts, code blocks |
| `--ink-primary` | `#e6edf3` | body text |
| `--ink-secondary` | `#9ea7b3` | metadata |
| `--ink-tertiary` | `#5e6772` | timestamps |
| `--rule` | `#262e3a` | borders — readable hairline against `#0a0e14` page bg |
| `--rule-strong` | `#3d4756` | head-of-table, dividers |
| `--accent-cyan` | `#4dd4cf` | primary signal — phosphor cyan, used for live state |
| `--accent-amber` | `#ffb454` | live data, "now" markers — phosphor amber |
| `--accent-green` | `#7ce38b` | pass, success |
| `--accent-red` | `#ff7b7b` | fail, error |
| `--accent-violet` | `#a78bfa` | external/comparator series |

The amber and cyan in dark mode are the signature colors. They do double duty: they are the only highly-saturated values in the palette (everything else is greys), so any chart point or active label naturally reads as "live data." Use them sparingly — overuse loses the meaning.

### Semantic chart palette

Always assign colors by **role**, not aesthetics. Never have two unrelated series share a color in the same view.

| role | light | dark |
|---|---|---|
| primary measurement | `--accent-cyan` | `--accent-cyan` |
| secondary / comparison | `--accent-amber` | `--accent-amber` |
| pass / positive | `--accent-green` | `--accent-green` |
| fail / negative | `--accent-red` | `--accent-red` |
| external / third-party | `--accent-violet` | `--accent-violet` |
| neutral / baseline | `--ink-tertiary` | `--ink-tertiary` |

## Typography

Two families. No third. Both available as variable fonts so weight is a continuous axis.

- **Mono** (`--font-mono`): `"JetBrains Mono Variable", ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace`. Used for: all numerals in tables and charts, code, captions, eyebrow labels, hero headlines, brand mark.
- **Sans** (`--font-sans`): `"Inter Variable", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, system-ui, sans-serif`. Used for: long-form prose, navigation, body text in cards, button labels.

If JetBrains Mono Variable isn't available the system mono stack is acceptable — never substitute a proportional font for a mono context.

### Type scale

Modular scale, ratio 1.250 (major third), base 16 px:

| token | size | line-height | use |
|---|---|---|---|
| `--text-xs`   | 0.75 rem (12 px)  | 1.4 | table dense / metadata |
| `--text-sm`   | 0.875 rem (14 px) | 1.5 | secondary body, captions |
| `--text-base` | 1 rem (16 px)     | 1.6 | body prose |
| `--text-lg`   | 1.125 rem (18 px) | 1.55| lead paragraphs |
| `--text-xl`   | 1.25 rem (20 px)  | 1.4 | section subheadings (h3) |
| `--text-2xl`  | 1.5625 rem (25 px)| 1.3 | section headings (h2) |
| `--text-3xl`  | 1.953 rem (31 px) | 1.2 | page titles (h1) |
| `--text-4xl`  | 2.441 rem (39 px) | 1.1 | hero |

**Important readability minimums.** No body text below `--text-sm` (14 px). No prose narrower than 65 ch on docs pages. Tables may use `--text-xs` for cell content but headers and numerics are always `--text-sm` minimum. The previous report styling went to 11 px in places — that is now forbidden.

### Headline mechanics

- h1, h2, h3 in **mono**, 500 weight, slight letter-spacing (-0.01em). They look like instrument-panel labels, not magazine headlines.
- Body text in **sans**, 400 weight, normal tracking.
- All-caps eyebrow labels (e.g., "HARDWARE", "HARNESS") in **mono** at `--text-xs` with 0.08em letter-spacing.

## Spacing scale

4 px base, doubling and quartering. Use these tokens — never one-off pixel values.

| token | value |
|---|---|
| `--space-1` | 4 px |
| `--space-2` | 8 px |
| `--space-3` | 12 px |
| `--space-4` | 16 px |
| `--space-5` | 24 px |
| `--space-6` | 32 px |
| `--space-7` | 48 px |
| `--space-8` | 64 px |
| `--space-9` | 96 px |

Section vertical rhythm: `--space-7` between top-level sections; `--space-5` between subsections; `--space-3` between paragraphs.

## Radii, borders, shadows

Instruments don't blur. We don't use shadows. Depth is conveyed by a 1-pixel hairline rule against a slightly darker surface.

- `--radius-sm` 2 px (pills, badges)
- `--radius-md` 4 px (cards, tables, panels) — the default
- `--radius-lg` 8 px (hero blocks, large readout panels)
- Border width: always 1 px. Color from `--rule` or `--rule-strong`.
- Box shadow: prohibited. If you need to imply elevation, use a thicker rule or a different surface token.

## The rotating-wheel motif

Fizeau's apparatus had a brass disk pierced by 720 evenly-spaced teeth. The motif appears in three places:

1. **Brand mark / logo**: an SVG of a stylized 24-tooth wheel, rendered as concentric arcs. 16 × 16 favicon, 32 × 32 in the navbar, 64 × 64 in the hero. The wheel is static in static contexts.
2. **Loading state**: same wheel, slowly rotating (4-second period, linear). Uses `--accent-cyan`.
3. **Section divider**: a horizontal rule containing a small (12 px) wheel mark inset at the left third. Used between numbered report sections.

The wheel is the *only* iconography; we don't introduce ad-hoc icons. Where Hextra's defaults supply icons (search, github), keep them. New surfaces don't add new visual primitives.

## Components

The vocabulary is small on purpose. Every Fizeau surface should look like it was assembled from the same kit of parts.

### Panel

The fundamental container. White (light) or `--surface-panel` (dark) on top of `--surface-page`. 1-px hairline rule. `--radius-md`. Used for: cards, tables, profile cards, machine-info blocks, any region that should read as "an instrument panel."

```
┌─ HARDWARE ─────────────┐
│ chassis  Custom desktop│
│ cpu      AMD 5950X     │
│ gpu      RTX 5090 Ti   │
│ os       Windows + WSL │
└────────────────────────┘
```

### Eyebrow label

Mono, `--text-xs`, uppercase, 0.08em tracking, `--ink-secondary`. Sits flush against the top edge of a Panel or as a section indicator. Defines what the panel contains in 1–3 words.

### Data table

- Mono numerals, right-aligned.
- First column left-aligned.
- 1-px rule between rows in `--rule`; thicker rule below `<thead>` in `--rule-strong`.
- Zebra-striping disabled. Hover highlight: `--surface-sunken`.
- Cells `--text-sm`. Headers `--text-xs` uppercase.

### Pill

Mono, `--text-xs`, 1px-rule outline, `--radius-sm`, `--space-1` `--space-2` padding. Default uses `--ink-secondary` text and rule. Color variants: `cyan` (active/live), `amber` (warning/comparator), `red` (failure), `green` (pass), `violet` (external).

### Hero readout

Big mono number flanked by an eyebrow label and a unit. Used on the homepage hero and at the top of the benchmark report. Numeric value at `--text-4xl` mono, 500 weight, `--accent-cyan`. Label above in eyebrow style, unit below in `--text-sm` mono `--ink-tertiary`.

```
LATEST QWEN3.6-27B BENCH
       546.7
   tokens / second  · openrouter · 2026-05-09
```

### Strip chart

Inline SVG, single signal over time, plotted on a faint dotted grid. The y-axis label is mono `--text-xs` rotated 90°; the x-axis is unlabeled (time is implicit). Uses `--accent-cyan` for the trace, `--accent-amber` for the most recent sample, `--ink-tertiary` for the grid. Used on the homepage for "live measurement" and on the benchmark page above each chart section.

### Code block

Mono `--text-sm`, `--surface-sunken` background, 1-px rule, no shadow. Hextra's syntax highlighting is preserved with custom token colors that match the palette.

## Page anatomy

### Homepage

Top-down: navbar (Hextra default with mono brand mark) → hero block (project name in mono `--text-4xl`, one-line value prop in sans `--text-lg`, primary CTA, hero readout panel showing live measurement) → feature grid (4–6 panels, each with eyebrow + 1-sentence prose + small instrument illustration where relevant) → footer with the "Named for Hippolyte Fizeau" credit and the rotating-wheel mark.

### Benchmark report

Snapshot eyebrow at top with timestamp + sample size. Section headings in mono. Data tables and charts dominate; prose is contextual scaffolding around them. Each chart is wrapped in a Panel with its own eyebrow label and a one-line caption underneath.

### Docs page

Reading width capped at 70 ch. Sans body, mono headings. Hextra's left-sidebar nav and right-side TOC are preserved (typography retuned). Long-form prose pages may include "instrument insets" — small captioned diagrams (e.g., the toothed-wheel apparatus) — which are SVG inside a Panel.

## Implementation notes

- All overrides live in `website/assets/css/custom.css`. Hextra picks this file up automatically.
- Custom partials in `website/layouts/partials/custom/` (e.g., `head-end.html`) for additions like the brand mark SVG and font preloads.
- Web fonts are loaded from `rsms.me/inter` and `cdn.jsdelivr.net/.../jetbrains-mono` by default; can be self-hosted later for offline / air-gapped builds.
- The benchmark report's scoped CSS (`REPORT_CSS` in `scripts/benchmark/generate-report.py`) is reduced to a thin shim that consumes the same CSS variables defined here. Updating this DESIGN.md plus `custom.css` is the only place visual changes happen.

## Process notes

- Treat this document as the single source of truth. New components, colors, or icons need to be added here first; if a one-off appears in code without a corresponding entry, that's a bug.
- The Stitch design system (when it eventually works) should be initialized from this file via `mcp__stitch-sa__create_design_system_from_design_md`. We did not start from Stitch mockups; we wrote the tokens deliberately and Stitch is an iteration tool, not the source of authority.
