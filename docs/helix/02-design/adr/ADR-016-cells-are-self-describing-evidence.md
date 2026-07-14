---
ddx:
  id: ADR-016
  created: 2026-05-16
  depends_on:
    - ADR-007
    - ADR-015
    - SD-009
    - SD-010
    - SD-012
  supersedes_scope_of:
    - plan-2026-05-15-benchmark-runner-simplification
  review:
    self_hash: ac33068859c7d86a3e11b375abe0e2a1e37175db533918c48a449dd370b8df72
    deps:
      ADR-007: 8a70a46a9efdc916ea7e3146dce7050a12d054c1c9d56c006bb4c3245b4d4300
      ADR-015: eacf3702878a7101f78e029e8e8e1717a5789a60830748c5d493970cfb9c9ca1
      SD-009: 7f518a10f93a3bee6ab4e09dadb24b7bfd43822db3c0bbfed43de0abb664b83a
      SD-010: 12f0b391c23a5a2b7e2abbbc6fec88ec6c7a57a7ff0a20e39c3a065a6c818e7a
      SD-012: bf30b7ba2939970dc4b6f94343403b37d72bc5626314a689682abcdd93d3acb2
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-016: Cells Are Self-Describing Evidence

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-16 | Accepted | Fizeau maintainers | ADR-007, ADR-015, SD-009, SD-010, SD-012 | High |

## Context

The benchmark runner accumulated six overlapping concepts in
`scripts/benchmark/terminalbench-2-1-sweep.yaml` (~2000 lines): subsets,
recipes, lanes, comparison groups, resource groups, and lane aliases. The
operator pain symptoms are concrete:

- Adding a model required edits in 3–4 places (profile YAML, lane block,
  recipe enrollment, shell alias case arm). The `fiz-vidar-ds4-mtp`
  incident left a lane half-wired with a missing profile, missing recipe
  membership, and a missing alias.
- A simple URL change (e.g. lucebox endpoint port 1236 → 8080) required
  updating `metadata.endpoint`, `provider.base_url`, the snapshot string,
  `resource_groups[].base_url`, the lane's `FIZEAU_BASE_URL`, and the
  lane's denormalized `endpoint` field. `cmd/bench/sweep.go:910-919`
  validates that lane env matches profile fields, forcing the duplication
  to be kept in sync by hand.
- The closed EPIC `fizeau-62693ed0` ("simplify benchmark lane authoring")
  responded by adding more Go tooling (`fiz-bench lanes clone`,
  `validateSweepLaneEnvMatch`) rather than reducing the multi-file
  structure. That solved authoring papercuts but locked in the
  duplication.
- Analytics scripts (`scripts/website/build-benchmark-data.py:150`)
  maintain a `PROFILE_ALIASES` rename map plus an `EXCLUDED_PROFILES` set
  because profile IDs in older cells refer to YAMLs that no longer exist
  in `scripts/benchmark/profiles/`. As of 2026-05-16 there are 4 orphan
  profile IDs (`sindri-club-3090`, `vidar-qwen3-6-27b-openai-compat`,
  `sindri-club-3090-llamacpp`, `gpt-5-3-mini`) covering 663 cell
  directories.
- The `comparison_groups:` block in the sweep YAML is referenced only by
  the sweep YAML itself and lane-meta JSON — zero Python readers.
  Operators maintain it but no analytics consume it.

The root cause is treating cells as *references* into a live configuration
graph (profile YAML × sweep YAML × machines.yaml × PROFILE_ALIASES). When
any node in that graph changes or disappears, historical cells either
break or require alias machinery to survive.

A 2026-05-15 plan (`plan-2026-05-15-benchmark-runner-simplification.md`)
proposed moving execution from `cmd/bench` Go code to a thin shell
driver, keeping recipes and lanes as orthogonal axes. That fixes
operator UX but leaves the underlying graph-of-references problem in
place — recipes, lanes, and resource groups still must be cross-validated
against profile YAMLs, and historical cells still depend on the YAMLs
they were written against.

## Decision

**Cells are self-describing evidence.** Each cell `report.json` embeds
the full resolved profile snapshot at write time. After a cell is
written, it stands alone. Profile YAMLs and bench-set YAMLs are
*authoring templates used at write time*, not *join targets at read
time*.

Concretely:

1. **Cell schema embeds full profile.** Every `report.json` carries an
   embedded `profile:` block containing the complete resolved provider
   config, sampling, limits, pricing, harness, surface, metadata, and
   versioning state used when the cell ran. Cells also carry
   `framework`, `dataset`, `task_id`, and a `cell_id` (timestamp +
   random suffix).
2. **No profile-ID join required for analytics.** Renaming a profile,
   deleting a profile, or changing a profile's config does not affect
   historical cells. The cell carries what it carried.
3. **Profile YAMLs and bench-set YAMLs are authoring templates only.**
   They feed the runner; they do not participate in cell-read paths.
4. **Bench-sets are admin metadata.** A bench-set declares which tasks
   to run, default reps, framework, and dataset. It is *not* recorded
   on the cell — same cell can be produced by any bench-set that
   enumerates the same task.
5. **Cells have monotonic unique IDs.** Cell directories are named with
   `<ISO-timestamp>-<random-suffix>` (e.g.
   `20260516T103045Z-a4c1`). No rep counter; no race on concurrent
   writes; chronological sort gives history.

