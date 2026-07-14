---
ddx:
  id: ADR-012
  depends_on:
    - ADR-009
  child_of: fizeau-67f2d585
  review:
    self_hash: 5c24642fbb06edd9f8fede71adc0a1a4375c2e17a95f7c61b1add3f24a5f622a
    deps:
      ADR-009: d9968b4818b0f45508f3e0689b403ff6997c2722924e7457605bc43080ae5a4a
    reviewed_at: "2026-07-14T20:00:14Z"
---
# ADR-012: Per-Source On-Disk Cache for Discovery + Runtime Signals

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-05-11 | Accepted | Fizeau maintainers | `ADR-009`, `ADR-010` | High |

## Context

The `fiz models` command (bead EPIC) must produce an available-models snapshot
that combines two tiers of information and feeds `route(client_inputs,
fiz_models_snapshot)`:

- **Discovery signals** — whether a source exposes a model at all
  (provider `/v1/models` endpoint, harness PTY enumeration,
  `/props` introspection). Slow to collect: PTY enumeration takes
  ~30 s; OR `/api/v1/models` takes ~316 ms p50 over the open
  internet.
- **Runtime signals** — quota availability, rate-limit headroom,
  observed latency. Change frequently (5-min order) but don't affect
  model existence.

Three requirements conflict without a cache:

1. **UI latency ≤ 100 ms.** `fiz models` must return stale data immediately
   rather than block on discovery IO.
2. **Multi-process safety.** `ddx work`, `ddx try`, `fiz models`,
   and the routing layer all run concurrently and must share cache
   state without corruption or thundering-herd re-discovery.
3. **Crash safety.** A killed refresh process must not leave the cache in a
   corrupt or permanently-locked state.

Fizeau has no required daemon. Its cache contract is cache-first reads plus
coordinated refresh through cross-process locks. `fiz models` and route hot
paths are quick by default: stale snapshot data is returned immediately when
freshness is pending. They may request a best-effort background refresh for
stale fields, but only through the same lock, marker, and single-flight path
used by blocking refresh; a short-lived CLI process must not spawn independent
probe storms or make correctness depend on a detached worker. `fiz models
--refresh` blocks on routing-relevant stale fields, and `fiz models
--refresh-all` blocks on every refreshable field. Without a long-running
freshness maintainer, stale output should tell the operator to run `fiz models
--refresh` or start/configure a DDx server freshness heartbeat.

Fizeau already has a battle-tested file-locking idiom in
`cmd/bench/matrix.go:acquireMatrixLock` (lines 1222–1259): atomic
`O_CREATE|O_EXCL` create, JSON `{PID, StartedAt}` payload, crash
recovery via `syscall.Kill(pid, 0)`, cleanup-on-exit closure. The
harness quota caches (`internal/harnesses/claude/quota_cache.go`,
`internal/harnesses/codex/quota_cache.go`,
`internal/harnesses/gemini/quota_cache.go`) demonstrate the
atomic-write contract: write to `.tmp`, `os.Rename` to final path,
`chmod 0o600`.

This ADR extends both idioms with a **two-tier lock + long-lived
refresh marker** pattern to handle the case where the IO under the
lock can take 30 s (PTY) — far longer than what a brief mutex is
designed to hold.

## Decision

### 1. File layout

```
~/.cache/fizeau/              (os.UserCacheDir() + "/fizeau")
├── discovery/                # slow, stable; one file per source
│   ├── openrouter.json                 # 24 h TTL
│   ├── claude-subscription.json        # 24 h TTL
│   ├── codex-subscription.json         # 24 h TTL
│   ├── vidar-ds4.json                  # 1 h TTL  (LAN, local)
│   └── sindri-club-3090-llamacpp.json  # 1 h TTL  (LAN, local)
└── runtime/                  # hot, volatile; one file per source
    ├── openrouter.json                 # 5 min TTL
    ├── claude-subscription.json        # 5 min TTL
    └── ...
```

Source names are kebab-case slugs derived from the provider entry in
fizeau's config (same identifier used by the routing layer). Each
data file is JSON. Top-level keys are fizeau's canonical model
identities: `<provider>/<model_id>` (e.g.
`openrouter/anthropic/claude-opus-4-5`). Alongside each data file,
the cache manager may write two side-car files:

- `<source>.lock` — brief mutation lock (held for microseconds).
- `<source>.refreshing` — long-lived refresh marker (held for the
  duration of the IO, up to `RefreshDeadline`).

