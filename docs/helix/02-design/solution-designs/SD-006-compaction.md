---
ddx:
  id: SD-006
  depends_on:
    - FEAT-001
    - SD-001
  review:
    self_hash: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
    deps:
      FEAT-001: cd37386d6fbdf5d388440be2d885fcad38298a0720429cb9fed602b55631260d
      SD-001: 7123b4d558d2ddd35289bf49390fde9e00b52081cbe90de37986d13fbbf36988
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Solution Design: SD-006 — Conversation Compaction

## Problem

The agent loop appends every message and tool result to the conversation
history. For tasks requiring many tool-call rounds, the history will exceed
the model's context window and the provider will return an error. Local models
have especially small windows (8K-32K) making this a practical blocker.

## Research Summary

### Pi's Approach
- **Trigger**: `contextTokens > contextWindow - reserveTokens` (reserve: 16K default)
- **Keep recent**: ~20K tokens of recent messages preserved verbatim
- **Summarize**: Everything before the cut point summarized by the LLM
- **Format**: Structured markdown — Goal, Progress (checkboxes), Key Decisions, Next Steps, Critical Context
- **Update mode**: When prior summary exists, merges new info rather than re-summarizing
- **File tracking**: Accumulates read/modified file lists in XML tags on the summary
- **Tool result truncation**: 2K chars max in summarization input
- **Split turn handling**: Separate "turn prefix summary" when cut falls mid-turn

### Codex's Approach
- **Trigger**: `total_usage_tokens >= auto_compact_token_limit` (per-model, e.g., 200K)
- **Two modes**: Local (LLM-based) and remote (OpenAI server-side `/responses/compact` API)
- **Prompt**: Short — "Create a handoff summary for another LLM that will resume the task"
- **Summary injection**: "Another language model started to solve this problem..."
- **User messages**: Keeps up to 20K tokens of recent user messages alongside summary
- **Timing**: Pre-turn (before user input) and mid-turn (between tool call rounds)
- **Fallback**: Trims oldest history items if compaction prompt itself exceeds context
- **Warning**: Alerts user that multiple compactions degrade accuracy
- **Cache preservation**: When trimming oversize compaction input, trims from the
  *beginning* to preserve prefix-based prompt cache hits. The comment explicitly
  states: "Trim from the beginning to preserve cache (prefix-based) and keep
  recent messages intact."
- **Initial context handling**: Codex has separate pre-turn and mid-turn
  reinjection modes. Fizeau does not adopt that ownership model: its separately
  owned `Request.SystemPrompt` never enters `History`, while every non-system
  initial-context message is an ordinary `History` message.
- **Window generation**: After compaction, advances a `window_generation` counter
  that invalidates the websocket session / prompt cache, forcing the provider to
  re-process the compacted history from scratch
- **Ghost snapshots**: Preserves undo/redo state snapshots across compaction by
  copying them into the replacement history

## Design: Fizeau Compaction

### Strategy

Fizeau follows pi's structured approach (richer summaries, file tracking) with
Codex's pragmatism (mid-turn compaction, configurable thresholds, graceful
fallback). The compaction is a library feature — not just CLI — so embedders
can control it.

### Configuration

```go
type CompactionConfig struct {
    // Enabled controls whether automatic compaction runs. Default: true.
    Enabled bool

    // ContextWindow is the compaction working window in tokens. If zero,
    // compaction uses DefaultContextWindow (currently 131072).
    ContextWindow int

    // ReserveTokens is the token budget reserved for the model's response
    // and the next prompt. Compaction triggers when conversation tokens
    // exceed ContextWindow - ReserveTokens. Default: 8192.
    ReserveTokens int

    // KeepRecentTokens is how many tokens of recent messages to preserve
    // verbatim after compaction. Default: 8192.
    KeepRecentTokens int

    // MaxToolResultChars is the max characters per tool result included in
    // the summarization input. Longer results are truncated with a
    // "[... N more characters truncated]" marker. Default: 2000 (matching pi).
    MaxToolResultChars int

    // SummarizationModel overrides the model used for summarization.
    // If empty, uses the same model as the agent loop. Useful for using
    // a faster/cheaper model for compaction (e.g., local model for
    // summarization even when the agent uses a cloud model).
    SummarizationModel string

    // SummarizationProvider overrides the provider for summarization.
    // If nil, uses the same provider as the agent loop.
    SummarizationProvider Provider

    // SummarizationFocus is optional caller-provided text appended to
    // the summarization prompt as "Additional focus: {text}". Lets
    // embedders influence what the summary emphasizes.
    SummarizationFocus string

    // EffectivePercent is the percentage of ContextWindow to actually use.
    // Default: 95. Provides safety margin since models may fail slightly
    // below their advertised limit. Set via the top-level config field
    // `compaction_percent` (e.g., compaction_percent: 75).
    EffectivePercent int
}
```

