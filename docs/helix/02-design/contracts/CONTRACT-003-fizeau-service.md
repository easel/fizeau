---
ddx:
  id: CONTRACT-003
  depends_on:
    - helix.prd
    - ADR-006
    - ADR-008
    - ADR-009
  review:
    self_hash: 14c07663bf82781f011226995ad21dc91db82763e3dd4defa1b92dc8a4d1679e
    deps:
      ADR-006: 70e1de266a6e8c6289f23c05e36bc2fed2af4dc8ad131d352e40876dc46f6793
      ADR-008: 3f36c9ae5997a72d2575876d739d110a7dd6950456a517695ed0d0cd8e118db3
      ADR-009: d9968b4818b0f45508f3e0689b403ff6997c2722924e7457605bc43080ae5a4a
      helix.prd: aac943d5a9d416aafbadb68c4740707e9fa40a31833766e060a20cb9b8f2bd77
    reviewed_at: "2026-07-19T22:52:02Z"
---
# CONTRACT-003: FizeauService Service Interface

**Status:** Draft
**Contract Version:** v0.15 (next product API)
**Owner:** Fizeau maintainers
**Replaces:** CONTRACT-002-ddx-harness-interface

## Purpose

This contract defines the public Go surface of the Fizeau module. The root
package `fizeau` is the facade: service construction, request and response
types, routing/status projections, session-log projections, and public errors.
Concrete execution, transcript rendering, provider adapters, quota state,
routing quality, and catalog mechanics remain behind `internal/`.

Consumers such as DDx, the standalone `fiz` CLI, and future embedders interact
through this surface only. They do not import internal packages and they do not
parse private session-log records when a public projection exists.

ADR-009 owns the v0.11 routing vocabulary: callers express routing intent with
`Policy`, `MinPower`, and `MaxPower`; they express hard overrides with
`Harness`, `Provider`, and `Model`.

The routing entrypoint is conceptually
`route(caller_intent, catalog_and_live_inventory)`. Caller intent is limited to
policy and numeric power intent, hard pins, and explicit execution constraints.
Fizeau owns the catalog and its cached, refreshable live inventory; `fiz models`
and the inventory APIs expose that evidence for inspection. Fizeau does not
require a daemon for correctness. A long-running consumer may keep inventory
fresh, but freshness maintenance is not a second routing authority.

## Scope and Boundaries

- **In scope:** the public root-package service facade; routing and inventory
  projections; execution, continuation, session events, terminal facts,
  session-log projections, and wrapped-harness lifecycle ownership.
- **Experimental, not a v0.15 release gate:** route-neutral portable-runtime
  preparation and activation. The currently shipped additive symbols remain
  documented to avoid misdescribing the code, but their completion,
  compatibility promises, OCI evidence, and Linux isolation mechanics are not
  prerequisites for the core product release.
- **Experimental, not a v0.15 release gate:** the currently compiled
  `Continue` method and its continuation types. FR-1 through FR-8 require
  neither conversation continuation nor harness-native resume. This material
  remains implementation reference for experimental callers pending a separate
  versioned API refactor; it creates no v0.15 compatibility, conformance, or
  lifecycle-expansion commitment.
- **Out of scope:** concrete adapter implementation, provider-native protocols,
  DDx worktree setup, gates, review, landing, preservation, tracker mutation,
  and bead closure policy.
- **Owning system:** Fizeau owns each accepted service invocation through
  terminal cleanup. The caller owns task semantics and every workflow decision
  outside that invocation.

### v0.15 routing boundary (FR-4 and FR-5)

The v0.15 routing promise is intentionally narrow and caller-visible:

- Fizeau combines its catalog with live inventory, applies the caller's pins
  and policy intent, selects and attributes one dispatch route, and records the
  route identity and execution measurements needed for replay.
- One `Execute` invocation dispatches one selected route. A transport retry may
  repeat that route when the execution contract permits it; it does not become
  cross-route failover. A caller that interprets an answer, a task failure, or
  a business outcome owns semantic retry, escalation, and any later request.
- Health, quota, capacity, utilization, cost, and similar signals are internal
  evidence. They may affect eligibility only when dispatch would otherwise be
  incorrect, and they may be projected when needed to preserve an FR-5
  measurement fact such as route attribution, known-versus-unknown cost, or a
  capacity rejection. They are not a new caller policy language or a promise of
  autonomous multi-route recovery.

New quota, health, capacity, scoring, or policy elaboration is outside v0.15
unless it is required to prevent an incorrect dispatch or to preserve one of
those replayable measurement facts. Existing public projections remain for
compatibility and inspection; they do not widen this product boundary.

## Normative Surface

The Go declarations, enum values, field rules, error mappings, process-lifecycle
requirements, compatibility rules, and conformance obligations in this document
are normative. Examples are illustrative but use only normative values.

## Interface

```go
package fizeau

import (
    "context"
    "errors"
    "io"
    "time"
)

type FizeauService interface {
    Execute(ctx context.Context, req ServiceExecuteRequest) (<-chan ServiceEvent, error)
    Continue(ctx context.Context, req ServiceContinuationRequest) (<-chan ServiceEvent, error)
    PreparePortableRuntime(ctx context.Context, req PortableRuntimeRequest) (*PortableRuntimeBundle, error)
    TailSessionLog(ctx context.Context, sessionID string) (<-chan ServiceEvent, error)

    ListHarnesses(ctx context.Context) ([]HarnessInfo, error)
    ListProviders(ctx context.Context) ([]ProviderInfo, error)
    ListModels(ctx context.Context, filter ModelFilter) ([]ModelInfo, error)
    RefreshModels(ctx context.Context, opts ModelRefreshOptions) (*ModelSnapshotInfo, error)
    ListPolicies(ctx context.Context) ([]PolicyInfo, error)

    HealthCheck(ctx context.Context, target HealthTarget) error
    ResolveRoute(ctx context.Context, req RouteRequest) (*RouteDecision, error)
    RecordRouteAttempt(ctx context.Context, attempt RouteAttempt) error
    RouteStatus(ctx context.Context) (*RouteStatusReport, error)

    UsageReport(ctx context.Context, opts UsageReportOptions) (*UsageReport, error)
    ListSessionLogs(ctx context.Context) ([]SessionLogEntry, error)
    WriteSessionLog(ctx context.Context, sessionID string, w io.Writer) error
    ReplaySession(ctx context.Context, sessionID string, w io.Writer) error
}

func New(opts ServiceOptions) (FizeauService, error)
func NewFromPortableRuntime(opts ServiceOptions) (FizeauService, error)
func ValidateUsageSince(spec string) error
func ValidateCachePolicy(v string) error
func ValidatePowerBounds(minPower, maxPower int) error
```

Seventeen service methods are currently compiled. `Execute` is the primary v0.15
verb. `Continue` is an experimental harness-neutral conversation-continuation
surface retained as implementation reference, not a v0.15 product promise. The
list methods expose the live routing inventory and policy metadata. `HealthCheck`,
`ResolveRoute`, `RecordRouteAttempt`, and `RouteStatus` are routing/status
projections. The remaining methods project service-owned session logs for
usage, listing, JSON rendering, and replay. `PreparePortableRuntime` is an
experimental route-neutral preparation boundary for an embedding caller that
will execute the configured service inside an isolated same-platform runtime.
`NewFromPortableRuntime` is its matching experimental constructor. Neither
expands the v0.15 product requirements or release gates; ADR-014 owns any
future promotion decision.

The v0.11 interface has no removed route-introspection service methods and no
separate model reference request field. Old route-reference names are not
compatibility fallbacks for the new policy surface.

## Construction

```go
type ProviderAlivenessProber func(ctx context.Context, provider, baseURL string) bool

type ServiceOptions struct {
    ConfigPath string
    Logger io.Writer
    ServiceConfig ServiceConfig

    QuotaRefreshDebounce time.Duration
    QuotaRefreshStartupWait time.Duration
    QuotaRefreshInterval time.Duration
    QuotaRefreshContext context.Context

    CatalogProbeTimeout time.Duration
    CatalogReloadTimeout time.Duration

    LocalCostUSDPer1kTokens float64
    SubscriptionCostCurve *SubscriptionCostCurve
    SessionLogDir string
    HarnessCleanupTimeout time.Duration
    StaleHarnessReaperGrace time.Duration

    HealthProbeInterval time.Duration
    HealthSignalTTL time.Duration
    PersistRouteHealth string
    AlivenessProber ProviderAlivenessProber
}

type ServiceConfig interface {
    ProviderNames() []string
    DefaultProviderName() string
    Provider(name string) (ServiceProviderEntry, bool)
    HealthCooldown() time.Duration
    WorkDir() string
    SessionLogDir() string
}

type ServiceProviderEntry struct {
    Type string
    BaseURL string
    ServerInstance string
    Endpoints []ServiceProviderEndpoint
    APIKey string
    Headers map[string]string
    Model string
    Billing BillingModel
    IncludeByDefault bool
    IncludeByDefaultSet bool
    ContextWindow int
    ConfigError string
    DailyTokenBudget int
    CreditBalanceThresholdUSD float64
    CreditProbeTTL time.Duration
}
```

The effective service session-log directory is resolved once from
`ServiceOptions.SessionLogDir`, then `ServiceConfig.SessionLogDir()`. It is also
the stable base for service-private continuation locator state; per-request
`ServiceExecuteRequest.SessionLogDir` overrides never change that base. When
the effective service directory is empty, ordinary execution remains valid but
completed-session lookup across hub eviction or restart is unavailable. When
it is non-empty, service construction MUST create or validate the private state
subdirectory with owner-only permissions and fail if that durable location
cannot be made usable. CONTRACT-004 defines its layout and crash recovery.

The service may auto-load configuration when `ServiceConfig` is nil and the
config package registered a loader. Embedders that need deterministic behavior
should pass `ServiceConfig` explicitly.

## Portable Runtime Preparation and Activation

> **Experimental / deferred track.** This section describes an additive
> implementation already present in the repository. It is not a v0.15 release
> requirement, does not gate the public service/CLI release, and must not be
> used to add Linux namespace, mount, PID-1, or OCI obligations to core Fizeau.
> A future proposal must establish a customer-facing requirement and a bounded
> release target before this track can become normative.

An embedding caller can ask its already-configured service to prepare the
complete runtime needed for a later unpinned `Execute` inside an isolated
runtime. Preparation is not routing and does not accept routing intent.

```go
type PortableRuntimeRequest struct {
    DestinationRoot string
    TargetGOOS      string
    TargetGOARCH    string
}

type PortableRuntimeMount struct {
    Source   string
    Target   string
    ReadOnly bool
}

type PortableRuntimeBundle struct {
    // implementation state is unexported
}

var (
    ErrPortableRuntimeRequestInvalid    = errors.New("invalid portable runtime request")
    ErrPortableRuntimeClosureIncomplete = errors.New("portable runtime closure incomplete")
    ErrPortableRuntimeActivationInvalid = errors.New("portable runtime activation invalid")
    ErrPortableRuntimeCleanupIncomplete = errors.New("portable runtime cleanup incomplete")
)

func (b *PortableRuntimeBundle) RuntimeRoot() string
func (b *PortableRuntimeBundle) Mounts() []PortableRuntimeMount
func (b *PortableRuntimeBundle) EnvironmentNames() []string
func (b *PortableRuntimeBundle) Close() error
func PortableRuntimeGuestRoot() string
```

`PortableRuntimeRequest` has exactly the three fields above. It MUST NOT gain a
`Harness`, `Provider`, `Model`, `Policy`, power bound, capability preference,
or other route selector. Preparation MUST NOT resolve a route, create a
session, emit a service event, contact a provider, or start a harness process.
The later `Execute` call remains the only routing decision.

`DestinationRoot` names an existing, caller-owned, empty real directory. A
missing path, non-directory, non-empty directory, symbolic-link destination,
or destination whose ancestry cannot be validated returns an error wrapping
`ErrPortableRuntimeRequestInvalid`. `TargetGOOS` and `TargetGOARCH` are required.
For the experimental implementation, `TargetGOOS` MUST equal both `runtime.GOOS` and `linux`, and
`TargetGOARCH` MUST equal `runtime.GOARCH`. A non-Linux preparing process or any
cross-target request fails before materialization. Darwin and Windows portable
activation require later platform-specific evidence; ordinary Fizeau execution
on those platforms is unaffected.

### Host and guest layout