### 2. TTL defaults

All values are operator-configurable via environment variables or the
fizeau config file. The env-var names are the canonical override
mechanism; config-file keys mirror them.

| Signal tier | Source type | Default TTL | Env override |
|-------------|-------------|-------------|--------------|
| Discovery | PTY enumeration | 24 h | `FIZ_TTL_DISCOVERY_PTY` |
| Discovery | HTTP `/v1/models` (remote) | 24 h | `FIZ_TTL_DISCOVERY_HTTP_REMOTE` |
| Discovery | HTTP `/v1/models` (LAN) | 1 h | `FIZ_TTL_DISCOVERY_HTTP_LOCAL` |
| Runtime | Any | 5 min | `FIZ_TTL_RUNTIME` |

Lock and deadline constants:

| Constant | Default | Env override |
|----------|---------|--------------|
| Lock acquisition timeout | 100 ms | `FIZ_LOCK_TIMEOUT` |
| Refresh deadline — PTY | 60 s | `FIZ_REFRESH_DEADLINE_PTY` |
| Refresh deadline — HTTP discovery | 10 s | `FIZ_REFRESH_DEADLINE_HTTP` |
| Refresh deadline — runtime | 5 s | `FIZ_REFRESH_DEADLINE_RUNTIME` |
| Marker staleness threshold | 2 × refresh deadline | derived |

**Rationale:** PTY is the slowest path; 60 s is conservative but not
unreachable on a cold harness boot. HTTP remote (OR) is ~316 ms p50
measured; 10 s is a 30× headroom factor. LAN endpoints respond in
< 15 ms; the 1 h TTL trades off freshness against SSH-tunnel startup
cost. Runtime signals change on a ~5 min order (quota windows, rate-
limit headers), matching the TTL.

### 3. Lock + marker pattern (two-tier)

**Tier 1 — `<source>.lock`** (brief mutation lock):

Identical to `acquireMatrixLock` semantics. Held only during state
transitions: "claim refresh slot" and "commit refresh result". Held
for microseconds. Uses `O_CREATE|O_EXCL` atomic create. Crash
recovery: if the owning PID is dead (`syscall.Kill(pid, 0)` returns
`ESRCH`), the lock is removed and the caller retries once.

JSON payload: `{"pid": <int>, "started_at": "<RFC3339>"}`.

**Tier 2 — `<source>.refreshing`** (long-lived refresh marker):

Written after the lock is acquired and claimed. Held for the duration
of the IO. Other processes inspect this marker to decide whether to
wait or return stale data immediately. It is removed (under the Tier
1 lock) after the data file is atomically committed.

JSON payload:
```json
{
  "pid": <int>,
  "started_at": "<RFC3339>",
  "deadline": "<RFC3339>"
}
```

A marker is considered **stale** (orphan) when either:
- `now > deadline + staleness_threshold` (2 × refresh deadline), OR
- `syscall.Kill(pid, 0)` returns `ESRCH` (process dead).

Stale markers are overridden by the next caller during `claim_refresh`
(the marker is removed under the Tier 1 lock before a new one is
written).

### 4. Algorithms (pseudocode)

#### Algorithm 1 — `claim_refresh(source) → ClaimedByMe | AlreadyInFlight(marker)`

```
func claim_refresh(source):
    acquire tier-1 lock (timeout=LockAcquisitionTimeout):
        on timeout: return error "lock contention"

    existing_marker = read_marker_if_exists(source)

    if existing_marker != nil:
        if is_stale(existing_marker):
            remove(source.refreshing)   // orphan cleanup
        else:
            release tier-1 lock
            return AlreadyInFlight(existing_marker)

    // Write the marker before releasing the lock so no other
    // process can claim the slot between lock release and marker
    // write.
    write_marker(source, pid=self, started_at=now,
                 deadline=now+RefreshDeadline(source))
    release tier-1 lock
    return ClaimedByMe
```

#### Algorithm 2 — `refresh_and_commit(source)`

