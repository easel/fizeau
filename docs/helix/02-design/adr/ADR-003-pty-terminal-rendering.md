---
ddx:
  id: ADR-003
  depends_on:
    - ADR-002
    - CONTRACT-003
  review:
    self_hash: e92a82cb3130952d3800c39674112f0ddeda09ede3c1f3a191580ce9d9f85b64
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      CONTRACT-003: 5a45d7c4113eb487a73fad736dc867e8305d7ef6718c7752af2e80f922755138
    reviewed_at: "2026-07-16T07:15:29Z"
---
# ADR-003: PTY Terminal Rendering and Screen Model

| Date | Status | Deciders | Related | Confidence |
|------|--------|----------|---------|------------|
| 2026-04-20 | Accepted, amended | Fizeau maintainers | `ADR-002`, `ADR-004`, `SPIKE-001`, `CONTRACT-003` | Medium |

## Context

ADR-002 selects direct PTY ownership and versioned PTY cassettes. ADR-004
constrains that decision with a build-vs-buy boundary: Fizeau must adopt or
wrap an existing terminal emulator rather than becoming a terminal emulator
project. That still leaves a hard implementation question: how does Fizeau
turn raw ANSI PTY output from real TUIs into stable screen frames for
assertions, replay, and inspection?

`top` was spiked through a direct PTY in
[SPIKE-001](../../02-design/spikes/SPIKE-001-direct-pty-top-rendering.md).
The spike successfully started `top`, sent input, resized the PTY, captured raw
bytes, and rendered useful frames with a VT emulator. It also showed that raw
output contains dense ANSI mode changes, cursor motion, screen clears, SGR
styling, and volatile terminal content. Regex stripping is not a viable screen
model.

## Decision

Fizeau will implement `internal/pty/terminal` as a wrapper around a real
VT/ANSI terminal emulator library. The project will not hand-roll ANSI parsing
or rely on regex stripping for TUI assertions. The implementation bead is
blocked on the ADR-004 build-vs-buy evaluation before choosing the concrete
backend.

`internal/pty/session` owns the PTY process and raw byte stream.
`internal/pty/terminal` consumes raw bytes and produces normalized screen
snapshots, frame diffs, cursor state, terminal size, and semantic extraction
helpers. `internal/pty/cassette` stores both the raw evidence stream and the
derived frame stream.

The emulator backend is intentionally hidden behind an internal interface so it
can be replaced if conformance tests expose gaps.

**Key Points**: real terminal emulator | raw bytes preserved | frames derived |
backend replaceable

## Terminal Model Contract

The terminal layer must expose:

- byte ingestion that preserves order from the PTY reader;
- current screen snapshot as cells or lines;
- frame snapshots or diffs with monotonic `t_ms`;
- cursor position and visibility;
- terminal size and resize handling;
- style metadata policy: either preserve color/style in cells or explicitly
  document what is dropped;
- semantic text extraction for harness probes;
- normalization hooks for volatile screen facts such as clocks, PIDs, elapsed
  durations, animation counters, and process ordering.

The terminal layer must not:

- spawn processes;
- write cassettes directly;
- know Claude, Codex, quota, model, reasoning, or token-usage semantics;
- import `internal/harnesses`.

## Library Selection

The first implementation bead must evaluate terminal emulator candidates before
locking one in. The spike proves `github.com/hinshun/vt10x` can render `top`
well enough for a first pass, but candidate evaluation should also consider
maintainability, Unicode/wide-character support, alternate screen behavior,
resize behavior, OSC/title handling, color/style support, API fit, and test
coverage.

The selected emulator backend and version must be recorded in
`manifest.terminal.emulator` for every cassette whose frames were derived
through that backend. Frame assertions either re-derive frames from raw output
with the manifest-pinned emulator or fail with a clear emulator mismatch.

Candidate families include:

- `github.com/hinshun/vt10x`
- terminal model pieces used by `go-expect`
- Charmbracelet/x ANSI tooling
- other small maintained VT parser/emulator libraries with a compatible API

## Conformance Tests

The PTY terminal model is not complete until tests prove behavior against real
terminal programs. These tests must be fully automated through the cassette
assertion framework defined in ADR-002: replay runs in collapsed time for fast
CI, record mode is scripted when enabled, and no support claim depends on
manual screen inspection.