Fizeau commits exactly one child named `runtime` beneath `DestinationRoot`.
`RuntimeRoot()` returns that child's clean absolute host path. The implementation
creates a private sibling staging directory on the same filesystem, validates
the caller directory by directory handle and identity, then performs exactly
one no-replace rename of the staged tree to `<DestinationRoot>/runtime`. A
concurrent preparer may win that name; every loser removes its staging tree,
returns `ErrPortableRuntimeRequestInvalid`, and leaves the winner untouched.
Failed preparation leaves `DestinationRoot` empty unless another preparer won.

In v0.15, `Mounts()` returns exactly one mount. Its `Source` equals
`RuntimeRoot()`, its `Target` equals the clean absolute guest path
returned by `PortableRuntimeGuestRoot()`, and `ReadOnly` is always `true`.
The function returns `/opt/fizeau/runtime` on Linux and the empty string on
unsupported platforms in v0.15. Preparation and activation reject an empty
guest root. Original discovered
harness/provider paths never appear in that public mount. The caller-supplied
destination path necessarily appears in `RuntimeRoot` and `Source`; that is not
an internal source-path disclosure. Every executable, state seed, and generated
manifest path is relative to the private mounted tree and is interpreted only
by Fizeau after activation.

The bundle is an opaque pointer with generic container-applicable data. Its
accessors return defensive copies. `EnvironmentNames()` is a stably sorted,
deduplicated list of valid non-empty environment variable names that the
external runtime must inherit by name. It never contains a value, an empty
assignment, or a `name=value` string. Preparation rejects an invalid name or a
required inherited name that is unset; empty and unset are distinct. The caller
MUST serialize environment mutation from preparation through runtime start and
copy the current value for each returned name opaquely, without interpreting,
logging, or persisting it. An orchestrator may perform an opaque lookup because
most OCI APIs require `name=value` input. Fizeau-owned path variables and
generated defaults such as portable HOME/PATH/XDG roots, TERM, and locale are
not public inherited names; activation derives them inside the guest.

### Complete route-neutral inventory

Preparation covers the complete structural runtime, not a speculative winning
route and not a snapshot of live health. The authoritative subprocess set is
the stable join of the production registry and the registered runner instances
used by this service. It includes every non-test, structurally
unpinned-capable subprocess instance whose executable is installed at
preparation entry. It classifies actual transports, so a configured native or
HTTP-backed Claude instance is not mislabeled as a subprocess merely because a
static registry row names a binary. Exact-pin-only and test-only instances are
recorded as excluded; they do not become unpinned-capable through preparation.

The inventory also preserves every effective configured provider instance,
including instances that are pinned-only or currently unhealthy, so the
in-runtime service receives the same configuration rather than a route-filtered
subset. Structural candidate identity and inclusion policy must match after
activation. Provider reachability, auth freshness, quota, and health are
evaluated later inside `Execute` and may legitimately differ in the isolated
runtime; preparation makes no promise of live eligibility parity.

Subprocess surfaces contribute a verified same-target executable/install
closure, including required interpreters, package trees, dynamic loaders,
shared libraries, and runtime support. A PATH launcher, final symlink target,
or GOOS/GOARCH label by itself is not a verified closure. CONTRACT-004 defines
the closure classes, content identities, registry classifications, and
contributor conformance. If a structurally included subprocess has an unknown
layout or incomplete closure, preparation returns an error wrapping
`ErrPortableRuntimeClosureIncomplete`; it never silently narrows the later
candidate set.

The generated private manifest records structural candidate identities,
entrypoints relative to `PortableRuntimeGuestRoot()`, environment requirements,
and a field-exhaustive effective provider projection. Every bundle with a
structurally included subprocess also includes exactly one service-owned,
content-addressed namespace launcher. The launcher is a target-specific,
statically linked, single-threaded artifact built from versioned Fizeau Zig
source and embedded in the module for the selected GOOS/GOARCH; preparation
copies those embedded bytes and verifies their compile-time digest rather than
discovering a host executable. It is a generic activation dependency, not a
harness contribution, and is invoked only at the fixed private target
`.fizeau/namespace-launcher` beneath `PortableRuntimeGuestRoot()`, never through
ambient `PATH`. Harness contributions may not claim that target or any exact,
ancestor, or descendant overlap. A target without an embedded verified launcher
fails as an incomplete closure.

The checked source is `internal/portableruntime/nslauncher/main.zig`.
`make portable-namespace-launcher-check` requires Zig 0.16.0, invokes
`scripts/generate-portable-namespace-launcher.sh --check` for both
`x86_64-linux-musl`/`linux-amd64` and
`aarch64-linux-musl`/`linux-arm64`, and fails unless its `-O ReleaseSmall`,
`-static`, `-fstrip`, `-fsingle-threaded`, and no-build-id outputs equal every
checked-in artifact and generated SHA-256 digest. The corresponding `--write`
mode is the only update path. Ordinary tests verify checked bytes, digest, ELF
shape, static linkage, and the absence of any second thread before authority
drop. The required `portable-namespace-launcher` job in
`.github/workflows/ci.yml` runs the check command; the release gate runs it
again before packaging.

`NewFromPortableRuntime` is the only public activation entrypoint. It reads
that manifest from the fixed guest root, verifies its version and content
identities, reconstructs the service config and the production
execution-dispatch launch mapping, creates owner-only guest-private writable
backing storage for unprojected prefix-preserving credential, quota, and cache
state seeds, assembles projection-consumed mutable seeds at their native
targets, and compiles the generic per-entrypoint namespace recipe defined by
CONTRACT-004. The constructor supplies Fizeau-owned path and locale variables
to harness launches but does not enter a namespace or start a process. The
namespace recipe becomes effective atomically in the production spawn path:
the verified service-owned launcher creates the child user and mount
namespaces, applies the projection boundaries, removes setup capabilities,
sets `no_new_privs`, and only then executes the governed harness command as the
mapped non-root UID. A
scheduler-only refresh map is insufficient: the later `Execute` must consume the
activated launch recipe. Activation does not depend on the
application-only `internal/config` loader. That overlay belongs to the external
runtime namespace, never writes back into the read-only bundle, and must
disappear when the caller destroys that runtime's writable storage.

`NewFromPortableRuntime` rejects a non-empty `ServiceOptions.ConfigPath` or a
non-nil `ServiceOptions.ServiceConfig`, ignores host configuration override
variables, and returns an error wrapping
`ErrPortableRuntimeActivationInvalid` for a missing, malformed, mismatched, or
tampered manifest. Before constructing service state it calls `LookupEnv` for
every manifest-required inherited name: missing is invalid, while present-empty
remains distinct and valid. An omitted name wraps
`ErrPortableRuntimeActivationInvalid` before any service activity. Other service
options retain their documented meaning. Activation starts no session, resolves
no route, contacts no provider, and starts no harness. A separately built
public-package consumer can therefore construct the in-runtime service without
importing an internal package or understanding a harness/provider path.

The activation process's current effective UID and primary GID are the mapping
authority. Both MUST be nonzero and its supplementary-group list MUST be empty;
`NewFromPortableRuntime` validates those facts without starting a process before
constructing service state. The external runtime MUST give that identity read
and execute access to the owner-only mounted tree without broadening its modes.
The required Linux OCI jobs run the consumer as exact UID/GID `65532:65532`
with no supplementary groups. Running the consumer as container root, with
primary group zero, or with any supplementary group is invalid activation and
not accepted evidence.
The outer runtime MUST also permit the verified namespace launcher to create
the nested user, mount, and PID namespaces needed for a child invocation.
Activation never requests a privileged outer container or a host
`CAP_SYS_ADMIN`; an unavailable nested-user-namespace facility fails closed
before the harness executable starts. The v0.15 Linux floor includes
`open_tree(2)`, `move_mount(2)`, and recursive `mount_setattr(2)` support.

The non-skipping workflow jobs `portable-runtime-oci-amd64` and
`portable-runtime-oci-arm64` run natively on GitHub's `ubuntu-24.04` and
`ubuntu-24.04-arm` labels. `make test-portable-runtime-oci` invokes
`scripts/test-portable-runtime-oci.sh`, verifies the pinned Docker Engine/CLI
29.6.2 and runc version/checksums recorded in
`scripts/toolchains/portable-runtime.env`, rejects a kernel older than 6.12,
and runs live feature preflights for user/mount/PID namespaces, seccomp filter,
`open_tree`, `move_mount`, and recursive `mount_setattr`. It builds a checked
`FROM scratch` fixture without network or image pulls, then invokes it with
`--user 65532:65532`, `--cap-drop=ALL`, `--network=none`, `--read-only`, exact
read-only bundle plus writable activation mounts, and only
`seccomp=unconfined`/`apparmor=unconfined` outer security options so the inner
launcher can install its stricter policy. The script rejects `--privileged`,
`--cap-add`, an architecture mismatch, a non-empty `Groups:` line, skipped
tests, or any failed feature probe.

### Materialization, secrecy, and cleanup

Asset ordering and deduplication are deterministic. Identical normalized
asset identities deduplicate; conflicting identities for one private target
are an error. All materialized paths remain beneath the committed host runtime
root. Directory traversal, copied symlinks, preserved hard links, source
identity or content changes during copy, and undeclared file types are
rejected. Fizeau never copies live process-lifecycle records or service session
logs into the bundle.

Runtime directories use mode `0700`. Credential, generated config, quota, and
cache regular files use mode `0600`; those classes are sensitive regardless of
contributor flags. Executable closures retain only required owner execution
bits and are never group/world writable. All public mounts are read-only.
Activation copies unprojected credential, quota, and cache seeds by their
`data/`, `state/`, or `cache/` target prefix to guest-private writable overlays
rather than modifying the mounted bundle. A projection-consumed seed does not
also take that prefix-preserving path; activation maps it with exact referenced
config assets into the declared activation-owned native directory. Projected
configuration remains immutable under an enforcement boundary that prevents
the harness UID from writing, unlinking, renaming, replacing, or shadowing it
while permitting credential refresh, declared lock creation, and generated
siblings. Ordinary mode bits on a config file inside a writable parent do not
satisfy this contract. This placement is generic and target-driven: an
unprojected state asset such as `data/opencode/auth.json` becomes `auth.json`
beneath the generated OpenCode data directory, while a projected Gemini or Pi
seed uses its exact projection entry. Validation failure, copy failure,
context cancellation, or commit failure removes every staging artifact.

Activation persists no ambient namespace-helper path. Its private recipe names
only the verified launcher closure under `PortableRuntimeGuestRoot()`, generated
scope and projection targets, and immutable runtime asset targets. Every
identity-bearing activation directory or mountpoint and each required-absent
parent is descriptor-pinned; mutable credential, quota, and cache leaves remain
replaceable. Otherwise the harness could rename an ancestor and shadow the
protected descendant. The launcher first makes mount propagation recursively
private, uses descriptor-relative `open_tree`, `move_mount`, and
`mount_setattr` operations rather than path re-resolution, and read-only binds
each projected config member last.

The canonical spawn seam atomically creates the single-threaded launcher with
`CLONE_NEWUSER|CLONE_NEWNS|CLONE_NEWPID`, gates it while the parent writes the
single-ID setup maps `0 <activation-euid> 1` and
`0 <activation-egid> 1` (`setgroups=deny` before `gid_map`), then releases it as
namespace UID/GID 0. Parent and child both verify the activation process's
supplementary-group list was empty; mapping or gate failure releases no harness
executable. The same `internal/processlifecycle`-owned direct child remains the
lifecycle target, outer session leader, and target process-group leader while
becoming PID 1 inside the new PID namespace. It mounts a private `/proc`, makes
mount propagation private, applies the descriptor-pinned recipe, and remains
the only owner of lifecycle control, gate, mount, recipe, and pidfd descriptors.