```
func refresh_and_commit(source):
    claim = claim_refresh(source)
    if claim == AlreadyInFlight:
        return wait_for_refresh(source, claim.marker,
                                max_wait=RefreshDeadline(source))

    // Perform slow IO outside any lock.
    data = fetch_source(source)   // PTY, HTTP, etc.

    // Atomic commit (mirrors harness quota_cache.go pattern).
    tmp = source.data_path + ".tmp"
    write_json(tmp, data)
    chmod(tmp, 0o600)
    os.Rename(tmp, source.data_path)   // atomic on POSIX

    // Remove marker under tier-1 lock.
    acquire tier-1 lock (timeout=LockAcquisitionTimeout):
        // Guard: if our deadline passed and another process
        // claimed the slot, do not remove the new marker.
        current_marker = read_marker_if_exists(source)
        if current_marker != nil && current_marker.pid == self:
            remove(source.refreshing)
    release tier-1 lock
```

#### Algorithm 3 — `wait_for_refresh(source, marker, max_wait)`

```
func wait_for_refresh(source, marker, max_wait):
    deadline = marker.deadline + staleness_threshold
    poll_interval = 250ms
    for now() < min(deadline, now() + max_wait):
        sleep(poll_interval)
        if not exists(source.refreshing):
            return read(source)   // refresh completed
        current = read_marker_if_exists(source)
        if current == nil:
            return read(source)   // refresh completed
        if is_stale(current):
            return read(source)   // orphan; return whatever is on disk
    // Timed out waiting; return stale data without error.
    return read(source)
```

#### Algorithm 4 — `read(source) → (data, fresh_bool)`

```
func read(source):
    if not exists(source.data_path):
        return (nil, false)
    data = read_json(source.data_path)   // no lock; file is immutable
                                          // until atomic rename
    fresh = (now() - data.captured_at) < TTL(source)
    return (data, fresh)

    // NB: stale data is always returned without error. The caller
    // decides whether to trigger a background refresh.
    // Torn reads are impossible: rename is atomic on POSIX; a
    // reader either sees the old complete file or the new complete
    // file, never a partial write.
```

#### Algorithm 5 — `force_refresh(source)`

```
func force_refresh(source):
    // Synchronous. Bypasses TTL check. Waits for any in-flight
    // refresh to finish, then reads fresh data.
    claim = claim_refresh(source)
    if claim == AlreadyInFlight:
        wait_for_refresh(source, claim.marker,
                         max_wait=RefreshDeadline(source))
        data, _ = read(source)
        return data

    // We hold the claim; run refresh synchronously.
    refresh_and_commit(source)
    data, _ = read(source)
    return data
```

#### Algorithm 6 — In-process single-flight composition

```
// singleflightGroup is a golang.org/x/sync/singleflight.Group,
// one per cache instance (process-lifetime).
//
// This composes with file-based coordination: within a process,
// singleflight ensures at most one goroutine runs refresh_and_commit
// per source. Across processes, the file-based marker (Algorithm 1)
// provides the same guarantee.

func maybe_background_refresh(source):
    data, fresh = read(source)
    if fresh:
        return data
    // Stale or missing — request a best-effort refresh via
    // singleflight so concurrent callers share one goroutine.
    // Route hot paths also use this nonblocking form before scoring;
    // explicit refresh/preflight surfaces use ensure_fresh / force_refresh.
    go singleflightGroup.Do(source.key(), func():
        refresh_and_commit(source)
    )
    return data   // return stale immediately; never block UI

func ensure_fresh(source):
    // Blocking variant used by force_refresh and tests.
    _, _, _ = singleflightGroup.Do(source.key(), func():
        refresh_and_commit(source)
    )
    data, _ = read(source)
    return data
```

`singleflight.Group` deduplicates concurrent `Do` calls with the
same key: if a refresh is already running, a second caller blocks
on the same goroutine and gets the same result. This eliminates
the in-process thundering herd without file IO overhead.

The background helper is optional and process-lifetime scoped. A `fiz models`
CLI invocation may call it after returning stale output, but no design depends
on the goroutine surviving after process exit. Long-running clients such as the
DDx server should call the same refresh APIs on a heartbeat when they want to
maintain fresh cache state asynchronously.

### 5. Crash recovery

No manual cleanup is required. Recovery happens lazily on the next
call to `claim_refresh`:

- **Stale `.lock` file** (dead PID): removed on next acquisition
  attempt, same as `acquireMatrixLock` semantics.
- **Stale `.refreshing` marker** (dead PID or past deadline +
  staleness threshold): removed under tier-1 lock before a new
  marker is written.
- **Partial data write** (`.tmp` file left behind): safe to remove
  or overwrite on the next `refresh_and_commit`; the final
  `os.Rename` was never called so the committed data file is
  intact.

### 6. Force-refresh semantics