The core request receives selected-route evidence and the raw public override:

```go
type Request struct {
    // ...existing fields...
    SelectedContextWindow    int
    SelectedContextSource    string
    CompactionContextWindow  int
    InitialCapacityAttempts  CapacityAttemptState
}

type Result struct {
    // ...existing fields...
    CapacityAttempts CapacityAttemptState
}
```

### Selected Context Window Authority

The context window resolved after routing is authoritative for native
execution. A positive raw selected-candidate value wins. When the selected
candidate is a permitted exact pin with raw unknown/zero capacity, serviceimpl
uses explicit provider configuration, cached provider-API evidence, catalog
metadata, then `compaction.DefaultContextWindow` (currently `131072`) with
source `default`. The root `RouteDecision` exposes that resolved execution
`ContextLength` and `ContextSource`, while its candidate trace preserves the
raw zero/unknown value. `internal/serviceimpl` copies the resolved facts through
its API-neutral execution-decision and native-request types into core `Request`
as `SelectedContextWindow` and `SelectedContextSource`; neither serviceimpl nor
core imports a root/public DTO. Keeping raw candidate evidence distinct from
resolved execution evidence explains when fallback was applied.

An explicit native fast path without a routing decision uses the same fallback
chain, beginning at provider configuration because it has no candidate value:

1. explicit provider configuration
2. cached provider-API snapshot
3. catalog metadata
4. `compaction.DefaultContextWindow` (currently `131072`), with source
   `default`

The default is fallback evidence, not a provider observation. No execution hot
path contacts a provider merely to fill this value.

`ServiceExecuteRequest.CompactionContextWindow` is an optional stricter
operator bound. Serviceimpl passes it unchanged into core. Core rejects a
negative raw override and owns the overflow-safe
`ResolveWorkingContextWindow(selected, override)` helper:

```text
selected_window = Request.SelectedContextWindow
error                                                  if CompactionContextWindow < 0
working_window = selected_window                         if CompactionContextWindow == 0
working_window = CompactionContextWindow                 if selected_window <= 0
working_window = min(selected_window, CompactionContextWindow) otherwise
```

Zero preserves the selected-route value; a positive override can reduce, but
never enlarge, a known selected window. Public validation rejects a negative
override before session acceptance, and core repeats that check before
`session.start` for direct callers. Serviceimpl uses the same
`ResolveWorkingContextWindow` helper to configure the compactor. Compaction
triggering and provider-call preflight therefore share one `working_window`
definition rather than independently resolving context.

Percent scaling never evaluates `working_window * percent`. For a non-negative
window and a validated `percent` in `0..100`, both effective and compaction
windows use:

```text
quotient = working_window / 100
remainder = working_window % 100
scale_percent(working_window, percent) =
    quotient * percent + floor(remainder * percent / 100)
effective_window = scale_percent(working_window, 95)
```

Because `quotient <= math.MaxInt/100`, `percent <= 100`, and
`remainder < 100`, neither multiplication nor the final addition can overflow.

The compactor uses `working_window` as its context window. Its
`CompactionReserveTokens` is subtracted once, only when deciding whether to
compact. Per-provider-call output headroom uses `effective_window` and does not
subtract the compaction reserve a second time.