The on-disk layout becomes:

```
benchmark-results/<canonical>/cells/
  <framework>-<dataset>/
    <task>/
      <cell-id>/
        report.json          # embedded profile + metrics + identity
        fiz.txt              # raw fiz log (when applicable)
        session/             # trajectory artifacts (when applicable)
```

The authoring templates live at:

```
scripts/benchmark/
  profiles/<id>.yaml         # provider, sampling, limits, pricing,
                             # harness, surface, metadata, versioning,
                             # concurrency_group
  bench-sets/<id>.yaml       # framework, dataset, task list, default reps
  concurrency-groups.yaml    # rate-limit shard caps (small)
  machines.yaml              # hardware reference (unchanged)
```

## Consequences

### Positive

- **Orphans evaporate.** Cells whose `profile_id` references a deleted
  YAML still carry their own metadata. The `PROFILE_ALIASES` and
  `EXCLUDED_PROFILES` tables in `build-benchmark-data.py` become
  unnecessary.
- **Cross-validation disappears.** No `validateSweepLaneEnvMatch`,
  no lane-vs-profile checks, no recipe-references-existing-lane checks.
  A profile is valid if its YAML loads; a bench-set is valid if its YAML
  loads; cells are valid evidence regardless.
- **Operator UX simplifies.** Adding a new model is one new
  `profiles/<id>.yaml` file. No lane block, no recipe enrollment, no
  shell alias case arm, no resource-group entry.
- **Profile renames become a non-event.** Historical cells survive; new
  cells take the new name.
- **Concurrent writes are race-free.** Timestamp-based cell IDs need no
  counter coordination.
- **`fiz-bench` execution path retires.** Per the 2026-05-15 plan,
  execution moves to `./benchmark` shell driver; `fiz-bench` shrinks to
  validate / aggregate / import tooling.

### Negative

- **Cell size grows by ~1KB.** Embedded profile snapshot adds ~200–800
  bytes of YAML-equivalent JSON per cell. Negligible at thousands of
  cells; trivially compressible.
- **Profile-level changes don't propagate retroactively.** If you fix a
  typo in a profile's `pricing.input_usd_per_mtok`, old cells keep the
  old (wrong) pricing in their embedded snapshot. This is a feature
  (cells are immutable evidence) but operators must understand it.
- **Backfill required.** Existing cells (~7000+ across `benchmark-results/`)
  need their profile metadata embedded. One-shot script reads each cell,
  resolves its `profile_id` against current YAMLs plus
  `PROFILE_ALIASES`, embeds the resolved snapshot. Cells whose profile
  is irrecoverable get deleted. Idempotent re-run safe.
- **Recipe/lane/comparison_group/resource_group disappear as concepts.**
  Operators who learned the old vocabulary must retrain on
  profile/bench-set. Six wrapper-harness lanes
  (`fiz-harness-claude-sonnet-4-6`, etc.) become six distinct profiles
  carrying explicit `harness:` fields rather than sharing a profile
  through a lane-level env override.

### Neutral

- **Analytics rewrites.** `build-benchmark-data.py`, `generate-report.py`,
  and `terminalbench_import.go` switch from "load profile YAML, join on
  profile_id" to "read embedded `cell.profile.*`." Smaller change than
  it sounds — most fields are already accessed via the profile_id join.

## Alternatives Considered

### A. Keep references; add machinery to manage staleness

The 2026-05-15 plan's direction: keep recipes and lanes, move execution
to shell, validate references early. Rejected because the reference
graph itself is the problem. `PROFILE_ALIASES` is already evidence that
references break and need rescue machinery. Adding more validation
catches errors earlier but does not prevent the underlying invalidation.

### B. Snapshot only the changed fields (delta encoding)

Embed only fields that differ from a baseline. Rejected as premature
optimization. Cells are already ~10KB+ from metrics; adding ~800 bytes
of metadata is noise. Delta encoding adds complexity for no observable
benefit.

### C. Content-addressed profile snapshots

Hash the profile YAML, store snapshots once in a content-addressed
store, reference by hash from cells. Rejected as over-engineered. The
simplest possible thing — embed the YAML — meets every requirement.

### D. Keep `profile_id` as primary identity; embedded metadata as
informational only

Reject. Defeats the purpose. As long as `profile_id` is the join key,
the staleness problem returns the first time a profile is renamed or
deleted.

## Migration

See `plan-2026-05-15-benchmark-runner-simplification.md` (rewritten as
of 2026-05-16) for the full sequence. Highlights:

1. Add `harness`, `surface`, `concurrency_group` to profile schema.
2. Add `bench-sets/*.yaml` and `concurrency-groups.yaml`.
3. Runner writes self-describing cells (embedded profile + cell_id).
4. One-shot backfill of existing cells.
5. Analytics switch to embedded cell metadata.
6. Delete lane / recipe / comparison_group / resource_group /
   lane_alias scaffolding (`cmd/bench/lanes*.go`,
   `validateSweepLaneEnvMatch`, sweep YAML lane block, PROFILE_ALIASES,
   shell alias case arms).
7. Replace `fiz-bench` execution with `./benchmark` shell driver.
8. Documentation + operator runbook updates.

## Status

Accepted 2026-05-16. Implementation tracked under beads derived from
`plan-2026-05-15-benchmark-runner-simplification.md` (rewritten).
