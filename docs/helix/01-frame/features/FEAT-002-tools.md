---
ddx:
  id: FEAT-002
  depends_on:
    - helix.prd
  review:
    self_hash: 1f53e72517347be0932a7b315aa1cc00cc48fc526ca3c53506cd179e8d0231a9
    deps:
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Feature Specification: FEAT-002 — Tool Set

**Feature ID**: FEAT-002
**Status**: Approved
**Priority**: P0
**Owner**: Fizeau Team
**Covered PRD Subsystem(s)**: Workspace Tools
**Covered PRD Requirements**: FR-2
**Cross-Subsystem Rationale**: Supplies the auditable action surface consumed
by embedded execution and projected by measurement and replay.
**User Stories**: [US-002 — Use Auditable Workspace Tools](../user-stories/US-002-workspace-tools.md)

## Overview

Fizeau provides a structured tool surface for filesystem and shell work:
read, write, edit, bash, find, grep, ls, patch, and task. The LLM uses these
tools to interact with the workspace, discover files, make precise changes,
and track work. Tools are the agent's hands. This implements PRD P0
requirement 2 and reflects the benchmark-driven navigation and task-tracking
capabilities already shipped.

## Problem Statement

- **Current situation**: Each agent CLI implements its own tools with different
  semantics (Claude Code has ~20 tools, pi has 4-7, codex has its own set).
  Fizeau now ships a broader, benchmark-informed surface than the original
  four-tool minimum.
- **Pain points**: Tool behavior varies across agents. DDx can't predict what
  file operations an agent will perform or constrain them.
- **Desired outcome**: A small, well-defined tool set with consistent behavior
  that Fizeau controls and DDx can audit.

## Requirements

### Functional Requirements

#### Core file and shell tools

1. `read` accepts path, optional line offset, and optional line limit
2. `read` resolves relative paths against the working directory
3. `read` returns file contents as string and errors when the file is missing
4. `write` accepts path and content, creates parent directories, and overwrites
   the file
5. `edit` accepts either multi-edit `edits[]` or legacy `old_string` + `new_string`
6. `edit` applies multi-edits atomically, from original content, with no overlap
7. `edit` fails when the match is missing or ambiguous
8. `bash` accepts a command and optional timeout, runs in the working directory,
   and captures stdout, stderr, and exit code
9. `bash` kills on timeout or context cancellation
10. `bash` supports an opt-in output filter. The filter applies **only to `bash`**;
    built-in `read`, `find`, `grep`, `ls`, and related tools are **not intercepted**.
    Filtering has two phases:
    a. **Before execution**: the filter may rewrite an allowlisted command to a
       proxy command such as `rtk git status`.
    b. **After execution**: the filter annotates the result and may apply internal
       post-processing for modes that do not proxy execution. The bash tool then
       applies bounded truncation to the final stdout/stderr.
    If the filter is unavailable or errors, the command result is still returned
    with normal truncation and a marker (`[output filter unavailable: ...]`).
    Filtering must not change exit code, stderr, timeout, or cancellation semantics.
    RTK mode executes `rtk` as the command proxy for allowlisted commands rather
    than post-processing arbitrary output. Commands not recognized or unsafe to
    rewrite run normally. If a rewritten command exits nonzero or times out, the
    agent preserves that result and does not re-run the original command.

#### Navigation, patching, and task-tracking tools

11. `find` finds files by pattern for codebase navigation
12. `grep` searches file contents in a read-only way
13. `ls` lists directory contents without requiring a shell command
14. `patch` applies structured search-and-replace edits
15. `task` creates and updates task-tracking records for multi-step work
16. Navigation and patch tools reduce the need for shell `ls`, `find`, and
    `grep` anti-patterns in benchmark workloads

### Non-Functional Requirements

- **Security**: Fizeau assumes it runs in a sandbox. File paths outside the
  working directory are allowed but logged. No path validation boundary.
- **Performance**: File operations complete in <10ms for files under 1MB.
  Bash tool adds <5ms overhead beyond the command's own execution time.
- **Reliability**: Tools never panic. All errors are returned as structured
  tool results that the model can interpret.

## Edge Cases and Error Handling

- **Symlink chains**: Resolve symlinks fully, log final target path
- **Binary file read**: Return error indicating binary content detected
- **Empty file write**: Allow (creates empty file)
- **Edit with empty old_string**: Reject (would match everything)
- **Bash command produces >1MB output**: Truncate with "[truncated]" marker
- **Bash command is interactive (reads stdin)**: Provide /dev/null as stdin

## Success Metrics

