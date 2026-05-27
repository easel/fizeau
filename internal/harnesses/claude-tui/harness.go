package claudetui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/anthropic"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

// ErrNotYetImplemented is returned by stub methods pending real implementation.
var ErrNotYetImplemented = errors.New("claude-tui harness: not yet implemented")

// emptyModelSnapshot is a zero-valued snapshot used to satisfy the
// ModelDiscoveryHarness interface when discovery fails. Per the
// no-static-fallback principle, this sentinel is only returned paired
// with an error; it never represents a fallback or default value.
var emptyModelSnapshot harnesses.ModelDiscoverySnapshot

// Harness is the sentinel harness for claude TUI.
// It satisfies the harnesses.Harness, harnesses.QuotaHarness,
// harnesses.AccountHarness, and harnesses.ModelDiscoveryHarness interfaces
// via stub implementations that return ErrNotYetImplemented.
type Harness struct {
}

// Info implements harnesses.Harness.
func (h *Harness) Info() harnesses.HarnessInfo {
	return harnesses.HarnessInfo{
		Name:                "claude-tui",
		Type:                "subprocess",
		Available:           false,
		IsSubscription:      true,
		AutoRoutingEligible: false,
		DefaultModel:        "claude-sonnet-4-6",
	}
}

// HealthCheck implements harnesses.Harness.
func (h *Harness) HealthCheck(ctx context.Context) error {
	return ErrNotYetImplemented
}

// Execute implements harnesses.Harness.
func (h *Harness) Execute(ctx context.Context, req harnesses.ExecuteRequest) (<-chan harnesses.Event, error) {
	eventChan := make(chan harnesses.Event, 100)
	go h.runTurn(ctx, req, eventChan)
	return eventChan, nil
}

// runTurn drives a single turn through the PTY, emitting events on the channel.
func (h *Harness) runTurn(ctx context.Context, req harnesses.ExecuteRequest, eventChan chan harnesses.Event) {
	defer close(eventChan)

	startTime := time.Now()
	seq := int64(0)

	// Get or create a pooled session for this workdir
	workdir := req.WorkDir
	if workdir == "" {
		workdir = "."
	}

	s, err := getOrCreateSession(
		ctx,
		"claude",
		nil,
		workdir,
		nil,
		session.Size{Rows: 50, Cols: 220},
	)
	if err != nil {
		seq++
		eventChan <- harnesses.Event{
			Type:     harnesses.EventTypeFinal,
			Sequence: seq,
			Time:     time.Now(),
			Data: marshalData(harnesses.FinalData{
				Status:     "error",
				DurationMS: time.Since(startTime).Milliseconds(),
				ExitCode:   1,
			}),
		}
		return
	}

	// Issue /clear between turns to reset session state
	if err := clearSession(s, "❯", 5); err != nil {
		seq++
		eventChan <- harnesses.Event{
			Type:     harnesses.EventTypeFinal,
			Sequence: seq,
			Time:     time.Now(),
			Data: marshalData(harnesses.FinalData{
				Status:     "error",
				DurationMS: time.Since(startTime).Milliseconds(),
				ExitCode:   1,
			}),
		}
		return
	}

	// Emit a Final event indicating successful session reuse
	seq++
	eventChan <- harnesses.Event{
		Type:     harnesses.EventTypeFinal,
		Sequence: seq,
		Time:     time.Now(),
		Data: marshalData(harnesses.FinalData{
			Status:     "success",
			DurationMS: time.Since(startTime).Milliseconds(),
			ExitCode:   0,
		}),
	}
}

// marshalData encodes data as JSON.
func marshalData(data interface{}) json.RawMessage {
	b, _ := json.Marshal(data)
	return b
}

// QuotaStatus implements harnesses.QuotaHarness.
func (h *Harness) QuotaStatus(ctx context.Context, now time.Time) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// RefreshQuota implements harnesses.QuotaHarness.
func (h *Harness) RefreshQuota(ctx context.Context) (harnesses.QuotaStatus, error) {
	return harnesses.QuotaStatus{
		State: harnesses.QuotaUnavailable,
	}, ErrNotYetImplemented
}

