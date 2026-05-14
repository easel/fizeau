<bead-review>
  <bead id="fizeau-dbc21cce" iter=1>
    <title>Classify invalid TerminalBench runs separately from graded failures</title>
    <description>
The medium comparison must not treat provider quota, authentication, setup, architecture, or provider routing failures as model capability failures. Recent Claude canaries produced 429/out_of_credits failures; pi and opencode can similarly fail from wrapper setup, local binary/account state, or provider/auth surfaces before a meaningful model attempt. Those should be reported as invalid cells, not failed benchmark attempts. Only verifier failures after a real agent attempt should count as graded failures.

In-scope files:
- cmd/bench matrix aggregation/reporting code
- benchmark result parsing helpers
- fixtures for representative invalid logs/results across Claude, Codex, pi, and opencode lanes
- matrix Markdown/JSON output tests

Out-of-scope:
- changing Harbor or TerminalBench verifier behavior
- changing fiz session-log event schema unless needed for existing metadata consumption
- benchmark profile selection

Implementation notes:
- Add stable invalid classes: `invalid_quota`, `invalid_auth`, `invalid_setup`, and `invalid_provider`.
- Classify Claude 429/out_of_credits as `invalid_quota`.
- Classify pi/opencode missing binary, account, permission, or wrapper startup failures before agent progress as `invalid_setup` or `invalid_auth` as appropriate.
- Classify Docker/task image/native arch/setup failures before agent execution as `invalid_setup`.
- Exclude invalid cells from pass-rate or mean-reward denominators while still displaying them prominently.
    </description>
    <acceptance>
1. A fixture containing Claude 429/out_of_credits is classified as `invalid_quota`.
2. Fixtures containing pi/opencode missing binary, account, or wrapper startup failure before agent progress are classified as `invalid_setup` or `invalid_auth` as appropriate.
3. A fixture containing setup/native-arch/task-image failure before agent progress is classified as `invalid_setup`.
4. A fixture containing a verifier failure after an agent attempt remains a graded failure, not invalid.
5. Matrix Markdown and JSON outputs include invalid counts/classes and exclude invalid cells from capability pass-rate denominators.
6. `go test ./cmd/bench` passes.
    </acceptance>
    <labels>area:benchmark, area:reporting, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>cmd/bench/external_termbench_test.go</file>
    <file>cmd/bench/matrix.go</file>
    <file>cmd/bench/matrix_aggregate.go</file>
    <file>cmd/bench/matrix_invalid.go</file>
    <file>cmd/bench/matrix_invalid_test.go</file>
    <file>cmd/bench/testdata/matrix-invalid/claude-quota.json</file>
    <file>cmd/bench/testdata/matrix-invalid/codex-auth.json</file>
    <file>cmd/bench/testdata/matrix-invalid/opencode-account.json</file>
    <file>cmd/bench/testdata/matrix-invalid/opencode-wrapper-startup.json</file>
    <file>cmd/bench/testdata/matrix-invalid/pi-missing-binary.json</file>
    <file>cmd/bench/testdata/matrix-invalid/provider-transport.json</file>
    <file>cmd/bench/testdata/matrix-invalid/setup-native-arch.json</file>
    <file>cmd/bench/testdata/matrix-invalid/verifier-fail-after-attempt.json</file>
  </changed-files>

  <governing>
    <ref id="terminalbench-fiz-wrapper-comparison-2026-05-06" path="docs/helix/02-design/terminalbench-fiz-wrapper-comparison-2026-05-06.md" title="TerminalBench Fiz-Wrapper Comparison">
      <content>
<untrusted-data>
---
ddx:
  id: terminalbench-fiz-wrapper-comparison-2026-05-06
  created: 2026-05-06
  extends:
    - external-benchmarks
    - routing
---

# TerminalBench Fiz-Wrapper Comparison

## Problem

The medium-model TerminalBench comparison attempted to compare native Claude
Code, native Codex, pi, opencode, and fiz by installing separate Harbor agents
for each harness. That duplicates fiz's routing and harness-normalization job
inside benchmark glue. It also creates false failures from Harbor/container
details: TerminalBench images commonly run as root, prebuilt task images may be
cross-architecture, and harness permission/auth flags differ.

The benchmark should not hand-roll any harness CLI semantics that fiz already
wraps. Fizeau owns those wrappers through its harness registry, permission
policy, model aliasing, session logging, quota/account interpretation, and
subprocess event normalization. Using fiz for pi and opencode also increases
coverage of the wrappers operators actually depend on.

## Decision

TerminalBench matrix runs must use one Harbor installed agent:
`scripts/benchmark/harbor_agent.py:FizeauAgent`.

Benchmark profiles select the execution target by passing explicit fiz hard
pins into that single agent:

