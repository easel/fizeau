---
ddx:
  id: TP-001
  depends_on:
    - helix.prd
    - US-001
    - US-002
    - US-003
    - US-004
    - US-005
    - US-006
    - US-007
    - US-008
  review:
    self_hash: 8b9ac8c637bdc4e7e36eb8271966356efb57d315650bbdf31f6d1e2f697dc8a4
    deps:
      US-001: 83ee6bdfd89336cf77cb0dd2a1f6d8250baf1de494605112a56ef21b835c9b83
      US-002: 6d7fad544c6ba871e42c6b6b2b4926d9e2a78b7c1e69f294fd14d46cca157aff
      US-003: f204f2e58a405ef53c8a3bab96afcf242e755ec10bbe85b1d7985e81b95abe81
      US-004: 7ee7f81b23b2c2ae22f0c1137c31f100e29e7f89a73f3f71268269d2d2738d25
      US-005: a1c55221d28ebcf4415320a93c02126c8d27a782b0cb1917e8dce4415e1d8180
      US-006: 5cfd99cb446fa9b71fab13b1b2f819736296bd491c6efa8461c0855916e46b80
      US-007: f7d77406d905f1c80b62432b28060e560dc7c8d811124159f3650ff2ab914ebf
      US-008: ec847be580408d23190b47f052551b4f7638365ab373de68e57d8ee06fb2bc4a
      helix.prd: edcba06017764a15c820d236ed64e1d4d55eb24f4e684fd9974dd328153da68a
    reviewed_at: "2026-07-14T05:16:22Z"
---
# Test Plan — Fizeau

## Testing Strategy

**Goals**: protect the embeddable public contract, measurement semantics, and
local/cloud parity | **Quality gates**: every package test passes and every
measured package meets its ratchet floor

**Out of Scope**: live paid-provider calls and hardware-specific benchmark
claims. Those require opt-in integration runs and recorded evidence.

**Traceability Source**: `docs/helix/01-frame/prd.md`, `FEAT-001` through
`FEAT-008`, and `US-001` through `US-008`.

### Test Levels

| Level | Coverage Target | Priority |
|---|---|---|
| Public contract | All changed exported behavior has an external-package or structural contract test | P0 |
| Integration | Native and subprocess paths use service-owned events and projections | P0 |
| Unit | 100% of measured packages meet `.helix-ratchets/coverage-floor.json` | P0 |
| E2E | Installer and browser workbench acceptance suites pass for affected releases | P1 |

### Frameworks

| Type | Framework | Reason |
|---|---|---|
| Contract | Go `testing` with `package fizeau_test` and structural tests | Exercises the consumer boundary |
| Integration | Go build tags, cassettes, and local test servers | Keeps default tests deterministic; live probes remain explicit |
| Unit | Go `testing`, `testify`, and focused property/fuzz tests | Matches package ownership |
| E2E | Shell acceptance tests and Playwright-driven Go smoke tests | Exercises released installer and browser surfaces |

## Test Data

| Type | Strategy |
|---|---|
| Fixtures | Checked-in provider/harness cassettes, session JSONL, catalog snapshots, and benchmark cells |
| Factories | Go helpers create isolated services, providers, workspaces, and temporary homes |
| Mocks | `internal/provider/virtual`, scripted harnesses, local HTTP servers, and fake release downloads |

No default test may require a paid credential, an external provider, or a
developer's real home-directory configuration.

## Coverage Requirements

| Metric | Target | Minimum | Enforcement |
|---|---|---|---|
| Ratcheted Go packages | 100% meet floor | Floor minus configured tolerance | `make coverage-ratchet` |
| Default Go suite | 100% pass | 100% | `go test -count=1 ./...` |
| Race suite | 100% pass | 100% | `make test-race` |
| P0 story criteria | Exercising test plus canonical citation | 100% | review and traceability check |

The ratchet is fail-closed: a failed `go test -cover ./...` run or a run that
parses zero packages is a measurement error, never a passing zero-row report.

### Critical Paths (P0)

1. Public construction and one bounded `Execute` lifecycle.
2. Tool validation, workspace resolution, and service-owned tool events.
3. Provider-neutral execution and explainable route selection.
4. Per-turn measurement, replay, and explicit unknown cost semantics.
5. CLI delegation through the public facade.

### Known Contract-to-Code Gaps

These desired contracts are intentionally not counted as passing evidence:

| Contract | Missing implementation | Required proof before alignment |
|---|---|---|
| CONTRACT-001 timing classification | Chat spans lack required availability, source, and available-fields attributes. | Telemetry contract tests assert all classifications for present and absent timing windows. |
| CONTRACT-003 final cost provenance | `DrainExecuteResult` and `ServiceFinalData` expose the amount but not the public `CostSource` classification. | Root facade and service-event tests cover reported, configured, unknown, and known-zero cases. |
| SD-011 progress usage | The internal progress payload still uses historical cached-input/retried-input fields instead of cache-read/cache-write. | Transcript tests assert the canonical four-stream payload and keep retry accounting separate. |
| SD-010 benchmark pricing map | Benchmark profiles expose one historical cached-input rate instead of separate cache-read/cache-write rates. | Profile-schema and cost-reconciliation tests cover all four runtime streams without pricing retry accounting. |

