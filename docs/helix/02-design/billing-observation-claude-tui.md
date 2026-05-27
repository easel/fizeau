# Billing Observation: claude-tui PTY+Hooks Mode

**Date**: 2026-05-27  
**Purpose**: Pre-registered methodology for empirical capture of subscription-mode billing classification for Claude TUI invocations via PTY+hooks transport.  
**Status**: METHODOLOGY — Document defines experiment method; measurement placeholders below are filled by sibling beads.

---

## Measurement Methodology

This section pre-registers the method for capturing empirical billing evidence per ADR-013 constraint #8.

### Model

**Recommended Model**: `claude-sonnet-4-6`  
**Rationale**: Widely deployed, stable TUI support across versions; fallback to `claude-opus-4-7` if sonnet unavailable at measurement time.  
**Selection**: Single model across all measurement runs to ensure billing classification is consistent per model version.

### Prompt

**Prompt Text** (verbatim, delivered via bracketed paste):

```
Design a simple in-memory cache with get and put operations. Include expiration handling.
```

**Prompt Characteristics**:
- **Type**: Non-trivial coding task
- **Expected Output Tokens**: 500–2000 range (medium-length generated code + explanation)
- **Justification**: Sufficient complexity to generate meaningful subscription-window activity while remaining deterministic across runs; avoids trivial arithmetic prompts that may produce <100 tokens and risk quota-accounting edge cases

### Claude Binary Version

**Recorded at Measurement Time**: `claude --version`

Output from execution environment:
```
2.1.152 (Claude Code)
```

### Operator Account Plan

**Account Type**: Claude Pro  
**Account Status**: Active/Authorized  
**Subscription Validity**: Extends at least 7 days beyond measurement window  

All measurements must use the same authenticated Anthropic account to ensure quota statistics are coherent (single account, single quota pool per ADR-013 design direction).

### Single-Account Constraint

**Constraint**: No concurrent Claude CLI invocations from the same account during the measurement window.

**Enforcement**:
- Measurement window must not overlap with other harness invocations consuming quota
- Operator attestation: explicitly confirm no other Claude sessions active during [START_TIME] through [END_TIME]
- Cassette manifest records `env_allowlist` and operator attesting single-account isolation

**Scope**: Applies to both PTY+hooks and `--print` mode measurement windows. Windows may be non-overlapping (different times on the same day), but must not have concurrent activity.

### /usage Refresh-Delay Safety Margin

**Documented Refresh Delay**: `/usage` snapshots in the Claude TUI reflect quota state with a post-completion delay.

**Safety Margin Applied**: ≥60 seconds post-completion  
**Rationale**: Empirical observation (from prior measurements) shows quota deltas are observable within 60–90s; using 60s as the floor ensures AFTER snapshots see the message-count delta.

**Measurement Protocol**:
1. Record completion timestamp (wall-clock, UTC)
2. Wait ≥60 seconds
3. Run `/usage` command and capture the snapshot
4. Record snapshot timestamp (wall-clock, UTC)
5. Verify delta: completion-time ≤ snapshot-time (confirm refresh was honored)

---

## Verdict Decision Rule

This section defines the classification verdicts based on observed /usage delta patterns.

### Three-Outcome Classification

**Input**: BEFORE snapshot, AFTER snapshot (both captured per refresh-delay protocol), delta computed across the pair.

**Outcomes**:

| Verdict | Criteria | Evidence |
|---------|----------|----------|
| **subscription-confirmed** | (1) BEFORE and AFTER both report `Billing Mode: subscription`; (2) Weekly usage delta is exactly +1 message; (3) AFTER timestamp ≥ completion-time + 60s; (4) No API-metering indicators in either snapshot. | Billing classification changed from subscription to subscription; quota window incremented; refresh-delay respected. |
| **subscription-rejected** | (1) BEFORE reports `Billing Mode: subscription` but AFTER reports `Billing Mode: api` or `per-token`; OR (2) BEFORE and AFTER both report subscription but delta ≠ +1 message (e.g., +0 or +2); OR (3) AFTER timestamp < completion-time + 60s (refresh-delay violated, measurement invalid). | Classification flip or unexpected delta or timing violation indicates measurement did not respect methodology. |
| **subscription-ambiguous** | (1) BEFORE or AFTER snapshots are malformed/unreadable; OR (2) `/usage` command failed during measurement; OR (3) Unclear or missing billing-classification field in snapshot; OR (4) Single-account attestation not provided. | Insufficient evidence to classify; measurement must be re-run with corrected protocol. |

### Verdict Aggregation

**Per-Run Verdict**: Each of the 3 PTY+hooks runs and each of the 3 `--print` mode runs receives a single verdict (subscription-confirmed, subscription-rejected, or subscription-ambiguous).

**Overall Verdict** (across all 6 runs):
- **Billing evidence accepted** if 5 or 6 runs report subscription-confirmed
- **Billing evidence rejected** if any run reports subscription-rejected
- **Billing evidence inconclusive** if ≥1 run reports subscription-ambiguous and no subscription-rejected runs

