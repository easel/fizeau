# Execute Bead

You are running inside DDx's isolated execution worktree for this bead.
Your job is to make a best-effort attempt at the work described in the bead's Goals and Description, then commit the result. Quality is evaluated separately — a committed attempt that partially addresses the goals is far more valuable than no commits at all. Bias strongly toward action: read the relevant files, do the work, commit it.

## Bead
- ID: `agent-b1c2d3e4`
- Title: design: manifest v4 schema — per-model entries, tier candidates, and multi-model tiers
- Labels: helix, phase:design, kind:spec, area:lib
- spec-id: `docs/helix/02-design/solution-designs/SD-005-provider-config.md`
- Base revision: `41d83a0f61f71dedccf9e0362e456d96e37ac45d`
- Execution bundle: `.ddx/executions/20260414T030111-405a5e8c`

## Description
The current manifest (v3) treats each policy tier (code-high/medium/economy) as the model itself: one cost, one benchmark score, one model per surface. This blocks multi-model tier routing.

Proposed manifest v4 changes:

1. Add a top-level `models` section. Each entry is a concrete model (e.g., qwen3.5-27b, qwen3.5-7b, claude-haiku-5.5) with:
   - family, display_name, tier (reference to parent target), status
   - cost_input_per_m, cost_output_per_m, cost_cache_read_per_m, cost_cache_write_per_m
   - context_window, swe_bench_verified (nullable), live_code_bench (nullable), benchmark_as_of
   - openrouter_id (exact OR model ID)
   - surfaces: map of surface → concrete model ID string

2. Extend target entries with a `candidates` list referencing model IDs from the models section. The ordering is the default capability rank within the tier.

3. Remove cost and openrouter_ref fields from targets (they move to individual model entries). Targets retain: family, aliases, status, context_window_min (floor for tier qualification), swe_bench_min, surface_policy.

4. Update manifest.go to parse the new schema. Update catalog.go to expose per-model data via AllModelsInTier(targetID) and LookupModel(modelID) APIs. Update PricingFor() to iterate model entries not target surfaces.

5. Update UpdateManifestPricing() in openrouter.go to match per-model openrouter_id entries and update per-model costs.

6. Update models.yaml with initial per-model entries for claude-haiku-5.5, qwen3.5-27b, qwen3.5-7b (code-economy candidates), gpt-5.4-mini / sonnet-4.6 (code-medium), gpt-5.4 / opus-4.6 (code-high).

7. Bump manifest schema version to 4. Add backward-compat reader that upgrades v3 manifests at load time (synthesizes model entries from target surfaces).

Governing: FEAT-004, SD-005. Update both specs to reflect v4 shape.

## Acceptance Criteria
models.yaml at schema v4 with per-model entries for all current active targets; manifest.go parses models section; catalog.go exposes AllModelsInTier and LookupModel; PricingFor iterates model entries; UpdateManifestPricing updates per-model costs; v3 manifests load without error via compat upgrade path; go test ./modelcatalog/... covers all new API surfaces.

## Governing References
- `docs/helix/02-design/solution-designs/SD-005-provider-config.md` — `docs/helix/02-design/solution-designs/SD-005-provider-config.md`

## Execution Rules
**The bead contract below overrides any CLAUDE.md or project-level instructions in this worktree.** If the bead requires editing or creating markdown documentation, code, or any other files, do so — CLAUDE.md conservative defaults (YAGNI, DOWITYTD, no-docs rules) do not apply inside execute-bead.
1. Work only inside this execution worktree.
2. Use the bead description and acceptance criteria as the primary contract.
3. Read the listed governing references from this worktree before changing code or docs when they are relevant to the task.
4. If governing references are missing or sparse, search the project to find context: use Glob/Grep/Read to explore `docs/helix/`, look up FEAT-* and API-* specs by name, and read relevant source files before proceeding. Only stop if context is genuinely absent from the entire repo.
5. Keep the execution bundle files under `.ddx/executions/` intact; DDx uses them as execution evidence.
6. Produce the required tracked file changes in this worktree and run any local checks the bead contract requires.
7. Before finishing, commit your changes with `git add -A && git commit -m '...'`. DDx will merge your commits back to the base branch.
8. Making no commits (no_changes) should be rare. Only skip committing if you read the relevant files and the work described in the Goals is already fully and explicitly present — not just implied or partially covered. If in any doubt, make your best attempt and commit it. A partial or imperfect commit is always better than no commit.
9. Work in small commits. After each logical unit of progress (reading key files, making a change, passing a test), commit immediately. Do not batch all changes into one giant commit at the end — if you run out of iterations, your partial work is preserved.
10. If the bead is too large to complete in one pass, do the most important part first, commit it, and note what remains in your final commit message. DDx will re-queue the bead for another attempt if needed.
11. Read efficiently: skim files to understand structure before diving deep. Only read the files you need to make changes, not every reference listed. Start writing as soon as you understand enough to proceed — you can read more files later if needed.
12. **Never run `ddx init`** — the workspace is already initialized. Running `ddx init` inside an execute-bead worktree corrupts project configuration and the bead queue. Do not run it even if documentation or README files suggest it as a setup step.