PID 1 then clones its second stage into a nested single-ID user namespace whose
maps are `<activation-euid> 0 1` and `<activation-egid> 0 1`. The nested
namespace inherits and verifies the permanent `setgroups=deny` state and the
empty supplementary-group list. Before releasing that stage, both launcher
processes prove `/proc/<pid>/task` contains exactly one thread. PID 1 sets
`PR_SET_DUMPABLE=0`, then sets and locks the restrictive securebits word:
`NOROOT`, `NO_SETUID_FIXUP`, `KEEP_CAPS` off, and `NO_CAP_AMBIENT_RAISE`, with
each corresponding lock bit. Only then does it clear every inheritable,
effective, permitted, bounding, and ambient capability, set `no_new_privs`,
install its persistent post-setup seccomp filter, and close setup descriptors.
Its filter rejects all later thread/namespace/mount creation or mutation. The second stage
independently performs the same single-threaded authority drop, retaining only
file descriptors 0, 1, and 2 after exact batch-pipe or PTY-slave duplication;
it never inherits the lifecycle channel, gate, recipe, mount descriptors, or
pidfds. It closes its sealed recipe input before harness exec and installs a
persistent filter that rejects `unshare`, `setns`, mount mutation, `ptrace`,
`process_vm_readv`, `process_vm_writev`, `pidfd_getfd`, and `clone` with
`CLONE_NEWUSER`. `clone3` returns `ENOSYS` so ordinary threading can fall back
to permitted non-namespace `clone`. The filter also returns `EPERM` for
`kill(1, ...)`, `kill(-1, ...)`, `tkill(1, ...)`, any `tgkill` whose thread
group or thread ID is 1, `rt_sigqueueinfo` whose target is 1 or -1, any
`rt_tgsigqueueinfo` whose thread group or thread ID is 1, `pidfd_open(1, ...)`,
and every `pidfd_send_signal`. This target-aware rule leaves ordinary
harness-child signalling available but prevents the untrusted harness from
driving its trusted PID-1 supervisor. All prohibited operations return
`EPERM`.

The harness stage enters a distinct process group. PID 1 alone receives
supervisor-originated cleanup signals sent to the lifecycle target group and
bridges each to the harness group, preventing direct-plus-forwarded duplicates.
For PTY execution PID 1 owns the controlling terminal and transfers foreground
ownership to the harness group, so terminal job-control signals reach that
foreground group directly. PID 1 reaps the entire namespace, waits for terminal
descendant cleanup, and only then mirrors the primary harness child's exact
`WaitStatus`; caller-death and cancellation use the same bridge. It passes the
closed environment through the child environment rather than command-line
assignments. Namespace setup or authority-drop failure is fail-closed and no
harness command starts. All portable subprocess launches sharing one activation
root use the single exclusive subprocess lease defined by CONTRACT-004 from
pre-spawn revalidation through terminal descendant cleanup.

Ownership has three non-overlapping layers. `PortableRuntimeBundle` owns only
the prepared host `runtime` child and its staging remnants. The external
runtime owns the successful activation backing root and destroys it only after
stopping the service runtime. Each launcher invocation owns only its ephemeral
user, mount, and PID namespaces, which disappear after its supervised process
tree exits. Activation failure removes its partial backing-storage staging;
successful activation and namespace teardown never write back to or remove the
read-only bundle.

The caller applies the one mount and inherited environment names verbatim
without interpreting provider or harness semantics, then calls
`NewFromPortableRuntime` inside the isolated process. The caller owns external
container or sandbox orchestration and MUST stop the runtime and destroy its
writable storage before calling `Close`. If either external cleanup step fails,
the caller MUST retain both the runtime/storage cleanup handle and the bundle,
retry those steps in that order, and MUST NOT call `Close` until runtime stop
and writable-storage destruction are confirmed. `Close` removes only Fizeau's
committed child and staging remnants; it does not remove `DestinationRoot` or
stop an external runtime. It is
concurrency-safe and idempotent after success. A cleanup failure wraps
`ErrPortableRuntimeCleanupIncomplete`; a later `Close` retries remaining
cleanup rather than permanently suppressing it.

The zero value of `PortableRuntimeBundle` is a closed empty bundle: its
accessors return empty values and `Close` succeeds. This permits external
interface mocks without exposing constructors for non-empty bundle state.
Successful non-empty bundles are created only by Fizeau preparation.

No returned error, JSON representation, `String` method, logger output, or
generic plan value may contain credential contents, API keys, header values,
environment values, or original harness/provider source paths. Diagnostics
identify a stable asset class and operation, not secret data or a concrete
home path. Caller-supplied `DestinationRoot`-derived values are allowed only in
`RuntimeRoot`, mount `Source`, and errors that directly validate that argument.
This is API opacity and accidental-disclosure prevention, not confidentiality
from the embedding caller: that caller already owns the source environment and
destination directory and can read their bytes. The contract ensures caller
code need not discover, interpret, log, or persist concrete asset semantics; it
does not claim filesystem modes can defend those assets from their owner.
Wrapped processes started by the later in-runtime `Execute` remain subject to
the independent containment and caller-death rules in this contract;
portable-bundle cleanup does not replace them.

`HarnessCleanupTimeout` is the v0.15 per-invocation deadline for stopping and
reaping a wrapped harness containment boundary after normal completion,
failure, timeout, cancellation, stream abandonment, or caller death. Zero uses
10 seconds. Negative values are invalid. Execution and provider timeouts may
trigger cleanup, but they do not shorten this service-owned cleanup deadline.

`StaleHarnessReaperGrace` is not a per-invocation cleanup timeout. It is the
minimum age of a persisted non-terminal harness record before a later service
startup may treat that record as stale and attempt recovery. Zero uses five
minutes. The two options MUST remain semantically independent.

`ProviderAlivenessProber` reports endpoint reachability: `true` means
reachable and `false` means unreachable. Implementations MUST respect `ctx`
cancellation. `HealthProbeInterval` controls background probes; a non-positive
value uses 60 seconds. `HealthSignalTTL` is the maximum age of probe evidence
used for routing and persisted-snapshot loading; a non-positive value uses 10
minutes. Empty `PersistRouteHealth` disables route-attempt and probe-state
persistence; a non-empty value is the persistence file path. A nil
`AlivenessProber` uses the default TCP-connect prober.

`IncludeByDefault` controls unpinned automatic routing participation. For
pay-per-token providers, `IncludeByDefault=true` is necessary but not
sufficient: the configuration projection must also reflect explicit
metered-spend opt-in, such as `routing.allow_metered: true`, before such a
provider participates in unpinned automatic routing. ServiceConfig
implementations that do not expose a separate metered flag should project this
policy by leaving pay-per-token providers default-excluded until spend opt-in is
known. Explicit provider/model/harness pins may still consider a provider that
is not included by default or metered-enabled, but pins do not bypass policy
`Require` constraints.

## Execute Request

```go
type ServiceExecuteRequest struct {
    Prompt string
    SystemPrompt string

    Model string
    Provider string
    Harness string
    Policy string
    MinPower int
    MaxPower int

    Reasoning Reasoning
    Permissions string
    EstimatedPromptTokens int
    RequiresTools bool
    CachePolicy string

    Temperature *float32
    TopP *float64
    TopK *int
    MinP *float64
    RepetitionPenalty *float64
    Seed *int64
    SamplingSource string

    WorkDir string
    NoStream bool
    Tools []Tool
    ToolPreset string
    PlanningMode bool

    Timeout time.Duration
    IdleTimeout time.Duration
    ProviderTimeout time.Duration

    MaxIterations int
    MaxTokens int
    ReasoningByteLimit int
    CompactionContextWindow int
    CompactionReserveTokens int
    StallPolicy *StallPolicy

    SessionLogDir string
    SelectedRoute string
    Metadata map[string]string
    Role string
    CorrelationID string
}
```

`Policy` is the named routing policy. `MinPower` and `MaxPower` are optional
numeric power hints on the catalog's 1..10 scale. `Model`, `Provider`, and
`Harness` are hard pins and are recorded as override signals for routing
quality. A request is unpinned when all three hard-pin fields are empty;
`Policy`, power hints, reasoning, capability flags, and token estimates do not
make a request pinned. `Role` and `CorrelationID` are observational metadata
only; they do not affect candidate eligibility or scoring.

`CachePolicy` accepts `""`, `"default"`, and `"off"`. `Reasoning` is the
single public reasoning control; provider-specific names remain adapter
terminology.

`MaxTokens` and `CompactionContextWindow` MUST be non-negative. A negative
value is a synchronous public validation error: `Execute` returns no event
channel and creates no session. `MaxTokens == 0` preserves the provider-default
output-budget convention. `CompactionContextWindow == 0` preserves the
selected route's context window; a positive value may only tighten that window
as specified below.

`ResolveRoute` and `Execute` are cache-first on the route hot path. They use
the catalog and the freshest available inventory evidence and may request a
coordinated background refresh. They must not synchronously contact local model
providers, block on stale `/v1/models` discovery, or fail the process solely
because one configured local provider is unreachable. A known fact may exclude
a route only when dispatching it would be incorrect; absence of a fact does not
invent a new failure policy. The detailed refresh and scoring mechanics below
are retained implementation and inspection reference, bounded by the FR-4/FR-5
routing boundary above.

### Caller-signalled cancellation and stream abandonment

The caller signals stream abandonment by cancelling the context passed to
`Execute` or `Continue`. Silently ceasing to receive from the returned Go
channel is not observable and is not a cancellation signal. A caller that
stops draining an event stream MUST cancel its context.

Fizeau MUST NOT make cleanup or terminalization depend on consumer receive
progress. It MAY coalesce or drop non-terminal events when necessary to retain
capacity for the terminal fact and stream closure. Context cancellation begins
wrapped-harness cleanup, but cleanup runs under a service-owned context bounded
by `HarnessCleanupTimeout`, not under the cancelled request context.

## Session Continuation

> **Experimental / deferred track.** This section documents the currently
> compiled continuation implementation so experimental callers can understand
> it. It is not a v0.15 normative surface or release gate: canonical FR-1
> through FR-8 do not require continuation, and `FizeauService` at v0.14.50
> did not have `Continue`. Do not add continuation policies, durable formats,
> lifecycle behavior, or conformance requirements under v0.15. A future
> versioned API refactor decides whether this surface is retained, replaced, or
> removed.

Callers request continuation without naming a concrete harness, provider-native
conversation token, subprocess flag, or adapter type. Fizeau resolves the prior
session's continuation capability and owns translation to a supported
subprocess harness. In this contract version, native-provider routes do not
implement the CONTRACT-004 harness capability and therefore follow the
unsupported-route policy behavior; they MUST NOT be wrapped in a subprocess
`Harness` merely to appear resumable.

```go
type ContinuationPolicy string

const (
    ContinuationRequireResume ContinuationPolicy = "require_resume"
    ContinuationPreferResume  ContinuationPolicy = "prefer_resume"
    ContinuationFreshSession  ContinuationPolicy = "fresh_session"
)

type ContinuationDisposition string

const (
    ContinuationResumed ContinuationDisposition = "resumed"
    ContinuationFresh   ContinuationDisposition = "fresh"
)

type ServiceContinuationRequest struct {
    SessionID string
    Prompt string
    Policy ContinuationPolicy
    FreshRequest ServiceExecuteRequest
    Metadata map[string]string
    CorrelationID string
}

var (
    ErrContinuationPolicyInvalid = errors.New("invalid continuation policy")
    ErrContinuationSessionUnavailable = errors.New("continuation session unavailable")
    ErrContinuationUnsupported = errors.New("continuation unsupported")
)
```

`SessionID`, `Prompt`, and `Policy` are required. `SessionID` identifies the
completed Fizeau session whose conversation may be resumed. `FreshRequest`
defines the normal `Execute` request used only when policy permits a fresh
session; Fizeau MUST replace `FreshRequest.Prompt` with `Prompt`. Callers MAY
leave hard pins empty and use normal policy and power intent. No continuation
request requires a harness name or harness-specific resume token.

For `require_resume`, `FreshRequest` MUST be the zero value. For
`prefer_resume` and `fresh_session`, normal `ServiceExecuteRequest` validation
applies to `FreshRequest` after Fizeau replaces its prompt. Outer `Metadata`
and `CorrelationID` override fields with the same meaning in `FreshRequest` and
are recorded on the new session.

Each accepted `Continue` call creates a new Fizeau session ID. Its terminal
projection and service-owned session-start log record MUST carry the parent
`SessionID` and actual `ContinuationDisposition`. A resumed session reuses
harness-owned conversation state behind the public boundary. A
fresh session starts an ordinary `Execute` using `FreshRequest`; it preserves
lineage but MUST NOT claim that provider or harness conversation state was
resumed.

Policy behavior is normative:

| Policy | Supported completed parent route | Valid completed parent whose actual route cannot resume |
|--------|------------------------------------|--------------------------------------------------------|
| `require_resume` | Resume and report `resumed`. | Return `ErrContinuationUnsupported`; do not start a session or spawn a process. |
| `prefer_resume` | Resume and report `resumed`. | Start `FreshRequest` as a new session and report `fresh`. |
| `fresh_session` | Do not probe or invoke resume capability; start `FreshRequest` and report `fresh`. | Same behavior; support is irrelevant. |