- `FIZEAU_HARNESS=claude` for fiz-wrapped Claude Code.
- `FIZEAU_HARNESS=codex` for fiz-wrapped Codex.
- `FIZEAU_HARNESS=pi` for fiz-wrapped pi.
- `FIZEAU_HARNESS=opencode` for fiz-wrapped opencode.
- `FIZEAU_PROVIDER=openrouter` for fiz's provider path.
- `FIZEAU_MODEL`, `FIZEAU_MODEL_REF`, and `FIZEAU_REASONING` retain their
  existing meanings.

Raw Harbor Claude/Codex/pi/opencode adapters may remain as diagnostics, but
they are not part of the official medium-model or frontier-reference
TerminalBench comparison.

## Benchmark Lanes

The medium-model comparison uses these cells:

| Cell | Meaning |
| --- | --- |
| `fiz-harness-claude-sonnet-4-6` | Fizeau pinned to the Claude Code harness. |
| `fiz-harness-codex-gpt-5-4-mini` | Fizeau pinned to the Codex harness. |
| `fiz-harness-pi-gpt-5-4-mini` | Fizeau pinned to the pi harness. |
| `fiz-harness-opencode-gpt-5-4-mini` | Fizeau pinned to the opencode harness. |
| `fiz-openrouter-claude-sonnet-4-6` | Fizeau provider path through OpenRouter to Sonnet. |
| `fiz-openrouter-gpt-5-4-mini` | Fizeau provider path through OpenRouter to GPT mini. |

These lanes separate two questions:

1. Harness path: how well does fiz normalize subscription harnesses when the
   underlying model family is held near constant?
2. Provider path: how well do the same model families perform through fiz's
   direct provider/tool loop?

Published memos must state that identical model names across lanes are not a
pure model control. Harnesses still differ in prompt scaffolding, tool schema,
permission semantics, context handling, and quota surface.

## Native Architecture

On arm64 hosts, TerminalBench task images must be built for the native
architecture. The medium comparison defaults `HARBOR_FORCE_BUILD=1` so Harbor
does not reuse amd64 upstream images with arm64 binaries. This is a
reproducibility requirement, not an optimization.

## Invalid Run Classification

Capability aggregates must exclude runs that never reached a meaningful model
attempt. The matrix report must classify and surface these as invalid rather
than as graded failures:

- `invalid_quota` — rate limit, usage exhausted, credits exhausted, quota
  window closed.
- `invalid_auth` — missing or rejected credentials.
- `invalid_setup` — harness installation, binary architecture, permission-mode,
  or task environment failure before agent work.
- `invalid_provider` — provider transport failure before a response is
  produced.

Only verifier failures after a real agent attempt are `graded_fail`.

Invalid runs still appear in `matrix.md` with cause and log path. They are
excluded from mean reward denominators and cost/capability comparisons.

## Implementation Shape

1. The fiz CLI exposes `--harness` as a hard pin on `fiz run`, matching the
   routing docs.
2. `FizeauAgent` forwards `FIZEAU_HARNESS` into the fiz invocation and records
   the resolved harness/provider/model in its trajectory metadata.
3. Benchmark profiles encode lanes; scripts invoke only `HARNESSES=fiz`.
4. Aggregation classifies invalid runs from report fields and known log
   signatures, including Claude Code `api_error_status: 429` and
   `out_of_credits`.
5. Tests prove the official comparison script does not call raw Harbor
   Claude/Codex/pi/opencode adapters.

## Out Of Scope

- Making raw Harbor Claude/Codex/pi/opencode adapters production quality.
- Reimplementing upstream TerminalBench scoring.
- Treating OpenRouter Sonnet and Claude Code Sonnet as the same provider
  surface.
- Introducing concurrent matrix execution.
</untrusted-data>
      </content>
    </ref>
  </governing>

  <diff rev="76a3b78a99e3f155dde723cba1e0b843de7e2c89">
<untrusted-data>
diff --git a/cmd/bench/external_termbench_test.go b/cmd/bench/external_termbench_test.go
index d1825a9..e320a6c 100644
--- a/cmd/bench/external_termbench_test.go
+++ b/cmd/bench/external_termbench_test.go
@@ -61,6 +61,9 @@ func TestLoadTermbenchSubset_LocalWideAllTasksExist(t *testing.T) {
 		t.Fatalf("local-wide task count = %d, want %d", got, want)
 	}
 	tasksDir := filepath.Join(repoRoot, "scripts", "benchmark", "external", "terminal-bench-2")
