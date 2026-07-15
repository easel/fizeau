---
ddx:
  id: deployment-checklist
  depends_on:
    - implementation-plan
    - FEAT-007
    - CONTRACT-003
    - CONTRACT-004
    - ADR-002
    - ADR-013
    - ADR-014
    - SD-005
    - SD-006
  review:
    self_hash: 4bf8db3cd5220f56291016ce860c813eae5b4da4bd5e634c3bb27bc4624c3870
    deps:
      ADR-002: 973f858cdad07342b377ef3e4f58481ae0383c946077fac4e44e790e81687e7e
      ADR-013: 7b6760fa222d244517cf807e75414d2bf8282531ade62b9ec7ea961bd17b21c1
      ADR-014: 990917e08305df61afc31821a38e4acc63a84d0ab204c7849d6f126391282d67
      CONTRACT-003: 50cbc8709ce89d676bd10df9ba3d635089cb474823dbc10a468e2f7ecd72cf31
      CONTRACT-004: 0e19d06f34a0697f0f46fde18a66b4f66f074f840307978ffe3d66a0dff27c0e
      FEAT-007: 20cf41ca595074feb1345729785859f504ce1fa570547ffc31ea38a264aa719b
      SD-005: e0acdb5a9db144a415aa5831485fe198aa3f9c7fdf0ac7d100f5a01a117df1a0
      SD-006: bd9f4cf464dbad08e003533906b67eb25735384eac4d522e367adccc9a3a7db6
      implementation-plan: 7ed12733f4cfb90d87c0e7a1c9df3cf90ea13c3e0b691a162e16a2b129594307
    reviewed_at: "2026-07-15T18:54:32Z"
---
# Release Checklist — Fizeau

## Release Scope

- Component: public Go module plus the thin `fiz` proof CLI
- Version or commit: `v*` tag and its immutable commit SHA
- Release owner: tag creator
- Rollback owner: repository maintainer on duty
- Sources of truth: `CONTRACT-003`, `CONTRACT-004`, `SD-005`, `SD-006`,
  `ADR-002`, `ADR-013`, `ADR-014`, `.github/workflows/ci.yml`,
  `.github/workflows/release.yml`, `install.sh`, and
  `tests/install_sh_acceptance.sh`

## Pre-Deploy Checks

