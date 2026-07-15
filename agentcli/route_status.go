package agentcli

// route_status.go implements `fiz route-status --overrides`
// (ADR-006 §5). The base `route-status` command lives in routing_smart.go
// and dispatches into this file when --overrides is set.
//
// This is the operator-facing surface that prints override_class_breakdown
// over a recent time window so operators can diagnose where auto-routing
// is mis-deciding. Per ADR-006 §Migration, this is an operator-driven
// feedback loop; automatic learning from this signal is a future ADR.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	rootfizeau "github.com/easel/fizeau"
	agentConfig "github.com/easel/fizeau/internal/config"
)

const defaultRouteStatusOverridesWindow = 7 * 24 * time.Hour

type routeStatusUsageReporter interface {
	UsageReport(context.Context, rootfizeau.UsageReportOptions) (*rootfizeau.UsageReport, error)
}

var newRouteStatusUsageReporter = func(opts rootfizeau.ServiceOptions) (routeStatusUsageReporter, error) {
	return rootfizeau.New(opts)
}

// routeStatusOverridesOutput is the stable JSON envelope emitted by
// `route-status --overrides --json`. The shape is snapshot-tested in
// TestRouteStatusOverridesJSONStable; new fields must be added at the end
// or behind a new flag.
type routeStatusOverridesOutput struct {
	Since                    string                           `json:"since"`
	WindowStart              time.Time                        `json:"window_start"`
	WindowEnd                time.Time                        `json:"window_end"`
	AxisFilter               string                           `json:"axis_filter,omitempty"`
	AutoAcceptanceRate       float64                          `json:"auto_acceptance_rate"`
	OverrideDisagreementRate float64                          `json:"override_disagreement_rate"`
	TotalRequests            int                              `json:"total_requests"`
	TotalOverrides           int                              `json:"total_overrides"`
	OverrideClassBreakdown   []rootfizeau.OverrideClassBucket `json:"override_class_breakdown"`
}

// cmdRouteStatusOverrides implements the --overrides mode. It is wired in
// from cmdRouteStatus (routing_smart.go); kept here to keep the operator
// surface self-contained.
func cmdRouteStatusOverrides(workDir, since, axis string, jsonOut bool) int {
	return runRouteStatusOverrides(workDir, since, axis, jsonOut, os.Stdout, os.Stderr, time.Now().UTC())
}

func runRouteStatusOverrides(workDir, since, axis string, jsonOut bool, stdout, stderr io.Writer, now time.Time) int {
	if err := validateRouteStatusOverridesAxis(axis); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}

	dur, err := parseRouteStatusOverridesSince(since)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 2
	}
	start := now.Add(-dur)
	end := now

	cfg, err := agentConfig.Load(workDir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}
	// This report is historical and read-only. Cancel before New so service
	// startup cannot dispatch quota/account refresh work for this command.
	refreshCtx, cancelRefresh := context.WithCancel(context.Background())
	cancelRefresh()
	svc, err := newRouteStatusUsageReporter(rootfizeau.ServiceOptions{
		ServiceConfig:       agentConfig.NewServiceConfig(cfg, workDir),
		SessionLogDir:       sessionLogDir(workDir, cfg),
		QuotaRefreshContext: refreshCtx,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	report, err := svc.UsageReport(context.Background(), rootfizeau.UsageReportOptions{
		Since: formatUsageWindowSince(start, end),
		Now:   now,
	})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err)
		return 1
	}

	metrics := report.RoutingQuality
	filtered := filterOverrideBreakdownByAxis(metrics.OverrideClassBreakdown, axis)

	out := routeStatusOverridesOutput{
		Since:                    since,
		WindowStart:              start,
		WindowEnd:                end,
		AxisFilter:               axis,
		AutoAcceptanceRate:       metrics.AutoAcceptanceRate,
		OverrideDisagreementRate: metrics.OverrideDisagreementRate,
		TotalRequests:            metrics.TotalRequests,
		TotalOverrides:           metrics.TotalOverrides,
		OverrideClassBreakdown:   filtered,
	}
	if out.OverrideClassBreakdown == nil {
		out.OverrideClassBreakdown = []rootfizeau.OverrideClassBucket{}
	}

	if jsonOut {
		writeRouteStatusOverridesJSON(stdout, out)
		return 0
	}
	writeRouteStatusOverridesText(stdout, out)
	return 0
}

// parseRouteStatusOverridesSince parses --since as a Go duration. Empty
// means the default 7d window.
func parseRouteStatusOverridesSince(spec string) (time.Duration, error) {
	if spec == "" {
		return defaultRouteStatusOverridesWindow, nil
	}
	d, err := time.ParseDuration(spec)
	if err != nil {
		return 0, fmt.Errorf("--since: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--since: must be positive, got %s", spec)
	}
	return d, nil
}