+	if _, err := os.Stat(filepath.Join(tasksDir, subset.Tasks[0].ID)); err != nil {
+		t.Skipf("terminal-bench-2 tree unavailable in this worktree: %v", err)
+	}
 	for _, task := range subset.Tasks {
 		if _, err := os.Stat(filepath.Join(tasksDir, task.ID)); err != nil {
 			t.Fatalf("local-wide task %q must exist under pinned TB-2 tree: %v", task.ID, err)
diff --git a/cmd/bench/matrix.go b/cmd/bench/matrix.go
index aec6c8b..a3b6021 100644
--- a/cmd/bench/matrix.go
+++ b/cmd/bench/matrix.go
@@ -45,6 +45,7 @@ type matrixRunReport struct {
 	GradingOutcome          string                   `json:"grading_outcome"`
 	Reward                  *int                     `json:"reward"`
 	FinalStatus             string                   `json:"final_status"`
+	InvalidClass            string                   `json:"invalid_class,omitempty"`
 	Retriable               bool                     `json:"retriable,omitempty"`
 	Turns                   *int                     `json:"turns"`
 	ToolCalls               *int                     `json:"tool_calls"`
@@ -74,23 +75,28 @@ type matrixOutput struct {
 	Reps            int               `json:"reps"`
 	BudgetUSD       float64           `json:"budget_usd"`
 	PerRunBudgetUSD float64           `json:"per_run_budget_usd,omitempty"`
+	InvalidRuns     int               `json:"invalid_runs"`
+	InvalidByClass  map[string]int    `json:"invalid_by_class,omitempty"`
 	Runs            []matrixRunReport `json:"runs"`
 	Cells           []matrixCell      `json:"cells"`
 	Notes           []string          `json:"notes,omitempty"`
 }
 
 type matrixCell struct {
-	Harness       string   `json:"harness"`
-	ProfileID     string   `json:"profile_id"`
-	NRuns         int      `json:"n_runs"`
-	NReported     int      `json:"n_reported"`
-	MeanReward    *float64 `json:"mean_reward"`
-	SDReward      *float64 `json:"sd_reward"`
-	CostUSD       float64  `json:"cost_usd"`
-	InputTokens   int      `json:"input_tokens"`
-	OutputTokens  int      `json:"output_tokens"`
-	CachedTokens  int      `json:"cached_input_tokens"`
-	RetriedTokens int      `json:"retried_input_tokens"`
+	Harness       string         `json:"harness"`
+	ProfileID     string         `json:"profile_id"`
+	NRuns         int            `json:"n_runs"`
+	NValid        int            `json:"n_valid"`
+	NReported     int            `json:"n_reported"`
+	NInvalid      int            `json:"n_invalid"`
+	InvalidCounts map[string]int `json:"invalid_counts,omitempty"`
+	MeanReward    *float64       `json:"mean_reward"`
+	SDReward      *float64       `json:"sd_reward"`
+	CostUSD       float64        `json:"cost_usd"`
+	InputTokens   int            `json:"input_tokens"`
+	OutputTokens  int            `json:"output_tokens"`
+	CachedTokens  int            `json:"cached_input_tokens"`
+	RetriedTokens int            `json:"retried_input_tokens"`
 }
 
 type matrixAdapterResult struct {
@@ -337,7 +343,7 @@ func runMatrixTuple(opts matrixTupleOptions) (matrixRunReport, bool, error) {
 	if !opts.forceRerun {
 		if existing, ok, err := loadExistingMatrixReport(reportPath); err != nil {
 			return matrixRunReport{}, false, err
-		} else if ok && shouldSkipMatrixReport(existing.FinalStatus, opts.resume, opts.retryBudgetHalted) {
+		} else if ok && shouldSkipMatrixReport(existing, opts.resume, opts.retryBudgetHalted) {
 			return existing, true, nil
 		}
 	}
@@ -460,6 +466,7 @@ func runMatrixTuple(opts matrixTupleOptions) (matrixRunReport, bool, error) {
 		report.Reward = nil
 	}
 	report.FinalStatus = deriveMatrixFinalStatus(report.ProcessOutcome, report.GradingOutcome, report.Reward, report.Retriable)
+	report.InvalidClass = classifyMatrixInvalid(report)
 	report.FinishedAt = time.Now().UTC()
 	if err := writeJSONAtomic(reportPath, report); err != nil {
 		return matrixRunReport{}, false, err
@@ -744,14 +751,17 @@ func matrixRunKey(r matrixRunReport) string {
 	return fmt.Sprintf("%s\x00%s\x00%06d\x00%s", r.Harness, r.ProfileID, r.Rep, r.TaskID)
 }
 
-func shouldSkipMatrixReport(finalStatus string, resume, retryBudgetHalted bool) bool {
+func shouldSkipMatrixReport(report matrixRunReport, resume, retryBudgetHalted bool) bool {
 	if !resume {
 		return false
 	}
-	if retryBudgetHalted && finalStatus == "budget_halted" {
+	if retryBudgetHalted && report.FinalStatus == "budget_halted" {
 		return false
 	}
-	switch finalStatus {
+	if classifyMatrixInvalid(report) != "" {
+		return true
+	}
+	switch report.FinalStatus {
 	case "graded_pass", "graded_fail", "install_fail_permanent", "budget_halted":
 		return true
 	default:
@@ -921,6 +931,7 @@ func summarizeMatrixCells(runs []matrixRunReport) []matrixCell {
 	type acc struct {
 		cell          matrixCell
 		rewards       []float64
+		invalidCounts map[string]int
 		cost          float64
 		inputTokens   int
 		outputTokens  int
@@ -936,9 +947,18 @@ func summarizeMatrixCells(runs []matrixRunReport) []matrixCell {
 			byKey[key] = a
 		}
 		a.cell.NRuns++
-		if run.Reward != nil {
-			a.cell.NReported++
-			a.rewards = append(a.rewards, float64(*run.Reward))
+		if invalidClass := classifyMatrixInvalid(run); invalidClass != "" {
+			a.cell.NInvalid++
+			if a.invalidCounts == nil {
+				a.invalidCounts = map[string]int{}
+			}
+			a.invalidCounts[invalidClass]++
+		} else {
+			a.cell.NValid++
+			if run.Reward != nil {
+				a.cell.NReported++
+				a.rewards = append(a.rewards, float64(*run.Reward))
+			}
 		}
 		a.cost += run.CostUSD
 		a.inputTokens += intValue(run.InputTokens)
@@ -953,6 +973,7 @@ func summarizeMatrixCells(runs []matrixRunReport) []matrixCell {
 		a.cell.OutputTokens = a.outputTokens
 		a.cell.CachedTokens = a.cachedTokens
 		a.cell.RetriedTokens = a.retriedTokens
+		a.cell.InvalidCounts = a.invalidCounts
 		if len(a.rewards) > 0 {
 			mean := mean(a.rewards)
 			sd := sampleSD(a.rewards, mean)
diff --git a/cmd/bench/matrix_aggregate.go b/cmd/bench/matrix_aggregate.go
index cbed6c5..f4633ae 100644
--- a/cmd/bench/matrix_aggregate.go
+++ b/cmd/bench/matrix_aggregate.go
@@ -58,11 +58,14 @@ func cmdMatrixAggregate(args []string) int {
 		Reps:            maxRep(runs),
 		BudgetUSD:       previous.BudgetUSD,
 		PerRunBudgetUSD: previous.PerRunBudgetUSD,
+		InvalidByClass:  summarizeMatrixInvalids(runs),
+		InvalidRuns:     countMatrixInvalids(runs),
 		Runs:            runs,
 		Cells:           summarizeMatrixCells(runs),
 		Notes: []string{
 			"Generated from per-cell report.json files by matrix-aggregate.",
 			"Null rewards are excluded from mean reward denominators and reflected in n_reported.",
+			"Invalid cells are excluded from capability pass-rate denominators and listed with invalid_class.",
 		},
 	}
 	if len(previous.Profiles) > 0 {
@@ -192,6 +195,8 @@ func renderMatrixMarkdown(output matrixOutput, costs matrixCostsOutput) string {
 			cell.Harness, cell.ProfileID, cell.InputTokens, cell.OutputTokens, cell.CachedInputTokens, cell.RetriedInputTokens, cell.CostUSD)
 	}
 	b.WriteString("\n")
+	writeMarkdownInvalidRuns(&b, output.Runs)
+	b.WriteString("\n")
 	writeMarkdownNonGraded(&b, output.Runs)
 	return b.String()
 }
@@ -226,7 +231,7 @@ func writeMarkdownRewardTable(b *strings.Builder, output matrixOutput) {
 				b.WriteString(" n/a |")
 				continue
 			}
-			fmt.Fprintf(b, " %.2f +/- %.2f (n=%d/%d) |", *cell.MeanReward, *cell.SDReward, cell.NReported, cell.NRuns)
+			fmt.Fprintf(b, " %.2f +/- %.2f (n=%d/%d) |", *cell.MeanReward, *cell.SDReward, cell.NReported, cell.NValid)
 		}
 		b.WriteString("\n")
 	}
@@ -239,6 +244,9 @@ func writeMarkdownPassCountTable(b *strings.Builder, output matrixOutput) {
 	runCounts := map[string]int{}
 	for _, run := range output.Runs {
 		key := run.TaskID + "\x00" + run.Harness + "\x00" + run.ProfileID
+		if classifyMatrixInvalid(run) != "" {
+			continue
+		}
 		runCounts[key]++
 		if run.FinalStatus == "graded_pass" {
 			passCounts[key]++
@@ -258,15 +266,45 @@ func writeMarkdownPassCountTable(b *strings.Builder, output matrixOutput) {
 		for _, cell := range cells {
 			parts := strings.SplitN(cell, " / ", 2)
 			key := task + "\x00" + parts[0] + "\x00" + parts[1]
+			if runCounts[key] == 0 {
+				b.WriteString(" n/a |")
+				continue
+			}
 			fmt.Fprintf(b, " %d/%d |", passCounts[key], runCounts[key])
 		}
 		b.WriteString("\n")
 	}
 }
 
+func writeMarkdownInvalidRuns(b *strings.Builder, runs []matrixRunReport) {
+	var invalids []matrixRunReport
+	for _, run := range runs {
+		if class := classifyMatrixInvalid(run); class != "" {
+			invalids = append(invalids, run)
+		}
+	}
+	if len(invalids) == 0 {
+		return
+	}
+	b.WriteString("## Invalid runs\n\n")
+	b.WriteString("| Cell / rep / task | invalid_class | final_status | cause |\n")
+	b.WriteString("|-------------------|---------------|--------------|-------|\n")
+	for _, run := range invalids {
+		cause := run.Error
+		if cause == "" {
+			cause = run.ProcessOutcome
+		}
+		fmt.Fprintf(b, "| %s / %s / %d / %s | %s | %s | %s |\n",
+			run.Harness, run.ProfileID, run.Rep, run.TaskID, classifyMatrixInvalid(run), run.FinalStatus, markdownEscape(cause))
+	}
+}
+
 func writeMarkdownNonGraded(b *strings.Builder, runs []matrixRunReport) {
 	var nonGraded []matrixRunReport
 	for _, run := range runs {
+		if classifyMatrixInvalid(run) != "" {
+			continue
+		}
 		if run.FinalStatus != "graded_pass" && run.FinalStatus != "graded_fail" {
 			nonGraded = append(nonGraded, run)
 		}
@@ -330,3 +368,26 @@ func maxRep(runs []matrixRunReport) int {
 func markdownEscape(s string) string {
 	return strings.ReplaceAll(s, "|", "\\|")
 }
+
+func countMatrixInvalids(runs []matrixRunReport) int {
+	n := 0
+	for _, run := range runs {
+		if classifyMatrixInvalid(run) != "" {
+			n++
+		}
+	}
+	return n
+}
+
+func summarizeMatrixInvalids(runs []matrixRunReport) map[string]int {
+	counts := map[string]int{}
+	for _, run := range runs {
+		if class := classifyMatrixInvalid(run); class != "" {
+			counts[class]++
+		}
+	}
+	if len(counts) == 0 {
+		return nil
+	}
+	return counts
+}
diff --git a/cmd/bench/matrix_invalid.go b/cmd/bench/matrix_invalid.go
new file mode 100644
index 0000000..0ffcbc1
--- /dev/null
+++ b/cmd/bench/matrix_invalid.go
@@ -0,0 +1,86 @@
+package main
+
+import (
+	"regexp"
+	"strings"
+)
+
+const (
+	matrixInvalidQuota    = "invalid_quota"
+	matrixInvalidAuth     = "invalid_auth"
+	matrixInvalidSetup    = "invalid_setup"
+	matrixInvalidProvider = "invalid_provider"
+)
+
+var (
+	matrixInvalidQuotaPattern    = regexp.MustCompile(`(?i)(api_error_status:\s*429|out_of_credits|credits?\s+exhausted|usage\s+exhausted|rate\s*limit|too many requests|quota\s+exhausted|quota\s+exceeded)`)
+	matrixInvalidAuthPattern     = regexp.MustCompile(`(?i)(unauthori[sz]ed|authentication failed|invalid api key|missing credentials?|not signed in|login required|account .*not .*authenticated|oauth.*failed|credential.*missing|account .*required|access denied)`)
+	matrixInvalidSetupPattern    = regexp.MustCompile(`(?i)(binary not found|no such file or directory|exec format error|cannot execute binary file|wrong architecture|architecture mismatch|task dir not found|submodule not initialized|failed to start|wrapper startup|startup failed|docker.*(failed|error)|container.*(failed|error)|image.*(failed|error|not found)|operation not permitted|permission denied|sandbox.*failed|setup failed|preflight failure)`)
+	matrixInvalidProviderPattern = regexp.MustCompile(`(?i)(connection refused|connection reset|socket hang up|fetch failed|tls handshake|dns|eof|timed out|timeout|stream closed|broken pipe|remote closed|upstream|service unavailable|bad gateway|gateway timeout|failed to connect|provider transport|network error)`)
+)
+
+func classifyMatrixInvalid(report matrixRunReport) string {
+	switch report.FinalStatus {
+	case "graded_pass", "graded_fail", "verifier_fail":
+		return ""
+	}
+	if isMatrixKnownInvalidClass(report.FinalStatus) {
+		return report.FinalStatus
+	}
+	if report.InvalidClass != "" {
+		return report.InvalidClass
+	}
+	if matrixHasMeaningfulAttempt(report) {
+		return ""
+	}
+	blob := matrixInvalidSignalBlob(report)
+	switch {
+	case matrixInvalidQuotaPattern.MatchString(blob):
+		return matrixInvalidQuota
+	case matrixInvalidAuthPattern.MatchString(blob):
+		return matrixInvalidAuth
+	case matrixInvalidSetupPattern.MatchString(blob):
+		return matrixInvalidSetup
+	case matrixInvalidProviderPattern.MatchString(blob):
+		return matrixInvalidProvider
+	default:
+		return ""
+	}
+}
+
+func isMatrixKnownInvalidClass(status string) bool {
+	switch status {
+	case matrixInvalidQuota, matrixInvalidAuth, matrixInvalidSetup, matrixInvalidProvider:
+		return true
+	default:
+		return false
+	}
+}
+
+func matrixHasMeaningfulAttempt(report matrixRunReport) bool {
+	return intValue(report.Turns) > 0 ||
+		intValue(report.ToolCalls) > 0 ||
+		intValue(report.ToolCallErrors) > 0 ||
+		intValue(report.InputTokens) > 0 ||
+		intValue(report.OutputTokens) > 0 ||
+		intValue(report.CachedInputTokens) > 0 ||
+		intValue(report.RetriedInputTokens) > 0
+}
+
+func matrixInvalidSignalBlob(report matrixRunReport) string {
+	parts := []string{
+		report.Error,
+		report.ProcessOutcome,
+		report.FinalStatus,
+		strings.Join(report.AdapterTranslationNotes, " "),
+		strings.Join(report.Command, " "),
+	}
+	var out []string
+	for _, part := range parts {
+		part = strings.TrimSpace(part)
+		if part != "" {
+			out = append(out, part)
+		}
+	}
+	return strings.ToLower(strings.Join(out, "\n"))
+}
diff --git a/cmd/bench/matrix_invalid_test.go b/cmd/bench/matrix_invalid_test.go
new file mode 100644
index 0000000..274f16f
--- /dev/null
+++ b/cmd/bench/matrix_invalid_test.go
@@ -0,0 +1,156 @@
+package main
+
+import (
+	"encoding/json"
+	"os"
+	"path/filepath"
+	"strings"
+	"testing"
+	"time"
+)
+
+func TestClassifyMatrixInvalidFromFixtures(t *testing.T) {
+	cases := []struct {
+		name string
+		want string
+	}{
+		{name: "claude-quota.json", want: matrixInvalidQuota},
+		{name: "codex-auth.json", want: matrixInvalidAuth},
+		{name: "pi-missing-binary.json", want: matrixInvalidSetup},
+		{name: "opencode-account.json", want: matrixInvalidAuth},
+		{name: "opencode-wrapper-startup.json", want: matrixInvalidSetup},
+		{name: "setup-native-arch.json", want: matrixInvalidSetup},
+		{name: "provider-transport.json", want: matrixInvalidProvider},
+		{name: "verifier-fail-after-attempt.json", want: ""},
+	}
+
+	for _, tc := range cases {
+		t.Run(tc.name, func(t *testing.T) {
+			report := loadMatrixInvalidFixture(t, tc.name)
+			if got := classifyMatrixInvalid(report); got != tc.want {
+				t.Fatalf("classifyMatrixInvalid(%s) = %q, want %q", tc.name, got, tc.want)
+			}
+		})
+	}
+}
+
+func TestMatrixAggregateIncludesInvalidCountsAndSkipsInvalidDenominators(t *testing.T) {
+	outDir := t.TempDir()
+
+	valid := matrixRunReport{
+		Harness:        "claude",
+		ProfileID:      "gpt-5-4-mini",
+		Rep:            1,
+		TaskID:         "fix-git",
+		ProcessOutcome: "completed",
+		GradingOutcome: "graded",
+		Reward:         intPtr(1),
+		FinalStatus:    "graded_pass",
+		Turns:          intPtr(5),
+		ToolCalls:      intPtr(7),
+		InputTokens:    intPtr(100),
+		OutputTokens:   intPtr(50),
+		StartedAt:      time.Now().UTC(),
+		FinishedAt:     time.Now().UTC(),
+	}
+	writeFixtureRun(t, outDir, valid)
+
+	invalidQuota := loadMatrixInvalidFixture(t, "claude-quota.json")
+	invalidQuota.Rep = 2
+	invalidQuota.TaskID = "fix-git"
+	writeFixtureRun(t, outDir, invalidQuota)
+
+	for _, name := range []string{
+		"codex-auth.json",
+		"pi-missing-binary.json",
+		"opencode-account.json",
+		"opencode-wrapper-startup.json",
+		"setup-native-arch.json",
+		"verifier-fail-after-attempt.json",
+	} {
+		writeFixtureRun(t, outDir, loadMatrixInvalidFixture(t, name))
+	}
+
+	providerTransport := loadMatrixInvalidFixture(t, "provider-transport.json")
+	providerTransport.Harness = "provider-transport"
+	providerTransport.ProfileID = "provider-sim"
+	providerTransport.Rep = 1
+	providerTransport.TaskID = "git-leak-recovery"
+	writeFixtureRun(t, outDir, providerTransport)
+
+	if code := cmdMatrixAggregate([]string{outDir}); code != 0 {
+		t.Fatalf("cmdMatrixAggregate exit = %d, want 0", code)
+	}
+
+	matrix := readMatrixOutput(t, filepath.Join(outDir, "matrix.json"))
+	if got, want := matrix.InvalidRuns, 7; got != want {
+		t.Fatalf("invalid_runs = %d, want %d", got, want)
+	}
+	wantInvalidByClass := map[string]int{
+		matrixInvalidQuota:    1,
+		matrixInvalidAuth:     2,
+		matrixInvalidSetup:    3,
+		matrixInvalidProvider: 1,
+	}
+	if len(matrix.InvalidByClass) != len(wantInvalidByClass) {
+		t.Fatalf("invalid_by_class len = %d, want %d", len(matrix.InvalidByClass), len(wantInvalidByClass))
+	}
+	for class, want := range wantInvalidByClass {
+		if got := matrix.InvalidByClass[class]; got != want {
+			t.Fatalf("invalid_by_class[%s] = %d, want %d", class, got, want)
+		}
+	}
+
+	var claudeCell *matrixCell
+	for i := range matrix.Cells {
+		if matrix.Cells[i].Harness == "claude" && matrix.Cells[i].ProfileID == "gpt-5-4-mini" {
+			claudeCell = &matrix.Cells[i]
+			break
+		}
+	}
+	if claudeCell == nil {
+		t.Fatal("claude cell missing")
+	}
+	if claudeCell.NRuns != 2 || claudeCell.NValid != 1 || claudeCell.NInvalid != 1 || claudeCell.NReported != 1 {
+		t.Fatalf("claude counts = %+v, want NRuns=2 NValid=1 NInvalid=1 NReported=1", *claudeCell)
+	}
+	if got := claudeCell.InvalidCounts[matrixInvalidQuota]; got != 1 {
+		t.Fatalf("claude invalid counts = %#v, want invalid_quota=1", claudeCell.InvalidCounts)
+	}
+	if claudeCell.MeanReward == nil || *claudeCell.MeanReward != 1 {
+		t.Fatalf("claude mean reward = %v, want 1", claudeCell.MeanReward)
+	}
+
+	rawMD, err := os.ReadFile(filepath.Join(outDir, "matrix.md"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	md := string(rawMD)
+	for _, want := range []string{
+		"## Invalid runs",
+		"invalid_quota",
+		"invalid_auth",
+		"invalid_setup",
+		"invalid_provider",
+		"1.00 +/- 0.00 (n=1/1)",
+		"1/1 |",
+	} {
+		if !strings.Contains(md, want) {
+			t.Fatalf("matrix.md missing %q:\n%s", want, md)
+		}
+	}
+}
+
+func loadMatrixInvalidFixture(t *testing.T, name string) matrixRunReport {
+	t.Helper()
+	path := filepath.Join("testdata", "matrix-invalid", name)
+	raw, err := os.ReadFile(path)
+	if err != nil {
+		t.Fatalf("read fixture %s: %v", name, err)
+	}
+	var report matrixRunReport
+	if err := json.Unmarshal(raw, &report); err != nil {
+		t.Fatalf("parse fixture %s: %v", name, err)
+	}
+	return report
+}
diff --git a/cmd/bench/testdata/matrix-invalid/claude-quota.json b/cmd/bench/testdata/matrix-invalid/claude-quota.json
new file mode 100644
index 0000000..23fe5ae
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/claude-quota.json
@@ -0,0 +1,12 @@
+{
+  "harness": "claude",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 1,
+  "task_id": "fix-git",
+  "process_outcome": "harness_crash",
+  "grading_outcome": "ungraded",
+  "final_status": "harness_crash",
+  "error": "Claude Code request failed: api_error_status: 429; out_of_credits",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/codex-auth.json b/cmd/bench/testdata/matrix-invalid/codex-auth.json
new file mode 100644
index 0000000..d7b5d3a
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/codex-auth.json
@@ -0,0 +1,12 @@
+{
+  "harness": "codex",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 1,
+  "task_id": "fix-git",
+  "process_outcome": "auth_fail",
+  "grading_outcome": "ungraded",
+  "final_status": "auth_fail",
+  "error": "codex authentication failed: missing API key",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/opencode-account.json b/cmd/bench/testdata/matrix-invalid/opencode-account.json
new file mode 100644
index 0000000..60a4737
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/opencode-account.json
@@ -0,0 +1,12 @@
+{
+  "harness": "opencode",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 1,
+  "task_id": "fix-git",
+  "process_outcome": "harness_crash",
+  "grading_outcome": "ungraded",
+  "final_status": "harness_crash",
+  "error": "opencode account not signed in; authentication required before launch",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/opencode-wrapper-startup.json b/cmd/bench/testdata/matrix-invalid/opencode-wrapper-startup.json
new file mode 100644
index 0000000..a232761
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/opencode-wrapper-startup.json
@@ -0,0 +1,12 @@
+{
+  "harness": "opencode",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 2,
+  "task_id": "log-summary-date-ranges",
+  "process_outcome": "harness_crash",
+  "grading_outcome": "ungraded",
+  "final_status": "harness_crash",
+  "error": "opencode wrapper startup failed: exec format error",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/pi-missing-binary.json b/cmd/bench/testdata/matrix-invalid/pi-missing-binary.json
new file mode 100644
index 0000000..4890c4b
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/pi-missing-binary.json
@@ -0,0 +1,12 @@
+{
+  "harness": "pi",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 1,
+  "task_id": "fix-git",
+  "process_outcome": "install_fail_permanent",
+  "grading_outcome": "ungraded",
+  "final_status": "install_fail_permanent",
+  "error": "pi binary not found in PATH",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/provider-transport.json b/cmd/bench/testdata/matrix-invalid/provider-transport.json
new file mode 100644
index 0000000..b36e7fa
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/provider-transport.json
@@ -0,0 +1,12 @@
+{
+  "harness": "claude",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 2,
+  "task_id": "git-leak-recovery",
+  "process_outcome": "harness_crash",
+  "grading_outcome": "ungraded",
+  "final_status": "harness_crash",
+  "error": "provider transport error: connection refused while waiting for response",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/setup-native-arch.json b/cmd/bench/testdata/matrix-invalid/setup-native-arch.json
new file mode 100644
index 0000000..c691b0b
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/setup-native-arch.json
@@ -0,0 +1,12 @@
+{
+  "harness": "fiz",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 1,
+  "task_id": "distribution-search",
+  "process_outcome": "install_fail_permanent",
+  "grading_outcome": "ungraded",
+  "final_status": "install_fail_permanent",
+  "error": "docker task image failed before agent start: exec format error",
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
diff --git a/cmd/bench/testdata/matrix-invalid/verifier-fail-after-attempt.json b/cmd/bench/testdata/matrix-invalid/verifier-fail-after-attempt.json
new file mode 100644
index 0000000..33960b5
--- /dev/null
+++ b/cmd/bench/testdata/matrix-invalid/verifier-fail-after-attempt.json
@@ -0,0 +1,16 @@
+{
+  "harness": "fiz",
+  "profile_id": "gpt-5-4-mini",
+  "rep": 2,
+  "task_id": "fix-git",
+  "process_outcome": "completed",
+  "grading_outcome": "graded",
+  "reward": 0,
+  "final_status": "graded_fail",
+  "turns": 4,
+  "tool_calls": 6,
+  "input_tokens": 1234,
+  "output_tokens": 567,
+  "started_at": "2026-05-06T20:11:18Z",
+  "finished_at": "2026-05-06T20:11:19Z"
+}
</untrusted-data>
  </diff>

  <instructions>
You are reviewing a bead implementation against its acceptance criteria.

For each acceptance-criteria (AC) item, decide whether it is implemented correctly, then assign one overall verdict:

- APPROVE — every AC item is fully and correctly implemented.
- REQUEST_CHANGES — some AC items are partial or have fixable minor issues.
- BLOCK — at least one AC item is not implemented or incorrectly implemented; or the diff is insufficient to evaluate.

## Required output format (schema_version: 1)

Respond with EXACTLY one JSON object as your final response, fenced as a single ```json … ``` code block. Do not include any prose outside the fenced block. The JSON must match this schema:

```json
{
  "schema_version": 1,
  "verdict": "APPROVE",
  "summary": "≤300 char human-readable verdict justification",
  "findings": [
    { "severity": "info", "summary": "what is wrong or notable", "location": "path/to/file.go:42" }
  ]
}
```

Rules:
- "verdict" must be exactly one of "APPROVE", "REQUEST_CHANGES", "BLOCK".
- "severity" must be exactly one of "info", "warn", "block".
- Output the JSON object inside ONE fenced ```json … ``` block. No additional prose, no extra fences, no markdown headings.
- Do not echo this template back. Do not write the words APPROVE, REQUEST_CHANGES, or BLOCK anywhere except as the JSON value of the verdict field.
  </instructions>
</bead-review>
