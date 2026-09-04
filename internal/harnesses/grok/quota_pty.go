package grok

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/harnesses/ptyquota"
	"github.com/easel/fizeau/internal/pty/cassette"
	"github.com/easel/fizeau/internal/pty/session"
)

type quotaPTYOptions struct {
	binary      string
	args        []string
	workdir     string
	env         []string
	cassetteDir string
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

// grokTrustInterstitial answers Grok Build's first-run folder trust dialog
// ("... posing security risks. / Yes, proceed  y / No, quit  n"), which
// appears whenever the TUI is launched in a not-yet-trusted directory —
// including every fresh execute-bead worktree. Sending "y" accepts it; the
// driver then proceeds to the normal ready prompt.
func grokTrustInterstitial() ptyquota.Interstitial {
	return ptyquota.Interstitial{
		Name: "grok-folder-trust",
		Match: func(screen string) bool {
			return strings.Contains(screen, "Yes, proceed") &&
				strings.Contains(screen, "No, quit")
		},
		Send: []byte("y"),
	}
}

func readGrokQuotaViaPTY(timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, error) {
	windows, _, err := captureGrokQuotaViaPTY(context.Background(), timeout, opts...)
	return windows, err
}

func readGrokQuotaFromCassette(dir string) ([]harnesses.QuotaWindow, error) {
	reader, err := cassette.Open(dir)
	if err != nil {
		return nil, err
	}
	text := reader.Final().FinalText
	if text == "" {
		frames := reader.Frames()
		if len(frames) > 0 {
			text = strings.Join(frames[len(frames)-1].Text, "\n")
		}
	}
	windows := parseGrokUsageOutput(text)
	if len(windows) == 0 {
		return nil, fmt.Errorf("no quota windows found in grok quota cassette")
	}
	return windows, nil
}

func captureGrokQuotaViaPTY(ctx context.Context, timeout time.Duration, opts ...QuotaPTYOption) ([]harnesses.QuotaWindow, ptyquota.Result, error) {
	cfg := quotaPTYOptions{binary: "grok", args: []string{"--no-alt-screen"}}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	var windows []harnesses.QuotaWindow
	result, err := ptyquota.Run(ctx, ptyquota.Config{
		HarnessName:  "grok",
		Binary:       cfg.binary,
		Args:         cfg.args,
		Workdir:      cfg.workdir,
		Env:          cfg.env,
		Command:      "/usage show\r",
		ReadyMarkers: []string{"❯", "> "},
		// No DoneAnyMarkers: grok's differential renderer does not repaint
		// the unchanged prompt glyph after ResetBeforeCommand wipes the
		// emulator, so a prompt-marker done condition never re-matches. The
		// DoneWhen predicate (explicit "Weekly limit: N%" line) is the
		// completion signal.
		DoneWhen:           grokQuotaOutputComplete,
		Interstitials:      []ptyquota.Interstitial{grokTrustInterstitial()},
		ResetBeforeCommand: true,
		Timeout:            timeout,
		Size:               session.Size{Rows: 50, Cols: 220},
		CassetteDir:        cfg.cassetteDir,
		Quota: func(text string) (cassette.QuotaRecord, error) {
			windows = parseGrokUsageOutput(text)
			if len(windows) == 0 {
				return cassette.QuotaRecord{}, fmt.Errorf("no quota windows found in grok /usage output")
			}
			return quotaRecord(windows), nil
		},
	})
	if err != nil {
		return nil, result, err
	}
	if len(windows) == 0 {
		windows = parseGrokUsageOutput(result.Text)
	}
	if len(windows) == 0 {
		return nil, result, fmt.Errorf("no quota windows found in grok /usage output")
	}
	return windows, result, nil
}

func readGrokAccountOrNil() *harnesses.AccountInfo {
	account, _ := readGrokAccount()
	return account
}

// grokQuotaOutputComplete reports whether the emulated screen contains a
// parseable "/usage show" result. The explicit "Weekly limit: N%" scrollback
// line is required — the status bar's "Weekly limit left" is present before
// the command runs, so it must not satisfy the done predicate on its own.
func grokQuotaOutputComplete(text string) bool {
	clean := stripANSI(text)
	if grokWeeklyUsedPattern.MatchString(clean) {
		return true
	}
	used, _, _ := parseGrokDialogUsage(strings.ReplaceAll(clean, "\r\n", "\n"))
	return used >= 0
}

func quotaRecord(windows []harnesses.QuotaWindow) cassette.QuotaRecord {
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
	metadata := map[string]any{}
	var accountClass string
	if account, ok := readGrokAccount(); ok {
		metadata["plan_type"] = account.PlanType
		accountClass = account.PlanType
	}
	return cassette.QuotaRecord{
		Source:            "pty",
		Status:            string(ptyquota.StatusOK),
		CapturedAt:        time.Now().UTC().Format(time.RFC3339),
		FreshnessWindow:   defaultGrokQuotaStaleAfter.String(),
		StalenessBehavior: "stale quota evidence keeps grok out of automatic routing and is treated as limited",
		AccountClass:      accountClass,
		Windows:           records,
		Metadata:          metadata,
	}
}