An empty or unknown policy returns `ErrContinuationPolicyInvalid` before a
session starts. A missing, unreadable, or incomplete prior session returns
`ErrContinuationSessionUnavailable`; `prefer_resume` does not convert missing
lineage into a fresh session because the caller supplied an invalid parent.
Continuation capability is route-specific and MUST be reported as unsupported
rather than inferred from a harness name. The table applies only after Fizeau
has resolved a valid, completed parent and its actual terminal route; an
unresolved lineage is unavailable for every policy and never falls back to a
fresh session.

For a resume attempt, the service first asks the exact registered route to
prepare continuation. Preparation may validate and bind private durable
evidence but MUST NOT create a child session, acquire a child lifecycle lease,
spawn, or emit events. Only after preparation succeeds does the service create
the child Fizeau session and fresh lifecycle lease and allow the prepared
operation to start. If evidence becomes unusable after successful preparation,
the already-created child fails normally; `prefer_resume` MUST NOT start a
second fresh attempt. CONTRACT-004 owns the internal two-phase interface and
crash ordering.

## Session Lifecycle and Terminal Facts

### Stable terminal outcome, terminal cause, and session stage

While the caller process remains alive, every accepted `Execute` or `Continue`
call produces exactly one terminal service event before its event stream
closes. The terminal projection and `DrainExecuteResult` expose these typed
fields in addition to the legacy `Status` and human-readable `Error` fields:

```go
type SessionOutcome string

const (
    SessionOutcomeSuccess   SessionOutcome = "success"
    SessionOutcomeFailed    SessionOutcome = "failed"
    SessionOutcomeCancelled SessionOutcome = "cancelled"
    SessionOutcomeTimedOut  SessionOutcome = "timed_out"
)

type TerminalCause string

const (
    TerminalCauseCompleted         TerminalCause = "completed"
    TerminalCauseRouteUnavailable TerminalCause = "route_unavailable"
    TerminalCauseSpawnFailed      TerminalCause = "spawn_failed"
    TerminalCauseHarnessFailed    TerminalCause = "harness_failed"
    TerminalCauseProviderFailed   TerminalCause = "provider_failed"
    TerminalCauseToolLoopFailed   TerminalCause = "tool_loop_failed"
    TerminalCauseIterationLimit   TerminalCause = "iteration_limit"
    TerminalCauseBudgetHalted     TerminalCause = "budget_halted"
    TerminalCauseContextCapacityExceeded TerminalCause = "context_capacity_exceeded"
    TerminalCauseDeadlineExceeded TerminalCause = "deadline_exceeded"
    TerminalCauseContextCancelled TerminalCause = "context_cancelled"
    TerminalCauseCallerDied       TerminalCause = "caller_died"
    TerminalCauseCleanupFailed    TerminalCause = "cleanup_failed"
    TerminalCauseInternalError    TerminalCause = "internal_error"
)

type SessionStage string

const (
    SessionStageRouting      SessionStage = "routing"
    SessionStageSpawn        SessionStage = "spawn"
    SessionStageHarness      SessionStage = "harness"
    SessionStageProvider     SessionStage = "provider"
    SessionStageToolLoop     SessionStage = "tool_loop"
    SessionStageTimeout      SessionStage = "timeout"
    SessionStageCancellation SessionStage = "cancellation"
    SessionStageCleanup      SessionStage = "cleanup"
)

type ServiceFinalData struct {
    // Existing public fields omitted.
    Outcome SessionOutcome `json:"outcome"`
    Cause TerminalCause `json:"cause"`
    Stage SessionStage `json:"stage"`
    PrimaryOutcome SessionOutcome `json:"primary_outcome,omitempty"`
    PrimaryCause TerminalCause `json:"primary_cause,omitempty"`
    PrimaryStage SessionStage `json:"primary_stage,omitempty"`
    ParentSessionID string `json:"parent_session_id,omitempty"`
    Continuation ContinuationDisposition `json:"continuation,omitempty"`
    ContextCapacity *ServiceContextCapacityData `json:"context_capacity,omitempty"`
}
```

`ContextCapacity` is required when `Cause` is
`context_capacity_exceeded` and omitted for terminal facts with other causes.
A main provider call rejected by capacity preflight maps to
`failed / context_capacity_exceeded / tool_loop`.

### Executing-surface route-failure evidence

`ServiceFinalData.RoutingActual.FailureClass` is extensible, typed evidence
about a failure observed by the surface that actually executed. Stable values
currently produced by native and wrapped executing surfaces include:

- `credential_invalid` — the executing surface rejected or could not refresh
  credentials, including HTTP 401 evidence. This is distinct from
  `credential_missing`, which is pre-dispatch configuration evidence and is not
  produced by the Claude execution classifier.
- `quota_exhausted` — the executing surface reported a usage, credit-balance,
  or billing limit. This class does not itself promise a `Retry-After` value or
  a reset time.
- `transport` — connection, name-resolution, timeout, or equivalent transport
  failure from the executing surface.
- `protocol` — an infrastructure protocol failure such as a malformed service
  response or non-authentication HTTP error. Model output quality, task
  failure, and other semantic failures are never `protocol`.
- `capability` — the native selected provider/model route deterministically
  could not serve the accepted request. A residual context overflow after no
  effective compaction, or after the one same-route retry justified by measured
  token reduction, uses this class. It is exact-route feedback and never marks
  the endpoint unreachable or creates provider-wide availability cooldown.
- `unknown` — the executing surface failed without evidence sufficient for a
  more specific class.

The generic native-dispatch class `availability` remains a stable value outside
the Claude classifier's output set. Future minor releases may add classes;
consumers preserve unknown values rather than treating this list as a closed
Go enum.

Failure evidence belongs only to the executing surface. A failed Claude batch
process does not declare Claude TUI, the host account, or any other route
failed, and the inverse is also true. The adapter attaches its class before
terminal delivery. The service-resolved `RouteDecision` is authoritative for
`Harness`, `Provider`, `ServerInstance`, `Model`, `ContextLength`, and
`ContextSource`: final projection overwrites conflicting adapter identity with
those values while preserving the adapter-owned `FailureClass`.

Before emission, adapter diagnostics follow ADR-002 secret and account-data
scrubbing. Claude diagnostics are bounded to 2048 bytes. A TUI adapter retains
only matched fatal lines, never the full rendered frame or user prompt.
`unknown` and semantic failures do not enter route health. Admission of other
classes is service policy; a class is evidence, not an unconditional hard gate
or a claim about sibling surfaces.

Terminalization is the creation of one immutable terminal fact for an accepted
session. `Outcome` is its stable coarse result, `Cause` is its stable reason,
and `Stage` is the Fizeau-owned stage that determined it. Terminalization is
separate from live event delivery and session-log persistence. `Status` remains
a compatibility/detail field; consumers MUST NOT parse `Status` or `Error` to
reconstruct outcome, cause, or stage.

While the caller remains alive, Fizeau MUST deliver exactly one live terminal
event containing that fact before closing the stream. Persistence is
best-effort under the existing session-log reliability policy. After caller
death, Fizeau still MUST terminalize the session and MUST attempt to persist the
terminal fact when its cleanup supervisor or recovery path can write the log,
but there may be neither a live event nor a durable terminal or session-log
record. The pre-spawn lifecycle ownership record remains mandatory recovery
evidence; it is not a successful terminal fact. Delivery and persistence
failures do not create a second terminalization. Absence of a terminal event or
terminal record after caller death MUST NOT be interpreted as success.

The complete session-stage vocabulary is routing, spawn, harness, provider,
tool-loop, timeout, cancellation, and cleanup. The Go/JSON value for tool-loop
is `tool_loop`; there are no workflow-orchestrator stages in this enum.

The required mappings include:

| Condition | Outcome | Cause | Stage |
|-----------|---------|-------|-------|
| Native text-only or empty successful completion | `success` | `completed` | `tool_loop` |
| Wrapped harness completes successfully | `success` | `completed` | `harness` |
| No eligible route | `failed` | `route_unavailable` | `routing` |
| Harness process cannot start | `failed` | `spawn_failed` | `spawn` |
| Harness exits unsuccessfully | `failed` | `harness_failed` | `harness` |
| Native provider exhausts transport retries | `failed` | `provider_failed` | `provider` |
| Tool loop cannot continue | `failed` | `tool_loop_failed` | `tool_loop` |
| Iteration limit reached | `failed` | `iteration_limit` | `tool_loop` |
| Known-cost cap stops the next turn | `failed` | `budget_halted` | `tool_loop` |
| Wall, idle, provider, or tool deadline expires | `timed_out` | `deadline_exceeded` | `timeout` |
| Context is cancelled | `cancelled` | `context_cancelled` | `cancellation` |
| Caller death is observed | `cancelled` | `caller_died` | `cancellation` |
| Owned process tree cannot be reaped by the cleanup deadline | `failed` | `cleanup_failed` | `cleanup` |
| Internal invariant or service implementation fails outside cleanup | `failed` | `internal_error` | The active Fizeau stage |

Every `internal_error` MUST name the active stage; there is no `internal` stage
and an empty stage is invalid. An internal error that prevents cleanup from
finishing is classified as `cleanup_failed / cleanup`, not
`internal_error / cleanup`.

Cleanup failure has deterministic precedence over the primary execution fact.
If the primary execution reaches any tuple and cleanup then misses
`HarnessCleanupTimeout` or establishes a containment escape, the final tuple
MUST be `failed / cleanup_failed / cleanup`. `PrimaryOutcome`, `PrimaryCause`, and
`PrimaryStage` are then required and preserve the pre-cleanup tuple. Those
fields MUST be omitted when cleanup succeeds or no primary tuple exists. Error
text MAY add diagnostics, but it cannot change this precedence.

`SessionStage` has no DDx workflow values. In particular, worktree preparation,
pre- or post-run gates, review, landing, preservation, tracker updates, and bead
closure MUST NOT appear as Fizeau session stages.

### Fizeau session completion is not DDx bead completion

A caller-alive Fizeau session completes observably when its one accepted
`Execute` or `Continue` call has terminalized, cleanup has either succeeded or
reached `HarnessCleanupTimeout`, exactly one terminal event has been delivered,
and the stream has closed. A caller-death session terminalizes after the same
cleanup decision, but live delivery is impossible and durable persistence is
best-effort. A cleanup-failed session is terminal even while recovery ownership
continues asynchronously; terminalization does not claim that surviving
contained processes are already gone. `SessionOutcomeSuccess` means only that
the Fizeau invocation and its cleanup completed successfully.

DDx bead completion is an outer-orchestrator decision. DDx may still need to
inspect the worktree, run acceptance and repository gates, review evidence,
land or preserve commits, update the tracker, and close the bead. Fizeau MUST
NOT emit a bead-success fact, decide a bead outcome, or classify any of those
DDx-owned stages. A successful Fizeau session is necessary evidence for some
DDx attempts; it is never sufficient proof that a bead is complete.

### Wrapped process tree ownership and caller death

Fizeau MUST launch every wrapped harness tree inside a platform containment
boundary established before untrusted harness code runs:

- On Unix-like systems, a trusted Fizeau supervisor MUST lead a dedicated
  process group or session and release untrusted harness code only after the
  ownership record and launch gate are established. Fizeau MUST retain the
  group and process-birth identities and couple caller liveness through a
  supervisor control channel. A direct-child `Pdeathsig` MAY provide secondary
  protection, but it is not sufficient descendant containment.
- On Windows, the direct child and descendants MUST be assigned to a dedicated,
  non-inheritable job object configured for kill-on-job-close before the child
  resumes normal execution.
- Other platforms MUST provide an equivalent boundary or report wrapped
  harness execution unsupported before untrusted code runs.

The same boundary applies to batch runners, PTY sessions, quota and account
probes, model discovery, and every other live harness subprocess. No accepted
invocation may return a live harness process to a package-global or
service-global pool. Shared durable caches are allowed; shared live process
pools across accepted invocations are not.

Fizeau ownership covers the direct child, PTY helpers, adapter supervisors, and
all descendants that remain in the established containment boundary. A harness
MUST NOT daemonize, create an untracked session, request job breakaway, or
otherwise escape that boundary. An observed or credibly detected escape is a
harness-contract violation and produces `failed / cleanup_failed / cleanup`.
Fizeau does not promise control of an intentionally escaped descendant; the
cleanup guarantee applies to processes inside the boundary. Recovery evidence
MUST retain owner and containment process-birth identities, not only a PID or
PGID, so recovery refuses reused identities and diagnoses an escape rather than
claiming the escaped process was reaped.

