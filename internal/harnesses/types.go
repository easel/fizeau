package harnesses

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// AccountInfo captures provider account metadata from local auth files.
type AccountInfo struct {
	Email    string `json:"email,omitempty"`
	PlanType string `json:"plan_type,omitempty"`
	OrgName  string `json:"org_name,omitempty"`
}

// QuotaWindow captures one quota window (e.g. 5h, weekly, model-specific).
type QuotaWindow struct {
	Name          string  `json:"name"`               // e.g. "5h", "7d", "spark"
	LimitID       string  `json:"limit_id,omitempty"` // provider limit_id
	LimitName     string  `json:"limit_name,omitempty"`
	WindowMinutes int     `json:"window_minutes"`
	UsedPercent   float64 `json:"used_percent"`
	ResetsAt      string  `json:"resets_at,omitempty"`      // human-readable
	ResetsAtUnix  int64   `json:"resets_at_unix,omitempty"` // unix timestamp
	State         string  `json:"state"`
}

// QuotaStateFromUsedPercent maps a usage percentage to a quota state string.
func QuotaStateFromUsedPercent(usedPercent int) string {
	if usedPercent >= 95 {
		return "blocked"
	}
	if usedPercent >= 0 {
		return "ok"
	}
	return "unknown"
}

// ModelDiscoverySnapshot captures model and reasoning capability evidence for
// harnesses whose source of truth is a CLI/TUI surface instead of /v1/models.
type ModelDiscoverySnapshot struct {
	CapturedAt      time.Time `json:"captured_at"`
	Models          []string  `json:"models,omitempty"`
	ReasoningLevels []string  `json:"reasoning_levels,omitempty"`
	Source          string    `json:"source"`
	FreshnessWindow string    `json:"freshness_window,omitempty"`
	Detail          string    `json:"detail,omitempty"`
}

// EventType identifies the kind of event a harness emits during execution.
//
// The set is the closed union defined by CONTRACT-003 ("Event JSON shapes"):
// every backend (native + subprocess) emits these identically so the agent
// loop can multiplex them onto a single channel.
type EventType string

const (
	EventTypeTextDelta       EventType = "text_delta"
	EventTypeToolCall        EventType = "tool_call"
	EventTypeToolResult      EventType = "tool_result"
	EventTypeCompaction      EventType = "compaction"
	EventTypeProgress        EventType = "progress"
	EventTypeRoutingDecision EventType = "routing_decision"
	EventTypeStall           EventType = "stall"
	EventTypeFinal           EventType = "final"
)