| Target | Required Evidence |
|--------|-------------------|
| `top` | Capture multiple rendered frames from one run, including initial paint, refresh, input-driven state change, and resize-driven layout change. Assertions check semantic screen facts, not full raw byte equality. |
| Pager | A `less`-style flow proves scroll, quit, and alternate-screen or raw-mode behavior where available. |
| Full-screen TUI | An editor/curses-style flow such as `vim`, `nano`, or `dialog` proves cursor movement, screen redraw, and key handling. |
| Synthetic fixtures | Deterministic ANSI fixtures cover Unicode/wide characters, style policy, cursor movement, clear-screen, scroll regions, resize races, and malformed/partial escape sequences. |

Linux and macOS host smoke tests are required before promoting primary PTY
support. Docker Linux conformance is useful but cannot prove host-specific PTY
semantics. Windows remains out of scope until a Windows PTY adapter and fixtures
are designed.

## Consequences

| Type | Impact |
|------|--------|
| Positive | Harness probes can assert against rendered screens instead of brittle raw ANSI output. |
| Positive | Cassettes preserve raw evidence while also carrying human-reviewable frames. |
| Positive | The emulator backend can be swapped without rewriting harness adapters. |
| Negative | Fizeau inherits terminal-emulator edge cases and must maintain a conformance suite. |
| Negative | Terminal rendering is more work than PTY process control alone. |

## Risks

| Risk | Prob | Impact | Mitigation |
|------|------|--------|------------|
| Emulator library mishandles a real harness TUI | M | H | Keep backend behind `internal/pty/terminal`; require real TUI conformance fixtures before support claims |
| Tests become flaky due to volatile TUI content | H | M | Separate semantic normalization from secret scrubbing and assert stable screen facts |
| Unicode or style handling loses meaningful UI state | M | M | Add synthetic wide-character/style fixtures and document style preservation policy |
| Raw and rendered evidence diverge | M | M | Store `output.raw` as authoritative evidence and derive frames through deterministic replay tests |

## Validation

| Success Metric | Review Trigger |
|----------------|----------------|
| `top` spike behavior is reproduced in automated conformance tests | `top` can only be inspected manually |
| Terminal model handles raw byte streams, resize, input-driven redraw, and volatile normalization | Harness probes parse regex-stripped ANSI text |
| `output.raw` and `frames.jsonl` are both generated from the same PTY stream | Cassette contains frames without raw evidence |
| Terminal backend can be replaced behind one interface | Harness adapters import a concrete emulator package |
| Cassettes record the emulator name/version used for frame derivation | Frame assertions pass or fail differently after an emulator upgrade with no manifest mismatch |

## Amendment — 2026-07-14: Rendering Is Not Process Containment

**Status:** Accepted. This amendment clarifies the ownership phrase in the
original decision; it does not change the accepted terminal-emulator or
screen-model choice.

The statement that `internal/pty/session` “owns the PTY process” is limited to
PTY allocation, terminal modes and sizing, file descriptors, raw input/output,
and projection of child exit observed through the PTY. It does not assign
generic process-tree ownership, caller-death handling, cleanup policy, or stale
recovery to the PTY package.

`internal/processlifecycle` owns the per-invocation supervisor, launch gate,
platform containment boundary, caller-liveness control channel, durable
process-birth identity record, graceful and forceful cleanup, reaping, and
boundary-empty verification required by ADR-002 and CONTRACT-003. It
establishes containment before untrusted harness code runs and supplies the I/O
attachment needed by `internal/pty/session`. Batch and PTY harnesses use the
same lifecycle authority; `internal/pty/terminal` only consumes ordered bytes
after that boundary has been established.

Rendered frames and cassette replay prove terminal projection and parser
behavior. They do not prove process-group or Job Object membership,
caller-death cleanup, descendant reaping, or safe recovery identity. Those
claims require the live platform-specific lifecycle tests defined by ADR-002
and CONTRACT-003.

## References

- [ADR-002 PTY Cassette Transport](../../02-design/adr/ADR-002-pty-cassette-transport.md)
- [ADR-004 Terminal Harness Build-vs-Buy Boundary](../../02-design/adr/ADR-004-terminal-harness-build-vs-buy.md)
- [SPIKE-001 Direct PTY Rendering With Unix Top](../../02-design/spikes/SPIKE-001-direct-pty-top-rendering.md)
- [CONTRACT-003 Fizeau Service Interface](../contracts/CONTRACT-003-fizeau-service.md)

## Review Checklist

- [x] Context names a specific problem
- [x] Decision statement is actionable
- [x] Alternatives are represented by library-selection criteria
- [x] Consequences include positive and negative impacts
- [x] Risks have mitigations
- [x] Validation section defines review triggers