- All shipped tools pass acceptance tests with both local and cloud models
- All file operations are logged with resolved paths
- Bash timeout reliably kills runaway processes

## Acceptance Criteria

| ID | Criterion | Suggested Verification |
|----|-----------|------------------------|
| AC-FEAT-002-01 | `read`, `write`, `edit`, and `bash` implement the documented core semantics: relative-path resolution, parent-directory creation, atomic multi-edit behavior, ambiguous/missing edit failures, and timeout/cancellation handling without panics. | `go test ./tool ./...` |
| AC-FEAT-002-02 | Binary reads are rejected, `grep` skips binary files, interactive `bash` commands receive no interactive stdin, and oversized command output is truncated with the documented marker. | `go test ./tool ./...` |
| AC-FEAT-002-03 | File-path handling resolves chained symlinks to the final target, records the resolved path for tool-visible/log-visible reporting, and preserves the documented outside-workdir behavior instead of silently rebasing paths. | `go test ./tool ./...` |
| AC-FEAT-002-04 | Navigation tools (`find`, `grep`, `ls`) and the `patch` tool implement the documented search, truncation, line-ending, Unicode, and search/replace behaviors without requiring shell fallbacks for the common benchmark navigation cases. | `go test ./tool ./eval/navigation ./...` |
| AC-FEAT-002-05 | The `task` tool supports create/update/get/list operations with structured validation errors and remains concurrency-safe for multi-step agent workflows. | `go test ./tool ./...` |
| AC-FEAT-002-06 | At least one model-backed acceptance path exercises the shipped tool surface end-to-end so the benchmark-oriented semantics are validated against real provider/tool interaction rather than unit tests alone. | `go test -tags=integration ./...` |
| AC-FEAT-002-07 | Opt-in bash output filtering supports RTK proxy execution for allowlisted commands, falls back safely when RTK is unavailable, preserves nonzero exit/stderr/timeout semantics, and does not intercept built-in read/find/grep/ls tools. | `go test ./internal/tool ./...` |

## Constraints and Assumptions

- No network-access tool (bash can do network operations, but there's no
  dedicated fetch/curl tool — keep the surface area small)
- Tools are not extensible in P0. Custom tools are a P2 concern.

## Dependencies

- **Other features**: FEAT-001 (agent loop calls tools)
- **Governing design**: [Provider Identity, Routing Policy, and Bash Output Filtering](./../../02-design/plan-2026-04-19-provider-routing-tool-output.md)
- **PRD requirements**: P0-2

## Tool Specifications

### `anchor_edit` (anchor mode v1)

A token-efficient edit tool that addresses lines by an opaque anchor word
emitted alongside each line on the preceding `read`. Replaces the
file-rewrite cost of conventional `edit` (which echoes `old_string` text)
with a per-line lookup. **Opt-in only** — registered when the operator
passes `--anchors` or sets `anchors: true` in config; absent by default so
existing workflows are unchanged.

**Wire shape**:

```json
{
  "path":         "string",
  "start_anchor": "string",
  "end_anchor":   "string",
  "new_text":     "string",
  "offset_hint":  "integer (optional; required when an anchor is ambiguous)"
}
```

Empty `new_text` deletes the range.

**Anchor vocabulary**: A static, build-time-frozen list of ~1024 single-token
English nouns (capital-first, no apostrophes/digits/non-ASCII, no Go keywords)
that map to exactly one token in both `cl100k_base` and a Llama BPE tokenizer.
Lives at `internal/tool/anchorwords/words.go`. Runtime assignment is by line
index modulo 1024, so files longer than 1024 lines wrap and produce ambiguous
anchors that `offset_hint` disambiguates.

**Session state (`internal/tool/anchorstore`)**: Per-`Run`, in-memory,
thread-safe (`sync.RWMutex`). One instance per `Run` call, passed by pointer
into tool constructors — NOT part of `core.Request`. API:

- `Assign(path string, fileOffset int, lines []string)` — record anchors for
  a just-read slice. `fileOffset` matches `read`'s offset param so absolute
  line numbers stay correct under partial reads.
- `Lookup(path, anchor string) (line int, ambiguous bool)` — returns
  `(line, false)` on unique match, `(-1, true)` if ambiguous, `(-1, false)`
  if not found or store invalid for the file.
- `Invalidate(path string)` — drops the file's anchor map.

**Read-tool integration**: `read` accepts an optional `*AnchorStore`. When
non-nil, output lines are prefixed `Word: content\n` (no padding — token
efficiency over alignment) and the anchor words are passed to
`AnchorStore.Assign`. Truncation marker uses `...` prefix specifically because
`...` is not a vocabulary word and cannot be used as an edit target. When the
store is nil (legacy mode), output is byte-identical to today.