Ownership starts when Fizeau durably registers the lifecycle boundary before
the launch gate releases untrusted code, and it continues after terminalization
when recovery is pending. Registration failure prevents harness launch. A
harness returning a final payload does not transfer ownership to the caller.
On normal completion, failure, timeout, caller-signalled context cancellation,
or caller death, Fizeau MUST immediately begin cleanup under a service-owned
context that survives request cancellation:

1. Signal the containment boundary for graceful termination.
2. Escalate to the platform's forceful containment termination before
   `HarnessCleanupTimeout` expires.
3. Signal all processes observed in the boundary, reap every process parented by
   Fizeau or its supervisor, and wait for the boundary to become empty until the
   deadline.
4. If the boundary is empty by the deadline, terminalize using the primary
   execution tuple and remove the lifecycle record only after emptiness is
   confirmed. If any contained process remains or containment state is
   indeterminate, terminalize as `failed / cleanup_failed / cleanup`, preserve
   the primary tuple, retain the recovery record, and close the caller-alive
   stream after its one terminal event.

The per-invocation deadline bounds terminal delivery; it is not a claim that no
process can survive it. After `cleanup_failed`, a separate recovery reaper MUST
continue attempting containment termination and reaping. A later startup MAY
use `StaleHarnessReaperGrace` to decide when a persisted record is old enough
for stale recovery; that startup threshold never delays or replaces current
per-invocation cleanup.

Caller death MUST trigger the same containment cleanup through the strongest
liveness mechanism available. The cleanup supervisor or recovery path MUST make
a best-effort attempt to persist `cancelled / caller_died / cancellation`, or
`failed / cleanup_failed / cleanup` when the cleanup deadline is missed. The
caller is gone, so neither a live terminal event nor a durable terminal or
session-log record is guaranteed. The lifecycle ownership record remains until
the boundary is confirmed empty; cleanup and recovery obligations remain.

## Final Measurement Projection

The result returned by compatibility drains and the terminal service event
preserve cost provenance on the public facade. The normative fields are:

```go
type CostSource string

const (
    CostSourceReported   CostSource = "reported"
    CostSourceConfigured CostSource = "configured"
    CostSourceUnknown    CostSource = "unknown"
)

type DrainExecuteResult struct {
    // Other public result fields omitted.
    CostUSD    *float64
    CostSource CostSource
}

type ServiceFinalData struct {
    // Other public final-event fields omitted.
    CostUSD    *float64   `json:"cost_usd,omitempty"`
    CostSource CostSource `json:"cost_source"`
}

type SessionEndData struct {
    // Other public durable session-end fields omitted.
    CostUSD    *float64   `json:"cost_usd,omitempty"`
    CostSource CostSource `json:"cost_source"`
}

type ServiceOverrideOutcome struct {
    Status     string     `json:"status"`
    CostUSD    *float64   `json:"cost_usd,omitempty"`
    CostSource CostSource `json:"cost_source"`
    DurationMS int64      `json:"duration_ms"`
}
```

Provider- or gateway-reported billing wins and uses `reported`. Exact
runtime/provider/model pricing supplied by the caller uses `configured` only
when no reported amount exists. If neither source exists, `CostSource` is
`unknown`, `CostUSD` is `nil`, and terminal JSON omits `cost_usd`. A present
reported or configured zero is a known zero, not an unknown value. Terminal
service events always emit `cost_source`; callers must use amount presence and
provenance together and must not infer provenance from the amount alone.

These validity rules apply uniformly to `ServiceFinalData`,
`DrainExecuteResult`, the public durable `SessionEndData` projection, and
`ServiceOverrideOutcome`. A present amount is valid only when it is zero or
positive and the normalized source is `reported` or `configured`. Producers
MUST NOT emit a negative amount, infer presence from `CostUSD > 0`, infer
provenance from the numeric value, or retain a present amount with an empty,
`unknown`, or invalid source. Any such invalid combination normalizes to `nil`
plus `unknown`. Legacy source-less final or `session.end` JSON remains
decodable, but its scalar amount is not promoted to authoritative billing
evidence; it normalizes to `nil` plus `unknown` when projected through the
v0.15 typed surface.

For a session total, any contributing turn with unknown cost makes the total
amount `nil` and the source `unknown`. When every contributing turn is known,
the amount is their sum: the source is `reported` if any turn used provider- or
gateway-reported billing, and `configured` only when every turn used exact
configured pricing. The public three-value source does not invent a `mixed`
state or discard an all-known amount. This public terminal classification does
not replace CONTRACT-001 root-span provenance: an all-known root span whose
turn-level provenance differs continues to emit the amount while omitting the
root `ddx.cost.source` and `ddx.cost.pricing_ref` attributes.

Accepted `override` events MUST include `ServiceOverrideOutcome` after
execution. Its amount and source follow the same rules as the terminal
projection: `nil` requires `unknown` and omits `cost_usd`; a present zero or
positive amount requires `reported` or `configured` and is serialized without
collapsing zero into absence.

A pre-dispatch `rejected_override` event MUST omit `outcome` entirely because
no execution cost exists. It MUST NOT fabricate a zero-cost outcome. Whenever
an outcome is present, `cost_source` is mandatory even when `cost_usd` is
omitted.

## Routing Types

```go
type RouteAttempt struct {
    Harness string
    Provider string
    Model string
    Endpoint string
    ServerInstance string
    Status string
    Reason string
    Error string
    Duration time.Duration
    Timestamp time.Time
}

type RouteRequest struct {
    Policy string
    Model string
    Provider string
    Harness string
    Reasoning Reasoning
    Permissions string
    AllowLocal bool
    Require []string
    MinPower int
    MaxPower int
    EstimatedPromptTokens int
    MaxTokens int
    RequiresTools bool
    CachePolicy string
    Role string
    CorrelationID string
}

type PolicyInfo struct {
    Name string
    MinPower int
    MaxPower int
    AllowLocal bool
    Require []string
    CatalogVersion string
    ManifestSource string
    ManifestVersion int
}

type RouteDecision struct {
    RequestedPolicy string
    PowerPolicy RoutePowerPolicy
    Harness string
    Provider string
    Endpoint string
    ServerInstance string
    Model string
    EstimatedPromptTokens int
    MaxTokens int
    RequiredContext int
    ContextLength int
    ContextSource string
    Reason string
    Sticky RouteStickyState
    Utilization RouteUtilizationState
    Power int
    Candidates []RouteCandidate
}

type RouteCandidate struct {
    Harness string
    Provider string
    Endpoint string
    ServerInstance string
    Model string
    ContextLength int
    ContextSource string
    Score float64
    Eligible bool
    Reason string
    FilterReason string
    Components RouteCandidateComponents
    ScoreComponents map[string]float64
    Utilization RouteUtilizationState
}
```

`RouteDecision` and `RouteCandidate` expose the evidence behind one selection
and the measurements necessary to audit it. They are not an extension point for
callers to program routing algorithms, request multi-route execution, or turn
every inventory signal into a policy control. Existing fields remain stable
compatibility surface; v0.15 adds no routing field or feature merely to expose
more health, quota, capacity, or scoring detail.

`RouteRequest.MaxTokens` is the requested per-provider-call output budget.
`ResolveRoute` and `Execute` reject a negative `MaxTokens` before candidate
construction. `Execute` copies `ServiceExecuteRequest.MaxTokens` unchanged into
`RouteRequest.MaxTokens` and the native core request. Candidate metadata does
not silently replace a caller's zero with a provider maximum. The router
computes the following with saturating integer addition:

```text
required_context = max(EstimatedPromptTokens, 0)
                 + floor(max(EstimatedPromptTokens, 0) / 4)
                 + max(MaxTokens, 0)
```

Each `+` saturates at Go `math.MaxInt`; overflow of non-negative `int` fields
cannot wrap to a smaller requirement. When `required_context > 0`, an unknown
or zero candidate `ContextLength` is ineligible with
`FilterReason="context_too_small"`; a positive value below the requirement is
also ineligible, while equality passes. When `required_context == 0`, this gate
does not reject an unknown window. FEAT-004 requirements 23–28 therefore permit
an exact pin to select an otherwise allowed exact-pin-only candidate with raw
unknown capacity; unpinned auto-routing exclusions still apply. This covers an
output-only budget, exact pins, and a single route. `MaxTokens == 0` contributes
no route-time output reserve.

`RouteDecision.EstimatedPromptTokens`, `MaxTokens`, and `RequiredContext`
preserve the request evidence used for the gate. Every candidate retains its
raw `ContextLength` and `ContextSource`. After selection, a positive candidate
value wins; otherwise selected-context resolution uses explicit provider
configuration, cached provider-API evidence, catalog metadata, then
`compaction.DefaultContextWindow` (currently `131072`) with source `default`.
`RouteDecision.ContextLength` and `ContextSource` expose that resolved execution
value and are authoritative for native execution.

The public `ServiceRoutingDecisionData` projection includes
`EstimatedPromptTokens`, `MaxTokens`, `RequiredContext`, `ContextLength`, and
`ContextSource`. Each `ServiceRoutingDecisionCandidate` includes its own
`ContextLength` and `ContextSource`. The terminal `ServiceRoutingActual`
projection repeats the selected `ContextLength` and `ContextSource`; consumers
never need private session JSON to recover capacity provenance.

The root route decision is copied through serviceimpl's API-neutral
execution-decision and native-request types into core `Request` as
`SelectedContextWindow`, `SelectedContextSource`, and the raw
`CompactionContextWindow`. Core and serviceimpl do not import root/public DTOs.

For a native execution path without a routing decision, the same chain starts
at explicit provider configuration because no selected candidate exists. These
fallbacks never perform a synchronous provider probe and source `default` never
claims provider-observed capacity.

Execution derives its window without enlarging the selected route:

```text
selected_window = RouteDecision.ContextLength
error                                                  if CompactionContextWindow < 0
working_window = selected_window                         if CompactionContextWindow == 0
working_window = CompactionContextWindow                 if selected_window <= 0
working_window = min(selected_window, CompactionContextWindow) otherwise

quotient = working_window / 100
remainder = working_window % 100
effective_window = quotient * 95 + floor(remainder * 95 / 100)
```

Core owns the overflow-safe `ResolveWorkingContextWindow` helper and rejects a
negative raw override before `session.start`. Serviceimpl uses that same helper
to configure the compactor, so compaction and provider preflight share one
working-window definition. Percentage scaling uses the quotient/remainder form
above for both the fixed 95-percent effective window and the validated
compaction percentage; it never evaluates `working_window * percent`.
`CompactionReserveTokens` remains compaction-only and MUST NOT be subtracted
from provider-call headroom. SD-006 owns canonical call estimation and the
per-attempt behavior.

The core compactor boundary is exact:

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

`History` excludes the separately owned system prompt. Core constructs
`ProviderMessages` as the exact next envelope with the system prompt once and
estimates `ProviderMessages` plus `ToolDefinitions`. `ExecutedToolCalls` exists
for file tracking. The trigger uses the supplied estimate; the compactor
retains `provider` for summarization and returns replacement history plus a
nullable result/error. Core alone rebuilds the provider envelope so the system
prompt cannot be duplicated; a nil result preserves compaction no-op semantics.

`RecordRouteAttempt` requires at least one of `Harness` or `Provider` after
whitespace normalization and requires a non-empty `Status`. Status matching is
case-insensitive. `success`, `ok`, and `succeeded` clear only the identical
normalized `(Harness, Provider, Model, Endpoint, ServerInstance)` failure key;
empty endpoint and server-instance fields are identity values, not wildcards.
Any other non-empty status records a failure. A zero `Timestamp` records the
attempt at the current time.

Raw `Model` constraints are normalized against provider-discovered model IDs
before route selection. The resolver uses the canonical fuzzy matcher for
case, vendor-prefix, separator, accelerator, and packaging differences when
the mapping is unambiguous. Ambiguous matches fail with
`ErrModelConstraintAmbiguous`; no match requests fail with
`ErrModelConstraintNoMatch` and return nearby candidates instead of silently
falling back to a different model.

See FEAT-004's candidate-construction rules for the routing-side reference
behavior this contract preserves.

`ListPolicies` returns the canonical v0.11 policy set: `cheap`, `default`,
`smart`, and `air-gapped`. Dropped compatibility names are not listed.

`Policy=air-gapped` carries `Require=["no_remote"]`. A remote provider or
subscription harness pin under that policy fails with
`ErrPolicyRequirementUnsatisfied`. This is deliberate: pins narrow where the
router may look, but they do not weaken policy requirements.