| Area | Check | Evidence or Command | Status |
|---|---|---|---|
| Repository | Branch is clean and tag commit is pushed | `git status --short`; upstream SHA | [ ] |
| Quality | Build, static analysis, default tests, and race tests pass | `make ci-checks` | [ ] |
| Unix batch containment | Caller-death and every-batch-runner conformance passes on Linux and macOS | `lifecycle-unix` CI jobs; `TestSubprocessHarnessDiesWithEmbeddingCaller`; `TestUnixBatchLifecycleAppliesToEveryBatchRunner` | [ ] |
| PTY containment | PTY caller-death and grandchild cleanup passes on Linux and macOS | `lifecycle-unix` CI jobs; `TestPTYLifecycleDiesWithEmbeddingCaller`; `TestPTYLifecycleReapsGrandchildOnClose` | [ ] |
| Windows containment | Real Job Object kill-on-close and grandchild tests pass | `lifecycle-windows` CI job; `TestWindowsJobKillOnOwnerHandleClose`; `TestWindowsJobReapsGrandchild` | [ ] |
| Cleanup terminalization | `HarnessCleanupTimeout` bounds current-invocation cleanup and terminal ordering; `StaleHarnessReaperGrace` governs only later startup adoption | `TestHarnessCleanupTimeoutDefaultAndValidation`; `TestStaleHarnessReaperGraceDoesNotDelayCleanup`; `TestTerminalWaitsForHarnessCleanup`; `TestCleanupFailureSupersedesPrimaryTuple` | [ ] |
| Continuation capability | The public `FizeauService.Continue(context.Context, ServiceContinuationRequest)` surface resolves a completed parent's exact endpoint-aware terminal route, uses the authoritative registered route instance, and creates no session or process when preparation reports unavailable evidence | public contract compile test; `TestCompletedSessionRouteResolutionRequiresTerminalRoute`; `TestCompletedSessionRouteResolutionUsesPerRequestLogOverrideAfterRestart`; `TestContinuationUsesRegisteredRouteInstance`; `TestContinuationEvidenceUnavailableBeforeSpawn` | [ ] |
| Continuation prepare/start ordering | Preparation creates no child, lease, process, or event; Start runs exactly once only after child creation and fresh-lease acquisition; a Start failure cannot trigger a fresh fallback | `TestContinuationPrepareOrdersChildAndSpawn` | [ ] |
| Continuation opacity | Public continuation and serialized events carry only Fizeau session lineage; service- or harness-derived route-native evidence never crosses the public or session-log boundary, while caller metadata remains opaque | `TestContinuationHarnessReceivesOnlyFizeauSessionRef`; `TestContinuationNativeReferenceIsNotSerialized`; public-field and JSON-tag structural searches | [ ] |
| Continuation durability | The service-private locator uses the configured effective service session-log root; private evidence, terminal log, locator completion, and public success follow the contract order; pending recovery uses only the exact recorded path and full route key | `TestContinuationEvidenceCommitsBeforeSuccessfulTerminal`; `TestContinuationRecoversPendingLocatorAfterTerminalCommit` | [ ] |
| Continuation containment | Every resumed, `prefer_resume` fallback, and `fresh_session` continuation creates a new child session and acquires a fresh lifecycle lease | `TestContinuationDispatchAcquiresFreshLifecycleLease`; `TestContinuationFreshPoliciesAcquireFreshLifecycleLease`; ADR-013 and ADR-014 conformance | [ ] |
| Portable-runtime contract and completeness | `PreparePortableRuntime` accepts only an empty destination and same-GOARCH Linux target; preparation selects no route, joins actual instances to registry classifications, and includes every installed structurally unpinned-capable subprocess closure/launch recipe plus every effective configured provider or fails atomically | `TestPreparePortableRuntimeOwnsHarnessFilesAndEnvironment`; `TestPortableRuntimePlanIsRouteNeutralAndOpaque`; `TestPortableRuntimeConfiguredProvidersPreserveUnpinnedCandidateSet`; registry/instance and provider field-parity fixtures | [ ] |
| Portable-runtime security and cleanup | One sibling-staged tree commits as one `runtime` child and one fixed read-only guest mount; sensitive material has restrictive modes, no value/original source path reaches diagnostics or plan, partial failure/cancellation rolls back, and concurrent cleanup is retryable | `TestPortableRuntimeBundleRedactsSecretsFromDiagnostics`; `TestPortableRuntimeBundleCleanupRemovesCredentialCopies`; concurrent-preparer and focused race evidence | [ ] |
| Portable-runtime activation and OCI conformance | A separate public-package process applies only the generic mount and inherited names, calls `NewFromPortableRuntime`, and reconstructs configured providers plus production dispatch; each static/dynamic/interpreted recipe executes through unpinned `Execute` as the mapped non-root UID; failed runtime/storage cleanup retains the bundle and forbids `Close` until ordered retry succeeds; the required Linux job never skips | `TestPortableRuntimeActivationFeedsProductionDispatch`; `TestPortableRuntimeExternalCleanupFailureRetainsBundle`; `FIZEAU_PORTABLE_RUNTIME_CONTAINER=1 go test . -run '^TestPortableRuntimeBundleRunsUnpinnedExecuteInContainer$' -count=1 -timeout=3m`; `TestPortableRuntimePublicCompileFixture` | [ ] |
| Default Claude-TUI launch | An unpinned `Policy=default`, `Permissions=unrestricted` Claude request selects and dispatches `claude-tui`; the production launch boundary receives `--permission-mode bypassPermissions`, generated hook settings, and the resolved model without `--print` | `TestExecuteDefaultClaudeUnrestrictedLaunchesTUIYolo`; `TestExecuteDefaultClaudeUnrestrictedSelectsClaudeTUI`; `TestClaudeTuiDefaultSupportsUnrestrictedPermissions`; `TestExplicitClaudePinBeatsTuiDefault`; `TestRoutingSurfacePreference`; `TestDispatcherCallsClaudeTui`; `TestBuildLaunchArgsBypassPermissions` | [ ] |
| Context evidence | Selected-route context comes from candidate/config, cached type-gated provider evidence, catalog metadata, or the documented default; route and execution hot paths make no synchronous limit probe | SD-005/SD-006 conformance evidence; structural probe-path review | [ ] |
| Capacity enforcement | The selected context value/source is authoritative, a positive compaction bound only tightens it, and every provider-call path applies the canonical estimate and fixed capacity envelope | CONTRACT-003 obligations 17–20; focused route/core evidence | [ ] |
| One-route boundary | Eligibility-time context rejection may leave the best eligible survivor for selection; after one route is selected, accepted-session capacity failure never dispatches the next ranked candidate and semantic retry is a new caller request | CONTRACT-003 obligations 17, 20, and 21; service dispatch evidence | [ ] |
| v0.15 public compatibility | Core-to-`internal/harnesses`-to-root capacity mapping is exhaustive; additive capacity events/final values preserve unknown JSON enums; changed exported structs use keyed literals; external mocks implement both new `Continue` and `PreparePortableRuntime` interface methods | CONTRACT-003 obligations 21–29; public compile, AST, and JSON fixture evidence | [ ] |
| Recovery | Reused process identity is refused and cleanup-failed records are retained | `TestRecoveryRefusesReusedIdentity`; `TestCleanupFailureRetainsRecoveryRecord` | [ ] |
| Governed documents | Lifecycle and capacity contracts, decisions, and designs are current | `ddx doc stale --json` omits `CONTRACT-003`, `CONTRACT-004`, `SD-005`, `SD-006`, `ADR-002`, `ADR-004`, `ADR-013`, and `ADR-014` | [ ] |
| Installer | Linux and macOS installer acceptance passes | `make test-install-sh` | [ ] |
| Artifact names | Workflow emits only `fiz-<os>-<arch>` names | `go test . -run TestReleaseWorkflowArtifactNamesFiz -count=1` | [ ] |
| Version | Tag starts with `v` and points at the intended commit | tag/SHA record | [ ] |