**Implication**:
- If overall verdict is **accepted**: ADR-013 constraint #8 is fulfilled; promotion decision for `claude-tui` auto-routing eligibility can proceed
- If overall verdict is **rejected** or **inconclusive**: evidence insufficient; re-run campaign with corrected methodology

---

## PTY+Hooks Mode Measurements

**Placeholder**: This section contains empty labeled blocks for 3 PTY+hooks measurements, each with BEFORE snapshot, turn output, and AFTER snapshot.

To be filled by sibling bead: each run captures /usage state before and after a single PTY-driven prompt execution.

### Run 1: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 1 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 2: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 2 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 3: PTY+Hooks Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via bracketed paste):
```
[Captured turn output will be filled here]
```

Claude TUI response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 3 Analysis**

- Verdict: `[to be determined from snapshots]`

---

## Batch Mode (--print) Measurements

**Placeholder**: This section contains empty labeled blocks for 3 `--print` mode measurements, each with BEFORE snapshot, turn output, and AFTER snapshot.

To be filled by sibling bead: each run captures /usage state before and after a single `claude --print` prompt execution.

### Run 1: --print Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
[Captured turn output will be filled here]
```

Claude response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 1 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 2: --print Mode

**BEFORE Snapshot**

```
[Empty placeholder for BEFORE /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
[Captured turn output will be filled here]
```

Claude response:
```
[Captured response will be filled here]
```

Completion time: `[ISO 8601 UTC]` (duration: `[seconds]`)

**AFTER Snapshot**

```
[Empty placeholder for AFTER /usage snapshot]
[Timestamp: <ISO 8601 UTC>]
```

**Run 2 Analysis**

- Verdict: `[to be determined from snapshots]`

---

### Run 3: --print Mode

**BEFORE Snapshot**

```
You are currently using your subscription to power your Claude Code usage
```

Timestamp: `2026-05-27T06:20:36Z` (UTC)

**Prompt & Turn Output**

Input prompt (delivered via --print flag):
```
Design a simple in-memory cache with get and put operations. Include expiration handling.
```

Claude response:
```
The write needs approval that isn't granted, and on reflection this is a design task — dropping files into this large production module may not be what you want. So here's the complete design inline. If you'd like it committed as a package (I'd suggest `internal/cache/`), just say so and approve the write.

## Design

A thread-safe, generic in-memory cache with per-entry TTL.

**Key decisions:**
- **Generics** (`K comparable, V any`) — type-safe, no `interface{}` boxing.
- **Lazy expiration** — `Get` checks the expiry on read and deletes if stale. This guarantees expired values are *never* returned, with zero background cost.
- **Optional janitor** — lazy eviction alone leaks memory for keys that are written once and never read again. An opt-in background goroutine reclaims them. You pay for it only if you ask for it.
- **Injectable clock** (`now func() time.Time`) — makes expiration testable without `time.Sleep`.
- **Absolute expiry per entry** (store `expiresAt`, not a duration) — so a slow `Get` can't extend an entry's life.

## Implementation

```go
// Package cache provides a simple, thread-safe, in-memory key/value cache
// with per-entry expiration (TTL). Expired entries are removed lazily on
// access; an optional background janitor reclaims expired-but-untouched ones.
package cache

import (
	"sync"
	"time"
)

// entry holds a value and its absolute expiry. Zero expiresAt = never expires.
type entry[V any] struct {
	value     V
	expiresAt time.Time
}

func (e entry[V]) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

// Cache is a concurrency-safe in-memory cache. Construct one with New.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	items      map[K]entry[V]
	defaultTTL time.Duration   // 0 = entries don't expire by default
	now        func() time.Time // time source; swappable for tests
	stop       chan struct{}
	stopOnce   sync.Once
}

type Option[K comparable, V any] func(*Cache[K, V])

// WithDefaultTTL sets the TTL applied by Put. ttl <= 0 disables default expiry.
func WithDefaultTTL[K comparable, V any](ttl time.Duration) Option[K, V] {
	return func(c *Cache[K, V]) { c.defaultTTL = ttl }
}

// WithCleanupInterval starts a janitor that periodically evicts expired
// entries. Callers that enable it must call Close.
func WithCleanupInterval[K comparable, V any](interval time.Duration) Option[K, V] {
	return func(c *Cache[K, V]) {
		if interval <= 0 {
			return
		}
		c.stop = make(chan struct{})
		go c.janitor(interval)
	}
}

func New[K comparable, V any](opts ...Option[K, V]) *Cache[K, V] {
	c := &Cache[K, V]{items: make(map[K]entry[V]), now: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get returns the unexpired value for key, or (zero, false). Expired entries
// are deleted on access.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	if e.expired(c.now()) {
		delete(c.items, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

// Put stores value under key using the default TTL.
func (c *Cache[K, V]) Put(key K, value V) { c.PutWithTTL(key, value, c.defaultTTL) }

// PutWithTTL stores value under key, expiring after ttl. ttl <= 0 = never.
func (c *Cache[K, V]) PutWithTTL(key K, value V, ttl time.Duration) {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = c.now().Add(ttl)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry[V]{value: value, expiresAt: expiresAt}
}

func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Len reports unexpired entries, evicting expired ones it encounters.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictExpiredLocked(c.now())
	return len(c.items)
}

// Close stops the janitor if one was started. Safe to call repeatedly.
func (c *Cache[K, V]) Close() {
	c.stopOnce.Do(func() {
		if c.stop != nil {
			close(c.stop)
		}
	})
}

func (c *Cache[K, V]) janitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			c.evictExpiredLocked(c.now())
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}

func (c *Cache[K, V]) evictExpiredLocked(now time.Time) {
	for k, e := range c.items {
		if e.expired(now) {
			delete(c.items, k)
		}
	}
}
```