Power bounds are soft scoring inputs in automatic routing. A candidate below
`MinPower` is penalized more than a candidate above `MaxPower`, because using a
weaker model is more likely to fail the task while using a stronger model is
primarily a cost/latency tradeoff. Missing-power and exact-pin-only models are
excluded from unpinned automatic routing but remain visible in inventory and
usable through exact pins when the selected harness/provider can serve them.
Cost, quality, health risk, latency, utilization, and power fit may inform the
single-route selection. They do not become hard gates unless dispatch would be
incorrect. `RouteCandidate.Components` is operator-facing aggregate evidence;
`RouteCandidate.ScoreComponents` preserves engine evidence when present. Neither
projection promises a stable or growing scoring vocabulary, nor does either add
a caller-controlled policy mechanism.

`fiz models` is the snapshot-first inspection path. It is expected to be quick,
return stale output by default when freshness is pending, and use
`RefreshModels` / `--refresh` for routing-relevant stale fields or
`--refresh-all` for every refreshable field. If no DDx server or other
long-running maintainer is keeping freshness warm, stale output should say so
and suggest an explicit refresh.

## Model Snapshot Freshness

```go
type ModelRefreshScope string

const (
    ModelRefreshRouting ModelRefreshScope = "routing"
    ModelRefreshAll     ModelRefreshScope = "all"
)

type ModelRefreshOptions struct {
    Scope ModelRefreshScope
    Harness string
    Provider string
}

type ModelSnapshotInfo struct {
    Version string
    CapturedAt time.Time
    Fresh bool
    RefreshInFlight bool
    Fields []ModelFieldFreshness
}

type ModelFieldFreshness struct {
    Field string
    Source string
    Fresh bool
    CapturedAt time.Time
    ExpiresAt time.Time
    LastError *StatusError
}
```

`RefreshModels` is synchronous. It blocks until the requested refresh scope is
fresh or has conclusively failed, coalesces with other processes through the
ADR-012 lock/marker contract, and writes snapshot state atomically. A DDx server
maintains asynchronous freshness by calling this method from its own background
task; Fizeau does not require a resident process.

`ModelRefreshRouting` refreshes the fields needed before autorouting can score:
health, quota, model availability/discovery, context/tool/reasoning support,
billing/effective-cost metadata when dynamic, and utilization when available.
`ModelRefreshAll` widens the scope to every refreshable display field.

## Inventory Types

```go
type HarnessInfo struct {
    Name string
    Type string
    Available bool
    Path string
    Error string
    Billing BillingModel
    AutoRoutingEligible bool
    TestOnly bool
    ExactPinSupport bool
    DefaultModel string
    SupportedPermissions []string
    SupportedReasoning []string
    CostClass string
    Quota *QuotaState
    Account *AccountStatus
    UsageWindows []UsageWindow
    LastError *StatusError
    CapabilityMatrix HarnessCapabilityMatrix
}

type ProviderInfo struct {
    Name string
    Type string
    BaseURL string
    Endpoints []ServiceProviderEndpoint
    Status string
    ModelCount int
    Capabilities []string
    Billing BillingModel
    IncludeByDefault bool
    IsDefault bool
    DefaultModel string
    CooldownState *CooldownState
    Auth AccountStatus
    EndpointStatus []EndpointStatus
    Quota *QuotaState
    UsageWindows []UsageWindow
    LastError *StatusError
}

type ModelInfo struct {
    ID string
    Provider string
    ProviderType string
    Harness string
    EndpointName string
    EndpointBaseURL string
    ServerInstance string
    ContextLength int
    ContextSource string
    Utilization RouteUtilizationState
    Capabilities []string
    Cost CostInfo
    EffectiveCostUSD float64
    CostSource string
    ActualCashSpend bool
    PerfSignal PerfSignal
    Power int
    AutoRoutable bool
    ExactPinOnly bool
    Billing BillingModel
    Available bool
    IsDefault bool
    RankPosition int
    SnapshotVersion string
    Freshness []ModelFieldFreshness
}
```

`HarnessInfo.Quota` and `HarnessInfo.Account` are populated by projecting the
internal harness contract defined in
[`CONTRACT-004`](./CONTRACT-004-harness-implementation.md). `ProviderInfo.Quota`
and `ProviderInfo.Auth` use the same projection rules for subscription-backed
providers.

`BillingModel` has four values: unknown (`""`), `fixed`, `per_token`, and
`subscription`. Billing feeds routing cost and default inclusion, but it is
also surfaced on harness, provider, and model inventory rows so operators can
audit why a candidate participated or was skipped.

`Cost` carries catalog/list price inputs. `EffectiveCostUSD` is the normalized
request-local or representative scoring cost used for route comparison.
Subscription rows keep `ActualCashSpend=false` while still carrying
PAYG-equivalent effective cost; pay-per-token rows set `ActualCashSpend=true`
when dispatch would create incremental metered billing.

## Routing Status and Usage

`RouteStatus` is an operator projection over recent routing decisions,
cooldowns, sticky assignments, selected endpoints, candidate health, and
routing-quality metrics. Routing-quality metrics measure how often automatic
routing agrees with users and completes; provider reliability remains a
candidate-level signal and must not be labeled as routing quality.

`UsageReport` aggregates service-owned session logs. It reports token streams,
known cost, unknown-cost sessions, runtime/model attribution, and routing
quality. Consumers use this projection rather than parsing private JSONL
records.

## Session Log and Events

Fizeau owns public `ServiceEvent` construction and session-log projection.
Consumers may subscribe through `Execute` or `TailSessionLog` and may render
stored sessions through `WriteSessionLog` and `ReplaySession`.

Context-capacity decisions use one public event type and payload:

```go
const ServiceEventTypeContextCapacity = "context_capacity"

type ServiceContextCapacityAction string

const (
    ServiceContextCapacityClamped         ServiceContextCapacityAction = "clamped"
    ServiceContextCapacityPlanningSkipped ServiceContextCapacityAction = "planning_skipped"
    ServiceContextCapacityRejected        ServiceContextCapacityAction = "rejected"
)

type ServiceContextCapacityCallKind string

const (
    ServiceContextCapacityPlanning ServiceContextCapacityCallKind = "planning"
    ServiceContextCapacityMain     ServiceContextCapacityCallKind = "main"
)

type ServiceContextCapacityData struct {
    Action                 ServiceContextCapacityAction `json:"action"`
    CallKind               ServiceContextCapacityCallKind `json:"call_kind"`
    TurnIndex              int    `json:"turn_index"`
    AttemptIndex           int    `json:"attempt_index"`
    ContextWindow          int    `json:"context_window"`
    EffectiveContextWindow int    `json:"effective_context_window"`
    EstimatedInputTokens   int    `json:"estimated_input_tokens"`
    RequestedMaxTokens     int    `json:"requested_max_tokens"`
    EffectiveMaxTokens     int    `json:"effective_max_tokens"`
    AvailableOutputTokens  int    `json:"available_output_tokens"`
}

type ServiceDecodedEvent struct {
    // Other decoded payload pointers omitted.
    ContextCapacity *ServiceContextCapacityData
}
```

`ContextWindow` is the `working_window` after the non-enlarging request
override. `EffectiveContextWindow` is its fixed 95-percent envelope.
`RequestedMaxTokens` is the original request value on every attempt;
`EffectiveMaxTokens` is the value sent to the provider, or zero for a skipped
or rejected call. `AvailableOutputTokens` is reported before the caller budget
is applied.

Core owns primitive `ContextCapacityEventData` and emits
`EventContextCapacity`; it imports no root/public DTO. Serviceimpl
field-exhaustively maps that payload to
`internal/harnesses.ContextCapacityData` and
`internal/harnesses.EventTypeContextCapacity`. The root facade owns
`ServiceContextCapacityData`, maps every field into the public event, and
populates `ServiceDecodedEvent.ContextCapacity` when decoding
`ServiceEventTypeContextCapacity`. Exhaustive mapping tests fail when a field is
added at one layer without being projected through the next.

`available_output_tokens == 0` is evaluated before clamping. Planning emits
only `planning_skipped`; main emits only `rejected`. A prevented call emits no
`clamped`, `llm.request`, or provider call. Planning capacity is checked against
the separate planning messages with no tool definitions; a skip also emits no
`llm.response` or `planning.turn`, then main execution continues without a
plan. A main `rejected` capacity event precedes core `session.end` and the
required public terminal fact `failed / context_capacity_exceeded / tool_loop`;
`ServiceFinalData.ContextCapacity` repeats the same structured payload.

When headroom is positive and `MaxTokens` is reduced, `clamped` is non-terminal
and immediately precedes the corresponding `llm.request`. That request event
carries the same `EffectiveMaxTokens` sent to the provider. `MaxTokens == 0`
remains zero when headroom exists and emits no clamp.

Core recomputes capacity before every actual native planning call, main
streaming or non-streaming call, same-route transient retry,
overflow-after-compaction retry, and service no-stream rerun. Each attempt
starts from the original requested `MaxTokens`; it does not inherit a previous
clamp. Compaction summarization calls are outside this contract. An
eligibility-time `context_too_small` rejection removes that candidate before
selection, after which routing may select the best eligible survivor; no
provider call was attempted for the rejected candidate. Once a route is
selected, an accepted-session capacity failure does not dispatch another route
candidate.

Index semantics are exact:

```go
type CapacityAttemptKey struct {
    CallKind string
    TurnIndex int
}

// Values are the last assigned AttemptIndex per key.
type CapacityAttemptState map[CapacityAttemptKey]int

// Relevant core fields; other fields omitted.
type Request struct {
    InitialCapacityAttempts CapacityAttemptState
}

type Result struct {
    CapacityAttempts CapacityAttemptState
}
```

- Planning uses `CallKind="planning"` and `TurnIndex=0`.
- Main uses `CallKind="main"`; `TurnIndex` is the one-based logical tool-loop
  turn within the accepted service session.
- A retry of the same logical turn preserves `TurnIndex`.
- Core defensively copies `Request.InitialCapacityAttempts`. Every preflight
  reserves the next one-based index in the copy, whether it proceeds normally
  or emits `clamped`, `planning_skipped`, or `rejected`; ordinary
  `llm.request` calls therefore advance state too.
- `Result.CapacityAttempts` returns the last assigned index per key. Transient
  and overflow-compaction retries use the same state and never reset it.
- Serviceimpl passes the first run's `Result.CapacityAttempts` as the no-stream
  rerun's `Request.InitialCapacityAttempts`. Repeated planning remains turn
  zero with the next index; a reattempted main call preserves its logical turn
  and gets the next index.

### Direct-core error identity versus accepted sessions

Callers that invoke `internal/core` directly receive a concrete
`ContextCapacityError` with stable code `CONTEXT_CAPACITY_EXCEEDED`. It supports
`errors.Is(err, ErrContextCapacityExceeded)` and `errors.As` into
`*ContextCapacityError`, and includes call kind, turn and attempt indexes,
selected and effective windows, estimated input, requested output, and
available output.

```go
const ContextCapacityErrorCode = "CONTEXT_CAPACITY_EXCEEDED"

var ErrContextCapacityExceeded = errors.New("agent: context capacity exceeded")

type ContextCapacityError struct {
    CallKind              string // planning or main
    TurnIndex             int    // planning is 0; main is one-based
    AttemptIndex          int    // one-based within the call
    ContextWindow         int
    EffectiveWindow       int
    EstimatedInputTokens  int
    RequestedMaxTokens    int
    AvailableOutputTokens int
}
```

`FizeauService.Execute` is asynchronous after acceptance and MUST NOT surface
that condition as a returned Go error. It reports accepted-session capacity
through `ServiceEventTypeContextCapacity` and, for a main rejection, the typed
terminal cause and final payload. Negative request fields remain synchronous
public validation errors because no session has yet been accepted.

Successful completion with empty `final_text` is a valid outcome. Consumers
MUST NOT retry, mark failure, or synthesize fallback text on empty text alone;
they must use the terminal status, process outcome, and error fields to decide
whether the run failed.

Session logs are versioned service artifacts. The v0.11 routing redesign is a
schema break for routing fields: route events and final-event routing summaries
use `policy` and `power_policy`; removed route-reference fields are not emitted.

Replay must remain backward-compatible with older logs where practical. Unknown
or removed fields from pre-v0.11 logs are ignored rather than reintroduced into
the public contract.

Cache-aware cost attribution keeps cache token streams separate from ordinary
input/output pricing. Manifest/runtime pricing fields such as
`cost_cache_read_per_m` and `cost_cache_write_per_m` price cache read/write
tokens when known. For nullable reported cache amounts, explicit zero means the
caller or provider opted out, for example through `CachePolicy=off`; nil means
the harness or provider did not report the amount. Consumers must not treat nil
as zero.

