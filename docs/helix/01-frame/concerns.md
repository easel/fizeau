# Project Concerns — Fizeau

## Area Labels

| Area | Description |
|------|-------------|
| `all` | Every bead |
| `lib` | Core library packages (agent loop, tools, providers, logging) |
| `cli` | Standalone CLI binary |
| `ui` | Hugo microsite, benchmark workbench browser assets, and workbench smoke coverage |
| `data` | Benchmark data builder scripts, emitted static data artifacts, and schema/evidence plumbing |

## Active Concerns

- **go-std** — Go + Standard Toolchain (areas: all)
- **testing** — Multi-layer testing with property-based, fuzz, and E2E coverage (areas: all)
- **hugo-hextra** — Hugo + Hextra microsite surface (areas: ui)
- **python-uv** — Python benchmark-data pipeline execution discipline (areas: data)
- **o11y-otel** — OpenTelemetry observability and cost tracking (areas: lib, cli)

## Project Overrides

### go-std

- **CLI framework**: None. Fizeau CLI is minimal enough for `flag` stdlib. Cobra
  is not needed.
- **Test framework**: Use `testing` stdlib + `testify/assert` for assertions.
  No external test runner.
- **Structured logging**: Use `log/slog` from stdlib. No third-party logger.
- **HTTP client**: Use provider SDKs (`openai-go`, `anthropic-sdk-go`) directly.
  No custom HTTP client abstraction.

### testing

- **Property-based testing**: Use `pgregory.net/rapid` for property-based tests
  in Go. Define properties for all serialization (session log events),
  tool-call round-trips, and provider message translation.
- **Fuzz testing**: Use Go's native `testing.F` fuzz support for parsers,
  config loading, and tool input handling.
- **E2E testing**: Full agent loop E2E tests run against LM Studio with a
  loaded model (build tag `e2e`). Verify a complete file-read-and-edit
  workflow end-to-end.
- **Integration tests**: Provider integration tests against real LM Studio and
  real Anthropic API using build tags (`integration`, `e2e`). Real subprocess
  harness tests use build tag `harness_integration` so they can be run and
  reported separately from provider HTTP tests and full E2E workflows.
- **Harness integration evidence**: A harness capability is not considered
  supported by tests unless real-binary integration evidence invokes the
  installed harness through each advertised public surface (`FizeauService.Execute`
  and/or the standalone CLI path, tracked separately). Parser tests, fixture
  replay, and fake binaries are unit/contract evidence only; they must not be
  described as proving Claude Code, OpenAI Codex, Pi, OpenCode, or any other
  external harness works. A single happy-path real run is only a harness-level
  smoke gate; it does not prove any other capability.
- **Deterministic harness tests**: When deterministic behavior is needed, use a
  reusable virtual/deterministic harness with dictionary-driven prompts,
  stable events, stable token usage, and explicit slow/error/cancel modes.
  This harness verifies shared service infrastructure and event contracts, not
  external harness compatibility. Real harness integration tests must assert
  that the resolved harness is not marked `TestOnly`.
- **Harness capability matrix**: Every harness capability must be declared in a
  machine-checkable matrix with one of: `required`, `supported`,
  `unsupported`, or `experimental`. Every `supported` capability requires real
  integration evidence for that harness and public surface. Each matrix row
  must carry evidence IDs that name the integration tests or recorded
  golden-master cassettes that prove it. CI must fail when a `supported`
  capability lacks evidence, when a row points at missing evidence, or when a
  capability is promoted from `experimental` to `supported` without adding
  evidence in the same change. `unsupported` capabilities must not be
  advertised by the public API, and requests for them must fail loudly with a
  typed capability-unsupported error rather than being silently ignored.
  `experimental` capabilities are excluded from "fully supported" claims until
  promoted and covered by integration evidence.