**Stale-state guards**: `write`, `edit`, and `patch` remain available in
anchor mode and do **not** call `Invalidate`. To prevent silent corruption:
(a) `anchor_edit` MUST verify the file's current line count matches the
stored anchor count before splicing — mismatch returns `"file changed since
anchors assigned; re-read"`; (b) the system prompt addendum activated under
`--anchors` MUST tell the model: *"Do not mix `edit`/`write` with
`anchor_edit` on the same file. Re-read to get fresh anchors after any
non-anchor change."*

**Execute order**: resolve path → lookup anchors (error on missing /
ambiguous-without-hint / invalidated) → validate `startLine ≤ endLine` →
splice → write → `Invalidate(path)` → return human-readable summary
referencing the anchor range. `Parallel() bool { return false }`.

**v1 explicitly excludes**:
- Myers-diff anchor refresh across non-anchor edits (deferred to v2).
- Automatic invalidation on `write`/`edit`/`patch` calls (manual re-read
  contract instead).
- Anchor state persistence across `Run` calls.

**Source provenance**: Anchor tool contract, AnchorStore API, and v1/v2
scope split extracted from
`docs/research/hash-anchored-edits-2026-05-01.md` (Scope, Anchor Word
Generation, Session State Design, AnchorEditTool sections).

### `load_skill` (progressive skill disclosure)

Opt-in tool that exposes a directory of `SKILL.md` files to the agent via
two-stage disclosure: a compact catalog injected into the system prompt at
session start, plus a `load_skill` tool that returns the full body of a
named skill on demand. Activates only when a skills directory exists or is
explicitly configured.

**SKILL.md frontmatter schema**:

```yaml
---
name: fix-tests        # required; slug [a-z0-9-_]+, max 64 chars
description: |         # required; under 1024 chars; embedded in system prompt
  Fix failing tests in a Go project. Use when tests are failing after a
  code change or when asked to make tests pass.
tags: [testing, go]    # optional
version: "1.0"         # optional
---
# Body in markdown; everything after the closing --- is loaded on demand.
```

Missing `name` or `description` → the skill is skipped with a warning at
scan time. Unknown YAML fields are silently ignored (forward-compat).

**Catalog injection (system prompt)**: When the catalog is non-empty,
`Builder.WithSkillCatalog(*skill.Catalog)` adds a `# Available Skills`
section to the system prompt. Entries are sorted by name (deterministic
output) and use the form `- <name>: <description>`. The section closes with
the activation rule: *"To use a skill, call the `load_skill` tool with the
skill name. Always load the skill before beginning the task it describes."*
Nil or empty catalog → section omitted entirely (zero overhead).

**`load_skill` tool wire shape**:

- **Parameters**: `{ "skill_name": string }`
- **On success**: returns the raw markdown body (everything after the
  closing `---`).
- **On unknown name**: returns `load_skill: skill "foo" not found;
  available: <comma-separated names>` so the model can self-correct without
  another tool round-trip.

**Configuration**:

```yaml
skills:
  dir: ".fizeau/skills"   # default; "-" disables; FIZEAU_SKILLS_DIR overrides
```

Resolution order: `FIZEAU_SKILLS_DIR` env > `config.yaml` `skills.dir` >
default `.fizeau/skills` relative to `workDir`. Absent directory → silently
disabled (no error). Explicit `"-"` → disabled even if the directory exists.

**Package layout note**: `LoadSkillTool` lives under `internal/skill`, not
`internal/tool`, to avoid an import cycle (`internal/tool` would otherwise
need to import `internal/skill`, which imports `internal/safefs`). The
`agentcli` wiring instantiates the tool directly from a `*skill.Catalog`
and appends it to the tool list, keeping `internal/tool` free of skill
imports.

**Source provenance**: Frontmatter schema, catalog format, `load_skill`
wire shape, and config keys extracted from
`docs/research/skill-progressive-disclosure-2026-05-01.md` (Frontmatter
Schema, System Prompt Injection Format, `load_skill` Tool, Config Keys
sections). Substantial enough to warrant a dedicated SD if a skill registry
ever grows beyond load-on-demand semantics; today it fits as a tool spec.

## Out of Scope

- File watching or filesystem events
- Interactive or per-call tool approval UX. The service-level native `agent`
  harness still enforces coarse permission modes by filtering the exposed tool
  set (`safe` read-only, `unrestricted` full built-ins, `supervised` rejected
  until an approval loop exists).
- MCP tool integration