## Mountable CLI

The standalone binary and embedding callers use the same Cobra command tree
from `agentcli`:

```go
package agentcli

func MountCLI(opts ...MountOption) *cobra.Command
func Run(opts Options) int
func ExitCode(err error) (int, bool)
```

`MountCLI` returns a fresh command tree, accepts stream/version/use/description
options, and never calls `os.Exit`. The standalone `cmd/fiz` binary owns
process termination. CLI subcommands consume the public service facade and do
not import internal packages.

## Precedence and Compatibility

v0.11 intentionally removes the v0.10 routing names listed in ADR-009. The
contract does not provide success-path compatibility fallbacks for removed flags
or removed service methods. CLI callers receive usage errors with migration
guidance. Go callers update code to `Policy`, `ListPolicies`, and exact
`Model` pins.

ADR-007 sampling defaults are separate catalog generation policy and are not
changed by this routing contract.

The typed session-lifecycle surface is introduced in product API v0.15.
`Continue` is currently compiled but experimental/deferred, not introduced as a
v0.15 product API. Its presence can break third-party implementations and mocks
of the current `FizeauService` interface, just as `PreparePortableRuntime` can;
that present-checkout impact is not a v0.15 compatibility promise. A future
versioned API refactor owns the decision to retain, replace, or remove either
experimental interface method. The standalone portable-runtime types and methods on
`PortableRuntimeBundle`, the `NewFromPortableRuntime` constructor, the
platform-specific fixed guest-root function, and the four stable portable error
sentinels are additive,
but callers use keyed literals for `PortableRuntimeRequest` and
`PortableRuntimeMount`. External interface mocks may return the documented
closed zero-value bundle. Adding
`HarnessCleanupTimeout` and typed terminal or primary-fact fields to exported
structs also breaks external unkeyed composite literals, while their JSON
fields are additive. These changes MUST ship only with the v0.15 API update and
migration notes. Callers that only consume a service returned by `New` do not
need to implement the new method, but must migrate unkeyed public struct
literals to keyed form.

Adding fields to exported Go structs—including `RouteRequest.MaxTokens`,
selected-route context evidence, `ServiceFinalData.ContextCapacity`, and
`ServiceDecodedEvent.ContextCapacity`—is source-breaking for external unkeyed
composite literals. These fields ship in the v0.15 migration, whose Go guidance
requires keyed literals for public Fizeau structs. Existing keyed literals and
field selectors remain source-compatible only for additive fields whose types
did not change.

Changing `CostUSD` from `float64` to `*float64` on `ServiceFinalData`,
`DrainExecuteResult`, and `ServiceOverrideOutcome` is a deliberate v0.15 Go
source break. It breaks keyed literals that assign a scalar and field-selector
expressions used directly in arithmetic, comparison, or formatting; keyed
literals and selectors do not make a type change source-compatible. Consumers
MUST migrate literals to `nil` for unknown or to a pointer for known zero and
positive amounts. They MUST replace numeric presence checks such as
`CostUSD > 0` with a nil check, inspect `CostSource`, and dereference only after
presence is established. Formatting and arithmetic likewise operate on the
dereferenced value, never on the pointer or on a synthesized zero.

Adding mandatory `CostSource` to `ServiceFinalData`, `DrainExecuteResult`,
`ServiceOverrideOutcome`, and the public durable `SessionEndData` projection
is part of the same v0.15 migration. The new `SessionEndData` field is additive
for keyed literals and JSON consumers but breaks external unkeyed literals;
source-less historical records remain readable under the normalization rule
above rather than being assigned guessed provenance.

The JSON/event additions—`ServiceEventTypeContextCapacity`,
`ServiceContextCapacityData`, `TerminalCauseContextCapacityExceeded`, and the
new capacity fields—are additive. Consumers MUST preserve unknown event and
terminal-cause values and may ignore the new non-terminal event when they do
not need capacity telemetry.

Compatibility rules after v0.15 are:

- `SessionOutcome`, `TerminalCause`, `SessionStage`, `ContinuationPolicy`, and
  `ContinuationDisposition` values MUST NOT be renamed or reused with different
  semantics within v0.x.
- Stable `FailureClass` values MUST NOT be renamed or reused with different
  semantics within v0.x. Consumers MUST preserve an unknown failure-class
  string for diagnostics and MUST NOT admit it to route health by default.
- New cause or stage values MAY be added in a minor release. Consumers MUST
  preserve unknown strings for diagnostics and MUST NOT interpret an unknown
  outcome as success.
- The JSON `outcome`, `cause`, and `stage` keys are required on every terminal
  event created under v0.15 or later. `parent_session_id` and `continuation` are
  required for sessions created by `Continue` and omitted for ordinary
  `Execute` sessions.
- `primary_outcome`, `primary_cause`, and `primary_stage` are required as a set
  only when cleanup failure supersedes a primary execution tuple. Partial sets
  are invalid.
- `HarnessCleanupTimeout` remains the per-invocation cleanup deadline.
  `StaleHarnessReaperGrace` remains only the startup stale-record minimum age;
  compatibility code MUST NOT alias or derive one from the other.
- Legacy `Status` and `Error` remain additive detail fields for at least one
  minor release after typed terminal facts ship. They cannot override the typed
  fields.
- Removing a field or enum, changing a required mapping, allowing a DDx-owned
  workflow stage into `SessionStage`, or weakening process-tree cleanup requires
  an explicit contract-version break.
- Replay of pre-v0.15 logs MAY derive typed fields only through a documented,
  deterministic legacy mapping. When no mapping is safe, replay reports an
  unknown legacy value and never fabricates success.

## Error Semantics

| Condition | Error / Outcome | Retry | Recovery Expectation |
|-----------|-----------------|-------|----------------------|
| Request rejected before a session starts | Public validation error; no event channel | After correcting request | Caller fixes the request; no cleanup or terminal event is expected because Fizeau accepted no session. |
| Portable destination or target rejected | Error wraps `ErrPortableRuntimeRequestInvalid`; no session, process, event, or committed bundle | After correcting the destination/target | Prepare on Linux and supply an existing empty real directory plus the current Linux GOARCH. |
| Structurally included subprocess lacks a complete portable closure | Redacted error wraps `ErrPortableRuntimeClosureIncomplete`; destination remains empty | After installing or fixing the contributing runtime | Fizeau does not omit the surface or preselect another route. |
| In-runtime manifest or activation is invalid | `NewFromPortableRuntime` returns an error wrapping `ErrPortableRuntimeActivationInvalid`; no session or process starts | After restoring the exact prepared mount and inherited names | Do not fall back to host config or a narrowed service. |
| Portable cleanup is incomplete | `Close` returns an error wrapping `ErrPortableRuntimeCleanupIncomplete` | Retry `Close` after resolving the filesystem condition | The bundle retains cleanup ownership; external runtime termination remains caller-owned. |
| Negative `MaxTokens` or `CompactionContextWindow` | Public validation error; no event channel | After correcting request | Supply zero or a positive value. Direct-core callers receive the same rejection before `session.start`. |
| Positive route requirement meets unknown or insufficient candidate context | Candidate is ineligible with `context_too_small`; routing may select the best eligible survivor, but makes no provider call for the rejected candidate. An exact pin with no survivor fails routing. | No retry when a survivor is selected; otherwise a corrected or new caller-owned request | Inspect `RequiredContext` and raw candidate context evidence; exact pins do not bypass a positive requirement. With a zero requirement, a permitted exact pin may select raw unknown capacity and execution resolves the fallback in `RouteDecision`. |
| Accepted main call has no context headroom | `failed / context_capacity_exceeded / tool_loop` plus required capacity event and final payload | Caller policy | No `llm.request` was emitted for the prevented call and Fizeau does not try the next candidate. |
| Planning call has no context headroom | Non-terminal `context_capacity` action `planning_skipped` | Main execution continues | No planning request, response, or planning-turn event is emitted. |
| Unknown continuation policy | `ErrContinuationPolicyInvalid`; no session | No, without correction | Use one of the three declared policy values. |
| Prior session missing or unusable | `ErrContinuationSessionUnavailable`; no session | After restoring lineage | Supply a readable completed Fizeau session ID. |
| Resume required but route lacks continuation | `ErrContinuationUnsupported`; no session | With a different policy | Choose `prefer_resume` or `fresh_session`; do not guess a harness-specific token. |
| Accepted session fails while caller remains alive | Exactly one terminal event with typed outcome, cause, and stage | Caller policy | Drain the event stream and use typed facts; do not parse error text. |
| Event stream closes without a terminal event while caller is alive | Contract violation | No automatic retry | Treat as failure and retain partial evidence for diagnosis. |
| Internal service failure outside cleanup | `failed / internal_error / <active-stage>` | Caller policy | Preserve the stage and diagnostics; never invent an `internal` stage. |
| Cleanup misses `HarnessCleanupTimeout` after another result | `failed / cleanup_failed / cleanup` plus required primary tuple | No immediate retry | Terminal delivery proceeds after the deadline while the recovery reaper retains ownership. |
| Harness escapes containment | `failed / cleanup_failed / cleanup` | No | Disable or fix the harness; report escaped identity as unresolved rather than reaped. |
| Caller process dies | Required terminalization; best-effort terminal persistence; live event unavailable | Outer orchestrator policy | Containment cleanup and recovery continue from the mandatory lifecycle ownership record; absence of a terminal record is never success evidence. |

## Examples

Required-resume request with no harness-specific knowledge:

```go
events, err := svc.Continue(ctx, fizeau.ServiceContinuationRequest{
    SessionID: "01JZ8W6P4X8Z3FD2A6TQ0R3M1C",
    Prompt:    "Run the focused tests and summarize failures.",
    Policy:    fizeau.ContinuationRequireResume,
    Metadata:  map[string]string{"bead_id": "fizeau-1234"},
})
```

If the prior route supports resume, this creates a new child session with
`continuation="resumed"`. If it does not, `Continue` returns
`ErrContinuationUnsupported` and creates no session.

Fresh fallback without a concrete harness pin:

```go
events, err := svc.Continue(ctx, fizeau.ServiceContinuationRequest{
    SessionID: "01JZ8W6P4X8Z3FD2A6TQ0R3M1C",
    Prompt:    "Try again from the recorded repository state.",
    Policy:    fizeau.ContinuationPreferResume,
    FreshRequest: fizeau.ServiceExecuteRequest{
        Policy:   "default",
        MinPower: 6,
        WorkDir:  "/work/fizeau",
    },
})
```

When resume is unsupported, Fizeau routes the fresh request normally and emits
`parent_session_id` plus `continuation="fresh"`. A terminal
`outcome="success"` proves only that this child Fizeau session completed; DDx
still owns repository gates, landing, and bead closure.

Portable preparation without route knowledge:

```go
bundle, err := svc.PreparePortableRuntime(ctx, fizeau.PortableRuntimeRequest{
    DestinationRoot: "/tmp/ddx-runtime",
    TargetGOOS:      runtime.GOOS,
    TargetGOARCH:    runtime.GOARCH,
})
if err != nil {
    return err
}

// The external runtime applies bundle.Mounts() and
// bundle.EnvironmentNames() verbatim. Its public-package entrypoint calls
// NewFromPortableRuntime before Execute; the caller interprets no asset.
isolated, err := caller.Start(bundle.Mounts(), bundle.EnvironmentNames())
if err != nil {
    return errors.Join(err, bundle.Close())
}
runErr := isolated.Wait()
if stopErr := isolated.Stop(); stopErr != nil {
    cleanupQueue.RetainRuntimeAndBundle(isolated, bundle)
    return errors.Join(runErr, stopErr)
}
if destroyErr := isolated.DestroyWritableStorage(); destroyErr != nil {
    cleanupQueue.RetainStorageAndBundle(isolated, bundle)
    return errors.Join(runErr, destroyErr)
}
closeErr := bundle.Close() // legal only after both external cleanup steps succeeded
if closeErr != nil {
    cleanupQueue.Retain(bundle) // retry Close without losing ownership
}
return errors.Join(runErr, closeErr)
```

## Conformance-Test Obligations

Every implementation of this contract MUST provide automated conformance tests
that prove:

1. Public Go compile fixtures can call `Execute`, `Continue`, and the session-log
   methods and configure `HarnessCleanupTimeout` without importing `internal/`
   packages.
2. Every accepted session creates exactly one immutable terminalization. A
   caller-alive session delivers that fact in exactly one terminal event before
   channel closure; a caller-death session may deliver no event and persist no
   terminal record without creating a second terminalization or implying
   success. Its lifecycle ownership record remains recovery evidence.