#### `compaction_percent` top-level config field

`compaction_percent` is a top-level config field (parallel to
`max_iterations`, `preset`, etc.) that overrides `CompactionConfig.EffectivePercent`.

```yaml
compaction_percent: 75   # trigger compaction at 75% of context window
```

When unset (zero), the compaction package default of 95% applies. A resolved
non-zero percentage must be in `1..100`; values outside that range are invalid.

#### `req.MaxTokens`

The non-negative requested output budget is passed to both route resolution and
the agent request:

```go
routeReq.MaxTokens = serviceReq.MaxTokens
coreReq.MaxTokens = serviceReq.MaxTokens
```

`MaxTokens > 0` is a caller limit that may be clamped for each provider call;
`MaxTokens == 0` preserves the provider-default convention. A negative value is
rejected synchronously before session acceptance and defensively by direct core
execution before `session.start`.

### Trigger Logic

```text
compaction_window = scale_percent(working_window, effectivePercent)
compaction_threshold = max(compaction_window - reserve_tokens, 0)
should_compact = EstimatedProviderCallTokens > compaction_threshold
```

The configurable `effectivePercent` controls only when compaction runs. The
provider-call capacity envelope always uses the selected-window formula above,
including its fixed five-percent safety margin. `reserve_tokens` is not part of
that per-call envelope.

### Canonical Provider-Call Estimator and Compactor Input

`internal/core` owns one estimator for the exact request about to be sent to a
native provider. For every included string it adds `ceil(UTF-8 byte length / 4)`
and uses saturating integer addition capped at Go `math.MaxInt`. The estimate
includes:

- every message role and content, including the system prompt as a provider
  message
- every assistant tool-call name and serialized JSON arguments
- every tool-result `ToolCallID`
- every available tool definition's name, description, and serialized JSON
  schema

Planning calls estimate their separate planning message list and have no tool
definitions. Provider-reported cumulative usage, previous-response usage, and
cache read/write accounting do not estimate the current call; those counters
remain usage and billing evidence only.

At both the pre-iteration and mid-turn compaction checks, core first constructs
the exact next `providerMessages`: the system message followed by the current
history in provider order. It estimates that complete list with current
`req.Tools`, then calls the compactor through a core-owned input contract:

```go
type CompactionInput struct {
    History                     []Message
    ProviderMessages            []Message
    ExecutedToolCalls           []ToolCallLog
    ToolDefinitions             []ToolDef
    EstimatedProviderCallTokens int
}

type Compactor func(
    ctx context.Context,
    input CompactionInput,
    provider Provider,
) ([]Message, *CompactionResult, error)
```

`History` is mutable conversation history and excludes the separately owned
system prompt. `ProviderMessages` is the exact next envelope, including that
system prompt exactly once. `ExecutedToolCalls` supplies file-tracking evidence;
`ToolDefinitions` is the exact built definition list for the next call.
`EstimatedProviderCallTokens` is computed from `ProviderMessages` plus
`ToolDefinitions`. The compactor uses the supplied estimate for its trigger and
may use the canonical estimator for retention accounting. It returns only
replacement `History`, a nullable result for no-op versus completed compaction,
and an error; `provider` remains available for summarization. Core alone
rebuilds `ProviderMessages` and prepends the system prompt, preventing
duplication. The trigger never uses response usage or cache counters. After
replacement, provider-call preflight estimates the rebuilt call again before
dispatch.

### Provider-Call Capacity Enforcement

Immediately before each actual native provider call, core recomputes
`estimated_input_tokens` from the exact messages and tools for that attempt:

```text
available_output_tokens = max(effective_window - estimated_input_tokens, 0)
```

`available_output_tokens == 0` is decided first. Planning emits only
`planning_skipped`; main emits only `rejected`. A prevented call never also
emits `clamped`. When headroom is positive, a positive requested `MaxTokens`
sends `min(MaxTokens, available_output_tokens)`. If that reduces the request,
core emits `EventContextCapacity{Action: "clamped"}` immediately before the
corresponding `EventLLMRequest`; the request event carries the same
`EffectiveMaxTokens` sent to the provider. For `MaxTokens == 0`, the call
preserves zero so the provider may apply its default.

