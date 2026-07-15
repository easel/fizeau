package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau"
)

func TestRouteStatusOutputIncludesPowerPolicy(t *testing.T) {
	out := routeStatusOutput{
		Policy: "default",
		PowerPolicy: routeStatusPowerPolicy{
			PolicyName: "default",
		},
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"power_policy"`) {
		t.Fatalf("JSON missing power_policy field: %s", data)
	}
	if !strings.Contains(string(data), `"policy":"default"`) {
		t.Fatalf("JSON missing policy evidence: %s", data)
	}
	if !strings.Contains(string(data), `"policy_name":"default"`) {
		t.Fatalf("JSON missing power policy name evidence: %s", data)
	}
}

// TestRouteStatusOverridesSinceFilter verifies that --since trims the
// breakdown window. Three session logs are written: one 1h ago (inside
// every window), one 48h ago (inside 7d but outside 24h), one 30d ago
// (outside both).
func TestRouteStatusOverridesSinceFilter(t *testing.T) {
	isolateCatalogHome(t)
	workDir := t.TempDir()
	logDir := filepath.Join(workDir, ".fizeau", "sessions")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now().UTC()
	writeOverrideSessionLog(t, logDir, "s-1h", now.Add(-time.Hour), "harness", false, "success")
	writeOverrideSessionLog(t, logDir, "s-48h", now.Add(-48*time.Hour), "harness", false, "success")
	writeOverrideSessionLog(t, logDir, "s-30d", now.Add(-30*24*time.Hour), "harness", false, "success")

	// 24h window: only s-1h is inside.
	out := runRouteStatusOverridesJSON(t, workDir, "24h", "", now)
	if out.TotalOverrides != 1 {
		t.Fatalf("--since=24h: TotalOverrides = %d, want 1", out.TotalOverrides)
	}
	totalCount := 0
	for _, b := range out.OverrideClassBreakdown {
		totalCount += b.Count
	}
	if totalCount != 1 {
		t.Errorf("--since=24h: breakdown count sum = %d, want 1", totalCount)
	}

	// Default 7d window (--since="" → 168h): s-1h + s-48h.
	out = runRouteStatusOverridesJSON(t, workDir, "", "", now)
	if out.TotalOverrides != 2 {
		t.Fatalf("default 7d: TotalOverrides = %d, want 2", out.TotalOverrides)
	}

	// 168h is the explicit equivalent of the default and must produce the
	// same result as the default window.
	out2 := runRouteStatusOverridesJSON(t, workDir, "168h", "", now)
	if out2.TotalOverrides != out.TotalOverrides {
		t.Errorf("168h vs default: TotalOverrides %d vs %d should match",
			out2.TotalOverrides, out.TotalOverrides)
	}

	// 24h header text mentions the chosen window.
	textOut, _ := runRouteStatusOverridesText(t, workDir, "24h", "", now)
	if !strings.Contains(textOut, "Window: ") {
		t.Errorf("text output missing Window header: %q", textOut)
	}
	if !strings.Contains(textOut, "24h") {
		t.Errorf("text output should echo since=24h: %q", textOut)
	}
}

// TestRouteStatusOverridesAxisFilter verifies that --axis filters the
// breakdown rows to overrides on the named axis only.
func TestRouteStatusOverridesAxisFilter(t *testing.T) {
	isolateCatalogHome(t)
	workDir := t.TempDir()
	logDir := filepath.Join(workDir, ".fizeau", "sessions")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := time.Now().UTC()
	writeOverrideSessionLog(t, logDir, "s-h-1", now.Add(-time.Hour), "harness", false, "success")
	writeOverrideSessionLog(t, logDir, "s-h-2", now.Add(-2*time.Hour), "harness", false, "failed")
	writeOverrideSessionLog(t, logDir, "s-m-1", now.Add(-3*time.Hour), "model", false, "success")
	writeOverrideSessionLog(t, logDir, "s-p-1", now.Add(-4*time.Hour), "provider", false, "success")

	cases := []struct {
		axis     string
		wantRows int
	}{
		{"harness", 1}, // both harness overrides land in the same bucket
		{"model", 1},
		{"provider", 1},
		{"", 3}, // no filter → all axes
	}
	for _, tc := range cases {
		out := runRouteStatusOverridesJSON(t, workDir, "24h", tc.axis, now)
		if len(out.OverrideClassBreakdown) != tc.wantRows {
			t.Errorf("axis=%q: rows = %d, want %d (rows=%+v)",
				tc.axis, len(out.OverrideClassBreakdown), tc.wantRows, out.OverrideClassBreakdown)
		}
		if tc.axis != "" {
			for _, b := range out.OverrideClassBreakdown {
				if b.Axis != tc.axis {
					t.Errorf("axis=%q: row has axis=%q (must be filtered out)", tc.axis, b.Axis)
				}
			}
		}
	}

	// Reject unknown axis values with exit code 2.
	var stdout, stderr bytes.Buffer
	rc := runRouteStatusOverrides(workDir, "24h", "bogus", true, &stdout, &stderr, now)
	if rc != 2 {
		t.Errorf("--axis=bogus: exit=%d, want 2 (stderr=%s)", rc, stderr.String())
	}
}

// TestRouteStatusOverridesEmptyWindow verifies AC #6: with zero overrides
// the command exits 0 with an empty breakdown rather than erroring.
func TestRouteStatusOverridesEmptyWindow(t *testing.T) {
	isolateCatalogHome(t)
	workDir := t.TempDir()
	now := time.Now().UTC()

	var stdout, stderr bytes.Buffer
	rc := runRouteStatusOverrides(workDir, "24h", "", true, &stdout, &stderr, now)
	if rc != 0 {
		t.Fatalf("exit=%d, want 0 (stderr=%s)", rc, stderr.String())
	}
	var out routeStatusOverridesOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout.String())
	}
	if out.TotalRequests != 0 {
		t.Errorf("TotalRequests = %d, want 0", out.TotalRequests)
	}
	if out.TotalOverrides != 0 {
		t.Errorf("TotalOverrides = %d, want 0", out.TotalOverrides)
	}
	if len(out.OverrideClassBreakdown) != 0 {
		t.Errorf("OverrideClassBreakdown = %+v, want empty", out.OverrideClassBreakdown)
	}

	// Text output also must succeed and surface the empty marker.
	stdout.Reset()
	stderr.Reset()
	rc = runRouteStatusOverrides(workDir, "24h", "", false, &stdout, &stderr, now)
	if rc != 0 {
		t.Fatalf("text mode: exit=%d, want 0 (stderr=%s)", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No override events recorded") {
		t.Errorf("text mode: missing empty-window marker:\n%s", stdout.String())
	}
}

func TestRouteStatusOverridesDisablesRefreshWorkers(t *testing.T) {
	isolateCatalogHome(t)
	original := newRouteStatusUsageReporter
	constructorCalls := 0
	newRouteStatusUsageReporter = func(opts fizeau.ServiceOptions) (routeStatusUsageReporter, error) {
		constructorCalls++
		if opts.QuotaRefreshContext == nil {
			t.Fatal("QuotaRefreshContext = nil, want an already-canceled context")
		}
		if err := opts.QuotaRefreshContext.Err(); err != context.Canceled {
			t.Fatalf("QuotaRefreshContext.Err() = %v, want context.Canceled before construction", err)
		}
		return emptyRouteStatusUsageReporter{}, nil
	}
	t.Cleanup(func() { newRouteStatusUsageReporter = original })

	var stdout, stderr bytes.Buffer
	rc := runRouteStatusOverrides(t.TempDir(), "24h", "", true, &stdout, &stderr, time.Now().UTC())
	if rc != 0 {
		t.Fatalf("exit=%d, want 0 (stderr=%s)", rc, stderr.String())
	}
	if constructorCalls != 1 {
		t.Fatalf("constructor calls = %d, want 1", constructorCalls)
	}
}

type emptyRouteStatusUsageReporter struct{}

func (emptyRouteStatusUsageReporter) UsageReport(context.Context, fizeau.UsageReportOptions) (*fizeau.UsageReport, error) {
	return &fizeau.UsageReport{}, nil
}

// TestRouteStatusOverridesParseSinceErrors ensures invalid --since values
// produce a clean exit-code-2 validation error rather than a 1.
func TestRouteStatusOverridesParseSinceErrors(t *testing.T) {
	workDir := t.TempDir()
	now := time.Now().UTC()
	cases := []string{"banana", "-1h", "0s", "7d"} // 7d is not a Go duration
	for _, c := range cases {
		var stdout, stderr bytes.Buffer
		rc := runRouteStatusOverrides(workDir, c, "", true, &stdout, &stderr, now)
		if rc != 2 {
			t.Errorf("--since=%q: exit=%d, want 2 (stderr=%s)", c, rc, stderr.String())
		}
	}
}

// TestRouteStatusOverridesHelpMentionsADR verifies AC #5: the route-status
// --help output mentions --overrides and ADR-006.
func TestRouteStatusOverridesHelpMentionsADR(t *testing.T) {
	// Capture flag.PrintDefaults output by invoking with --help. The
	// flagset uses ContinueOnError so we get usage on stderr.
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	_ = cmdRouteStatus(t.TempDir(), []string{"--help"})
	w.Close()
	os.Stderr = oldStderr
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	help := buf.String()
	if !strings.Contains(help, "--overrides") && !strings.Contains(help, "-overrides") {
		t.Errorf("help output missing --overrides flag:\n%s", help)
	}
	if !strings.Contains(strings.ToLower(help), "adr-006") {
		t.Errorf("help output should reference ADR-006:\n%s", help)
	}
}

// runRouteStatusOverridesJSON is a test helper: runs the command in JSON
// mode and decodes the envelope.
func runRouteStatusOverridesJSON(t *testing.T, workDir, since, axis string, now time.Time) routeStatusOverridesOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc := runRouteStatusOverrides(workDir, since, axis, true, &stdout, &stderr, now)
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}
	var out routeStatusOverridesOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
	}
	return out
}

func runRouteStatusOverridesText(t *testing.T, workDir, since, axis string, now time.Time) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	rc := runRouteStatusOverrides(workDir, since, axis, false, &stdout, &stderr, now)
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}
	return stdout.String(), stderr.String()
}

// writeOverrideSessionLog writes a JSONL session log containing
// session.start, override, session.end. ScanRoutingQuality requires
// session.start, so all three are written.
func writeOverrideSessionLog(t *testing.T, dir, sessionID string, startedAt time.Time, axis string, coincide bool, outcomeStatus string) {
	t.Helper()
	tokens := 1500
	ovr := fizeau.ServiceOverrideData{
		AxesOverridden: []string{axis},
		PromptFeatures: fizeau.ServiceOverridePromptFeatures{
			EstimatedTokens: &tokens,
			Reasoning:       "",
		},
		Outcome: &fizeau.ServiceOverrideOutcome{Status: outcomeStatus},
	}
	if coincide {
		ovr.MatchPerAxis = map[string]bool{axis: true}
	}
	startData := fizeau.SessionStartData{Provider: "p", Model: "m"}
	endData := fizeau.SessionEndData{Status: fizeau.StatusSuccess}

	mustEncode := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	events := []fizeau.SessionEvent{
		{SessionID: sessionID, Seq: 0, Type: fizeau.EventSessionStart, Timestamp: startedAt, Data: mustEncode(startData)},
		{SessionID: sessionID, Seq: 1, Type: fizeau.SessionEventType("override"), Timestamp: startedAt.Add(time.Second), Data: mustEncode(ovr)},
		{SessionID: sessionID, Seq: 2, Type: fizeau.EventSessionEnd, Timestamp: startedAt.Add(2 * time.Second), Data: mustEncode(endData)},
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
}