## Rollout Plan

| Stage | Action | Exit Condition |
|---|---|---|
| Build | Push the `v*` tag; GitHub Actions cross-compiles with `CGO_ENABLED=0` | Linux/darwin × amd64/arm64 jobs upload `fiz-${GOOS}-${GOARCH}` |
| Publish | Publish the four downloaded artifacts as one GitHub release | Release for the exact tag exists and lists all four binaries |
| Install verification | Run `install.sh` with `FIZEAU_VERSION=<tag>` in a temporary install directory | Correct OS/arch artifact downloads, becomes executable, and answers `fiz --version` |

The installer supports Linux and macOS on amd64 and arm64. It normalizes an
explicit version to a `v` tag, otherwise reads the latest GitHub release, writes
to `FIZEAU_INSTALL_DIR` (default `$HOME/.local/bin`), makes `fiz` executable,
and checks the installed binary.

Windows lifecycle CI validates the Go library contract only. It does not add a
Windows release artifact; the release matrix remains Linux and macOS on amd64
and arm64 until a separate artifact decision expands it.

## Verification Checks

| Signal or Check | Expected Result | Evidence or Command | Status |
|---|---|---|---|
| Workflow | `release` matrix and `publish` job are green | GitHub Actions run for tag | [ ] |
| Assets | Exactly four supported platform artifacts are present | GitHub release asset list | [ ] |
| Explicit install | Requested tag is downloaded, not latest | `FIZEAU_VERSION=<tag> FIZEAU_INSTALL_DIR=<tmp> ./install.sh` | [ ] |
| Execution | Installed binary reports version successfully | `<tmp>/fiz --version` | [ ] |
| Library | Tagged module remains consumable through the public root package | release commit `go test -count=1 ./...` evidence | [ ] |
| Caller death | No registered wrapped harness leaves a contained process after embedding-caller death | Linux, macOS, and Windows lifecycle CI evidence | [ ] |
| Claude-TUI | Default unrestricted routing reaches the interactive yolo launch contract, and successful execution leaves no live PTY or Claude process | `TestExecuteDefaultClaudeUnrestrictedLaunchesTUIYolo`; `TestClaudeTUIExecuteLeavesNoLiveSession` | [ ] |
| Cleanup failure | `cleanup_failed` retains lifecycle ownership evidence for recovery | `TestCleanupFailureRetainsRecoveryRecord` | [ ] |
| Identity safety | Startup recovery never signals an identity-mismatched or reused process | `TestRecoveryRefusesReusedIdentity` | [ ] |
| Continuation | Resumed, prefer-fallback, and explicitly fresh outcomes each create a new child Fizeau session with a fresh lease; implementation-derived native evidence is absent from public projections and logs | continuation conformance suite; public-field and serialized-tag structural searches | [ ] |
| Context provenance | Routing event, execution handoff, capacity event, and final projection agree on selected context value/source; raw unknown candidate evidence remains distinguishable | CONTRACT-003 selected-context conformance evidence | [ ] |
| Capacity event order | Clamp immediately precedes its request; planning skip prevents planning request/response/turn; main rejection precedes session end and typed terminal, with no provider call or next-route dispatch | CONTRACT-003 capacity event-order evidence | [ ] |
| Portable runtime | Preparation is route-neutral and value-opaque; separate-process activation reproduces structural candidate identities, configured providers, and production dispatch; runtime destruction removes guest overlays and explicit Close leaves no host bundle credential copy | all six parent `TestPreparePortableRuntime*` / `TestPortableRuntime*` conformance tests; `TestPortableRuntimeConfiguredProvidersPreserveUnpinnedCandidateSet`; `TestPortableRuntimeActivationFeedsProductionDispatch`; required Linux OCI job | [ ] |
| v0.15 migration | External keyed Go literals compile, additive capacity JSON accepts unknown future event/cause values, and third-party service mocks implement `Continue` plus `PreparePortableRuntime`; public consumers compile `NewFromPortableRuntime` | public compile/AST and JSON compatibility fixtures | [ ] |

## Rollback Triggers

