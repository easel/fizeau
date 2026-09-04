package claude

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

type quotaPTYOptions struct {
	binary        string
	binaryVersion string
	args          []string
	workdir       string
	env           []string
	cassetteDir   string
}

type QuotaPTYOption func(*quotaPTYOptions)

func WithQuotaPTYCommand(binary string, args ...string) QuotaPTYOption {
	return func(opts *quotaPTYOptions) {
		opts.binary = binary
		opts.args = append([]string(nil), args...)
	}
}

func WithQuotaPTYWorkdir(workdir string) QuotaPTYOption {
	return func(opts *quotaPTYOptions) {
		opts.workdir = workdir
	}
}

func WithQuotaPTYEnv(env ...string) QuotaPTYOption {
	return func(opts *quotaPTYOptions) {
		opts.env = append([]string(nil), env...)
	}
}

func WithQuotaPTYCassetteDir(dir string) QuotaPTYOption {
	return func(opts *quotaPTYOptions) {
		opts.cassetteDir = dir
	}
}

func readClaudeQuotaViaPTY(timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
	windows, account, _, err := captureClaudeQuotaViaPTY(context.Background(), timeout, opts...)
	return windows, account, err
}

func refreshClaudeQuotaViaPTY(timeout time.Duration, opts ...QuotaPTYOption) (claudeQuotaSnapshot, error) {
	windows, account, _, err := captureClaudeQuotaViaPTY(context.Background(), timeout, opts...)
	if err != nil {
		return claudeQuotaSnapshot{}, err
	}
	return claudeQuotaSnapshotFromWindows(windows, account), nil
}

func readClaudeQuotaFromCassette(dir string) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, error) {
	reader, err := cassette.Open(dir)
	if err != nil {
		return nil, nil, err
	}
	text := reader.Final().FinalText
	if text == "" {
		frames := reader.Frames()
		if len(frames) > 0 {
			text = stringsJoinLines(frames[len(frames)-1].Text)
		}
	}
	windows, account := parseClaudeUsageOutput(text)
	if len(windows) == 0 {
		return nil, account, fmt.Errorf("no quota windows found in claude quota cassette")
	}
	if err := validateClaudeQuotaEvidence(windows, account); err != nil {
		return nil, account, fmt.Errorf("incomplete claude quota cassette: %w", err)
	}
	return windows, account, nil
}

func captureClaudeQuotaViaPTY(ctx context.Context, timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, *harnesses.AccountInfo, ptyquota.Result, error) {
	cfg := quotaPTYOptions{binary: "claude"}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	var windows []harnesses.QuotaWindow
	var account *harnesses.AccountInfo
	// Claude Code >= 2.1.260 renders /usage as a full-screen dialog that
	// carries no plan line; the plan is only visible on the startup banner
	// ("Fable 5.1 with high effort · Claude Max") before the dialog covers
	// it. Capture it from any intermediate DoneWhen tick and fall back to it
	// when the final screen yields no account.
	var bannerAccount *harnesses.AccountInfo
	result, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:   "claude",
		Binary:        cfg.binary,
		BinaryVersion: cfg.binaryVersion,
		Args:          cfg.args,
		Workdir:       cfg.workdir,
		Env:           cfg.env,
		Command:       "/usage\r",
		ReadyMarkers:  []string{"❯", "> "},
		Interstitials: []ptyquota.Interstitial{claudeTrustInterstitial()},
		DoneWhen: func(text string) bool {
			if bannerAccount == nil {
				bannerAccount = parseClaudePlanAccount(text)
			}
			return claudeUsageComplete(text)
		},
		Timeout:     timeout,
		Size:        session.Size{Rows: 50, Cols: 220},
		CassetteDir: cfg.cassetteDir,
		Quota: func(text string) (cassette.QuotaRecord, error) {
			windows, account = parseClaudeUsageOutput(text)
			if account == nil {
				account = bannerAccount
			}
			if len(windows) == 0 {
				return cassette.QuotaRecord{}, fmt.Errorf("no quota windows found in claude /usage output")
			}
			if err := validateClaudeQuotaEvidence(windows, account); err != nil {
				return cassette.QuotaRecord{}, fmt.Errorf("incomplete claude /usage output: %w", err)
			}
			return quotaRecord(windows, map[string]any{"plan_type": accountPlan(account)}), nil
		},
	})
	if err != nil {
		return nil, nil, result, err
	}
	if len(windows) == 0 {
		windows, account = parseClaudeUsageOutput(result.Text)
	}
	if account == nil {
		account = bannerAccount
	}
	if len(windows) == 0 {
		return nil, account, result, fmt.Errorf("no quota windows found in claude /usage output")
	}
	if err := validateClaudeQuotaEvidence(windows, account); err != nil {
		return nil, account, result, fmt.Errorf("incomplete claude /usage output: %w", err)
	}
	return windows, account, result, nil
}

