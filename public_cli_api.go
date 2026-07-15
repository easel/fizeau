package fizeau

// public_cli_api.go re-exports the minimal set of types and helpers that the
// `cmd/fiz` CLI binary needs. These exist so the binary can stay behind a
// strict service-boundary import allowlist (see
// agentcli/service_boundary_test.go and approvedProductionInternalImports)
// while still using shared building blocks. Add re-exports here only when
// removing one would force the CLI to import an internal package directly.

import (
	"context"

	"github.com/easel/fizeau/internal/compaction"
	agentcore "github.com/easel/fizeau/internal/core"
	oaiProvider "github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/session"
	"github.com/easel/fizeau/internal/skill"
	"github.com/easel/fizeau/internal/tool"
	"github.com/easel/fizeau/internal/tool/anchorstore"
)

// Skill discovery and the load_skill tool.

type SkillCatalog = skill.Catalog

// ScanSkillsDir walks dir for SKILL.md files and returns the discovered
// catalog. A non-existent directory returns an empty catalog with no
// error so callers can opt in to skill discovery without branching.
func ScanSkillsDir(dir string) (*SkillCatalog, []string, error) {
	return skill.ScanDir(dir)
}

// NewLoadSkillTool returns a Tool exposing the catalog as the
// `load_skill` tool. Returns nil when the catalog is nil or empty so
// callers can append unconditionally.
func NewLoadSkillTool(cat *SkillCatalog) Tool {
	if cat == nil || cat.Len() == 0 {
		return nil
	}
	return &skill.LoadSkillTool{Catalog: cat}
}

// Compaction.

type CompactionConfig = compaction.Config

func DefaultCompactionConfig() CompactionConfig { return compaction.DefaultConfig() }

// Built-in tool wiring.

type BashOutputFilterConfig = tool.BashOutputFilterConfig
type AnchorStore = anchorstore.AnchorStore
type ReadTool = tool.ReadTool

func BuiltinToolsForPreset(workDir, preset string, bashFilter BashOutputFilterConfig) []Tool {
	return tool.BuiltinToolsForPreset(workDir, preset, bashFilter)
}

func NewAnchorStore() *AnchorStore {
	return anchorstore.New()
}

func NewReadTool(workDir string, anchors *AnchorStore) Tool {
	return &tool.ReadTool{WorkDir: workDir, AnchorStore: anchors}
}

func NewAnchorEditTool(workDir string, anchors *AnchorStore) Tool {
	return &tool.AnchorEditTool{WorkDir: workDir, AnchorStore: anchors}
}

// OpenAI-shaped model discovery and ranking.

type ScoredModel = oaiProvider.ScoredModel

func DiscoverModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	return oaiProvider.DiscoverModels(ctx, baseURL, apiKey)
}

func RankModels(candidates []string, knownModels map[string]string, pattern string) ([]ScoredModel, error) {
	return oaiProvider.RankModels(candidates, knownModels, pattern)
}

func NormalizeModelID(requested string, catalog []string) (string, error) {
	return oaiProvider.NormalizeModelID(requested, catalog)
}

// Session log inspection.

type (
	SessionEvent     = agentcore.Event
	SessionEventType = agentcore.EventType
	SessionStatus    = agentcore.Status
	SessionStartData = session.SessionStartData
	TokenUsage       = agentcore.TokenUsage
)

// SessionEndData is the root-owned public projection of a durable
// session.end record. Keep it wire-compatible with the internal persistence
// payload, while retaining public type identity for terminal classifications.
type SessionEndData struct {
	Status                 SessionStatus                  `json:"status"`
	Outcome                SessionOutcome                 `json:"outcome,omitempty"`
	Cause                  TerminalCause                  `json:"cause,omitempty"`
	Stage                  SessionStage                   `json:"stage,omitempty"`
	PrimaryOutcome         SessionOutcome                 `json:"primary_outcome,omitempty"`
	PrimaryCause           TerminalCause                  `json:"primary_cause,omitempty"`
	PrimaryStage           SessionStage                   `json:"primary_stage,omitempty"`
	Output                 string                         `json:"output"`
	Tokens                 TokenUsage                     `json:"tokens"`
	CostUSD                *float64                       `json:"cost_usd,omitempty"`
	DurationMs             int64                          `json:"duration_ms"`
	Model                  string                         `json:"model,omitempty"`
	SelectedProvider       string                         `json:"selected_provider,omitempty"`
	SelectedEndpoint       string                         `json:"selected_endpoint,omitempty"`
	SelectedServerInstance string                         `json:"selected_server_instance,omitempty"`
	SelectedRoute          string                         `json:"selected_route,omitempty"`
	Sticky                 ServiceRoutingStickyState      `json:"sticky,omitempty"`
	Utilization            ServiceRoutingUtilizationState `json:"utilization,omitempty"`
	RequestedHarness       string                         `json:"requested_harness,omitempty"`
	ResolvedHarness        string                         `json:"resolved_harness,omitempty"`
	HarnessSource          string                         `json:"harness_source,omitempty"`
	RequestedModel         string                         `json:"requested_model,omitempty"`
	ResolvedModel          string                         `json:"resolved_model,omitempty"`
	Reasoning              Reasoning                      `json:"reasoning,omitempty"`
	ReasoningIntent        Reasoning                      `json:"reasoning_intent,omitempty"`
	ReasoningEmitted       Reasoning                      `json:"reasoning_emitted,omitempty"`
	ResolvedReasoning      Reasoning                      `json:"resolved_reasoning,omitempty"`
	ReasoningSource        string                         `json:"reasoning_source,omitempty"`
	AttemptedProviders     []string                       `json:"attempted_providers,omitempty"`
	FailoverCount          int                            `json:"failover_count,omitempty"`
	Metadata               map[string]string              `json:"metadata,omitempty"`
	Error                  string                         `json:"error,omitempty"`
	ProcessOutcome         string                         `json:"process_outcome,omitempty"`
	CostCapUSD             *float64                       `json:"cost_cap_usd,omitempty"`
}

const (
	EventSessionStart = agentcore.EventSessionStart
	EventSessionEnd   = agentcore.EventSessionEnd
	StatusSuccess     = agentcore.StatusSuccess
)

func ReadSessionEvents(path string) ([]SessionEvent, error) {
	return session.ReadEvents(path)
}

// SessionLogger writes session log events. CLI tests construct one to seed
// log fixtures that the running CLI later reads back.
type SessionLogger = session.Logger

func NewSessionLogger(dir, sessionID string) *SessionLogger {
	return session.NewLogger(dir, sessionID)
}

func NewSessionEvent(sessionID string, seq int, eventType SessionEventType, data any) SessionEvent {
	return session.NewEvent(sessionID, seq, eventType, data)
}