- **Harness capability granularity**: Do not collapse distinct harness behavior
  into vague labels. Track default model reporting, exact model pinning,
  declared/catalog model support, live model discovery, reasoning selection,
  progress events, token usage, session logging, cancellation, permission mode
  handling, workdir/context use, and quota monitoring as separate capabilities.
  Define each capability's observable contract before marking it supported; for
  example, cancellation must specify subprocess termination, final event status,
  and session-log flushing requirements.
- **Harness golden masters**: Real harness integration tests should support a
  PTY-backed record/replay workflow. Record mode runs the real authenticated
  harness through the selected PTY transport, fails fast when the binary,
  credentials, subscription, configured model, transport dependency, or quota
  surface is unavailable, and writes sanitized golden-master cassettes
  containing command arguments, binary version, transport/session metadata, pane
  transcript, event sequence, final metadata, usage, quota probe output, and
  relevant session-log records. Replay mode must also run through the selected
  PTY transport, using a cassette player that recreates the recorded pane
  transcript and structured event stream deterministically. Replay is contract
  evidence for parser/event/session/PTY transport behavior, but replay alone is
  not evidence that the external harness still works today.
- **Harness live-run policy**: Skipped live harness integration tests do not
  count as passing evidence. CI must distinguish absent credentials from
  failing behavior. If live tests cannot run on every PR, run them in a
  scheduled or manually triggered job with pinned harness versions and publish
  the evidence freshness. A capability with stale, repeatedly skipped, or
  version-mismatched live evidence must be downgraded to `experimental` or
  `unsupported` until a fresh record-mode run passes.
- **Inspectable harness execution**: Real subprocess harness integration runs
  require one consistent attachable PTY execution transport. Do not let tmux
  become a narrow quota-only or debugging-only dependency while normal harness
  execution uses a separate transport. The transport decision record must choose
  one architecture for live harness execution, quota/status probing, record
  mode, replay mode, cancellation, and inspection: either standardize on tmux
  for all of those harness paths, or own direct PTY/session supervision and
  remove tmux from the harness architecture. The decision must evaluate tmux,
  direct PTY management, `ntm`, and any other credible terminal supervisor
  against attachability, pane/screen capture, input injection, timing capture,
  exit-status capture, cleanup, cancellation, replay fidelity, portability,
  operational inspectability, and implementation cost. Tests for the selected
  transport must prove attachability, pane capture, cancellation, subprocess
  cleanup, and session-log consistency. If the cassette recorder or player
  becomes a generic PTY record/replay tool rather than Fizeau-specific
  harness evidence plumbing, it should be split into its own project with an
  explicit API and versioned cassette format.
- **Quota tests**: Claude Code and OpenAI Codex quota monitoring requires
  parser, cache, public API, and real quota-probe integration coverage. Tests
  should prove probe/cache/API behavior without requiring measurable quota burn;
  before/after quota-consumption deltas are manual or optional unless they can
  be made cheap, stable, and account-safe. Record mode for quota tests must
  fail fast when the harness is not authenticated rather than silently writing a
  "no quota" cassette.
- **Test data**: Use `rapid` generators for structured test data (Messages,
  ToolCalls, TokenUsage). Factory functions with sensible defaults for complex
  types.
- **Performance ratchets**: Track agent loop overhead (<1ms per iteration
  excluding model inference) and tool execution overhead via benchmarks.
- **Benchmark workbench verification**: When a change touches both the
  browser workbench (`website/`) and the benchmark data builder
  (`scripts/website/`), run the local gates from both `hugo-hextra` and
  `python-uv` in the same pass so the generated data, bundled assets, and
  rendered Hugo page stay aligned.

### hugo-hextra

- **Theme version**: Hextra v0.12.1 pinned in `website/go.mod`.
- **Hugo version**: Hugo extended 0.160.0 pinned in
  `.github/workflows/ci.yml` and `.github/workflows/website.yml`.
- **Workbench shell**: `website/content/benchmarks/explorer/_index.md`
  owns the benchmark workbench page inside the Hugo/Hextra docs shell;
  keep Hextra for navigation, docs structure, and global styling
  primitives.