// Event is the structured event a harness emits during Execute. It mirrors
// the shape defined in CONTRACT-003 §"Event JSON shapes". The Data field is
// a JSON-encoded payload whose schema is determined by Type.
type Event struct {
	Type     EventType         `json:"type"`
	Sequence int64             `json:"sequence"`
	Time     time.Time         `json:"time"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Data     json.RawMessage   `json:"data"`
}

// TextDeltaData is the payload for type=text_delta events.
type TextDeltaData struct {
	Text string `json:"text"`
}

// ToolCallData is the payload for type=tool_call events.
type ToolCallData struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolResultData is the payload for type=tool_result events.
type ToolResultData struct {
	ID         string `json:"id"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// SessionOutcome is the stable coarse result of one accepted Fizeau session.
// It is distinct from FinalData.Status, which remains a compatibility/detail
// field for older consumers.
type SessionOutcome string

const (
	SessionOutcomeSuccess   SessionOutcome = "success"
	SessionOutcomeFailed    SessionOutcome = "failed"
	SessionOutcomeCancelled SessionOutcome = "cancelled"
	SessionOutcomeTimedOut  SessionOutcome = "timed_out"
)

// TerminalCause is the stable, machine-readable reason a session ended.
type TerminalCause string

const (
	TerminalCauseCompleted        TerminalCause = "completed"
	TerminalCauseRouteUnavailable TerminalCause = "route_unavailable"
	TerminalCauseSpawnFailed      TerminalCause = "spawn_failed"
	TerminalCauseHarnessFailed    TerminalCause = "harness_failed"
	TerminalCauseProviderFailed   TerminalCause = "provider_failed"
	TerminalCauseToolLoopFailed   TerminalCause = "tool_loop_failed"
	TerminalCauseIterationLimit   TerminalCause = "iteration_limit"
	TerminalCauseBudgetHalted     TerminalCause = "budget_halted"
	TerminalCauseDeadlineExceeded TerminalCause = "deadline_exceeded"
	TerminalCauseContextCancelled TerminalCause = "context_cancelled"
	TerminalCauseCallerDied       TerminalCause = "caller_died"
	TerminalCauseCleanupFailed    TerminalCause = "cleanup_failed"
	TerminalCauseInternalError    TerminalCause = "internal_error"
)

// SessionStage identifies the Fizeau-owned lifecycle stage that determined a
// terminal result. It intentionally contains no outer workflow stages.
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

// FinalData is the payload for type=final events.
type FinalData struct {
	Status         string            `json:"status"` // success|iteration_limit|failed|stalled|timed_out|cancelled
	Outcome        SessionOutcome    `json:"outcome"`
	Cause          TerminalCause     `json:"cause"`
	Stage          SessionStage      `json:"stage"`
	PrimaryOutcome SessionOutcome    `json:"primary_outcome,omitempty"`
	PrimaryCause   TerminalCause     `json:"primary_cause,omitempty"`
	PrimaryStage   SessionStage      `json:"primary_stage,omitempty"`
	ExitCode       int               `json:"exit_code"`
	Error          string            `json:"error,omitempty"`
	FinalText      string            `json:"final_text,omitempty"`
	DurationMS     int64             `json:"duration_ms"`
	Usage          *FinalUsage       `json:"usage,omitempty"`
	Warnings       []FinalWarning    `json:"warnings,omitempty"`
	CostUSD        float64           `json:"cost_usd,omitempty"`
	SessionLogPath string            `json:"session_log_path,omitempty"`
	RoutingActual  *RoutingActual    `json:"routing_actual,omitempty"`
	Reasoning      *ReasoningActual  `json:"reasoning,omitempty"`
	Extra          map[string]string `json:"-"`
}

// FinalUsage carries token totals on a final event. Count fields are pointers
// so unavailable token dimensions are omitted instead of serialized as zero.
// A present pointer to 0 means the harness explicitly reported zero usage.
type FinalUsage struct {
	InputTokens      *int                  `json:"input_tokens,omitempty"`
	OutputTokens     *int                  `json:"output_tokens,omitempty"`
	CacheReadTokens  *int                  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int                  `json:"cache_write_tokens,omitempty"`
	CacheTokens      *int                  `json:"cache_tokens,omitempty"`
	ReasoningTokens  *int                  `json:"reasoning_tokens,omitempty"`
	TotalTokens      *int                  `json:"total_tokens,omitempty"`
	Source           string                `json:"source,omitempty"`
	Fresh            *bool                 `json:"fresh,omitempty"`
	CapturedAt       string                `json:"captured_at,omitempty"`
	Sources          []UsageSourceEvidence `json:"sources,omitempty"`
}

// FinalWarning is normalized metadata about non-fatal final-event issues.
type FinalWarning struct {
	Code    string                `json:"code"`
	Message string                `json:"message,omitempty"`
	Sources []UsageSourceEvidence `json:"sources,omitempty"`
}

// UsageSourceEvidence records one usage source considered by the resolver.
type UsageSourceEvidence struct {
	Source     string            `json:"source"`
	Fresh      *bool             `json:"fresh,omitempty"`
	CapturedAt string            `json:"captured_at,omitempty"`
	Usage      *UsageTokenCounts `json:"usage,omitempty"`
	Warning    string            `json:"warning,omitempty"`
}

// UsageTokenCounts is the normalized token-count vocabulary shared by
// subprocess harnesses and CONTRACT-003 final metadata.
type UsageTokenCounts struct {
	InputTokens      *int `json:"input_tokens,omitempty"`
	OutputTokens     *int `json:"output_tokens,omitempty"`
	CacheReadTokens  *int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int `json:"cache_write_tokens,omitempty"`
	CacheTokens      *int `json:"cache_tokens,omitempty"`
	ReasoningTokens  *int `json:"reasoning_tokens,omitempty"`
	TotalTokens      *int `json:"total_tokens,omitempty"`
}

// Any reports whether at least one token dimension is known.
func (c UsageTokenCounts) Any() bool {
	return c.InputTokens != nil ||
		c.OutputTokens != nil ||
		c.CacheReadTokens != nil ||
		c.CacheWriteTokens != nil ||
		c.CacheTokens != nil ||
		c.ReasoningTokens != nil ||
		c.TotalTokens != nil
}

func IntPtr(v int) *int {
	return &v
}

func BoolPtr(v bool) *bool {
	return &v
}

// RoutingActual captures the resolved fallback chain on a final event.
type RoutingActual struct {
	Harness            string   `json:"harness"`
	Provider           string   `json:"provider,omitempty"`
	ServerInstance     string   `json:"server_instance,omitempty"`
	Model              string   `json:"model"`
	FallbackChainFired []string `json:"fallback_chain_fired,omitempty"`
	FailureClass       string   `json:"failure_class,omitempty"`
	// Power is the catalog-projected power of the actually-dispatched
	// Model. 0 means unknown/exact-pin-only/no catalog entry.
	Power int `json:"power,omitempty"`
}

type ReasoningActual struct {
	Harness            string   `json:"harness,omitempty"`
	RequestedReasoning string   `json:"requested_reasoning,omitempty"`
	ResolvedReasoning  string   `json:"resolved_reasoning,omitempty"`
	Source             string   `json:"source,omitempty"`
	DiscoverySource    string   `json:"discovery_source,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	Warning            string   `json:"warning,omitempty"`
	SupportedReasoning []string `json:"supported_reasoning,omitempty"`
}

// HarnessInfo describes a registered harness. Mirrors the public
// HarnessInfo type defined in CONTRACT-003. Internal callers use this to
// implement the public ListHarnesses surface without re-declaring the shape.
type HarnessInfo struct {
	Name                 string
	Type                 string // "native" | "subprocess"
	Available            bool
	Path                 string
	Error                string
	IsLocal              bool
	IsSubscription       bool
	AutoRoutingEligible  bool
	ExactPinSupport      bool
	DefaultModel         string
	SupportedPermissions []string
	SupportedReasoning   []string
	CostClass            string
}

// ExecuteRequest is the internal request carried into Harness.Execute. It
// is intentionally narrower than the public ExecuteRequest in CONTRACT-003:
// the agent's routing layer is expected to resolve provider/model/reasoning
// /permissions/timeouts before invoking a harness, so the harness sees a
// concrete, ready-to-run request.
type ExecuteRequest struct {
	// Prompt is the resolved user prompt sent to the model.
	Prompt string

	// SystemPrompt is the resolved system prompt; empty means harness default.
	SystemPrompt string

	// Provider is the resolved provider identifier when applicable. May be
	// empty for harnesses that have no provider concept (e.g. claude CLI).
	Provider string

	// Model is the resolved model identifier; empty means harness default.
	Model string

	// WorkDir is the working directory for tool operations. Required when
	// the chosen harness uses tools.
	WorkDir string

	// Permissions is "safe" | "supervised" | "unrestricted". Empty defaults to "safe".
	Permissions string

	// Temperature is the model sampling temperature requested by the caller.
	// Harness adapters may ignore it when their CLI has no equivalent control.
	Temperature float32

	// Seed is the requested sampling seed. Zero means unset/provider chooses.
	// Harness adapters may ignore it when their CLI has no equivalent control.
	Seed int64

	// Reasoning is the normalized public reasoning scalar. Empty/off means no
	// adapter flag should be emitted.
	Reasoning string

	// Timeout is the wall-clock cap for the entire request. 0 disables.
	Timeout time.Duration

	// IdleTimeout is the streaming-quiet cap. 0 uses harness default.
	IdleTimeout time.Duration

	// SessionLogDir overrides the per-run session-log directory; harness
	// uses this to direct progress traces into a per-bundle evidence dir.
	SessionLogDir string

	// SessionID is a stable identifier for the run, used in progress log
	// filenames and event metadata. Empty means the harness generates one.
	SessionID string

	// LifecycleStateDir is the service-selected directory for durable process
	// ownership records. It is separate from the human-readable session log.
	LifecycleStateDir string

	// CleanupTimeout is the service-owned deadline for stopping and proving the
	// per-invocation containment boundary empty.
	CleanupTimeout time.Duration

	// Metadata is echoed back into Event.Metadata (e.g. bead_id, attempt_id).
	Metadata map[string]string
}

// Harness is the internal contract every harness implementation in
// internal/harnesses/<name> satisfies. It is the minimal surface the agent
// dispatcher needs to route a resolved request into a backend.
//
// A Harness is responsible for emitting events on the returned channel until
// execution completes; the channel MUST be closed after the final event so
// downstream consumers can detect end-of-stream. The final event is always
// of type EventTypeFinal.
type Harness interface {
	// Info returns identity + capability metadata for this harness.
	Info() HarnessInfo

	// HealthCheck triggers a fresh probe (binary present, auth ok, etc.)
	// and returns nil if the harness is ready to execute.
	HealthCheck(ctx context.Context) error

	// Execute runs one resolved request. Events stream on the returned
	// channel; a single final event closes the stream. The first error
	// return is reserved for setup failures (binary missing, etc.) — once
	// the channel is returned, all per-run failures are reported via a
	// final event with Status != "success".
	Execute(ctx context.Context, req ExecuteRequest) (<-chan Event, error)
}

// QuotaStateValue is the normalized state enumeration consumed by
// CONTRACT-004 sub-interfaces. Only QuotaOK and QuotaStale carry
// routing-usable signal; other values MUST NOT result in
// RoutingPreferenceAvailable.
type QuotaStateValue string

const (
	QuotaOK              QuotaStateValue = "ok"
	QuotaStale           QuotaStateValue = "stale"
	QuotaBlocked         QuotaStateValue = "blocked"
	QuotaUnavailable     QuotaStateValue = "unavailable"
	QuotaUnauthenticated QuotaStateValue = "unauthenticated"
	QuotaUnknown         QuotaStateValue = "unknown"
)

// RoutingPreference is the routing layer's consumable signal indicating
// whether a harness should be preferred given its current quota evidence.
// It is an internal routing signal — never projected into the public
// CONTRACT-003 surface.
type RoutingPreference int

const (
	RoutingPreferenceUnknown RoutingPreference = iota
	RoutingPreferenceAvailable
	RoutingPreferenceBlocked
)

// QuotaStatus is the universal quota report defined by CONTRACT-004.
// Each harness's private snapshot type projects into this; the private
// snapshot is never exposed across package boundaries.
type QuotaStatus struct {
	// Source identifies how the underlying evidence was captured:
	// "pty", "cache", "session-token-count", "cli", "api".
	Source string

	// CapturedAt is when the underlying evidence was observed (not when
	// this status struct was assembled).
	CapturedAt time.Time

	// Fresh reports whether CapturedAt is within QuotaFreshness() at the
	// time of the call.
	Fresh bool

	// Age is now - CapturedAt at the time of the call.
	Age time.Duration

	// State is the normalized state. Only QuotaOK and QuotaStale carry
	// routing-usable signal.
	State QuotaStateValue

	// Windows captures per-window evidence (5h, weekly, tier-specific).
	// Authoritative for any structured fact the routing layer or
	// operator surfaces consume — including tier breakdowns.
	Windows []QuotaWindow

	// Account is the account/plan/auth evidence captured alongside
	// quota. Nil when the harness has no concept of account or when
	// account evidence is delivered through AccountHarness only.
	Account *AccountSnapshot

	// RoutingPreference indicates whether the routing layer should
	// prefer this harness given the current evidence.
	RoutingPreference RoutingPreference

	// Reason is a short human-readable explanation of State and
	// RoutingPreference — surfaced in operator views and routing logs.
	Reason string

	// Detail is harness-specific opaque metadata for diagnostic display
	// only. Service code MAY surface it verbatim in operator views;
	// service code MUST NOT branch on its keys or values for routing
	// decisions.
	Detail map[string]string
}

// AccountSnapshot is the universal account/auth report defined by
// CONTRACT-004. Projects onto the public AccountStatus type defined in
// CONTRACT-003.
type AccountSnapshot struct {
	Authenticated   bool
	Unauthenticated bool
	Email           string
	PlanType        string
	OrgName         string
	Source          string // file path, env var name, "cache", "cli"
	CapturedAt      time.Time
	Fresh           bool
	Detail          string // free-form diagnostic detail
}

// ErrAliasNotResolvable is returned by ModelDiscoveryHarness.ResolveModelAlias
// when the requested family is not recognized or the supplied discovery
// snapshot has no matching concrete model.
var ErrAliasNotResolvable = errors.New("model alias not resolvable from snapshot")

// ErrModelDiscoveryEvidenceMissing is returned by ModelDiscoveryHarness.DefaultModelSnapshot
// when live evidence cannot be obtained (PTY failure, parse failure, etc.) and
// no static fallback is available per the no-static-fallback principle.
var ErrModelDiscoveryEvidenceMissing = errors.New("model discovery evidence missing")

// QuotaHarness is implemented by harnesses that own a subscription or
// quota window. See CONTRACT-004 for the full normative contract.
type QuotaHarness interface {
	Harness

	// QuotaStatus returns the current quota state from the harness's
	// owned cache, with Fresh/Age computed against now. MUST be cheap
	// (no live probe) and safe to call on every routing decision.
	// Absence of evidence is reported via State=QuotaUnavailable on a
	// valid QuotaStatus value; the error return is reserved for call
	// failure (ctx cancelled, IO failure, lock acquisition failure).
	QuotaStatus(ctx context.Context, now time.Time) (QuotaStatus, error)

	// RefreshQuota drives the harness's live probe, persists the
	// result through the harness's owned cache, and returns the
	// resulting status. Single-flight per harness instance via the
	// harness's cache lock; concurrent callers block. Probe failure
	// is reported as a QuotaStatus with State=QuotaUnavailable (or
	// QuotaUnauthenticated for auth-related failures), not as an
	// error. The error return is reserved for call failure.
	RefreshQuota(ctx context.Context) (QuotaStatus, error)

	// QuotaFreshness returns the harness's freshness window (e.g. 15m).
	// Constant for the harness; cheap to call.
	QuotaFreshness() time.Duration

	// SupportedLimitIDs returns the harness's stable set of emitted
	// Windows[].LimitID values. Constant for the harness; the
	// conformance suite reads this value to verify that emitted
	// Windows[].LimitID strings are a subset of this set. Empty
	// slice is allowed for harnesses that emit no windows.
	SupportedLimitIDs() []string
}

// AccountHarness is implemented by harnesses that expose authentication
// or account state independent of quota. See CONTRACT-004 for the full
// normative contract.
type AccountHarness interface {
	Harness

	// AccountStatus returns the harness's current account/auth state.
	// Cheap; reads cached evidence only. Absence of evidence is
	// reported via AccountSnapshot fields on a valid snapshot; the
	// error return is reserved for call failure.
	AccountStatus(ctx context.Context, now time.Time) (AccountSnapshot, error)

	// RefreshAccount drives the harness's account probe and persists
	// the result. Single-flight per harness instance; concurrent
	// callers block. Probe failure is reported via AccountSnapshot
	// fields, not as an error.
	RefreshAccount(ctx context.Context) (AccountSnapshot, error)

	// AccountFreshness returns the harness's account freshness window
	// (e.g. 7 days for gemini). Constant for the harness; cheap.
	AccountFreshness() time.Duration
}

// ModelDiscoveryHarness is implemented by harnesses whose model surface
// extends beyond a single Info().DefaultModel — i.e. they support family
// aliases (sonnet, gpt, gemini) that resolve through discovery evidence.
// See CONTRACT-004 for the full normative contract.
type ModelDiscoveryHarness interface {
	Harness

	// DefaultModelSnapshot returns the harness's seed/fallback
	// discovery snapshot. Used to bootstrap the catalog before the
	// first live refresh lands. Per the no-static-fallback principle,
	// returns ErrModelDiscoveryEvidenceMissing when live evidence cannot
	// be obtained; never returns a cached or literal fallback.
	DefaultModelSnapshot() (ModelDiscoverySnapshot, error)

	// ResolveModelAlias maps a family-style requested model to a
	// concrete model ID using the provided discovery snapshot.
	// Returns ErrAliasNotResolvable if the family is not recognized
	// or the snapshot has no matching concrete model.
	ResolveModelAlias(family string, snapshot ModelDiscoverySnapshot) (string, error)

	// SupportedAliases returns the harness's stable set of family
	// aliases ResolveModelAlias recognizes. Constant for the harness;
	// the conformance suite uses this value to verify
	// ResolveModelAlias covers each documented family (positive path)
	// and rejects out-of-set families with ErrAliasNotResolvable
	// (negative path). Empty slice is allowed for harnesses that
	// recognize no family aliases.
	SupportedAliases() []string
}

// ContextModelDiscoveryHarness is the optional cancellation-aware extension
// to ModelDiscoveryHarness. Service-owned refreshers prefer this interface so
// their exact lifecycle context reaches live model-discovery probes. The
// context-free DefaultModelSnapshot method remains available for legacy
// callers and harness implementations.
type ContextModelDiscoveryHarness interface {
	ModelDiscoveryHarness

	// DefaultModelSnapshotWithContext returns live discovery evidence while
	// honoring ctx. Implementations must not replace ctx with a background
	// context, and must finish process cleanup before returning cancellation.
	DefaultModelSnapshotWithContext(ctx context.Context) (ModelDiscoverySnapshot, error)
}

// PortableRuntimeTarget is the internal Linux same-platform preparation
// target. Portable runtime v0.15 does not support cross-platform packaging.
type PortableRuntimeTarget struct {
	GOOS   string
	GOARCH string
}

// PortableRuntimeTransport is the actual execution transport represented by a
// portable-runtime inventory row. It is independent from historical registry
// type labels.
type PortableRuntimeTransport string

// PortableRuntimeInclusion explains whether a registry row contributes a
// subprocess closure to an unpinned portable runtime.
type PortableRuntimeInclusion string

// PortableRuntimeStructuralMode is the explicit route-shape capability of an
// actual runner, independent of model discovery and registry defaults.
type PortableRuntimeStructuralMode string

const (
	PortableRuntimeTransportSubprocess PortableRuntimeTransport = "subprocess"
	PortableRuntimeTransportNative     PortableRuntimeTransport = "native"
	PortableRuntimeTransportEmbedded   PortableRuntimeTransport = "embedded"
	PortableRuntimeTransportHTTP       PortableRuntimeTransport = "http"

	PortableRuntimeInclusionRequired      PortableRuntimeInclusion = "required"
	PortableRuntimeInclusionNonSubprocess PortableRuntimeInclusion = "non_subprocess"
	PortableRuntimeInclusionExactPinOnly  PortableRuntimeInclusion = "exact_pin_only"
	PortableRuntimeInclusionTestOnly      PortableRuntimeInclusion = "test_only"

	PortableRuntimeStructuralUnpinned      PortableRuntimeStructuralMode = "unpinned"
	PortableRuntimeStructuralExactPinOnly  PortableRuntimeStructuralMode = "exact_pin_only"
	PortableRuntimeStructuralNonSubprocess PortableRuntimeStructuralMode = "non_subprocess"
)

// PortableRuntimeSurface is one deterministic registry-to-instance join row.
// Instance is the exact service-owned runner object for subprocess/native
// harnesses; non-subprocess registry rows have a nil Instance.
type PortableRuntimeSurface struct {
	Name      string
	Transport PortableRuntimeTransport
	Inclusion PortableRuntimeInclusion
	Instance  Harness
}

// PortableRuntimeStructure is the side-effect-free structural description of
// one actual runner instance.
type PortableRuntimeStructure struct {
	Name      string
	Transport PortableRuntimeTransport
	Mode      PortableRuntimeStructuralMode
}

// PortableRuntimeStructuralHarness is implemented by every actual production
// runner instance that participates in the registry join.
type PortableRuntimeStructuralHarness interface {
	PortableRuntimeStructure() PortableRuntimeStructure
}

type PortableRuntimeAssetKind string
type PortableRuntimePathKind string
type PortableRuntimeClosureClass string
type PortableRuntimeGuestPathScope string
type PortableRuntimeEnvironmentConstraintKind string

const (
	PortableRuntimeAssetExecutable  PortableRuntimeAssetKind = "executable"
	PortableRuntimeAssetInstallTree PortableRuntimeAssetKind = "install_tree"
	PortableRuntimeAssetConfig      PortableRuntimeAssetKind = "config"
	PortableRuntimeAssetCredential  PortableRuntimeAssetKind = "credential"
	PortableRuntimeAssetQuota       PortableRuntimeAssetKind = "quota"
	PortableRuntimeAssetCache       PortableRuntimeAssetKind = "cache"
	PortableRuntimeAssetSupport     PortableRuntimeAssetKind = "runtime_support"

	PortableRuntimePathFile PortableRuntimePathKind = "file"
	PortableRuntimePathTree PortableRuntimePathKind = "tree"

	PortableRuntimeClosureStatic      PortableRuntimeClosureClass = "static"
	PortableRuntimeClosureDynamic     PortableRuntimeClosureClass = "dynamic"
	PortableRuntimeClosureInterpreted PortableRuntimeClosureClass = "interpreted"

	PortableRuntimeGuestPathRuntime PortableRuntimeGuestPathScope = "runtime"
	PortableRuntimeGuestPathHome    PortableRuntimeGuestPathScope = "home"
	PortableRuntimeGuestPathConfig  PortableRuntimeGuestPathScope = "config"
	PortableRuntimeGuestPathData    PortableRuntimeGuestPathScope = "data"
	PortableRuntimeGuestPathCache   PortableRuntimeGuestPathScope = "cache"
	PortableRuntimeGuestPathState   PortableRuntimeGuestPathScope = "state"
	PortableRuntimeGuestPathTmp     PortableRuntimeGuestPathScope = "tmp"

	PortableRuntimeEnvironmentFixedTrue   PortableRuntimeEnvironmentConstraintKind = "fixed_true"
	PortableRuntimeEnvironmentFixedFalse  PortableRuntimeEnvironmentConstraintKind = "fixed_false"
	PortableRuntimeEnvironmentGuestPath   PortableRuntimeEnvironmentConstraintKind = "guest_path"
	PortableRuntimeEnvironmentUnset       PortableRuntimeEnvironmentConstraintKind = "unset"
	PortableRuntimeEnvironmentRuntimePath PortableRuntimeEnvironmentConstraintKind = "runtime_path"
)

// PortableRuntimeAsset is one harness-owned member of a verified executable
// or state closure. Source remains internal and may be sensitive. Target is a
// clean slash-relative path below the portable runtime guest root.
type PortableRuntimeAsset struct {
	Kind          PortableRuntimeAssetKind
	PathKind      PortableRuntimePathKind
	Source        string
	Target        string
	ContentSHA256 string
	Executable    bool
}

// PortableRuntimeLaunch is a guest-relative executable recipe. All targets
// are slash-relative beneath the portable runtime guest root.
type PortableRuntimeLaunch struct {
	EntrypointTarget   string
	InterpreterTarget  string
	LoaderTarget       string
	RuntimeArgs        []string
	LibraryRootTargets []string
}

// PortableRuntimeEnvironment carries an inherited variable name only. It
// cannot represent a value or a name=value assignment.
type PortableRuntimeEnvironment struct {
	Name string
}

// PortableRuntimeGuestPath identifies a path beneath one activation-owned
// guest root. Target is slash-relative and never carries a host path.
type PortableRuntimeGuestPath struct {
	Scope  PortableRuntimeGuestPathScope
	Target string
}

// PortableRuntimeEnvironmentConstraint declares one activation-owned
// environment treatment without carrying a raw environment value.
type PortableRuntimeEnvironmentConstraint struct {
	Name      string
	Kind      PortableRuntimeEnvironmentConstraintKind
	GuestPath PortableRuntimeGuestPath
}

// PortableRuntimeExecutionConstraints is harness-declared execution evidence.
// Activation interprets it generically after materialization persists it.
type PortableRuntimeExecutionConstraints struct {
	Environment         []PortableRuntimeEnvironmentConstraint
	ReadOnlyPaths       []PortableRuntimeGuestPath
	RequiredAbsentPaths []PortableRuntimeGuestPath
	FixedArguments      []string
}

type PortableRuntimeContribution struct {
	ClosureClass         PortableRuntimeClosureClass
	Launch               PortableRuntimeLaunch
	Assets               []PortableRuntimeAsset
	Environment          []PortableRuntimeEnvironment
	ExecutionConstraints PortableRuntimeExecutionConstraints
}

// PortableRuntimeHarness is the optional harness-owned asset-discovery
// capability. It is read-only: it does not materialize files or start work.
type PortableRuntimeHarness interface {
	Harness

	PortableRuntimeAssets(context.Context, PortableRuntimeTarget) (PortableRuntimeContribution, error)
}

var (
	ErrPortableRuntimeTargetUnsupported = errors.New("portable runtime target unsupported")
	ErrPortableRuntimeClosureIncomplete = errors.New("portable runtime asset closure incomplete")
)