`force_refresh` is synchronous and bypasses the TTL check. It:

1. Inspects the marker. If a refresh is already in flight, waits
   up to `RefreshDeadline` for it to complete.
2. If no refresh is in flight, runs one synchronously.
3. Returns fresh data.

Used by `fiz models --refresh`, by the `fiz cache refresh <source>`
subcommand, and by explicit preflight/test surfaces. `ResolveRoute` and
`Execute` do not use this blocking path for ordinary autorouting: they read
cached facts immediately, request coordinated background refresh for stale or
missing local/provider facts, and let cached failure evidence gate known-dead
providers.

`--refresh-all` is the strict variant that blocks until all refreshable fields
are fresh enough for display, not just the routing-relevant subset. It uses the
same refresh coordinator and marker discipline; it just widens the blocking
surface before returning snapshot data.

### 7. Cache prune

`fiz cache prune` removes discovery and runtime data files (and
their sidecar lock/marker files) for sources not named in the
current fizeau config. The command is **explicit only** — it is
not called at startup. Rationale: auto-prune at startup could
silently discard a cache that another process is actively using;
the operator should decide when pruning is safe.

Safety: `fiz cache prune` acquires the tier-1 lock for each source
before removing its files. It skips sources that have an active
`.refreshing` marker.

### 8. Prior art: acquireMatrixLock extension

`cmd/bench/matrix.go:acquireMatrixLock` (lines 1222–1259) is the
direct prior art for the tier-1 `.lock` file:

- `O_CREATE|O_EXCL` atomic create.
- JSON `{PID, StartedAt}` payload for post-mortem inspection.
- `processAlive(PID)` (`syscall.Kill(pid, 0)`) for crash recovery.
- Single-use: acquired before a matrix cell run, released on exit.

This ADR extends that pattern with a **separate** tier-2
`.refreshing` marker so that the tier-1 lock can be released
promptly after writing the marker. This is necessary because the
IO under the marker (PTY enumeration, HTTP) can take 30–60 s — far
longer than what a blocking lock should hold. The two-tier design
keeps the brief-lock semantics of `acquireMatrixLock` intact while
allowing other processes to observe in-progress refresh state.

### 9. Mandatory test matrix for bead M1

The implementation bead (M1) **must** ship all 10 tests passing.
No test may be skipped, marked as flaky, or guarded by a build tag.
Multi-process tests must use the standard `TestMain` +
`os.Args[0]` helper-process pattern (spawn the test binary itself
as a child process) to avoid requiring external binaries.

| # | Name | What it proves |
|---|------|----------------|
| 1 | `TestConcurrentClaimTwoProcs` | Two child processes race `claim_refresh`; exactly one returns `ClaimedByMe`, the other `AlreadyInFlight`. |
| 2 | `TestReaderDuringRefresh` | Atomic rename guarantees no torn read: 100 concurrent readers observe only complete, checksummed versions while a writer produces 100 versions sequentially. |
| 3 | `TestCrashDuringRefresh` | A child process writes a marker and is killed (`SIGKILL`). Next process detects dead PID in marker and claims successfully. |
| 4 | `TestRefreshTimeout` | Marker deadline is set to `now - 1 s` (expired). Next process claims the slot; the original process's late commit is rejected (tier-1 lock check guards the marker removal). |
| 5 | `TestForceRefreshWaitsAndReadsFresh` | `force_refresh` with an in-flight marker waits until the refresh completes and returns the newly written data, not stale data. |
| 6 | `TestConcurrentNormalAndForce` | Concurrent `read` (normal) + `force_refresh`: normal returns stale immediately without blocking; `force_refresh` waits and singleflight deduplicates the goroutines. |
| 7 | `TestAtomicRenameVerified` | 100 concurrent readers + 1 writer producing 100 versions; every read returns a complete, consistent version (verified by checksum); no version is ever partially written. |
| 8 | `TestPruneDoesNotRaceActiveSources` | `fiz cache prune` with a source whose `.refreshing` marker is active; prune skips that source and does not remove any of its files. |
| 9 | `TestStaleWhileRevalidate` | Stale cache entry: `read` returns stale data immediately (≤ 5 ms); an optional background refresh request is coalesced through singleflight/markers; after a refresh completes, subsequent `read` returns fresh data. |
| 10 | `TestPIDReuseSafety` | Marker contains a PID that has since been reused by an unrelated OS process (alive check passes); deadline check is the safety net — marker is treated as stale once `now > deadline + staleness_threshold`. |

