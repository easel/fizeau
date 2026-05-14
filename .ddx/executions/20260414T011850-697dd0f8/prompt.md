# Execute Bead

You are running inside DDx's isolated execution worktree for this bead.
Your job is to make a best-effort attempt at the work described in the bead's Goals and Description, then commit the result. Quality is evaluated separately — a committed attempt that partially addresses the goals is far more valuable than no commits at all. Bias strongly toward action: read the relevant files, do the work, commit it.

## Bead
- ID: `agent-39586e24`
- Title: review: stale open beads (AR-2026-04-12)
- Parent: `agent-0065014c`
- Labels: helix, phase:review, kind:review
- Base revision: `7cb398b1782a8a847a2112905bfe70a529914ff9`
- Execution bundle: `.ddx/executions/20260414T011850-697dd0f8`

## Description
Review area: tracker hygiene — open beads for work that has already been shipped.

Stale beads identified:
- agent-7f1dec9b (fix: detect and abort identical consecutive tool-call loops): FULLY IMPLEMENTED in loop.go (toolCallLoopLimit, toolCallFingerprint) and errors.go (ErrToolCallLoop), tested in loop_test.go (TestRun_ToolCallLoopDetection, TestRun_ToolCallLoopCounterResetsOnDifferentCall), merged in v0.3.1.
- agent-cf7e4cc8 (fix: detect runaway reasoning loop and abort stream early): FULLY IMPLEMENTED in stream_consume.go (ErrReasoningOverflow at reasoningByteLimit=32k, ErrReasoningStall with stall timer), loop.go (non-retryable handling), tested in stream_consume_test.go (TestConsumeStream_ReasoningOverflow, TestConsumeStream_ReasoningStall), merged in v0.3.1.

Both acceptance criteria are met by the merged implementation.

## Acceptance Criteria
Both stale beads closed; tracker cleaned up

## Governing References
No governing references were pre-resolved. Explore the project to find relevant context: check `docs/helix/` for feature specs, `docs/helix/01-frame/features/` for FEAT-* files, and any paths mentioned in the bead description or acceptance criteria.

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