func claudeQuotaSnapshotFromWindows(windows []harnesses.QuotaWindow, account *harnesses.AccountInfo) claudeQuotaSnapshot {
	fiveHourUsed := usedPercentFor(windows, "session")
	weeklyUsed := usedPercentFor(windows, "weekly-all")
	if weeklyUsed < 0 {
		weeklyUsed = usedPercentFor(windows, "weekly-sonnet")
	}
	return claudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourLimit:     100,
		FiveHourRemaining: remainingPercent(fiveHourUsed),
		WeeklyLimit:       100,
		WeeklyRemaining:   remainingPercent(weeklyUsed),
		Windows:           append([]harnesses.QuotaWindow(nil), windows...),
		Source:            "pty",
		Account:           account,
	}
}

func usedPercentFor(windows []harnesses.QuotaWindow, limitID string) float64 {
	for _, window := range windows {
		if window.LimitID == limitID {
			return window.UsedPercent
		}
	}
	return -1
}

func hasQuotaWindow(windows []harnesses.QuotaWindow, limitID string) bool {
	return usedPercentFor(windows, limitID) >= 0
}

func claudeUsageComplete(text string) bool {
	windows, _ := parseClaudeUsageOutput(text)
	return hasQuotaWindow(windows, "session") && (hasQuotaWindow(windows, "weekly-all") || hasQuotaWindow(windows, "weekly-sonnet"))
}

func validateClaudeQuotaEvidence(windows []harnesses.QuotaWindow, account *harnesses.AccountInfo) error {
	if accountPlan(account) == "" {
		return fmt.Errorf("missing account plan")
	}
	if !hasQuotaWindow(windows, "session") {
		return fmt.Errorf("missing session window")
	}
	if !hasQuotaWindow(windows, "weekly-all") && !hasQuotaWindow(windows, "weekly-sonnet") {
		return fmt.Errorf("missing weekly window")
	}
	return nil
}

func remainingPercent(used float64) int {
	if used < 0 {
		return 0
	}
	remaining := int(math.Round(100 - used))
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func quotaRecord(windows []harnesses.QuotaWindow, metadata map[string]any) cassette.QuotaRecord {
	records := make([]map[string]any, 0, len(windows))
	for _, window := range windows {
		records = append(records, map[string]any{
			"name":           window.Name,
			"limit_id":       window.LimitID,
			"window_minutes": window.WindowMinutes,
			"used_percent":   window.UsedPercent,
			"resets_at":      window.ResetsAt,
			"state":          window.State,
		})
	}
	accountClass, _ := metadata["plan_type"].(string)
	return cassette.QuotaRecord{
		Source:            "pty",
		Status:            string(ptyquota.StatusOK),
		CapturedAt:        time.Now().UTC().Format(time.RFC3339),
		FreshnessWindow:   defaultClaudeQuotaStaleAfter.String(),
		StalenessBehavior: "stale quota evidence keeps Claude out of automatic routing and is treated as limited",
		AccountClass:      accountClass,
		Windows:           records,
		Metadata:          metadata,
	}
}

func accountPlan(account *harnesses.AccountInfo) string {
	if account == nil {
		return ""
	}
	return account.PlanType
}

func stringsJoinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