## Consequences

**Positive:**

- `fiz models` does not hang by default. The read path is always cache-bounded
  (≤ 100 ms) because `read` never waits on IO — it returns stale
  data immediately and may request a coordinated best-effort background
  refresh.
- Autorouting remains responsive even when no DDx server or other long-running
  maintainer is active: route-time refresh requests use the same coordinated
  cache machinery in the background, while scoring consumes cached evidence.
- Multi-process safe across `ddx work`, `ddx try`, `fiz models`,
  and the routing layer. Exactly one process refreshes each source
  at a time; others either wait (force) or return stale
  (normal read).
- Crash-safe by construction. PID-alive checks and deadline
  checks together recover from all observed failure modes
  (kill -9, OOM, timeout).
- Reuses `acquireMatrixLock`'s proven `O_CREATE|O_EXCL` + PID
  idiom. No new cross-platform lock library is required.
- Each source has an independent lifecycle. OR re-discovery does
  not block PTY discovery. A slow PTY source does not delay the
  `fiz models` response for cloud sources.
- Atomic-rename write contract (from harness quota caches) ensures
  readers never observe a partial write.

**Negative:**

- Two sidecar files per source (`.lock` + `.refreshing`) is more
  structural complexity than a single in-process mutex.
- File-based coordination has inherent edge cases on network
  filesystems (NFS, CIFS) where `O_EXCL` and `rename` may not be
  atomic. The cache directory is always under `os.UserCacheDir()`,
  which is local on all supported platforms; NFS is not a target.
- PID reuse is a real (if rare) edge case: a new OS process that
  happens to get the same PID as the crashed refresher would
  defeat the PID-alive check. The deadline check is the safety
  net; the worst outcome is serving stale data past the deadline,
  which is within the acceptable degradation envelope.
- The test matrix (10 tests, several multi-process) is non-trivial
  to maintain. The `TestMain` helper-process pattern requires
  discipline: new multi-process tests must follow the established
  pattern or they will not work in CI.

## Out of scope

- Persistent storage of historical signals. The cache is
  current-state only; evicted entries are gone.
- Multi-machine cache sharing. Per-host only; distributed caches
  require coordination primitives beyond file locks.
- Compression. Files are plain JSON; the largest anticipated cache
  file (OR full model list) is ~200 KB uncompressed.
- Encryption at rest. Cache files contain no secrets; model lists
  and quota windows are non-sensitive operational data.
- OR sub-provider cache (deferred to bead M5). Sub-provider
  routing metadata warrants a separate cache tier with its own TTL
  strategy.
- NFS / network filesystem support. The cache directory is always
  local.
- A Fizeau-owned daemon. Asynchronous freshness is provided by callers that
  choose to run long-lived processes, such as a DDx server heartbeat, over the
  same synchronous lock-coordinated refresh primitives.

## References

- Bead A (`fizeau-b2c5c826`) — version-aware ranker; parses model
  IDs that become the canonical key `<provider>/<model_id>`.
- Bead D (`fizeau-d18e11f5`) — `IncludeByDefault` filter; composes
  with `AutoRoutable` in the snapshot produced from the cache.
- Bead E1 (`fizeau-c04be6b0`) — `quota_pool` catalog field;
  consumed in bead M2 enrichment which reads from the runtime
  cache tier defined here.
- Bead E2 (`fizeau-5b6512ef`) — ADR-011 cost-based routing;
  downstream consumer of the available-models snapshot.
- `cmd/bench/matrix.go:acquireMatrixLock` (lines 1222–1259) —
  prior-art tier-1 lock idiom extended by this ADR.
- `internal/harnesses/claude/quota_cache.go` — atomic-rename
  (`.tmp` → final) write pattern reused for data files.
- `internal/harnesses/codex/quota_cache.go` — same atomic-rename
  pattern; TTL freshness check pattern (`CapturedAt` + TTL).
- `internal/harnesses/gemini/quota_cache.go` — per-tier routing
  decision pattern; model of how the snapshot layer consumes cache
  data.
- `golang.org/x/sync/singleflight` — in-process deduplication
  composing with file-based cross-process coordination (§4
  Algorithm 6).
- ADR-009 (routing surface redesign) — defines the snapshot
  concept that this cache backs.
- ADR-010 (reasoning wire form from catalog) — L1 introspection
  data from `/props` is a candidate input to the discovery cache
  tier defined here.