### Secondary Paths (P1-P2)

- P1: installer/update release flow and benchmark workbench smoke coverage.
- P2: opt-in live providers, hardware benchmarks, fuzzing, and long-running
  integration probes.

## Acceptance Criteria Layer Allocation

The following files contain the exercising tests and canonical citations for
each product story. The 2026-07-14 traceability check found all 21 top-level
criteria cited. Citation is necessary but not sufficient: reviewers still
verify that the named assertions exercise the criterion.

| PRD / Story | Criteria | Primary test evidence | Primary layer | Trace state |
|---|---|---|---|---|
| FR-1 / US-001 | `US-001-AC1`, `US-001-AC2` | `harness_golden_integration_test.go`; `service_execute_test.go` | Contract + integration | CITED |
| FR-2 / US-002 | `US-002-AC1`, `US-002-AC2` | `internal/core/integration_test.go`; `service_execute_test.go` | Unit + integration | CITED |
| FR-3 / US-003 | `US-003-AC1`, `US-003-AC2` | `service_http_provider_test.go`; `service_providers_test.go` | Contract + integration | CITED |
| FR-4 / US-004 | `US-004-AC1` through `US-004-AC3` | `service_contract_snapshot_test.go`; `service_snapshot_autorouting_test.go`; `service_route_evidence_test.go` | Contract + unit | CITED |
| FR-5 / US-005 | `US-005-AC1` through `US-005-AC3` | `internal/core/loop_test.go`; `internal/core/telemetry_contract_test.go`; `internal/session/replay_test.go` | Integration + unit | CITED |
| FR-6 / US-006 | `US-006-AC1` through `US-006-AC3` | `agentcli/cli_process_test.go`; `agentcli/service_boundary_test.go` | Contract + integration | CITED |
| FR-7 / US-007 | `US-007-AC1` through `US-007-AC4` | `tests/install_sh_acceptance.sh`; `agentcli/update_test.go` | Integration + E2E | CITED |
| FR-8 / US-008 | `US-008-AC1`, `US-008-AC2` | `website/benchmark_workbench_smoke_test.go` | E2E + integration | CITED |

Canonical citations use `@covers US-<n>-AC<m>` immediately above the test that
exercises the criterion, for example `// @covers US-001-AC1` in Go or
`# @covers US-007-AC1` in shell/Python. A citation supplements an exercising,
passing assertion; it does not replace one. Missing citation is
`UNCITED_COVERAGE`, no exercising test is `UNTESTED`, and a citation without a
relevant assertion is `ASSERTED_UNBACKED`.

## Implementation Order

1. Add or update the focused test with each behavior change and attach its
   story citation where the test genuinely exercises an acceptance criterion.
2. Run the focused package and structural acceptance checks named by the bead.
3. Run the full default and race suites before closeout; run release or browser
   E2E gates when their surfaces change.

## Infrastructure

| Requirement | Specification |
|---|---|
| CI tool | GitHub Actions using Go from `go.mod` |
| Services | None for the default suite; local servers and subprocess cassettes only |
| Platform jobs | Linux default/race; Linux and macOS installer acceptance |
| Browser | Hugo plus Playwright Chromium for workbench smoke |

## Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Tests read developer configuration | High | Isolate HOME/XDG/Fizeau/Codex paths in subprocess tests |
| Live network or PTY behavior flakes | High | Default to cassettes; keep live recording opt-in |
| Coverage command fails but output is partially parseable | High | Ratchet returns measurement error on any test failure |
| Named test exists but does not exercise the criterion | High | Review assertion relevance; classify as `ASSERTED_UNBACKED` |

**Traceability rule**: a syntactically valid citation does not override a
failed test or an assertion-relevance review. Any later semantic mismatch is
classified `ASSERTED_UNBACKED` and blocks the affected criterion.

**Explicit contract gap**: CONTRACT-001 requires every chat span to expose
`ddx.timing.availability`, `ddx.timing.source`, and — for available or partial
timing — `ddx.timing.available_fields`. No Go implementation or test currently
contains those fields. `US-005-AC1` verifies measured first-token and generation
values but does not satisfy that separate CONTRACT-001 requirement; the
contract check remains `UNIMPLEMENTED`.

## Build Handoff

**Commands**: `go test -count=1 ./...` | `make test-race` |
`make coverage-ratchet`

**Priority**: focused structural acceptance first, then repository gates.

**Blocking Gate**: no failed tests, no empty coverage measurement, and every
measured package at or above its configured floor minus tolerance.

## Review Checklist

- [ ] Changed public behavior has a contract test
- [ ] Story criteria have exercising tests and canonical citations
- [ ] Default and race suites pass
- [ ] Coverage ratchet measures at least one package and passes
- [ ] Installer/browser E2E gates run when those surfaces change
- [ ] No test depends on real developer configuration or paid credentials