| Trigger | Threshold or Condition | Immediate Action | Owner |
|---|---|---|---|
| Missing/wrong artifact | Any of four matrix assets absent or mislabeled | Mark release unusable and stop installer promotion | Release owner |
| Installer mismatch | Explicit tag downloads a different version/platform or cannot execute | Mark release unusable; fix forward with a new tag | Release owner |
| Tag commit gate failure | Any required repository gate fails against the tag SHA | Mark release unusable; do not retag the same version | Maintainer |
| Public contract regression | A consumer cannot build the tagged module | Mark release unusable; publish a corrective version | Maintainer |
| Wrapped harness leak | Any contained process remains after successful cleanup or caller-death recovery | Hold release; preserve lifecycle evidence and fix forward | Maintainer |
| Premature terminal event | A final event is emitted before the cleanup decision | Hold release; restore cleanup-before-terminal ordering | Maintainer |
| Unsafe stale recovery | Recovery signals a reused or unowned process identity | Hold release; disable recovery path until identity validation is fixed | Maintainer |
| Unsafe Windows resume | A Windows child resumes before Job Object assignment | Hold release; reject wrapped execution until assignment ordering is fixed | Maintainer |
| Indeterminate record deletion | A lifecycle record is removed before boundary emptiness is confirmed | Hold release; retain the record and fix forward | Maintainer |
| Continuation evidence leak | Any service- or harness-derived native session reference appears in a public type, event, projection, metadata field, or service-owned session log | Hold release; disable continuation for the route and remove the leaked evidence before a corrective release | Maintainer |
| Continuation lease reuse | A resumed or fresh continuation reuses a live process, PTY, containment boundary, or lifecycle lease from its parent | Hold release; disable continuation and restore one-fresh-lease-per-invocation semantics | Maintainer |
| Continuation durability/order failure | Successful continuation evidence is not durable before public success, locator recovery scans outside its recorded exact path, or Start can run before child lease acquisition | Hold release; disable continuation and restore prepare/persist/start ordering before a corrective release | Maintainer |
| Context hot-path probe | Route resolution or execution synchronously contacts a provider only to fill missing context/output limits | Hold release; disable that probe and restore cached type-gated evidence with catalog/default fallback | Maintainer |
| Capacity authority mismatch | Execution enlarges the selected route window, projections disagree on value/source, or a capacity failure advances to another candidate | Hold release; restore selected-route authority and one-route-per-`Execute` behavior | Maintainer |
| Capacity projection regression | A capacity payload field is dropped between core, harness-neutral event, root decode, or final projection, or event order permits a prevented provider call | Hold release; restore exhaustive mapping and required event order | Maintainer |
| v0.15 migration regression | Public examples use unkeyed changed structs or additive JSON/event values are rejected as unknown | Hold release; correct migration fixtures and tolerant decoding before publishing v0.15 | Maintainer |
| Portable runtime narrows the structural set | Any installed structurally unpinned-capable subprocess or effective provider is silently omitted, actual transport is misclassified, or preparation chooses a route | Hold release; restore exhaustive registry/instance and provider inventory, then defer live eligibility to `Execute` | Maintainer |
| Portable activation drifts | `NewFromPortableRuntime` cannot reconstruct provider/structural identity in a separate public-only process or falls back to host config | Hold release; disable portable execution until fixed-root manifest activation is authoritative | Maintainer |
| Portable dispatch bypass | Activation updates only a scheduler/refresh map while production `Execute` constructs or selects an unconfigured runner | Hold release; wire activated launch recipes into production dispatch and prove them with distinguishable fixtures | Maintainer |
| Portable executable closure is incomplete | A staged PATH launcher lacks its interpreter, package tree, loader, shared runtime, content identity, or same-target offline proof | Hold release; reject that preparation path until the owning contributor returns a conforming closure | Maintainer |
| Portable secret or cleanup regression | A credential/environment value appears in an error, JSON, String, log, or plan; failed/cancelled preparation leaves material; guest writable storage survives runtime destruction; failed stop/storage destruction permits host `Close`; or any cleanup cannot be retried with both handles retained | Hold release; disable portable preparation and retain runtime, storage, and bundle cleanup ownership until every owned copy is removed | Maintainer |
| Portable OCI proof skips | The required Linux job skips or passes without separate-process activation, configured-provider bootstrap, all three closure classes, and unpinned `Execute` | Hold release; restore the non-skipping public-consumer OCI gate | Maintainer |

Published tags and DDx audit commits are immutable. Rollback means stopping use
of the bad release and publishing a new corrective tag; it never means moving
or rewriting the published tag.

## Go or No-Go Decision

- Decision: [Go / Hold / Mark unusable]
- Tag and commit: [tag / SHA]
- Decision time: [timestamp]
- Workflow run and release: [links]
- Exceptions or follow-up: [none or tracked work]
- Decision owner: [name]
