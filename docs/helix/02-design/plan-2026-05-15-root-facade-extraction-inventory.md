# Plan 2026-05-15: Root Facade Extraction Inventory for `fizeau-cd9af16b`

This inventory is the design prerequisite for parent bead `fizeau-cd9af16b`.
It reconciles that parent with ADR-008 instead of trying to land the
non-executable requirement "zero root `.go` files."

## Governing constraint

ADR-008 keeps the module root as the public facade:

- `docs/helix/02-design/adr/ADR-008-service-package-and-transcript-boundaries.md`
  says `github.com/easel/fizeau` remains the compatibility boundary and that
  concrete execution, transcript, route-health, quota, and routing-quality
  mechanics move behind `internal/*`.
- `service.go` defines the public `FizeauService` interface and the root
  request/response/event types that downstream code already imports.
- `website/content/docs/embedding/_index.md` documents the root package as the
  importable library surface and lists the public packages under this module.

Because ADR-008 explicitly rejects both a nested `fizeau/` package and a
module split as the immediate answer, parent AC #1 ("zero root `.go` files")
is incompatible with the accepted design unless a separate change first ships a
replacement public package, updates generated docs, and migrates consumers.

The executable interpretation of `fizeau-cd9af16b` is therefore:

1. Keep the root `package fizeau` importable at `github.com/easel/fizeau`.
2. Split mixed root files into public contract stubs plus internal
   implementation owners.
3. Move implementation-owned files and white-box tests behind the
   ADR-008-aligned `internal/*` packages.
4. Leave only the public facade, intentional shims, and root black-box
   contract tests at the module root.

## In-repo consumers of `github.com/easel/fizeau`

These consumers make the root facade a real compatibility boundary today.

| Consumer | Evidence | Why root removal breaks it |
|---|---|---|
| `agentcli/` | `agentcli/harnesses_command.go`, `agentcli/policies_command.go`, `agentcli/route_status.go`, `agentcli/routing_smart.go`, `agentcli/run.go`, plus related tests | Directly imports root service types such as `FizeauService`, `UsageReportOptions`, `OverrideClassBucket`, harness status, and routing/report surfaces. |
| `cmd/bench/` | `cmd/bench/configadapter.go`, `cmd/bench/discovery.go`, `cmd/bench/external_termbench.go`, `cmd/bench/runner.go` | Directly imports root config-adapter, service, and routing/model surfaces. |
| `cmd/fiz/` | `cmd/fiz/main.go` imports `github.com/easel/fizeau/agentcli` | Transitive consumer: `cmd/fiz` builds through `agentcli`, and `agentcli` is a direct root-package consumer guarded by import-boundary tests. |
| Root black-box tests | `routing_errors_example_test.go`, `service_events_test.go`, `service_execute_test.go`, `service_final_usage_zero_test.go`, `service_harness_dispatch_test.go`, `service_new_test.go`, `service_override_test.go`, `service_policies_test.go`, `service_progress_test.go`, `service_public_api_smoke_test.go`, `service_role_correlation_test.go`, `service_taillog_test.go`, `service_test.go`, `testseam_test.go` | These tests exist specifically to prove the root import path remains a usable external facade. |
| `internal/config/` | `internal/config/serviceconfig.go` | Adapts loaded config into the root `ServiceConfig` interface. |
| `internal/benchmark/external/termbench/` | `internal/benchmark/external/termbench/plan.go` | Directly uses root request/tool-facing types. |
| Doc generators | `cmd/docgen-embedding/main.go`, `cmd/docgen-tools/main.go` | Directly import the root package to generate and inspect the documented public API. |

Documentation also consumes the root facade even when it is not a Go import:
`website/content/docs/embedding/_index.md` and
`cmd/docgen-embedding/page.tmpl` both describe `github.com/easel/fizeau` as
the library entry point.

## Root file inventory

The inventory buckets every current root-level `.go` file into one of three
classes:

- `Public facade`: root must keep these symbols importable, although several
  files are mixed and need internal logic split out.
- `Public compatibility alias/shim`: intentionally thin root wrappers,
  re-exports, or build-tag seams that exist so consumers do not import
  `internal/*`.
- `Implementation-owned`: concrete mechanics or white-box tests that should
  move behind the named internal-package owner.

## Post-extraction root source allowlist

Update 2026-05-15: the ADR-008 extraction chain is now far enough along that
the module root should be read as an explicit allowlist, not as the planning
inventory above. The enforced source-file set is locked by
`TestRootFacadeSourceAllowlist` in `service_root_facade_allowlist_test.go`.

### Public contract and compatibility surface

These files are the deliberate import boundary for `github.com/easel/fizeau`:

- Contract types, constructors, and public metrics:
  `service.go`, `service_events.go`, `service_session_projection.go`,
  `service_routing_quality.go`, `routing_errors.go`, `role_correlation.go`.