// QuotaFreshness implements harnesses.QuotaHarness.
func (h *Harness) QuotaFreshness() time.Duration {
	return 15 * time.Minute
}

// SupportedLimitIDs implements harnesses.QuotaHarness.
func (h *Harness) SupportedLimitIDs() []string {
	return nil
}

// AccountStatus implements harnesses.AccountHarness.
func (h *Harness) AccountStatus(ctx context.Context, now time.Time) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// RefreshAccount implements harnesses.AccountHarness.
func (h *Harness) RefreshAccount(ctx context.Context) (harnesses.AccountSnapshot, error) {
	return harnesses.AccountSnapshot{}, ErrNotYetImplemented
}

// AccountFreshness implements harnesses.AccountHarness.
func (h *Harness) AccountFreshness() time.Duration {
	return 24 * time.Hour
}

// DefaultModelSnapshot implements harnesses.ModelDiscoveryHarness.
// Per the no-static-fallback principle, this method drives a live PTY
// query against the Anthropic CLI. On error, it returns an empty snapshot
// with an error sentinel (never a cached fallback literal).
func (h *Harness) DefaultModelSnapshot() (harnesses.ModelDiscoverySnapshot, error) {
	snapshot, err := readClaudeTuiModelDiscoveryViaPTY(context.Background(), 30*time.Second)
	if err != nil {
		return emptyModelSnapshot, err
	}
	if len(snapshot.Models) == 0 {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

// ResolveModelAlias implements harnesses.ModelDiscoveryHarness.
func (h *Harness) ResolveModelAlias(family string, snapshot harnesses.ModelDiscoverySnapshot) (string, error) {
	return "", harnesses.ErrAliasNotResolvable
}

// Shutdown enumerates live PTY sessions in the pool and reaps each one
// within a bounded timeout, sending SIGTERM and escalating to SIGKILL
// if the process does not exit cleanly.
func (h *Harness) Shutdown(ctx context.Context) error {
	const defaultTimeout = 10 * time.Second

	// Extract deadline from context or use a default timeout
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultTimeout)
	}

	sessions := getLiveSessionsSnapshot()
	if len(sessions) == 0 {
		return nil
	}

	// Distribute remaining time across sessions
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = 100 * time.Millisecond
	}
	perSessionTimeout := remaining / time.Duration(len(sessions))
	if perSessionTimeout < 100*time.Millisecond {
		perSessionTimeout = 100 * time.Millisecond
	}

	for _, s := range sessions {
		if time.Until(deadline) <= 0 {
			break
		}
		sessionCtx, cancel := context.WithTimeout(context.Background(), perSessionTimeout)
		_ = reapSession(sessionCtx, s)
		cancel()
	}

	return nil
}

// SupportedAliases implements harnesses.ModelDiscoveryHarness.
func (h *Harness) SupportedAliases() []string {
	return nil
}

// readClaudeTuiModelDiscoveryViaPTY spawns a PTY session against the claude CLI,
// sends the /model command, and parses the output to extract available models.
// Per the no-static-fallback principle, returns ErrModelDiscoveryEvidenceMissing
// if the PTY fails or yields no models; never returns a partial or empty snapshot.
func readClaudeTuiModelDiscoveryViaPTY(ctx context.Context, timeout time.Duration) (harnesses.ModelDiscoverySnapshot, error) {
	// Variable to capture the snapshot once discovery succeeds.
	var snapshot harnesses.ModelDiscoverySnapshot
	var discoveryErr error

	_, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:  "claude-tui",
		Binary:       "claude",
		Args:         nil,
		Workdir:      "",
		Env:          nil,
		Command:      "/model\r",
		ReadyMarkers: []string{"❯", "> "},
		DoneWhen:     claudeTuiModelDiscoveryComplete,
		Timeout:      timeout,
		Size:         session.Size{Rows: 50, Cols: 220},
		Discovery: func(text string) (cassette.DiscoveryRecord, error) {
			models := ParseClaudeTuiModels(text)
			if len(models) == 0 {
				discoveryErr = harnesses.ErrModelDiscoveryEvidenceMissing
				return cassette.DiscoveryRecord{}, fmt.Errorf("no models found in /model output")
			}
			// Build snapshot only after confirming we have models.
			snapshot.CapturedAt = time.Now().UTC()
			snapshot.FreshnessWindow = (24 * time.Hour).String()
			snapshot.Source = "pty"
			snapshot.Models = models
			snapshot.ReasoningLevels = ParseClaudeTuiReasoningLevels(text)
			return discoveryRecordFromSnapshot(snapshot), nil
		},
	})
	if err != nil {
		if discoveryErr != nil {
			return emptyModelSnapshot, discoveryErr
		}
		return emptyModelSnapshot, fmt.Errorf("model discovery PTY: %w", err)
	}
	if len(snapshot.Models) == 0 {
		return emptyModelSnapshot, harnesses.ErrModelDiscoveryEvidenceMissing
	}
	return snapshot, nil
}