// validateRouteStatusOverridesAxis rejects unknown axis values. Empty is
// allowed (no filter).
func validateRouteStatusOverridesAxis(axis string) error {
	switch axis {
	case "", "harness", "provider", "model":
		return nil
	default:
		return fmt.Errorf("--axis: must be one of harness|provider|model, got %q", axis)
	}
}

// filterOverrideBreakdownByAxis returns rows where Axis == axis. Empty axis
// returns the input unchanged. Rows are returned in the same deterministic
// order produced by the aggregator.
func filterOverrideBreakdownByAxis(rows []rootfizeau.OverrideClassBucket, axis string) []rootfizeau.OverrideClassBucket {
	if axis == "" {
		return rows
	}
	out := make([]rootfizeau.OverrideClassBucket, 0, len(rows))
	for _, r := range rows {
		if r.Axis == axis {
			out = append(out, r)
		}
	}
	return out
}

// sortOverrideBreakdownForDisplay returns a copy sorted by Count desc, then
// success rate desc, then deterministic key. The default JSON ordering is
// preserved by the aggregator; this is the table-render ordering.
func sortOverrideBreakdownForDisplay(rows []rootfizeau.OverrideClassBucket) []rootfizeau.OverrideClassBucket {
	out := make([]rootfizeau.OverrideClassBucket, len(rows))
	copy(out, rows)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		li := bucketSuccessRate(out[i])
		lj := bucketSuccessRate(out[j])
		if li != lj {
			return li > lj
		}
		if out[i].PromptFeatureBucket != out[j].PromptFeatureBucket {
			return out[i].PromptFeatureBucket < out[j].PromptFeatureBucket
		}
		if out[i].Axis != out[j].Axis {
			return out[i].Axis < out[j].Axis
		}
		return !out[i].Match && out[j].Match
	})
	return out
}

func bucketSuccessRate(b rootfizeau.OverrideClassBucket) float64 {
	total := b.SuccessOutcomes + b.StalledOutcomes + b.FailedOutcomes + b.CancelledOutcomes + b.UnknownOutcomes
	if total == 0 {
		return 0
	}
	return float64(b.SuccessOutcomes) / float64(total)
}

func writeRouteStatusOverridesJSON(w io.Writer, out routeStatusOverridesOutput) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func writeRouteStatusOverridesText(w io.Writer, out routeStatusOverridesOutput) {
	fmt.Fprintf(w, "Window: %s .. %s (%s)\n",
		out.WindowStart.Format(time.RFC3339),
		out.WindowEnd.Format(time.RFC3339),
		windowDurationLabel(out.Since))
	if out.AxisFilter != "" {
		fmt.Fprintf(w, "Axis filter: %s\n", out.AxisFilter)
	}
	fmt.Fprintf(w, "Auto-acceptance rate: %.3f (%d / %d)\n",
		out.AutoAcceptanceRate,
		out.TotalRequests-out.TotalOverrides,
		out.TotalRequests)
	fmt.Fprintf(w, "Override disagreement rate: %.3f (%d overrides)\n",
		out.OverrideDisagreementRate,
		out.TotalOverrides)

	if len(out.OverrideClassBreakdown) == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "No override events recorded in this window.")
		return
	}

	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROMPT_FEATURES\tAXIS\tMATCH\tCOUNT\tSUCCESS%\tSUCCESS\tSTALLED\tFAILED\tCANCELLED\tUNKNOWN")
	for _, b := range sortOverrideBreakdownForDisplay(out.OverrideClassBreakdown) {
		successPct := bucketSuccessRate(b) * 100
		fmt.Fprintf(tw, "%s\t%s\t%t\t%d\t%.1f%%\t%d\t%d\t%d\t%d\t%d\n",
			b.PromptFeatureBucket, b.Axis, b.Match, b.Count, successPct,
			b.SuccessOutcomes, b.StalledOutcomes, b.FailedOutcomes,
			b.CancelledOutcomes, b.UnknownOutcomes)
	}
	_ = tw.Flush()
}

func windowDurationLabel(since string) string {
	if since == "" {
		return "default 7d"
	}
	return since
}

// formatUsageWindowSince builds the date-range form
// "RFC3339..RFC3339" that internal/session.ParseUsageWindow accepts. We
// route through it (rather than a duration form) because the public
// UsageReport API only takes a string.
func formatUsageWindowSince(start, end time.Time) string {
	return start.UTC().Format(time.RFC3339) + ".." + end.UTC().Format(time.RFC3339)
}