- Public aliases, wrappers, and CLI-facing compatibility seams:
  `public_api.go`, `public_cli_api.go`, `provider_quota_state.go`,
  `provider_burn_rate.go`, `service_catalog_impl_adapters.go`,
  `metadata_billing.go`, `service_models.go`, `service_providers.go`,
  `service_policies.go`, `service_status.go`, `service_snapshot.go`,
  `service_aliveness.go`, `service_capabilities.go`,
  `service_catalog_cache.go`, `service_execute_fanout.go`,
  `service_progress.go`, `service_session_log.go`, `service_taillog.go`.
- Build-tag and test-seam compatibility files:
  `options_prod.go`, `options_testseam.go`, `service_execute_seam_prod.go`,
  `service_execute_seam_testseam.go`, `testseam_types.go`.

### Root facade adapters over internal owners

These root files still exist on purpose, but after the extraction chain they
are adapters/wiring over internal execution, routing, and quota owners rather
than the old home of the implementation bulk:

- Execution and harness dispatch:
  `service_execute.go`, `service_execute_dispatch.go`,
  `service_harness_instances.go`, `service_model_resolution.go`,
  `service_native_provider.go`, `service_override.go`,
  `service_projection.go`, `service_reasoning.go`.
- Routing, status, and quota adapters:
  `service_probe.go`, `service_routestatus.go`, `service_routing.go`,
  `service_subscription_quota.go`.

`RecordRouteAttempt` and final-attempt feedback now enter through the narrow
adapter in `service_dispatch_feedback.go`; classification and exact-success
clearing are owned by `internal/routehealth`.
Sticky lease and utilization mechanics now enter through API-neutral
`internal/routehealth.StickyState` operations at the routing and model
projection boundaries; the former `service_route_leases.go` adapter is gone.

- Platform-specific lifecycle adapters:
  `service_stale_harness_reaper.go`,
  `service_stale_harness_reaper_unix.go`,
  `service_stale_harness_reaper_windows.go`.

Anything outside this allowlist is drift and should fail the root-facade audit.

### Public facade

These files define the public contract, or black-box tests that intentionally
exercise the root import path.

- Mixed root contract files that stay public but should be split before any
  implementation move:
  `service.go`, `service_events.go`, `service_session_projection.go`,
  `service_capabilities.go`, `service_override.go`,
  `service_routing_quality.go`, `service_aliveness.go`, `service_probe.go`,
  `service_catalog_cache.go`, `routing_errors.go`, `role_correlation.go`.
- Root contract and release/doc tests that should remain at the module root:
  `contract003_cache_cost_doc_test.go`,
  `contract003_empty_final_text_doc_test.go`,
  `release_artifact_names_test.go`, `routing_errors_example_test.go`,
  `service_adr005_test.go`, `service_cachepolicy_test.go`,
  `service_contract_post_refactor_structural_test.go`,
  `service_contract_pre_refactor_baseline_test.go`,
  `service_events_test.go`, `service_execute_test.go`,
  `service_final_usage_zero_test.go`,
  `service_harness_dispatch_test.go`, `service_new_test.go`,
  `service_override_test.go`, `service_policies_test.go`,
  `service_power_bounds_test.go`, `service_progress_test.go`,
  `service_public_api_smoke_test.go`,
  `service_role_correlation_test.go`, `service_taillog_test.go`,
  `service_test.go`, `service_testmain_test.go`,
  `service_validation_test.go`, `testseam_test.go`,
  `no_viable_provider_for_now_test.go`.

### Public compatibility alias/shim

These files are intentionally thin wrappers, alias surfaces, or build-tag
compatibility seams that keep the root package stable while implementation
resides elsewhere.

- Source files:
  `public_api.go`, `public_cli_api.go`, `provider_quota_state.go`,
  `provider_burn_rate.go`, `options_prod.go`, `options_testseam.go`,
  `service_execute_seam_prod.go`, `service_execute_seam_testseam.go`,
  `testseam_types.go`.
- Compatibility tests:
  `public_api_harnesses_test.go`, `provider_quota_compat_test.go`.

### Implementation-owned

#### Owner: `internal/serviceimpl`

These files implement concrete service construction, dispatch, provider/model
listing, status assembly, and service-local helpers. They should not remain in
the public root once the split is complete.

- Source files:
  `metadata_billing.go`, `service_execute.go`,
  `service_execute_dispatch.go`, `service_harness_instances.go`,
  `service_model_resolution.go`, `service_models.go`,
  `service_native_provider.go`, `service_policies.go`,
  `service_projection.go`, `service_providers.go`,
  `service_reasoning.go`, `service_refresh_scheduler.go`,
  `service_routing.go`, `service_snapshot.go`, `service_status.go`,
  `service_stale_harness_reaper.go`,
  `service_stale_harness_reaper_unix.go`,
  `service_stale_harness_reaper_windows.go`.