Planning is non-fatal. If it has no output headroom, core emits
`context_capacity` with action `planning_skipped`, emits no `llm.request`,
`llm.response`, or `planning.turn` for that prevented call, and continues to
the main turn without a plan. A main call with no output headroom emits action
`rejected` and terminates with `context_capacity_exceeded`; no `llm.request` is
emitted for the prevented call.

The preflight applies to the planning `Chat`, every main `Chat` or `ChatStream`,
same-route transient retries, overflow-after-compaction retries, and service
no-stream reruns. Each attempt derives fresh call options from the original
requested `MaxTokens`; a clamp from one attempt is never reused as the next
attempt's request. Compaction summarization calls are excluded because their
budget is governed by the compactor. Capacity failure never switches to the
next routing candidate.

Capacity indexes are stable across those paths:

```go
type CapacityAttemptKey struct {
    CallKind string
    TurnIndex int
}

// Value is the last assigned AttemptIndex for the key.
type CapacityAttemptState map[CapacityAttemptKey]int
```

- `TurnIndex == 0` denotes planning. Main `TurnIndex` is the one-based logical
  tool-loop turn within the accepted service session.
- Retrying the same logical planning or main turn preserves `TurnIndex`.
- Core defensively copies `Request.InitialCapacityAttempts` before mutation.
  Every preflight reserves the next one-based index in that copy, including an
  ordinary request and a `clamped`, `planning_skipped`, or `rejected` outcome.
  Ordinary `llm.request` attempts therefore advance the same state as capacity
  events.
- `Result.CapacityAttempts` returns the last assigned index for every key.
  Transient and overflow-compaction retries use that same state and never
  reset it.
- For a service no-stream rerun, serviceimpl passes the first
  `Result.CapacityAttempts` as the second core run's
  `InitialCapacityAttempts`. Repeated planning remains planning turn zero and
  gets the next index; a reattempted main call keeps its logical main turn and
  also gets the next index.

Direct callers of the core loop receive stable typed identity:

```go
const ContextCapacityErrorCode = "CONTEXT_CAPACITY_EXCEEDED"

var ErrContextCapacityExceeded = errors.New("agent: context capacity exceeded")

type ContextCapacityError struct {
    CallKind             string // planning or main
    TurnIndex            int    // planning is 0; main is one-based
    AttemptIndex         int    // one-based within the call
    ContextWindow        int
    EffectiveWindow      int
    EstimatedInputTokens int
    RequestedMaxTokens   int
    AvailableOutputTokens int
}
```

The concrete error supports `errors.Is(err, ErrContextCapacityExceeded)` and
`errors.As` into `*ContextCapacityError`. An accepted service session does not
return this Go error through `Execute`; it projects the same evidence through
the public capacity event and terminal payload described below.

Compaction itself remains checked at two points:
1. **Pre-iteration**: Before sending the next prompt to the model
2. **Mid-iteration**: After tool results are appended (a large bash output
   can push over the limit between iterations)

### What Gets Compacted