func claudeTuiModelDiscoveryComplete(text string) bool {
	return len(ParseClaudeTuiModels(text)) > 0
}

var (
	claudeFullModelPattern         = regexp.MustCompile(`\bclaude-[a-z0-9][a-z0-9._-]*\b`)
	claudeFullFamilyVersionPattern = regexp.MustCompile(`\bclaude-(sonnet|opus|haiku)-([0-9]+)[.-]([0-9]{1,2})(?:\b|-)`)
	claudeFamilyVersionPattern     = regexp.MustCompile(`\b(?:claude\s+)?(sonnet|opus|haiku)\s+([0-9]+(?:[.-][0-9]+){0,2})\b`)
	claudeAliasPattern             = regexp.MustCompile(`(?m)(?:^|[\s'"])(sonnet|opus|haiku)(?:$|[\s'"])`)
	claudeEffortPattern            = regexp.MustCompile(`--effort\s+<level>.*\(([^)]*)\)`)
)

// ParseClaudeTuiModels extracts available model names from claude /model output.
func ParseClaudeTuiModels(text string) []string {
	text = anthropic.StripANSI(strings.ReplaceAll(text, "\r\n", "\n"))
	lower := strings.ToLower(text)
	models := uniqueMatches(claudeFullModelPattern.FindAllString(lower, -1))
	for _, match := range claudeFullFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 3 {
			models = appendUniqueString(models, match[1]+"-"+match[2]+"."+match[3])
		}
	}
	for _, match := range claudeFamilyVersionPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 2 {
			models = appendUniqueString(models, match[1]+"-"+strings.ReplaceAll(match[2], "-", "."))
		}
	}
	for _, match := range claudeAliasPattern.FindAllStringSubmatch(lower, -1) {
		if len(match) > 1 {
			models = appendUniqueString(models, match[1])
		}
	}
	return models
}

// ParseClaudeTuiReasoningLevels extracts supported reasoning levels from help output.
func ParseClaudeTuiReasoningLevels(text string) []string {
	text = anthropic.StripANSI(strings.ReplaceAll(text, "\n", " "))
	m := claudeEffortPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return nil
	}
	parts := strings.Split(m[1], ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = appendUniqueString(out, strings.TrimSpace(part))
	}
	return out
}

func discoveryRecordFromSnapshot(snapshot harnesses.ModelDiscoverySnapshot) cassette.DiscoveryRecord {
	return cassette.DiscoveryRecord{
		Source:            snapshot.Source,
		Status:            string(ptyquota.StatusOK),
		Models:            append([]string(nil), snapshot.Models...),
		ReasoningLevels:   append([]string(nil), snapshot.ReasoningLevels...),
		CapturedAt:        snapshot.CapturedAt.UTC().Format(time.RFC3339),
		FreshnessWindow:   snapshot.FreshnessWindow,
		StalenessBehavior: "stale model discovery evidence requires authenticated PTY refresh before capability promotion",
		Metadata:          map[string]any{"detail": snapshot.Detail},
	}
}

func uniqueMatches(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueString(out, value)
	}
	return out
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// Compile-time interface satisfaction assertions per CONTRACT-004.
var (
	_ harnesses.Harness               = (*Harness)(nil)
	_ harnesses.QuotaHarness          = (*Harness)(nil)
	_ harnesses.AccountHarness        = (*Harness)(nil)
	_ harnesses.ModelDiscoveryHarness = (*Harness)(nil)
)