3. No caller-alive terminal event is emitted before contained-process cleanup
   succeeds or `HarnessCleanupTimeout` expires. A missed deadline emits
   `cleanup_failed` once and leaves a recovery record without waiting forever.
4. Unix fixtures prove dedicated process-group/session containment; Windows
   fixtures prove kill-on-job-close containment. Cancellation and timeout
   fixtures exercise a harness grandchild, not only the direct child.
5. A caller-death fixture uses a separate caller process and proves the
   containment boundary receives cleanup, accepts either successful cleanup or
   `cleanup_failed` at `HarnessCleanupTimeout`, and permits neither a live event
   nor a durable terminal record to be required after the caller is gone.
6. Supported continuation reports `resumed`; unsupported `require_resume`
   returns `ErrContinuationUnsupported` without spawning; unsupported
   `prefer_resume` and explicit `fresh_session` create a child session reporting
   `fresh`.
7. Missing lineage and invalid continuation policies return their typed errors
   without creating a session.
8. A structural assertion rejects DDx-only stage values, including worktree,
   gate, review, landing, preservation, tracker, and bead-closure vocabulary.
9. Replay accepts pre-v0.15 terminal records and handles unknown additive enum
   values without treating them as success.
10. `internal_error` maps to `failed` plus each non-cleanup active Fizeau stage.
    An internal cleanup error maps to `cleanup_failed / cleanup`. If cleanup
    supersedes an earlier tuple, all three primary fields preserve that tuple.
11. `HarnessCleanupTimeout` and `StaleHarnessReaperGrace` have independent zero
    defaults and behavior. A stubborn contained process can survive the
    per-invocation deadline only with `cleanup_failed` and an active recovery
    record; the recovery reaper continues and is tested separately.
12. A fixture that attempts containment escape is rejected or terminalizes as
    `cleanup_failed`; test evidence MUST NOT claim the escaped identity was
    reaped.
13. A caller stops receiving events and cancels its context; cleanup begins,
    terminalization remains bounded, and no receiver-liveness heuristic is
    required.
14. A successful Claude-TUI invocation leaves no live PTY or Claude process
    after its terminal event.
15. Recovery refuses a lifecycle record whose PID, PGID, or job identity has a
    mismatched process-birth identity.
16. Cleanup failure or indeterminate boundary state retains its lifecycle
    ownership record until a later recovery confirms emptiness.
17. Route-capacity fixtures cover prompt-plus-25-percent-safety-plus-output,
    `math.MaxInt` saturation, equality, output-only budgets, explicit pins, and
    eligible-survivor selection before one route is dispatched. Unknown and
    zero candidate windows reject only for a positive requirement; a permitted
    exact pin with a zero requirement survives and resolves execution context
    through config, cache, catalog, or default while its candidate trace
    remains raw zero.
18. Selected `ContextLength` and `ContextSource` survive route decision,
    execution handoff, routing event, and terminal projection. A positive
    `CompactionContextWindow` can only reduce the selected value; zero passes it
    through and negatives fail before session acceptance and direct-core
    `session.start`. Serviceimpl and core use the same working-window helper;
    quotient/remainder scaling cannot overflow at `math.MaxInt`.
19. The canonical provider-call estimator covers message roles and content,
    assistant tool-call names and JSON arguments, tool-result IDs, and tool
    definition names, descriptions, and schemas. Pre-iteration and mid-turn
    checks pass history without system, exact provider messages with system
    once, executed tool-call logs, exact built definitions, and the core
    estimate through `CompactionInput`; the trigger uses that estimate and no
    provider usage or cache counter. The compactor returns replacement history
    only, and core rebuilds the provider envelope without duplicating system.
20. Planning, main streaming and non-streaming calls, same-route transient
    retries, overflow-after-compaction retries, and service no-stream reruns
    recompute capacity from fresh inputs and the original `MaxTokens`.
    Compaction summarization calls are excluded. Every ordinary or capacity
    preflight reserves a one-based index in a defensively copied
    `CapacityAttemptState`; `Result.CapacityAttempts` feeds a service-created
    second core run, so indexes remain monotonic per `(CallKind, TurnIndex)`.
21. Clamp, planning-skip, and main-rejection fixtures assert the complete
    `ServiceContextCapacityData` payload and event order. Zero headroom emits no
    clamp or `llm.request`; a clamp immediately precedes a request carrying its
    effective maximum; rejection precedes `session.end` and
    `failed / context_capacity_exceeded / tool_loop` without trying another
    candidate. Exhaustive core-to-harness-to-root mapping and
    `ServiceDecodedEvent.ContextCapacity` decoding are covered.
22. Direct-core fixtures prove `errors.Is` against
    `ErrContextCapacityExceeded` and `errors.As` into
    `*ContextCapacityError`. Public accepted-session fixtures prove the same
    condition is event/final evidence rather than an `Execute` return error.
23. Public compile and AST fixtures require keyed literals for exported Fizeau
    structs added to in v0.15. JSON fixtures accept the additive capacity event,
    payload, decoded-event field, and terminal cause while preserving unknown
    future event and enum values.
24. Public compile and AST fixtures call `PreparePortableRuntime` from
    `package fizeau_test`, require the exact three-field request, use only the
    generic bundle accessors and `Close`, call `NewFromPortableRuntime` in a
    separate process, and reject route selectors or exported cleanup,
    provider, harness, environment-value, or activation-manifest state.
25. Inventory fixtures join the production registry to its actual registered
    instances, classify every transport and structural inclusion state, and
    preserve every `ServiceProviderEntry` field, provider order, default,
    `HealthCooldown`, and the explicit portable treatment of `WorkDir` and
    `SessionLogDir`. Every structurally included subprocess surface supplies a
    verified same-target closure; missing or incompatible evidence fails
    preparation instead of disappearing from the structural candidate set.
26. Filesystem fixtures cover empty-root validation, directory-handle identity,
    one-child no-replace commit, concurrent preparers, copied symlinks, hardlink
    re-creation rather than preservation, traversal, source identity/content
    changes, deterministic conflicts, `0700` directories, `0600` sensitive
    regular files, owner-only executable permissions, cancellation, every
    partial-failure point, and atomic rollback.
27. Redaction fixtures seed recognizable file, API-key, header, and environment
    values and prove they are absent from returned errors, JSON, `String`, logs,
    and every public plan value. Environment output contains valid names only;
    the caller-supplied destination is allowed only in `RuntimeRoot`, mount
    `Source`, and direct request-validation errors. Activation fixtures prove a
    manifest-required name omitted after preparation fails before service
    activity, while a present-empty value remains valid.
28. Cleanup fixtures cover normal, repeated, concurrent, and failed-then-retried
    `Close`; they prove only the committed child and Fizeau staging remnants are
    removed, the caller-owned destination becomes empty, and cleanup neither
    stops an external container nor touches process-lifecycle ownership.
29. Required native Linux amd64 and arm64 OCI jobs build a public-package consumer without pulling
    an image, applies the one read-only generic mount and inherited names, calls
    `NewFromPortableRuntime`, and performs a credential-free unpinned `Execute`.
    Hermetic fixtures cover static-symlink, dynamic-ELF, and
    interpreter/package-tree closure classes, a sentinel inherited value, and
    generated configured-provider bootstrap. Each closure class is the sole
    structurally included subprocess in one table case, and
    `TestPortableRuntimeActivationFeedsProductionDispatch` proves its unpinned
    `Execute` consumes the activated typed launch recipe rather than a fresh
    default runner. The consumer runs as activation UID/GID `65532:65532` with
    an empty supplementary-group list;
    `TestPortableRuntimeRejectsUnsafeActivationIdentity` proves zero UID, zero
    GID, or any supplementary group fails before service construction. Each job
    proves preparation made no route decision and that in-runtime structural
    candidate identities match the prepared set; it does not equate
    network/quota health with structural parity. Each opted-in job fails rather
    than skips when OCI, its native architecture, the pinned runtime, or a
    required kernel feature is unavailable.
    `TestPortableRuntimeNamespaceLauncherArtifactParity` proves checked-in
    bytes, compile-time digest, ELF class/GOARCH/static/single-thread shape,
    exact `.fizeau/namespace-launcher` target,
    zero/one bundle cardinality, tamper rejection, and public diagnostic
    opacity. `make portable-namespace-launcher-check` and its required workflow
    job rebuild both supported targets and prove source/version-to-byte parity.
    Both required native OCI jobs also run
    `TestPortableRuntimeProjectionDeniesConfigMutation`,
    `TestPortableRuntimeProjectionAllowsMutableState`,
    `TestPortableRuntimeNamespaceLauncherDropsAuthority`,
    `TestPortableRuntimeNamespaceLauncherLifecycle`, and
    `TestPortableRuntimeNamespaceSetupFailureDoesNotExec` without skips. Those
    fixtures attack config and every governed ancestor with write, truncate,
    unlink, rename, rename-over, `RENAME_EXCHANGE`, hard-link, mount, and
    namespace-shadow attempts; prove mutable in-place and atomic refresh plus
    lock/file/directory siblings; prove the activation UID/GID maps and empty
    `Groups:`, private `/proc`, final required-absent check, exclusive activation
    lease, and every PID-1/harness `/proc/<pid>/task/*/status` has zero `Cap*`,
    locked securebits, `NoNewPrivs`, and the expected seccomp state. They prove
    PID 1 is single-threaded and non-dumpable, stage 2 owns only descriptors
    0/1/2, and `ptrace`, process-VM, `/proc/1/mem`, `/proc/1/fd`, and
    `pidfd_getfd` attacks fail. Direct, queued, thread-directed, process-group,
    and pidfd signal attempts against namespace PID 1 also fail with `EPERM`
    without changing supervisor state. They also prove
    `clone3`/prohibited-operation errno with ordinary thread creation,
    setup-gate fail-closed behavior, distinct launcher/harness process groups,
    no duplicate cleanup signal,
    PTY foreground job control, exact mirrored `WaitStatus`, cancellation/
    caller-death cleanup, descendant reaping, and OpenCode auth plus sibling
    database/log writes.
    A public structural fixture proves launcher identity, paths, digests, and
    namespace recipes add no exported field, environment-name entry,
    diagnostic, `String`, JSON, or caller-interpreted plan value.
    `TestPortableRuntimeExclusiveSubprocessLease` runs under `-race` and proves
    same-root serialization through descendant cleanup, queued cancellation
    without spawn, release after success/failure/setup-failure/cancellation,
    independent-root concurrency, and no lease surviving runtime teardown.
30. `TestV015CostPointerMigrationCompile` builds an external
    `package fizeau_test` consumer that uses keyed pointer literals, nil
    branching, dereference, and `CostSource` inspection for
    `ServiceFinalData`, `DrainExecuteResult`, and `ServiceOverrideOutcome`.
    Final and override conformance fixtures also prove unknown omission, known
    zero and positive preservation, mandatory normalized provenance, rejected
    override outcome omission, and the absence of negative producer values or
    numeric presence inference. `TestPublicSessionEndTypedValuesDurableRoundTrip`
    proves the durable public projection follows the same normalization and
    preserves source-less legacy records as unknown without fabricating an
    amount.

## Validation Checklist

- [ ] Normative request, terminal, continuation, and error fields exist on the
  public root-package facade.
- [ ] Fizeau session completion and DDx bead completion are tested as separate
  facts.
- [ ] Platform containment, per-invocation cleanup timeout, recovery reaping,
  and caller-death delivery/persistence modes pass subprocess conformance tests.
- [ ] Caller-signalled cancellation, Claude-TUI per-invocation teardown,
  process-birth identity reuse, and lifecycle-record retention have named tests.
- [ ] Compatibility fixtures cover legacy logs and unknown additive values.
- [ ] Route capacity, selected-window precedence, canonical estimation,
      overflow-safe scaling, compaction input, monotonic attempts, capacity event
      order/mapping, keyed-literal migration, and direct-core error identity have
      named tests.
- [ ] Portable-runtime public opacity, complete harness/provider inventory,
      same-target dependency closures, restrictive atomic materialization,
      embedded-launcher parity, descriptor-pinned namespace enforcement,
      authority removal, lifecycle preservation, exclusive subprocess leasing,
      redaction, retryable cleanup, and non-skipping native amd64/arm64 Linux
      OCI execution have
      named tests.
- [ ] The full repository test gate passes with the public conformance suite.
- [ ] Non-normative implementation notes cannot override this contract.