1. Walk backwards from newest messages, accumulating token estimates
2. Stop when `keepRecentTokens` is reached — everything after this point is kept
3. Everything before the cut point is serialized and summarized
4. The cut point must be at a valid turn boundary. Valid boundaries are:
   user messages, assistant messages, and bash tool executions (which are
   natural turn breaks like pi's `bashExecution` entries). Tool result
   messages are NOT valid cut points — they must follow their tool call.
5. **Re-compaction guard**: If the most recent entry is already a compaction
   summary, skip compaction (following pi — prevents compacting a summary
   that was just created)
5. **Previous compaction entries are excluded** from the messages-to-summarize
   (the previous summary is passed separately via `<previous-summary>` tags)
6. **The separately owned system prompt is excluded** from both input and
   replacement `History`; prefix-token accounting still reserves its capacity.
   Required non-system initial context, including instruction or environment
   messages, must be represented as ordinary messages in `History` and follows
   the same retention or summarization rules as the rest of that history. There
   is no hidden initial-context reinjection channel.
7. **Overlong individual user messages are truncated** to fit within the
   `keepRecentTokens` budget (following Codex's `build_compacted_history_with_limit`)

### Serialization for Summarization

Tool calls serialized compactly:
```
[User]: Read main.go and fix the bug
[Assistant → read(path="main.go")]: package main...
[Assistant → edit(path="main.go", old="bug", new="fix")]: Replaced 1 occurrence
[Assistant]: Fixed the bug by replacing...
```

Tool results truncated to `MaxToolResultChars` (default 2000 characters,
matching pi). Truncation keeps the beginning and appends
`[... N more characters truncated]`.

### Summarization Call

The summarization uses a **separate system prompt** (following pi) to prevent
the model from continuing the conversation instead of summarizing:

**System prompt** (for the summarization LLM call):
```
You are a context summarization assistant. Your task is to read a
conversation between a user and an AI coding assistant, then produce
a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions
in the conversation. ONLY output the structured summary.
```

**User prompt** (initial summarization):
```
<conversation>
{serialized conversation text}
</conversation>

You are performing a CONTEXT CHECKPOINT COMPACTION. Create a structured
handoff summary for another LLM that will resume this task.

Use this EXACT format:

## Goal
[What the user is trying to accomplish]

## Constraints & Preferences
- [Requirements, conventions, or preferences mentioned]

## Progress
### Done
- [x] [Completed work with file paths]

### In Progress
- [ ] [Current work]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next]

## Critical Context
- [Data, error messages, or references needed to continue]

### Blocked
- [Issues preventing progress, if any, or "(none)"]

Keep each section concise. Preserve exact file paths, function names,
and error messages.
```

After summarization, the file tracking module appends file lists as XML tags
outside the structured summary (following pi's format):

```xml
<read-files>
path/to/file1.go
path/to/file2.go
</read-files>

<modified-files>
path/to/edited.go
path/to/created.go
</modified-files>
```

These XML tags are machine-parseable and carried forward through subsequent
compactions. They are separate from the summary body so the LLM doesn't need
to maintain them — the compaction code manages them programmatically.

**Max tokens for summary response**: `0.8 * ReserveTokens`. For the default
8192 reserve, the summary can be at most ~6500 tokens. This prevents the
summary itself from filling the context window.

**Custom instructions**: The caller can provide a `SummarizationFocus` string
(e.g., "focus on which spec requirements were completed") that is appended
to the prompt as `"Additional focus: {instructions}"`. This lets embedders
like HELIX influence summary content.

**Reasoning effort**: If the provider/model supports reasoning levels, use
high effort for summarization (better summaries justify the cost).

### Update Mode

When a previous compaction summary exists, the prompt wraps both the
conversation and previous summary in XML tags:

```
<conversation>
{serialized NEW conversation since last compaction}
</conversation>

<previous-summary>
{previous compaction summary}
</previous-summary>

The messages above are NEW conversation since the last compaction.
Update the existing summary by merging new information.

RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context
- UPDATE Progress: move completed items from In Progress to Done
- UPDATE Next Steps based on what was accomplished
- PRESERVE exact file paths and error messages
- If something is no longer relevant, you may remove it
- UPDATE Files section with any new reads or modifications
```

**Previous compaction entries are excluded from the messages-to-summarize.**
The previous summary text is passed separately via `<previous-summary>` tags,
not re-serialized as conversation messages (following pi's
`getMessageFromEntryForCompaction` pattern).

### Summary Injection

The summary replaces all compacted messages as a user-role message:

```
The conversation history before this point was compacted into the
following summary:

<summary>
{structured summary}
</summary>
```

### File Tracking

Like pi, agent tracks which files were read and modified across compactions.
The file lists are appended to the summary in XML tags and carried forward
through subsequent compactions.

### Prompt Cache Preservation

Both Anthropic and OpenAI support prefix-based prompt caching — the provider
caches the tokenized prefix of the conversation, and subsequent requests that
share the same prefix get a cache hit (faster, cheaper). Compaction destroys
the prefix, but we can minimize the damage.

**Post-compaction message ordering** uses one invariant at every timing.

Replacement `History`, for pre-iteration, mid-turn, and overflow compaction:
```
1. Retained ordinary history messages ← includes any non-system initial context
2. Compaction summary (user msg)      ← LAST in history
```

The exact next application-provider envelope constructed by core is:
```
1. Request.SystemPrompt               ← exactly once when non-empty
2. Replacement History                ← unchanged order
```

The compactor neither owns nor returns `Request.SystemPrompt`. Core rebuilds
the provider envelope after any compaction, including a mid-turn overflow, and
prepends that prompt exactly once before preflight accounting and provider
dispatch. Non-system permissions, personality, developer instructions, or
environment context survive only through ordinary `History`; Fizeau neither
synthesizes nor reinjects a separate initial-context block.

**Key rules** (learned from Codex):

1. **Trim from the front when compaction overflows.** If the compaction
   prompt itself exceeds the context window, trim the oldest messages from
   the summarization input (not the newest). This preserves the prefix
   cache for the summarization call itself.

2. **System prompt ownership.** `Request.SystemPrompt` is never persisted in
   replacement `History`. For both pre-iteration and mid-turn paths, core
   constructs the next provider envelope by prepending the non-empty system
   prompt exactly once to the replacement history.

3. **Invalidate provider-side cache after compaction.** The conversation
   prefix has fundamentally changed. If the provider uses sticky sessions
   or incremental request tracking, signal that the window changed.
   Fizeau exposes this via `EventCompactionEnd` — providers that maintain
   session state should listen for it.

### Usage Accounting Is Not Capacity Estimation

Fizeau records cache token streams separately so billing and usage projections
retain provider evidence:

```go
type TokenUsage struct {
    Input      int `json:"input"`
    Output     int `json:"output"`
    CacheRead  int `json:"cache_read,omitempty"`
    CacheWrite int `json:"cache_write,omitempty"`
    Total      int `json:"total"`
}
```

These counters do not replace or augment the canonical provider-call estimator.
In particular, input, output, cache-read, and cache-write usage from a completed
response are not added to a later request's capacity estimate. The next call is
estimated from its complete messages and tool definitions at preflight.

### Events

Core event types:
```go
EventCompactionStart EventType = "compaction.start"
EventCompactionEnd   EventType = "compaction.end"
EventContextCapacity EventType = "context_capacity"
```

The `compaction.end` event data includes the summary text, tokens before/after,
and file lists.

Core owns an internal payload containing primitive fields only:

```go
type ContextCapacityEventData struct {
    Action                 string
    CallKind               string
    TurnIndex              int
    AttemptIndex           int
    ContextWindow          int
    EffectiveContextWindow int
    EstimatedInputTokens   int
    RequestedMaxTokens     int
    EffectiveMaxTokens     int
    AvailableOutputTokens  int
}
```

Core emits this payload as `EventContextCapacity` and does not import root or
public service DTOs. Serviceimpl field-exhaustively maps it to
`internal/harnesses.ContextCapacityData` with
`internal/harnesses.EventTypeContextCapacity`. The root facade then owns and
maps `ServiceContextCapacityData`, including
`ServiceDecodedEvent.ContextCapacity` in decoded streams. Added fields must
break an exhaustive mapping test instead of being silently dropped.

`ContextWindow` is `working_window`, `EffectiveContextWindow` is the fixed
95-percent envelope, and `RequestedMaxTokens` is always the original request
value for that attempt. `EffectiveMaxTokens` is the provider-call value, or
zero for a skipped or rejected call; `AvailableOutputTokens` is the headroom
before applying the caller budget.

Clamps and planning skips are non-terminal. When headroom is positive, a clamp
event immediately precedes its `llm.request`. When headroom is zero,
`planning_skipped` or `rejected` takes precedence and no clamp or request event
is emitted. A rejected capacity event precedes core `session.end` and the root
final service event. That final event has `outcome="failed"`,
`cause="context_capacity_exceeded"`, `stage="tool_loop"`, and the mapped
`ServiceContextCapacityData` payload.

### Split Turn Handling

Following pi: if the cut point falls in the middle of a multi-message turn
(e.g., between a user message and its assistant response with tool calls),
generate a separate **turn prefix summary** with a smaller token budget
(`0.5 * ReserveTokens`). Both summaries are generated **concurrently**
(following pi's `Promise.all` pattern — in Go, use goroutines + errgroup).
The turn prefix summary is appended to the main compaction summary as:

```
---

**Turn Context (split turn):**

## Original Request
[What the user asked]

## Early Progress
[Work done in the prefix]

## Context for Suffix
[Info needed to understand the kept suffix]
```

### Quality Degradation Warning

Following Codex: after every compaction, emit a warning event:

```
"Long conversations and multiple compactions can cause the model to be
less accurate. Consider starting a new session when possible."
```

This is emitted via `EventCallback` as an `EventCompactionEnd` with a
`warning` field, not printed to stderr (library, not CLI concern).

### Graceful Degradation

If the compaction prompt itself exceeds the context window:
1. Trim oldest messages from the summarization input **(from the front,
   to preserve prefix cache)** — this is the local/inline compaction path
2. If still too large, fall back to aggressive truncation (keep only the
   most recent messages, drop the summarization attempt)
3. Log a warning via callback

If the summarization LLM returns an empty response, use the fallback string
`"(no summary available)"` (following Codex) rather than leaving the summary
empty. This ensures downstream code always has a non-empty summary to work with.

## Implementation Plan

| # | Bead | Depends |
|---|------|---------|
| 1 | Core canonical estimator over exact provider messages and tool definitions, with `math.MaxInt` saturation and `CompactionInput` | — |
| 2 | Conversation serialization for summarization | — |
| 3 | Shared working-window resolver, overflow-safe percentage scaling, and compaction trigger from `EstimatedProviderCallTokens` | 1 |
| 4 | Summarization prompt and summary injection (cache-optimized ordering) | 2, 3 |
| 5 | File tracking across compactions | 4 |
| 6 | Pre-iteration and mid-turn compaction input from the exact next provider call, with core-owned provider-envelope reconstruction | 1, 4 |
| 7 | Update mode (merge with previous summary) + split turn handling | 4 |
| 8 | Per-call capacity enforcement, stable attempt indexes, and exhaustive core → harness → root event mapping | 1, 3, 6 |
| 9 | Integration test: multi-round task with compaction and retry/no-stream event order | 7, 8 |

## Design Decisions Not Taken

- **Remote server-side compaction** (Codex has this for OpenAI's `/responses/compact`
  endpoint). Not included — agent is provider-agnostic and shouldn't depend on
  one provider's server-side API. If OpenAI or others expose this, it can be added
  as a provider-specific optimization later.
- **Branch summarization** (pi has this for conversation tree navigation). Not
  included — agent is headless with linear conversations, no branching.
- **Ghost snapshots / undo** (Codex preserves these across compaction). Not
  included — agent has no undo mechanism. If added later, the compaction should
  preserve snapshot items similarly to Codex.

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| Local model produces poor summaries | M | H | Allow dedicated summarization model; structured format constrains output |
| Token estimation inaccurate | M | M | Estimate the exact prospective provider call and retain the fixed five-percent capacity margin |
| Multiple compactions degrade quality | M | M | Warn after compaction; update mode preserves prior summary content |
| Summarization adds latency | L | M | Use faster model for summarization; only triggers when needed |
| Compaction destroys prompt cache | H | M | Core keeps the system prefix stable and preserves replacement-history order; accepted cost |
| Integer overflow understates capacity needs | L | H | Saturate additive estimates at `math.MaxInt` and use quotient/remainder percentage scaling |
| Split turn summary inaccurate | L | L | Smaller token budget; separate focused prompt |