- Tests and support files:
  `harness_golden_integration_test.go`,
  `service_catalog_cache_test.go`,
  `service_contract_snapshot_test.go`,
  `service_execute_dispatch_test.go`,
  `service_execute_harness_pin_test.go`,
  `service_http_provider_test.go`,
  `service_model_resolution_test.go`,
  `service_models_integration_test.go`, `service_models_test.go`,
  `service_projection_test.go`, `service_providers_test.go`,
  `service_refresh_scheduler_test.go`,
  `service_snapshot_autorouting_test.go`, `service_snapshot_test.go`,
  `service_stale_harness_reaper_unix_test.go`, `service_status_test.go`,
  `service_test_helpers_test.go`.

#### Owner: `internal/transcript` and `internal/session`

These files own Fizeau-authored progress text, tool/result pairing,
session-log persistence, and replay/tail rendering.

- Source files:
  `service_progress.go`, `service_session_log.go`, `service_taillog.go`.
- Tests:
  `service_progress_internal_test.go`,
  `service_session_log_internal_test.go`,
  `service_subprocess_progress_test.go`.

#### Owner: `internal/routehealth`

These files own sticky-lease state, route-attempt feedback, cooldowns, and
route-status assembly. `service_routing.go` stays listed under
`internal/serviceimpl` because it is the public entrypoint, but its health and
cooldown helpers should drain into this package during the split.

- Source files:
  `service_route_attempts.go`, `service_route_leases.go`,
  `service_routestatus.go`.
- Tests:
  `service_aliveness_test.go`, `service_route_attempts_test.go`,
  `service_route_evidence_test.go`, `service_route_leases_test.go`,
  `service_routestatus_test.go`, `service_routing_errors_test.go`,
  `service_routing_snapshot_health_test.go`, `service_routing_test.go`.

#### Owner: `internal/quota`

These files own provider quota state, recovery probing, and subscription
quota math beyond the thin public wrappers kept at the root.

- Source files:
  `service_subscription_quota.go`.
- Tests and support files:
  `claude_quota_test_helpers_test.go`, `quota_header_observer_test.go`,
  `service_probe_test.go`, `service_subscription_quota_test.go`.

#### Owner: `internal/routingquality`

The public metric structs stay rooted in `service_routing_quality.go`, but the
aggregators, store records, and override-outcome bookkeeping should move here.

- Tests and support files:
  `service_override_internal_test.go`,
  `service_routing_quality_test.go`,
  `service_usage_routing_quality_test.go`.

## Ordered extraction slices

The parent bead should be broken into these smaller execution slices, in this
order.

| Order | Slice | Files/scope | Target internal owners |
|---|---|---|---|
| 1 | Thin the mixed root facade | Split `service.go`, `service_events.go`, `service_session_projection.go`, `service_override.go`, `service_routing_quality.go`, `service_catalog_cache.go`, `service_aliveness.go`, `service_probe.go`, `service_capabilities.go`, `routing_errors.go`, and `role_correlation.go` into public contract declarations plus private adapters/helpers. No consumer import changes yet. | `internal/serviceimpl`, `internal/transcript`, `internal/routehealth`, `internal/quota`, `internal/routingquality` |
| 2 | Move execute/runtime mechanics | Move `metadata_billing.go`, `service_execute.go`, `service_execute_dispatch.go`, `service_harness_instances.go`, `service_model_resolution.go`, `service_models.go`, `service_native_provider.go`, `service_policies.go`, `service_projection.go`, `service_providers.go`, `service_reasoning.go`, `service_refresh_scheduler.go`, `service_routing.go`, `service_snapshot.go`, `service_status.go`, and `service_stale_harness_reaper*.go`, along with their white-box tests. | `internal/serviceimpl` |
| 3 | Move transcript and session-log ownership | Move `service_progress.go`, `service_session_log.go`, `service_taillog.go`, and transcript/session white-box tests so progress rendering and replay stay Fizeau-owned but no longer live in the root package. | `internal/transcript`, `internal/session` |
| 4 | Move route-health, quota, and routing-quality state | Move `service_route_attempts.go`, `service_route_leases.go`, `service_routestatus.go`, `service_subscription_quota.go`, quota helpers/tests, and routing-quality helpers/tests. Drain the routehealth-specific helper code out of `service_routing.go` during the same sequence. | `internal/routehealth`, `internal/quota`, `internal/routingquality` |
| 5 | Root cleanup and contract lock | After slices 1-4 land, keep only the root facade, the intentional shims, and black-box contract tests at the module root. Regenerate the embedding docs and re-run the CLI/service boundary checks before touching downstream DDx work. | Existing owners above; no new public package |

## Non-goals for the parent bead

- Do not chase "zero root `.go` files" on this module.
- Do not remove the root public package.
- Do not rewrite `agentcli/`, `cmd/bench/`, or generated embedding docs to a
  different import path unless a separate design explicitly replaces the root
  facade.