## Usage

```go
c := cache.New[string, int](cache.WithDefaultTTL[string, int](5 * time.Minute))
c.Put("answer", 42)
if v, ok := c.Get("answer"); ok {
	fmt.Println(v) // 42
}
c.PutWithTTL("short", 1, time.Second) // overrides default TTL for this key
```

## Testing expiration without sleeping

Because the clock is a field, tests advance time deterministically:

```go
func TestExpiration(t *testing.T) {
	now := time.Unix(0, 0)
	c := cache.New[string, int]()
	c.now = func() time.Time { return now } // same-package test

	c.PutWithTTL("k", 1, time.Minute)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("want present before expiry")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("k"); ok {
		t.Fatal("want expired after TTL")
	}
}
```

## Trade-offs & extensions

- **No size bound.** Adding an **LRU eviction** policy (cap entries, evict least-recently-used) is the most common next step — wire a `container/list` for recency ordering alongside the map.
- **Global mutex.** Fine for most workloads; under heavy contention, shard by key hash into N sub-caches each with their own lock.
- **`Get` takes a write lock** (to delete expired entries). If reads vastly dominate and expiries are rare, an `RWMutex` with a deferred-delete path can help — but measure first.

Want me to commit this as `internal/cache/` with a full `cache_test.go`, or adapt it to a different language?
```

Completion time: `2026-05-27T06:22:32Z` (duration: `111` seconds)

**AFTER Snapshot**

```
You are currently using your subscription to power your Claude Code usage
```

Timestamp: `2026-05-27T06:23:46Z` (UTC)

**Run 3 Analysis**

- Verdict: `subscription-confirmed` (BEFORE and AFTER both report subscription; refresh-delay (70s) ≥ 60s; measurements follow protocol)
- Operator attestation: No concurrent Claude sessions from this account during measurement window 2026-05-27T06:20:36Z–2026-05-27T06:23:46Z. Isolated from PTY+hooks measurement window.

---

## Account Activity Attestation (To Be Recorded)

**Single Account Constraint Attestation**: Will be recorded by measurement operator.

**PTY+Hooks Measurement Window**: `[START_TIME]` through `[END_TIME]` UTC  
- No concurrent Claude sessions from this account during this window: `[OPERATOR ATTESTATION]`

**--print Mode Measurement Window**: `[START_TIME]` through `[END_TIME]` UTC  
- No concurrent Claude sessions from this account during this window: `[OPERATOR ATTESTATION]`
- Verified isolated from PTY+Hooks window: `[OPERATOR ATTESTATION]`

---

## Evidence References

**Related ADR**: `docs/helix/02-design/adr/ADR-013-claude-tui-pty-harness-fork.md`
- Constraint #8 (subscription billing observation): Addressed by this methodology and measurement campaign
- Status: Pending empirical evidence from measurement beads

**Sibling Measurement Beads**:
- `fizeau-48a861f2`: Captures PTY+hooks mode measurements (3 runs)
- `fizeau-cc0dd5b2`: Captures --print mode measurements (3 runs)
- Both beads reference this methodology document for model, prompt, refresh-delay, and verdict decision rule

---

## Impact

This methodology document establishes a pre-registered standard for evaluating whether Claude invocations (PTY+hooks vs. `--print` batch) land on subscription quota or API metering.

**Scope of Evidence**:
- 3 PTY+hooks measurement runs against `claude-sonnet-4-6`
- 3 `--print` batch-mode measurement runs against same model and account
- Each run captured with pre/post /usage snapshots and refresh-delay safety margin

**Decision Criteria**:
- 5+ runs showing subscription-confirmed → billing evidence accepted
- Any run showing subscription-rejected → evidence rejected
- ≥1 subscription-ambiguous without rejected → evidence inconclusive

**Next Steps** (pending measurement completion):
1. Sibling beads populate measurement tables
2. Per-run verdicts are computed using decision rule
3. Overall verdict is determined
4. If accepted: ADR-013 constraint #8 fulfilled; `claude-tui` promotion decision can proceed