- **Custom analytical UI allowance**: The benchmark workbench may use
  custom CSS and JS under `website/assets/` when Hextra utilities alone
  cannot express dense analytical controls, Perspective grid wiring, or
  pairwise comparison tables. Preserve the scientific-instrument rules in
  `website/DESIGN.md`; do not fork the broader docs shell.
- **Bundle entry point**: The browser module source lives at
  `website/assets/js/benchmark-workbench.js` and is bundled via the
  repo-root `npm run build:benchmark-workbench` script into
  `website/static/js/benchmark-workbench.js`.
- **Expected local quality gates**: UI-facing benchmark workbench changes
  should run `npm run build:benchmark-workbench`, `hugo --source website`,
  and `make benchmark-workbench-smoke`.

### python-uv

- **Execution model override**: Activate the concern for benchmark data
  work, but override the library default package layout. This repo does
  not maintain a `pyproject.toml`/`uv sync` Python application for the
  workbench pipeline. The active builder is the standalone script
  `scripts/website/build-benchmark-data.py`, with dependencies listed in
  `scripts/website/requirements.txt`.
- **uv usage**: Preferred ephemeral execution is
  `uv run --with PyYAML --with pyarrow --with duckdb python scripts/website/build-benchmark-data.py`.
  `make benchmark-data` is also valid when the local environment already
  provides `.venv-report/bin/python` or an equivalent Python with those
  packages installed.
- **Test framework override**: Builder regression coverage is the checked-in
  stdlib `unittest` module at `scripts/website/test_build_benchmark_data.py`,
  not a repo-wide `pytest` package layout.
- **Published artifacts**: Benchmark data changes are responsible for the
  normalized outputs under `website/static/data/`, especially
  `cells.parquet`, `task-combinations.parquet`, and
  `benchmark-data-manifest.json`.
- **Expected local quality gates**: Data-pipeline changes should run
  `python3 -m unittest scripts.website.test_build_benchmark_data`,
  `python3 -m compileall -q scripts/website/build-benchmark-data.py`, and
  regenerate or verify the data feed with
  `uv run --with PyYAML --with pyarrow --with duckdb python scripts/website/build-benchmark-data.py`
  or `make benchmark-data`.

### o11y-otel

- **Scope**: Core library instrumentation (lib area) and CLI (cli area). The
  library implements CONTRACT-001 OTel conformance for agent runs, LLM calls,
  and tool execution. Benchmark pipelines (data) and the Hugo site (ui) do not
  emit OTel spans; they remain orthogonal to this concern.
- **Span taxonomy**: Every library `Execute` call emits one root `invoke_agent`
  span. Every provider request attempt emits a `chat` span with model, provider,
  token usage (input, output, cached), and cost fields per CONTRACT-001. Every
  tool execution emits an `execute_tool` span with tool name, arguments, results,
  and execution status.
- **JSONL and OTel duality**: JSONL session logs remain the replay artifact
  (full prompt bodies, tool call details, session state). OTel spans are the
  canonical analytics surface (structured attributes, metrics, trace context
  propagation). A harness may emit both simultaneously.
- **Cost handling**: Use provider-reported cost when available. Fall back to
  gateway-reported cost, then explicitly configured pricing for the resolved
  provider/model pair. Record unknown cost explicitly; do not guess from stale
  price tables. Per CONTRACT-001, cost precedence is: provider-reported →
  gateway-reported → configured → unknown.
- **Testing**: Conformance tests verify that every agent execution path emits
  the required span taxonomy, identity fields (`ddx.harness.name`,
  `ddx.session.id`, turn/attempt indices), provider/model fields, token usage,
  and cost source/amount. Load tests verify that OTel overhead remains < 5%
  against baseline execution time.
- **Expected local quality gates**: Changes to the agent loop, provider
  implementations, or tool execution must verify CONTRACT-001 conformance via
  the harness capability matrix and integration tests that capture spans
  alongside JSONL logs.
