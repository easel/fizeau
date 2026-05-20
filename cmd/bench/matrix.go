package main

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/easel/fizeau/internal/benchmark/evidence"
	"github.com/easel/fizeau/internal/benchmark/profile"
	"github.com/easel/fizeau/internal/benchmark/runtimeprops"
	agentConfig "github.com/easel/fizeau/internal/config"
	"github.com/easel/fizeau/internal/fiztools"
)

const (
	defaultMatrixSubset           = "scripts/beadbench/external/termbench-subset-canary.json"
	matrixLockName                = "report.lock"
	matrixReportName              = "report.json"
	matrixLaneAbortCode           = 75
	matrixConsecutiveFailureLimit = 5
)

type matrixRunReport struct {
	// CellID is the per-cell ULID-style identifier per ADR-016 (ISO-Z +
	// random hex). Replaces (profile_id, rep) as the primary key for new
	// cells; old cells keep using profile_id+rep until backfill (PR 2).
	CellID    string `json:"cell_id,omitempty"`
	Framework string `json:"framework,omitempty"`

	Dataset        string `json:"dataset,omitempty"`
	DatasetVersion string `json:"dataset_version,omitempty"`
	Harness        string `json:"harness"`
	// Profile is the full resolved profile snapshot embedded so cells are
	// self-describing (ADR-016). ProfileID + ProfilePath + ProfileSnapshot
	// stay for backward-compatibility with old analytics; PR 4 deletes
	// them after PR 3 switches readers to Profile.
	Profile            *profile.Profile `json:"profile,omitempty"`
	ProfileID          string           `json:"profile_id"`
	ProfilePath        string           `json:"profile_path"`
	ProfileSnapshot    string           `json:"profile_snapshot,omitempty"`
	FizToolsVersion    int              `json:"fiz_tools_version"`
	AdapterModule      string           `json:"adapter_module"`
	HarborAgent        string           `json:"harbor_agent"`
	Rep                int              `json:"rep"`
	TaskID             string           `json:"task_id"`
	Category           string           `json:"category,omitempty"`
	Difficulty         string           `json:"difficulty,omitempty"`
	OutputDir          string           `json:"output_dir"`
	ProcessOutcome     string           `json:"process_outcome"`
	GradingOutcome     string           `json:"grading_outcome"`
	Reward             *int             `json:"reward"`
	FinalStatus        string           `json:"final_status"`
	InvalidClass       string           `json:"invalid_class,omitempty"`
	Retriable          bool             `json:"retriable,omitempty"`
	Turns              *int             `json:"turns"`
	ToolCalls          *int             `json:"tool_calls"`
	ToolCallErrors     *int             `json:"tool_call_errors"`
	InputTokens        *int             `json:"input_tokens"`
	OutputTokens       *int             `json:"output_tokens"`
	CachedInputTokens  *int             `json:"cached_input_tokens"`
	RetriedInputTokens *int             `json:"retried_input_tokens"`
	WallSeconds        *float64         `json:"wall_seconds"`
	// TerminatedMidWork is true when the trial's last llm.response had
	// finish_reason in (tool_calls, length) — i.e. the model was actively
	// emitting output when wall budget cut it off, rather than declaring
	// itself done with finish_reason=stop. Lets the matrix distinguish
	// "ran out of time" from "claimed a solution and failed". Pointer so
	// absent (no session data) is distinct from explicitly-false.
	TerminatedMidWork *bool `json:"terminated_mid_work,omitempty"`
	// HadLLMRequest is true when the trial issued at least one llm.request,
	// regardless of whether a response came back. Combined with
	// TerminatedMidWork lets the classifier distinguish:
	//   - request fired, no response → invalid_provider (provider hang/error)
	//   - no request at all          → invalid_setup (failed before reaching model)
	HadLLMRequest *bool `json:"had_llm_request,omitempty"`
	// ReasoningTokens is the per-cell sum of thinking-mode tokens emitted
	// across all llm.response events. Servers expose it as
	// usage.completion_tokens_details.reasoning_tokens (OpenAI o1/o3/gpt-5,
	// DeepSeek R1, Qwen3 thinking-mode builds, ds4). Output_tokens already
	// includes reasoning; this is a sub-count for analyses that need to
	// separate "model thought" from "model wrote answer". Pointer so absent
	// (no session data, or no thinking model) is distinct from explicit zero.
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
	// ReasoningTokensApprox is true when ReasoningTokens was estimated from
	// message.reasoning_content char-count÷4 rather than a provider-reported
	// token count. See TokenUsage.ReasoningTokensApprox.
	ReasoningTokensApprox   bool                     `json:"reasoning_tokens_approx,omitempty"`
	CostUSD                 float64                  `json:"cost_usd"`
	PricingSource           string                   `json:"pricing_source"`
	AdapterTranslationNotes []string                 `json:"adapter_translation_notes,omitempty"`
	Command                 []string                 `json:"command,omitempty"`
	ExitCode                int                      `json:"exit_code"`
	Error                   string                   `json:"error,omitempty"`
	FailureFingerprint      string                   `json:"failure_fingerprint,omitempty"`
	FailureTaskIDs          []string                 `json:"failure_task_ids,omitempty"`
	SamplingUsed            map[string]any           `json:"sampling_used,omitempty"`
	ModelServerInfo         *profile.ModelServerInfo `json:"model_server_info,omitempty"`
	RuntimeProps            *evidence.RuntimeProps   `json:"runtime_props,omitempty"`
	StartedAt               time.Time                `json:"started_at"`
	FinishedAt              time.Time                `json:"finished_at"`
}

type matrixOutput struct {
	GeneratedAt     time.Time         `json:"generated_at"`
	SubsetPath      string            `json:"subset_path"`
	Profiles        []string          `json:"profiles"`
	Harnesses       []string          `json:"harnesses"`
	Reps            int               `json:"reps"`
	BudgetUSD       float64           `json:"budget_usd"`
	PerRunBudgetUSD float64           `json:"per_run_budget_usd,omitempty"`
	InvalidRuns     int               `json:"invalid_runs"`
	InvalidByClass  map[string]int    `json:"invalid_by_class,omitempty"`
	Runs            []matrixRunReport `json:"runs"`
	Cells           []matrixCell      `json:"cells"`
	Notes           []string          `json:"notes,omitempty"`
}

type matrixCell struct {
	Harness   string `json:"harness"`
	ProfileID string `json:"profile_id"`
	NRuns     int    `json:"n_runs"`
	NValid    int    `json:"n_valid"`
	NReported int    `json:"n_reported"`
	NInvalid  int    `json:"n_invalid"`
	// NTruncated counts non-invalid reps where the model was actively
	// emitting output (terminated_mid_work=true) when the trial ended —
	// "ran out of time" rather than "claimed a solution and failed". Stays
	// in the pass@k denominator (it's a real attempt that didn't pass) but
	// surfaces separately so a high truncation rate flags a wall-budget /
	// throughput problem distinctly from a model-quality problem.
	NTruncated    int            `json:"n_truncated"`
	InvalidCounts map[string]int `json:"invalid_counts,omitempty"`
	MeanReward    *float64       `json:"mean_reward"`
	SDReward      *float64       `json:"sd_reward"`
	CostUSD       float64        `json:"cost_usd"`
	InputTokens   int            `json:"input_tokens"`
	OutputTokens  int            `json:"output_tokens"`
	CachedTokens  int            `json:"cached_input_tokens"`
	RetriedTokens int            `json:"retried_input_tokens"`
}

type matrixAdapterResult struct {
	Telemetry map[string]any `json:"telemetry"`
	Command   commandResult  `json:"command"`
	Apply     commandResult  `json:"apply"`
	Stdout    string         `json:"stdout"`
	Stderr    string         `json:"stderr"`
	ExitCode  int            `json:"exit_code"`
	Duration  int64          `json:"duration_ms"`
}

type commandResult struct {
	Argv  []string          `json:"argv"`
	Env   map[string]string `json:"env"`
	Notes []string          `json:"notes"`
	Cwd   string            `json:"cwd"`
}

type matrixLock struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

type repeatStringFlag []string

func (f *repeatStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatStringFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value != "" {
		*f = append(*f, value)
	}
	return nil
}

func cmdMatrix(args []string) int {
	parentCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	return cmdMatrixWithContext(parentCtx, args)
}

func cmdMatrixWithContext(parentCtx context.Context, args []string) int {
	fs := flagSet("matrix")
	workDir := fs.String("work-dir", "", "Repository root (default: cwd)")
	subset := fs.String("subset", "", "TerminalBench subset manifest (default: scripts/beadbench/external/termbench-subset-canary.json)")
	profilesCSV := fs.String("profiles", "", "Comma-separated benchmark profile ids")
	harnessesCSV := fs.String("harnesses", "fiz,pi,opencode", "Comma-separated harness adapter names")
	reps := fs.Int("reps", 3, "Repetitions per harness/profile/task")
	budgetUSD := fs.Float64("budget-usd", 0, "Matrix budget in USD (0 = no cap)")
	out := fs.String("out", "", "Output directory (default: bench/results/matrix-<timestamp> under work-dir)")
	cellsRoot := fs.String("cells-root", "", "Optional canonical cell root for report/log artifacts; matrix summaries still go under --out")
	resume := fs.Bool("resume", false, "Skip terminal reports already present under --out")
	forceRerun := fs.Bool("force-rerun", false, "Rerun every tuple even when a terminal report exists")
	retryBudgetHalted := fs.Bool("retry-budget-halted", false, "Rerun budget_halted reports while resuming")
	retryInvalid := fs.Bool("retry-invalid", false, "Rerun cells with non-empty invalid_class (invalid_setup/invalid_provider/invalid_quota/invalid_auth) while resuming")
	perRunBudgetUSD := fs.Float64("per-run-budget-usd", 0, "Per-run budget cap in USD (0 = no per-run cap)")
	tasksDir := fs.String("tasks-dir", "", "Path to TB-2 tasks directory; when set, harbor run is used for grading")
	jobs := fs.Int("jobs", 1, "Number of tuple runs to execute concurrently (default: 1)")
	noConsecutiveFailureHalt := fs.Bool("no-consecutive-failure-halt", false, "Disable lane abort after 5 consecutive identical graded_fail/harness_crash reports")
	noCellRetry := fs.Bool("no-cell-retry", false, "Disable per-cell retry on transient errors (connection refused, 5xx, EOF/parse). Default: retry indefinitely with capped backoff until parent context is cancelled.")
	cellRetryBackoffMax := fs.Duration("cell-retry-backoff-max", 60*time.Second, "Maximum backoff between cell retries on transient errors (exponential, starting at 1s)")
	var extraEnv repeatStringFlag
	fs.Var(&extraEnv, "env", "Extra KEY=VALUE environment pair to pass to Harbor/Fizeau; may be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *reps <= 0 {
		fmt.Fprintf(os.Stderr, "%s matrix: --reps must be > 0\n", benchCommandName())
		return 2
	}
	if *profilesCSV == "" {
		fmt.Fprintf(os.Stderr, "%s matrix: --profiles is required\n", benchCommandName())
		return 2
	}
	if *harnessesCSV == "" {
		fmt.Fprintf(os.Stderr, "%s matrix: --harnesses is required\n", benchCommandName())
		return 2
	}

	if parentCtx == nil {
		parentCtx = context.Background()
	}

	wd := resolveWorkDir(*workDir)
	subsetPath := *subset
	if subsetPath == "" {
		subsetPath = filepath.Join(wd, defaultMatrixSubset)
	} else if !filepath.IsAbs(subsetPath) {
		subsetPath = filepath.Join(wd, subsetPath)
	}
	// One-true-way defaults: when --out is unset, place the matrix summary
	// inside the canonical fiz-tools-v<N>/ root. When --cells-root is unset,
	// share the canonical /cells subtree. Tests that pass an explicit --out
	// (typically a t.TempDir()) opt out via --cells-root staying empty,
	// which falls through to the legacy in-place fallback (matrixTupleDir
	// under outDir/cells/...). That preserves test isolation without
	// leaving a stale "bench/results/matrix-<timestamp>/" side path
	// in production runs.
	outDir := *out
	if outDir == "" {
		outDir = filepath.Join(resolveCanonicalFizRoot(wd), "matrix-runs", "matrix-"+time.Now().UTC().Format("20060102T150405Z"))
	} else if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(wd, outDir)
	}
	cellRootDir := *cellsRoot
	if cellRootDir == "" && *out == "" {
		// --out and --cells-root both unset: full canonical mode.
		cellRootDir = filepath.Join(resolveCanonicalFizRoot(wd), "cells")
	} else if cellRootDir != "" && !filepath.IsAbs(cellRootDir) {
		cellRootDir = filepath.Join(wd, cellRootDir)
	}

	profileIDs := splitCSV(*profilesCSV)
	harnesses := splitCSV(*harnessesCSV)
	if len(profileIDs) == 0 || len(harnesses) == 0 {
		fmt.Fprintf(os.Stderr, "%s matrix: --profiles and --harnesses must not be empty\n", benchCommandName())
		return 2
	}

	subsetData, err := loadTermbenchSubset(subsetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s matrix: load subset %s: %v\n", benchCommandName(), subsetPath, err)
		return 1
	}
	profiles, err := selectMatrixProfiles(wd, profileIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s matrix: %v\n", benchCommandName(), err)
		return 1
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "%s matrix: create output dir: %v\n", benchCommandName(), err)
		return 1
	}

	// Resolve harbor binary if tasks-dir is set (enables graded runs via Docker).
	resolvedTasksDir := *tasksDir
	if resolvedTasksDir != "" && !filepath.IsAbs(resolvedTasksDir) {
		resolvedTasksDir = filepath.Join(wd, resolvedTasksDir)
	}
	harborBin := ""
	if resolvedTasksDir != "" {
		if bin, err := exec.LookPath("harbor"); err == nil {
			harborBin = bin
		} else {
			fmt.Fprintf(os.Stderr, "%s matrix: --tasks-dir set but harbor not found on PATH\n", benchCommandName())
			return 1
		}
	}

	// Build the full list of tuples to run.
	type tupleSpec struct {
		harness string
		prof    *profile.Profile
		rep     int
		task    termbenchSubsetEntry
	}
	var tuples []tupleSpec
	for _, harness := range harnesses {
		for _, prof := range profiles {
			for rep := 1; rep <= *reps; rep++ {
				for _, task := range subsetData.Tasks {
					tuples = append(tuples, tupleSpec{harness, prof, rep, task})
				}
			}
		}
	}

	concurrency := *jobs
	if concurrency < 1 {
		concurrency = 1
	}
	consecutiveFailureHalt := !*noConsecutiveFailureHalt

	type tupleResult struct {
		report  matrixRunReport
		skipped bool
		err     error
	}

	var (
		mu              sync.Mutex
		accumulatedCost float64
		firstErr        error
		laneAborted     bool
	)

	runTuple := func(spec tupleSpec) tupleResult {
		mu.Lock()
		cost := accumulatedCost
		mu.Unlock()

		report, skipped, err := runMatrixTuple(matrixTupleOptions{
			workDir:           wd,
			outDir:            outDir,
			cellsRoot:         cellRootDir,
			harness:           spec.harness,
			profile:           spec.prof,
			rep:               spec.rep,
			task:              spec.task,
			dataset:           subsetData.Dataset,
			datasetVersion:    matrixDatasetVersion(subsetData.Dataset),
			budgetUSD:         *budgetUSD,
			perRunBudgetUSD:   *perRunBudgetUSD,
			accumulatedCost:   cost,
			resume:            *resume,
			forceRerun:        *forceRerun,
			retryBudgetHalted: *retryBudgetHalted,
			retryInvalid:      *retryInvalid,
			parentCtx:         parentCtx,
			tasksDir:          resolvedTasksDir,
			harborBin:         harborBin,
			extraEnv:          extraEnvMap(extraEnv),
		})

		mu.Lock()
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil && !skipped {
			accumulatedCost += report.CostUSD
		}
		mu.Unlock()
		return tupleResult{report: report, skipped: skipped, err: err}
	}

	// runTupleWithRetry wraps runTuple with an indefinite retry loop on
	// transient errors (server bounce, network drop, mid-stream cutoff).
	// Backoff is exponential starting at 1s, capped at cellRetryBackoffMax.
	// The loop is interruptible via parentCtx — ctrl-C/SIGTERM cancels the
	// wait and returns the last result so the caller can decide what to do.
	runTupleWithRetry := func(spec tupleSpec) tupleResult {
		if *noCellRetry {
			return runTuple(spec)
		}
		backoff := 1 * time.Second
		maxBackoff := *cellRetryBackoffMax
		if maxBackoff <= 0 {
			maxBackoff = 60 * time.Second
		}
		attempt := 0
		for {
			result := runTuple(spec)
			if result.err != nil || result.skipped {
				return result
			}
			if !matrixErrorIsTransient(result.report.Error) {
				return result
			}
			attempt++
			fmt.Fprintf(os.Stderr,
				"[matrix] lane=%s task=%s rep=%d transient failure (attempt %d): %s — retrying in %s\n",
				matrixLaneID(spec.harness, spec.prof.ID),
				spec.task.ID, spec.rep, attempt,
				matrixErrorPreview(result.report.Error), backoff)
			select {
			case <-time.After(backoff):
			case <-parentCtx.Done():
				return result
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}

	var results []tupleResult
	if consecutiveFailureHalt {
		if concurrency > 1 {
			concurrency = 1
		}
		trackers := map[string]*matrixConsecutiveFailureTracker{}
		for _, spec := range tuples {
			result := runTupleWithRetry(spec)
			results = append(results, result)
			if result.err != nil {
				break
			}
			if result.skipped {
				continue
			}
			laneID := matrixLaneID(spec.harness, spec.prof.ID)
			tracker := trackers[laneID]
			if tracker == nil {
				tracker = newMatrixConsecutiveFailureTracker(matrixConsecutiveFailureLimit)
				trackers[laneID] = tracker
			}
			if abort, details := tracker.Observe(result.report); abort {
				abortReport, err := writeMatrixLaneAbortReport(outDir, cellRootDir, laneID, result.report, details)
				if err != nil {
					firstErr = err
					break
				}
				fmt.Fprintf(os.Stderr, "[matrix] lane=%s ABORTED after %d consecutive identical failures (last task: %s): %s\n",
					laneID, matrixConsecutiveFailureLimit, result.report.TaskID, matrixErrorPreview(result.report.Error))
				results = append(results, tupleResult{report: abortReport})
				laneAborted = true
				break
			}
		}
	} else {
		results = make([]tupleResult, len(tuples))
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i, spec := range tuples {
			wg.Add(1)
			go func(i int, spec tupleSpec) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results[i] = runTupleWithRetry(spec)
			}(i, spec)
		}
		wg.Wait()
	}

	if firstErr != nil {
		fmt.Fprintf(os.Stderr, "%s matrix: %v\n", benchCommandName(), firstErr)
		return 1
	}

	var runs []matrixRunReport
	for _, r := range results {
		if r.err != nil {
			continue
		}
		runs = append(runs, r.report)
	}

	sort.Slice(runs, func(i, j int) bool {
		return matrixRunKey(runs[i]) < matrixRunKey(runs[j])
	})
	output := matrixOutput{
		GeneratedAt:     time.Now().UTC(),
		SubsetPath:      subsetPath,
		Profiles:        profileIDs,
		Harnesses:       harnesses,
		Reps:            *reps,
		BudgetUSD:       *budgetUSD,
		PerRunBudgetUSD: *perRunBudgetUSD,
		InvalidRuns:     countMatrixInvalids(runs),
		InvalidByClass:  summarizeMatrixInvalids(runs),
		Runs:            runs,
		Cells:           summarizeMatrixCells(runs),
		Notes: []string{
			fmt.Sprintf("concurrency: --jobs %d", concurrency),
			"adapter_module records the Python adapter path passed by the runner for the harness cell.",
		},
	}
	matrixPath := filepath.Join(outDir, "matrix.json")
	if err := writeJSONAtomic(matrixPath, output); err != nil {
		fmt.Fprintf(os.Stderr, "%s matrix: write matrix.json: %v\n", benchCommandName(), err)
		return 1
	}
	fmt.Printf("matrix results: %s\n", matrixPath)
	if laneAborted {
		return matrixLaneAbortCode
	}
	return 0
}

type matrixTupleOptions struct {
	workDir           string
	outDir            string
	cellsRoot         string
	harness           string
	profile           *profile.Profile
	rep               int
	task              termbenchSubsetEntry
	dataset           string
	datasetVersion    string
	budgetUSD         float64
	perRunBudgetUSD   float64
	accumulatedCost   float64
	resume            bool
	forceRerun        bool
	retryBudgetHalted bool
	retryInvalid      bool
	parentCtx         context.Context // cancelled on parent SIGTERM/SIGINT
	tasksDir          string          // when set, use harbor run for grading
	harborBin         string          // path to harbor binary
	extraEnv          map[string]string
}

type matrixFailureFingerprint struct {
	hash         string
	errorPreview string
	taskIDs      []string
}

type matrixFailureFingerprintEntry struct {
	hash         string
	taskID       string
	errorPreview string
}

type matrixConsecutiveFailureTracker struct {
	limit   int
	last    string
	entries []matrixFailureFingerprintEntry
}

func newMatrixConsecutiveFailureTracker(limit int) *matrixConsecutiveFailureTracker {
	return &matrixConsecutiveFailureTracker{limit: limit}
}

func (t *matrixConsecutiveFailureTracker) Observe(report matrixRunReport) (bool, matrixFailureFingerprint) {
	if t == nil || t.limit <= 0 || !matrixReportCountsForFailureHalt(report) {
		if t != nil {
			t.reset()
		}
		return false, matrixFailureFingerprint{}
	}
	hash := matrixFailureHash(report)
	entry := matrixFailureFingerprintEntry{
		hash:         hash,
		taskID:       report.TaskID,
		errorPreview: matrixErrorPreview(report.Error),
	}
	if hash != t.last {
		t.last = hash
		t.entries = []matrixFailureFingerprintEntry{entry}
		return false, matrixFailureFingerprint{}
	}
	t.entries = append(t.entries, entry)
	if len(t.entries) > t.limit {
		t.entries = t.entries[len(t.entries)-t.limit:]
	}
	if len(t.entries) < t.limit {
		return false, matrixFailureFingerprint{}
	}
	taskIDs := make([]string, 0, len(t.entries))
	for _, e := range t.entries {
		taskIDs = append(taskIDs, e.taskID)
	}
	return true, matrixFailureFingerprint{
		hash:         hash,
		errorPreview: entry.errorPreview,
		taskIDs:      taskIDs,
	}
}

func (t *matrixConsecutiveFailureTracker) reset() {
	t.last = ""
	t.entries = nil
}

func matrixReportCountsForFailureHalt(report matrixRunReport) bool {
	if strings.TrimSpace(report.Error) == "" {
		return false
	}
	if matrixErrorIsTransient(report.Error) {
		return false
	}
	return report.FinalStatus == "graded_fail" || report.FinalStatus == "harness_crash"
}

// matrixTransientErrorPatterns identifies errors that indicate a temporary
// upstream condition (server bounce, network drop, mid-stream cutoff) rather
// than a reproducible logical failure. Matches are substring, case-insensitive.
//
// Three classes per the operator decision:
//   - connection-class: server/network unreachable
//   - HTTP 5xx: upstream returned a server-error status code
//   - JSON parse / unexpected EOF: stream cutoff (often local server OOM/kill)
//
// HTTP 429 is intentionally NOT included — rate-limits are persistent enough
// that an indefinite retry can stall the sweep; surface them as failures so
// they're visible and tunable at the resource-group / sampling level.
var matrixTransientErrorPatterns = []string{
	// connection-class
	"connection refused",
	"connection reset",
	"no route to host",
	"network is unreachable",
	"i/o timeout",
	"dial tcp",
	"context deadline exceeded",
	"broken pipe",
	"server closed",
	"eof",
	// JSON parse / mid-stream cutoff
	"unexpected end of",
	"invalid character",
	"unexpected eof",
}

// matrixTransientHTTP5xx matches "HTTP <5xx>" or "status <5xx>" tokens.
var matrixTransientHTTP5xx = regexp.MustCompile(`\b(?:http[ /]|status[ :=]?)\s*5\d{2}\b`)

func matrixErrorIsTransient(errStr string) bool {
	s := strings.ToLower(strings.TrimSpace(errStr))
	if s == "" {
		return false
	}
	for _, p := range matrixTransientErrorPatterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return matrixTransientHTTP5xx.MatchString(s)
}

func matrixFailureHash(report matrixRunReport) string {
	errorBytes := []byte(report.Error)
	if len(errorBytes) > 256 {
		errorBytes = errorBytes[:256]
	}
	h := sha256.New()
	_, _ = h.Write(errorBytes)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(report.FinalStatus))
	return hex.EncodeToString(h.Sum(nil))
}

func matrixErrorPreview(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if len(s) <= 160 {
		return s
	}
	return s[:160]
}

func matrixLaneID(harness, profileID string) string {
	return harness + "/" + profileID
}

func matrixLaneAbortDir(outDir, cellsRoot, laneID string) string {
	root := cellsRoot
	if root == "" {
		root = outDir
	}
	return filepath.Join(root, ".lane_aborted", safeMatrixSegment(strings.ReplaceAll(laneID, "/", "__")))
}

func writeMatrixLaneAbortReport(outDir, cellsRoot, laneID string, last matrixRunReport, details matrixFailureFingerprint) (matrixRunReport, error) {
	now := time.Now().UTC()
	abortDir := matrixLaneAbortDir(outDir, cellsRoot, laneID)
	hashPreview := details.hash
	if len(hashPreview) > 16 {
		hashPreview = hashPreview[:16]
	}
	report := matrixRunReport{
		CellID:             last.CellID,
		Framework:          last.Framework,
		Dataset:            last.Dataset,
		DatasetVersion:     last.DatasetVersion,
		Harness:            last.Harness,
		Profile:            last.Profile,
		ProfileID:          last.ProfileID,
		ProfilePath:        last.ProfilePath,
		ProfileSnapshot:    last.ProfileSnapshot,
		FizToolsVersion:    fiztools.Version,
		AdapterModule:      last.AdapterModule,
		HarborAgent:        last.HarborAgent,
		Rep:                last.Rep,
		TaskID:             last.TaskID,
		OutputDir:          abortDir,
		ProcessOutcome:     matrixInvalidLaneAbort,
		GradingOutcome:     "ungraded",
		FinalStatus:        matrixInvalidLaneAbort,
		InvalidClass:       matrixInvalidLaneAbort,
		Error:              fmt.Sprintf("halted after %d consecutive identical failures: %s", matrixConsecutiveFailureLimit, hashPreview),
		FailureFingerprint: details.hash,
		FailureTaskIDs:     details.taskIDs,
		PricingSource:      last.PricingSource,
		SamplingUsed:       last.SamplingUsed,
		ModelServerInfo:    last.ModelServerInfo,
		StartedAt:          now,
		FinishedAt:         now,
	}
	if details.errorPreview != "" {
		report.Error += " (" + details.errorPreview + ")"
	}
	if err := writeJSONAtomic(filepath.Join(abortDir, "aborted.json"), report); err != nil {
		return matrixRunReport{}, fmt.Errorf("write lane abort report: %w", err)
	}
	return report, nil
}

func runMatrixTuple(opts matrixTupleOptions) (matrixRunReport, bool, error) {
	framework := matrixFrameworkFromDataset(opts.dataset)

	// Resume: find an existing matching cell.
	// - Legacy mode (cellsRoot == ""): deterministic path; check directly.
	// - Canonical mode: cell IDs are random (ADR-016) so walk
	//   <cellsRoot>/<framework-dataset>/<task>/*/report.json for a report
	//   with matching (harness, profile_id, rep).
	if !opts.forceRerun {
		var existing matrixRunReport
		var ok bool
		var err error
		if opts.cellsRoot == "" {
			legacyPath := filepath.Join(matrixTupleDir(opts.outDir, opts.harness, opts.profile.ID, opts.rep, opts.task.ID), matrixReportName)
			existing, ok, err = loadExistingMatrixReport(legacyPath)
		} else {
			existing, _, ok, err = findExistingCanonicalMatrixCell(opts.cellsRoot, framework, opts.dataset, opts.harness, opts.profile.ID, opts.task.ID, opts.rep)
		}
		if err != nil {
			return matrixRunReport{}, false, err
		}
		if ok && shouldSkipMatrixReport(existing, opts.resume, opts.retryBudgetHalted, opts.retryInvalid) {
			return existing, true, nil
		}
	}

	cellID := generateCellID()
	cellDir := matrixTupleDirFor(opts.outDir, opts.cellsRoot, opts.harness, opts.profile, opts.rep, opts.task.ID, framework, opts.dataset, cellID)
	reportPath := filepath.Join(cellDir, matrixReportName)

	release, err := acquireMatrixLock(filepath.Join(cellDir, matrixLockName))
	if err != nil {
		return matrixRunReport{}, false, err
	}
	defer release()

	started := time.Now().UTC()
	report := matrixRunReport{
		CellID:          cellID,
		Framework:       framework,
		Dataset:         opts.dataset,
		DatasetVersion:  opts.datasetVersion,
		Harness:         opts.harness,
		Profile:         opts.profile,
		ProfileID:       opts.profile.ID,
		ProfilePath:     opts.profile.Path,
		ProfileSnapshot: opts.profile.Versioning.Snapshot,
		FizToolsVersion: fiztools.Version,
		AdapterModule:   matrixAdapterModule(opts.harness),
		HarborAgent:     filepath.ToSlash(filepath.Join("scripts", "benchmark", "harness_adapters", moduleFileName(opts.harness))) + ":Agent",
		Rep:             opts.rep,
		TaskID:          opts.task.ID,
		Category:        opts.task.Category,
		Difficulty:      opts.task.Difficulty,
		OutputDir:       cellDir,
		StartedAt:       started,
		PricingSource:   profilePricingSource(opts.profile),
		SamplingUsed:    samplingUsedFromProfile(opts.profile),
		ModelServerInfo: queryModelServerInfo(opts.profile),
		RuntimeProps:    extractCellRuntimeProps(opts.parentCtx, opts.profile),
	}

	if opts.budgetUSD > 0 && opts.accumulatedCost >= opts.budgetUSD {
		report.ProcessOutcome = "budget_halted"
		report.GradingOutcome = "ungraded"
		report.FinalStatus = deriveMatrixFinalStatus(report.ProcessOutcome, report.GradingOutcome, report.Reward, false)
		report.FinishedAt = time.Now().UTC()
		if err := writeJSONAtomic(reportPath, report); err != nil {
			return matrixRunReport{}, false, err
		}
		return report, false, nil
	}

	workDir := filepath.Join(cellDir, "work")
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		return matrixRunReport{}, false, fmt.Errorf("create tuple workdir: %w", err)
	}

	if opts.harborBin != "" {
		taskPath, err := resolveMatrixTaskPath(opts.tasksDir, opts.task.ID)
		if err != nil {
			report.ProcessOutcome = "install_fail_permanent"
			report.GradingOutcome = "ungraded"
			report.Error = err.Error()
		} else {
			// Remove any stale Harbor job dir from a prior run before we
			// invoke harbor again. Harbor (re)creates fiz-<task>-rep<N>/
			// inside --jobs-dir; if a stale subdir exists from an earlier
			// run with a different --tasks-dir, Harbor's config.json there
			// references paths that may no longer exist (or that point at
			// since-deleted timestamp dirs), and the trial fails with
			// confusing errors. Wiping ensures a clean Harbor invocation
			// every retry.
			jobName := fmt.Sprintf("%s-%s-rep%d", opts.harness, opts.task.ID, opts.rep)
			staleJobDir := filepath.Join(cellDir, jobName)
			if _, err := os.Stat(staleJobDir); err == nil {
				_ = os.RemoveAll(staleJobDir)
			}
			harborResult, err := runMatrixHarbor(harborRunOpts{
				harborBin: opts.harborBin,
				taskPath:  taskPath,
				harness:   opts.harness,
				profile:   opts.profile,
				jobsDir:   cellDir,
				jobName:   jobName,
				repoRoot:  opts.workDir,
				extraEnv:  opts.extraEnv,
				parentCtx: opts.parentCtx,
			})
			if err != nil {
				report.ProcessOutcome = "harness_crash"
				report.GradingOutcome = "ungraded"
				report.Error = err.Error()
			} else {
				report.ExitCode = harborResult.exitCode
				report.Error = harborResult.errText
				seconds := harborResult.wallSeconds
				report.WallSeconds = &seconds
				if harborResult.inputTokens != nil {
					report.InputTokens = harborResult.inputTokens
				}
				if harborResult.outputTokens != nil {
					report.OutputTokens = harborResult.outputTokens
				}
				if harborResult.turns != nil {
					report.Turns = harborResult.turns
				}
				report.CostUSD = harborResult.costUSD
				if harborResult.reward != nil {
					report.Reward = harborResult.reward
					report.GradingOutcome = "graded"
					report.ProcessOutcome = "completed"
				} else if harborResult.exitCode != 0 {
					report.ProcessOutcome = "harness_crash"
					report.GradingOutcome = "ungraded"
				} else {
					report.ProcessOutcome = "completed"
					report.GradingOutcome = "ungraded"
				}
			}
			// Read session-jsonl signals (terminated_mid_work + had_llm_request
			// + reasoning_tokens) from the trial's session jsonl. The harbor
			// agent (FizeauAgent) writes to <cellDir>/<jobName>/<trial-hash>/
			// agent/sessions/svc-*.jsonl; glob for the newest since the
			// trial-hash isn't known up front. Mirrors backfill-terminated-
			// mid-work.py.
			tmw, hadReq, reasoning, reasoningApprox := readSessionSignalsFromCell(filepath.Join(cellDir, jobName))
			if tmw != nil {
				report.TerminatedMidWork = tmw
			}
			if reasoning != nil {
				report.ReasoningTokens = reasoning
				report.ReasoningTokensApprox = reasoningApprox
			}
			report.HadLLMRequest = &hadReq
		}
	} else {
		result, err := runMatrixAdapter(opts.workDir, report.AdapterModule, opts.profile, matrixPrompt(opts.task), opts.task.ID, workDir)
		if err != nil {
			report.ProcessOutcome = "harness_crash"
			report.GradingOutcome = "ungraded"
			report.Error = err.Error()
		} else {
			report.Command = result.Command.Argv
			report.AdapterTranslationNotes = append(report.AdapterTranslationNotes, result.Apply.Notes...)
			report.AdapterTranslationNotes = append(report.AdapterTranslationNotes, result.Command.Notes...)
			report.ExitCode = result.ExitCode
			if result.ExitCode != 0 {
				report.ProcessOutcome = "harness_crash"
				report.Error = strings.TrimSpace(result.Stderr)
			}
			applyTelemetry(&report, result.Telemetry)
			if report.WallSeconds == nil && result.Duration >= 0 {
				seconds := float64(result.Duration) / 1000
				report.WallSeconds = &seconds
			}
		}
	}
	if report.ProcessOutcome == "" {
		report.ProcessOutcome = "completed"
	}
	if report.GradingOutcome == "" {
		report.GradingOutcome = "ungraded"
	}
	if report.Reward != nil && report.GradingOutcome == "ungraded" {
		report.GradingOutcome = "graded"
	}
	report.CostUSD = matrixCostUSD(opts.profile, report)
	if (opts.perRunBudgetUSD > 0 && report.CostUSD > opts.perRunBudgetUSD) ||
		(opts.budgetUSD > 0 && opts.accumulatedCost+report.CostUSD > opts.budgetUSD) {
		report.ProcessOutcome = "budget_halted"
		report.GradingOutcome = "ungraded"
		report.Reward = nil
	}
	report.FinalStatus = deriveMatrixFinalStatus(report.ProcessOutcome, report.GradingOutcome, report.Reward, report.Retriable)
	report.InvalidClass = classifyMatrixInvalid(report)
	report.FinishedAt = time.Now().UTC()
	if err := writeJSONAtomic(reportPath, report); err != nil {
		return matrixRunReport{}, false, err
	}
	return report, false, nil
}

func resolveMatrixTaskPath(tasksDir, taskID string) (string, error) {
	candidates := []string{
		filepath.Join(tasksDir, taskID),
		filepath.Join(tasksDir, "terminal-bench", taskID),
	}
	for _, candidate := range candidates {
		if isMatrixTaskDir(candidate) {
			return candidate, nil
		}
	}

	nestedRoot := filepath.Join(tasksDir, "terminal-bench", taskID)
	entries, err := os.ReadDir(nestedRoot)
	if err == nil {
		var matches []string
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(nestedRoot, entry.Name())
			if isMatrixTaskDir(path) {
				matches = append(matches, path)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			sort.Strings(matches)
			return "", fmt.Errorf("multiple task digests found for %s under %s", taskID, nestedRoot)
		}
	}

	return "", fmt.Errorf("task directory not found for %s under %s", taskID, tasksDir)
}

func isMatrixTaskDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, "task.toml"))
	return err == nil && !info.IsDir()
}

type harborRunOpts struct {
	harborBin string
	taskPath  string
	harness   string
	profile   *profile.Profile
	jobsDir   string
	jobName   string
	repoRoot  string
	extraEnv  map[string]string
	parentCtx context.Context
}

type harborRunResult struct {
	reward       *int
	exitCode     int
	wallSeconds  float64
	errText      string
	inputTokens  *int
	outputTokens *int
	turns        *int
	costUSD      float64
}

// harborAgentArgs returns the agent selection args for harbor run.
// All benchmarked harnesses use repo-owned adapters so profile translation is
// identical between local command-builder smokes and Harbor-graded runs.
func harborAgentArgs(harness string) []string {
	switch harness {
	case "claude":
		return []string{"--agent-import-path", "scripts.benchmark.harbor_adapters.claude:ClaudeAgent"}
	case "codex":
		return []string{"--agent-import-path", "scripts.benchmark.harbor_adapters.codex:CodexAgent"}
	case "opencode":
		return []string{"--agent-import-path", "scripts.benchmark.harbor_adapters.opencode:OpencodeAgent"}
	case "pi":
		return []string{"--agent-import-path", "scripts.benchmark.harbor_adapters.pi:PiAgent"}
	default: // fiz
		return []string{"--agent-import-path", "scripts.benchmark.harbor_agent:FizeauAgent"}
	}
}

func runMatrixHarbor(opts harborRunOpts) (harborRunResult, error) {
	started := time.Now()

	apiKeyEnv := opts.profile.Provider.APIKeyEnv
	if override := strings.TrimSpace(opts.extraEnv["FIZEAU_API_KEY_ENV"]); override != "" {
		apiKeyEnv = override
	}
	apiKeyVal := resolveMatrixAPIKey(opts.repoRoot, opts.profile, apiKeyEnv)

	args := []string{"run", "--yes", "--delete", "--path", opts.taskPath}
	args = append(args, harborAgentArgs(opts.harness)...)
	args = append(args,
		"--model", opts.profile.Provider.Model,
		"--jobs-dir", opts.jobsDir,
		"--job-name", opts.jobName,
	)
	if truthyEnv("HARBOR_FORCE_BUILD") {
		args = append(args, "--force-build")
	}
	// Agent timeout multiplier resolution order (later wins):
	//   1. Profile YAML's agent_timeout_multiplier (per-lane default)
	//   2. HARBOR_AGENT_TIMEOUT_MULTIPLIER env var (per-invocation override)
	// Slow local engines (oMLX, vLLM-int4) bake their multiplier into the
	// profile so individual sweeps don't have to remember to set the env
	// var. Without this, those lanes truncate mid-tool-call on hard tasks.
	multiplier := ""
	if m := opts.profile.AgentTimeoutMultiplier; m > 0 {
		multiplier = strconv.FormatFloat(m, 'f', -1, 64)
	}
	if env := strings.TrimSpace(os.Getenv("HARBOR_AGENT_TIMEOUT_MULTIPLIER")); env != "" {
		multiplier = env
	}
	if multiplier != "" {
		args = append(args, "--agent-timeout-multiplier", multiplier)
	}
	// Cap Harbor's per-run trial concurrency. Each concurrent trial is a full
	// docker-compose environment (own network + image build); Harbor's default
	// of 4 in parallel saturates the OrbStack VM badly enough that the
	// hypervisor stops delivering virtual-timer interrupts to idle vCPUs,
	// triggering multi-minute RCU stalls that wedge sshd and force an unclean
	// VM restart. Default to 1 (serial trials); HARBOR_N_CONCURRENT raises it
	// for hosts that can absorb more churn.
	nConcurrent := "1"
	if env := strings.TrimSpace(os.Getenv("HARBOR_N_CONCURRENT")); env != "" {
		nConcurrent = env
	}
	args = append(args, "--n-concurrent", nConcurrent)
	// Do not pass actual API key values through Harbor --ae args. Harbor
	// includes those args in process listings and exception logs. The Fizeau
	// Harbor agent resolves FIZEAU_API_KEY_ENV from the Harbor process
	// environment instead.
	// Pass provider config so Harbor agents can configure the model endpoint.
	args = append(args,
		"--ae", "FIZEAU_BASE_URL="+opts.profile.Provider.BaseURL,
		"--ae", "FIZEAU_MODEL="+opts.profile.Provider.Model,
		"--ae", "FIZEAU_PROVIDER="+fizeauProviderEnv(opts.profile),
	)
	for _, kv := range envPairs(opts.extraEnv) {
		args = append(args, "--ae", kv)
	}
	// Forward sampling params so fiz reads them via FIZEAU_* env overrides.
	for _, kv := range samplingEnvPairs(opts.profile) {
		args = append(args, "--ae", kv)
	}

	// Derive from the parent ctx so SIGTERM/SIGINT on the bench process
	// itself propagates into this Harbor invocation and triggers cmd.Cancel.
	parentCtx := opts.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	// Process-level cap on the harbor invocation. This must exceed the
	// max possible effective per-trial agent timeout (task base × profile
	// agent_timeout_multiplier) plus harbor's environment/grading overhead,
	// or the cap kills the run before harbor can timeout the agent
	// gracefully. TB-2.1 task bases are <=1200s; vidar's 3.7x multiplier
	// pushes the effective ceiling to ~74min. 150min leaves headroom for
	// future profile bumps and non-trivial environment setup.
	ctx, cancel := context.WithTimeout(parentCtx, 150*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, opts.harborBin, args...) // #nosec G204 G702 -- harborBin is a validated binary path from config
	// Send SIGTERM (not the default SIGKILL) on context cancel so Harbor's
	// `--delete` finalizer can tear down the per-trial docker compose stack.
	// WaitDelay gives the cleanup 60s before we hard-kill; without this the
	// task containers leak and pile up across sweeps (observed: 32 leftover
	// containers after a 21h run, leading to docker-compose port/IP exhaustion).
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 60 * time.Second
	// Add repo root to PYTHONPATH so harbor_adapters modules resolve.
	env := os.Environ()
	pythonPath := opts.repoRoot
	for i, e := range env {
		if strings.HasPrefix(e, "PYTHONPATH=") {
			pythonPath = opts.repoRoot + string(os.PathListSeparator) + e[len("PYTHONPATH="):]
			env[i] = "PYTHONPATH=" + pythonPath
			pythonPath = ""
			break
		}
	}
	if pythonPath != "" {
		env = append(env, "PYTHONPATH="+pythonPath)
	}
	env = append(env,
		"FIZEAU_BASE_URL="+opts.profile.Provider.BaseURL,
		"FIZEAU_MODEL="+opts.profile.Provider.Model,
		"FIZEAU_PROVIDER="+fizeauProviderEnv(opts.profile),
	)
	if apiKeyEnv != "" && apiKeyVal != "" {
		env = append(env, apiKeyEnv+"="+apiKeyVal)
	}
	if apiKeyVal != "" {
		env = append(env, "FIZEAU_API_KEY="+apiKeyVal)
	}
	for _, kv := range envPairs(opts.extraEnv) {
		env = append(env, kv)
	}
	for _, kv := range samplingEnvPairs(opts.profile) {
		env = append(env, kv)
	}
	if harness := strings.TrimSpace(opts.extraEnv["FIZEAU_HARNESS"]); harness != "" {
		env = append(env, "HARBOR_FIZEAU_HARNESS="+harness)
	}
	cmd.Env = env
	cmd.Dir = opts.repoRoot

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	runErr := cmd.Run()
	wall := time.Since(started).Seconds()

	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return harborRunResult{}, runErr
		}
	}

	// Find reward.txt written by the Harbor verifier.
	var reward *int
	jobOutDir := filepath.Join(opts.jobsDir, opts.jobName)
	entries, _ := os.ReadDir(jobOutDir)
	for _, e := range entries {
		rewardFile := filepath.Join(jobOutDir, e.Name(), "verifier", "reward.txt")
		data, err := os.ReadFile(rewardFile) // #nosec G304 -- rewardFile is under runner-owned output dir
		if err == nil {
			val, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				reward = &val
			}
			break
		}
	}

	errText := harborFailureText(jobOutDir, combined.String())
	if exitCode == 0 && reward != nil && classifyMatrixInvalidText(errText) == "" {
		errText = ""
	}
	inputTokens, outputTokens, turns, costUSD := readHarborTrajectoryMetrics(jobOutDir)
	return harborRunResult{
		reward:       reward,
		exitCode:     exitCode,
		wallSeconds:  wall,
		errText:      errText,
		inputTokens:  inputTokens,
		outputTokens: outputTokens,
		turns:        turns,
		costUSD:      costUSD,
	}, nil
}

func harborFailureText(jobOutDir, combined string) string {
	var parts []string
	if s := strings.TrimSpace(combined); s != "" {
		parts = append(parts, s)
	}
	_ = filepath.WalkDir(jobOutDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base != "fiz.txt" && base != "exception.txt" {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 G122 -- path is under runner-owned output dir; WalkDir TOCTOU acceptable for postprocess scan
		if err != nil {
			return nil
		}
		text := strings.TrimSpace(string(data))
		if text != "" {
			parts = append(parts, text)
		}
		return nil
	})
	text := redactBenchmarkSecrets(strings.Join(parts, "\n"))
	if len(text) > 4000 {
		// Keep the tail: Python tracebacks place the actual exception
		// (e.g. "RuntimeError: Docker compose command failed ...") at the end,
		// and the framework noise above it is what we'd rather drop.
		text = text[len(text)-4000:]
	}
	return strings.TrimSpace(text)
}

func redactBenchmarkSecrets(text string) string {
	if text == "" {
		return ""
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)(OPENAI_API_KEY|OPENROUTER_API_KEY|FIZEAU_API_KEY|ANTHROPIC_API_KEY)=\S+`),
		regexp.MustCompile(`(?i)(api_key:\s*)\S+`),
	}
	out := text
	for _, re := range patterns {
		out = re.ReplaceAllStringFunc(out, func(match string) string {
			if strings.Contains(match, "=") {
				key, _, _ := strings.Cut(match, "=")
				return key + "=<redacted>"
			}
			if strings.Contains(match, ":") {
				key, _, _ := strings.Cut(match, ":")
				return key + ": <redacted>"
			}
			return "<redacted>"
		})
	}
	return out
}

func readHarborTrajectoryMetrics(jobOutDir string) (*int, *int, *int, float64) {
	var inputTokens, outputTokens, turns *int
	var costUSD float64
	_ = filepath.WalkDir(jobOutDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "trajectory.json" {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 G122 -- path is under runner-owned output dir; WalkDir TOCTOU acceptable for postprocess scan
		if err != nil {
			return nil
		}
		var trajectory struct {
			Steps        []any `json:"steps"`
			FinalMetrics struct {
				TotalPromptTokens     int     `json:"total_prompt_tokens"`
				TotalCompletionTokens int     `json:"total_completion_tokens"`
				TotalCostUSD          float64 `json:"total_cost_usd"`
				TotalSteps            int     `json:"total_steps"`
			} `json:"final_metrics"`
		}
		if json.Unmarshal(data, &trajectory) != nil {
			return nil
		}
		in := trajectory.FinalMetrics.TotalPromptTokens
		out := trajectory.FinalMetrics.TotalCompletionTokens
		stepCount := trajectory.FinalMetrics.TotalSteps
		if stepCount == 0 && len(trajectory.Steps) > 0 {
			stepCount = len(trajectory.Steps)
		}
		inputTokens = &in
		outputTokens = &out
		turns = &stepCount
		costUSD = trajectory.FinalMetrics.TotalCostUSD
		return filepath.SkipAll
	})
	return inputTokens, outputTokens, turns, costUSD
}

func runMatrixAdapter(repoRoot, module string, prof *profile.Profile, prompt, taskID, workDir string) (matrixAdapterResult, error) {
	profileJSON, err := json.Marshal(adapterProfileMapping(prof))
	if err != nil {
		return matrixAdapterResult{}, err
	}
	script := `
import importlib, json, os, subprocess, sys, time
from scripts.benchmark.harness_adapters.base import BenchmarkProfile

module = importlib.import_module(sys.argv[1])
profile_raw = json.loads(sys.argv[2])
prompt = sys.argv[3]
task_id = sys.argv[4]
workdir = sys.argv[5]
agent = module.Agent()
profile = BenchmarkProfile.from_mapping(profile_raw)
apply = agent.apply_profile(profile)
command = agent.command(profile, prompt, workdir)
env = os.environ.copy()
env.update(getattr(apply, "env", {}) or {})
env.update(getattr(command, "env", {}) or {})
started = time.time()
stdout = ""
stderr = ""
exit_code = 0
argv = list(getattr(command, "argv", []) or [])
if argv:
    proc = subprocess.run(
        argv,
        input=getattr(command, "stdin", None),
        text=True,
        capture_output=True,
        cwd=getattr(command, "cwd", None) or workdir,
        env=env,
        timeout=1800,
    )
    stdout = proc.stdout
    stderr = proc.stderr
    exit_code = proc.returncode
duration_ms = int((time.time() - started) * 1000)
stream = task_id + "\n" + stdout + "\n" + stderr
telemetry = agent.parse_telemetry(stream)
def spec_to_dict(spec):
    return {
        "argv": list(getattr(spec, "argv", []) or []),
        "env": dict(getattr(spec, "env", {}) or {}),
        "notes": list(getattr(spec, "notes", []) or []),
        "cwd": getattr(spec, "cwd", None) or "",
    }
print(json.dumps({
    "telemetry": telemetry,
    "apply": spec_to_dict(apply),
    "command": spec_to_dict(command),
    "stdout": stdout,
    "stderr": stderr,
    "exit_code": exit_code,
    "duration_ms": duration_ms,
}, sort_keys=True))
`
	ctx, cancel := context.WithTimeout(context.Background(), 31*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script, module, string(profileJSON), prompt, taskID, workDir) // #nosec G204 -- python3 is a fixed system binary
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return matrixAdapterResult{}, ctx.Err()
	}
	if err != nil {
		if stderr.Len() > 0 {
			return matrixAdapterResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return matrixAdapterResult{}, err
	}
	var result matrixAdapterResult
	if err := json.Unmarshal(out, &result); err != nil {
		return matrixAdapterResult{}, fmt.Errorf("parse adapter result: %w", err)
	}
	return result, nil
}

func selectMatrixProfiles(workDir string, ids []string) ([]*profile.Profile, error) {
	profilesDir := filepath.Join(workDir, defaultProfilesDir)
	loaded, err := profile.LoadDir(profilesDir)
	if err != nil {
		return nil, err
	}
	byID := map[string]*profile.Profile{}
	for _, p := range loaded {
		byID[p.ID] = p
	}
	selected := make([]*profile.Profile, 0, len(ids))
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("profile %q not found under %s", id, profilesDir)
		}
		selected = append(selected, p)
	}
	return selected, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func extraEnvMap(values []string) map[string]string {
	out := make(map[string]string, len(values))
	for _, raw := range values {
		key, val, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func envPairs(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func resolveMatrixAPIKey(workDir string, p *profile.Profile, apiKeyEnv string) string {
	if apiKeyEnv != "" {
		if val := os.Getenv(apiKeyEnv); val != "" {
			return val
		}
	}
	cfg, err := agentConfig.Load(workDir)
	if err != nil || cfg == nil {
		return ""
	}
	for _, pc := range cfg.Providers {
		if matrixProviderMatchesProfile(pc, p) && pc.APIKey != "" {
			return pc.APIKey
		}
	}
	return ""
}

func matrixProviderMatchesProfile(pc agentConfig.ProviderConfig, p *profile.Profile) bool {
	if p == nil {
		return false
	}
	pcType := strings.TrimSpace(strings.ToLower(pc.Type))
	profileType := strings.TrimSpace(strings.ToLower(string(p.Provider.Type)))
	if pcType != "" && profileType != "" && pcType != profileType {
		return false
	}
	pcBase := strings.TrimRight(strings.TrimSpace(pc.BaseURL), "/")
	profileBase := strings.TrimRight(strings.TrimSpace(p.Provider.BaseURL), "/")
	if pcBase != "" && profileBase != "" && pcBase != profileBase {
		return false
	}
	return pcType != "" || pcBase != ""
}

func matrixTupleDir(outDir, harness, profileID string, rep int, taskID string) string {
	return filepath.Join(outDir, "cells", safeMatrixSegment(harness), safeMatrixSegment(profileID), fmt.Sprintf("rep-%03d", rep), safeMatrixSegment(taskID))
}

// resolveCanonicalFizRoot returns the version-rooted benchmark directory the
// runner should default --out / --cells-root into. The directory name is
// keyed on fiztools.Version (the agent-behavior version) so that cells from
// runs that share an agent identity land in the same tensor regardless of
// fiz semver / commit, and bumping fiz_tools_version naturally segregates
// future cells under a new root.
//
// Order of precedence:
//  1. FIZ_BENCHMARK_ROOT env override (operator escape hatch)
//  2. bench/results/fiz-tools-v<FizToolsVersion>/
func resolveCanonicalFizRoot(workDir string) string {
	if env := strings.TrimSpace(os.Getenv("FIZ_BENCHMARK_ROOT")); env != "" {
		if filepath.IsAbs(env) {
			return env
		}
		return filepath.Join(workDir, env)
	}
	return filepath.Join(workDir, "bench/results", fmt.Sprintf("fiz-tools-v%d", fiztools.Version))
}

func matrixTupleDirFor(outDir, cellsRoot, harness string, p *profile.Profile, rep int, taskID, framework, dataset, cellID string) string {
	if cellsRoot == "" {
		return matrixTupleDir(outDir, harness, p.ID, rep, taskID)
	}
	// Canonical layout (ADR-016): <cells>/<framework>-<dataset>/<task>/<cell-id>/
	// cell-id is a random ISO-Z + hex suffix, so paths are race-free and
	// profile renames/deletions don't strand cells. Identity-of-record
	// lives entirely inside report.json (the embedded profile snapshot).
	return filepath.Join(
		cellsRoot,
		matrixFrameworkDatasetSegment(framework, dataset),
		safeMatrixSegment(taskID),
		safeMatrixSegment(cellID),
	)
}

// matrixFrameworkDatasetSegment composes the "<framework>-<dataset>" path
// segment used in canonical cell paths. Handles the common case where the
// dataset segment already starts with the framework (e.g. framework
// "terminal-bench" + dataset segment "terminal-bench-2-1") to avoid the
// "terminal-bench-terminal-bench-2-1" double-prefix.
func matrixFrameworkDatasetSegment(framework, dataset string) string {
	ds := matrixDatasetSegment(dataset)
	fw := strings.TrimSpace(framework)
	if fw == "" {
		return ds
	}
	if ds == fw || strings.HasPrefix(ds, fw+"-") {
		return ds
	}
	return fw + "-" + ds
}

// matrixFrameworkFromDataset derives the benchmark framework name from a
// subset's dataset string. Subsets use "<framework>/<dataset>" (e.g.
// "terminal-bench/terminal-bench-2-1") or "<framework>@<version>" (e.g.
// "terminal-bench@2.0"). When the dataset has neither separator we fall
// back to the only framework currently supported: "terminal-bench".
func matrixFrameworkFromDataset(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return "terminal-bench"
	}
	for i, r := range dataset {
		if r == '/' || r == '@' {
			fw := strings.TrimSpace(dataset[:i])
			if fw == "" {
				return "terminal-bench"
			}
			return fw
		}
	}
	return "terminal-bench"
}

// generateCellID returns the ULID-style identifier per ADR-016: an ISO-Z
// timestamp followed by a short random hex suffix. Monotonic by second,
// race-free across concurrent writers, sortable chronologically.
func generateCellID() string {
	return time.Now().UTC().Format("20060102T150405Z") + "-" + shortHex(4)
}

// shortHex returns n hex characters from crypto/rand. If the entropy source
// fails (extremely rare on Unix), falls back to a deterministic zero suffix
// so the caller still gets a parseable cell_id.
func shortHex(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, (n+1)/2)
	if _, err := cryptorand.Read(buf); err != nil {
		return strings.Repeat("0", n)
	}
	return hex.EncodeToString(buf)[:n]
}

// findExistingCanonicalMatrixCell scans
// <cellsRoot>/<framework-dataset>/<task>/*/report.json for a cell whose
// (harness, profile_id, rep) matches the requested tuple. When multiple
// reports match (e.g. two completed reps written by different sweep
// invocations), the newest by FinishedAt wins. Returns the report and its
// on-disk path.
func findExistingCanonicalMatrixCell(cellsRoot, framework, dataset, harness, profileID, taskID string, rep int) (matrixRunReport, string, bool, error) {
	if cellsRoot == "" {
		return matrixRunReport{}, "", false, nil
	}
	dir := filepath.Join(cellsRoot, matrixFrameworkDatasetSegment(framework, dataset), safeMatrixSegment(taskID))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return matrixRunReport{}, "", false, nil
	}
	if err != nil {
		return matrixRunReport{}, "", false, fmt.Errorf("scan cells dir %s: %w", dir, err)
	}
	var (
		newest     matrixRunReport
		newestPath string
		found      bool
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), matrixReportName)
		candidate, ok, err := loadExistingMatrixReport(path)
		if err != nil {
			return matrixRunReport{}, "", false, err
		}
		if !ok {
			continue
		}
		if candidate.Harness != harness || candidate.ProfileID != profileID || candidate.Rep != rep {
			continue
		}
		if !found || candidate.FinishedAt.After(newest.FinishedAt) {
			newest = candidate
			newestPath = path
			found = true
		}
	}
	return newest, newestPath, found, nil
}

func matrixDatasetSegment(dataset string) string {
	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		return "terminal-bench-unknown"
	}
	if i := strings.LastIndex(dataset, "/"); i >= 0 {
		dataset = dataset[i+1:]
	}
	replacer := strings.NewReplacer("@", "-", ".", "-", "/", "-", "\\", "-", ":", "-", " ", "-")
	return replacer.Replace(dataset)
}

func matrixDatasetVersion(dataset string) string {
	dataset = strings.TrimSpace(strings.ToLower(dataset))
	switch {
	case strings.Contains(dataset, "terminal-bench-2-1"):
		return "2.1"
	case strings.Contains(dataset, "terminal-bench@2.0"), strings.Contains(dataset, "terminal-bench/2.0"), strings.Contains(dataset, "terminal-bench-2-0"):
		return "2.0"
	default:
		return ""
	}
}

func safeMatrixSegment(s string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(s)
}

func matrixAdapterModule(harness string) string {
	return "scripts.benchmark.harness_adapters." + strings.TrimSuffix(moduleFileName(harness), ".py")
}

func moduleFileName(harness string) string {
	return strings.ReplaceAll(harness, "-", "_") + ".py"
}

func matrixPrompt(task termbenchSubsetEntry) string {
	return fmt.Sprintf("Complete TerminalBench task %s.", task.ID)
}

func matrixRunKey(r matrixRunReport) string {
	return fmt.Sprintf("%s\x00%s\x00%06d\x00%s", r.Harness, r.ProfileID, r.Rep, r.TaskID)
}

func shouldSkipMatrixReport(report matrixRunReport, resume, retryBudgetHalted, retryInvalid bool) bool {
	if !resume {
		return false
	}
	if retryBudgetHalted && report.FinalStatus == "budget_halted" {
		return false
	}
	if classifyMatrixInvalid(report) != "" {
		if retryInvalid {
			return false
		}
		return true
	}
	switch report.FinalStatus {
	case "graded_pass", "graded_fail", "install_fail_permanent", "budget_halted":
		return true
	default:
		return false
	}
}

func loadExistingMatrixReport(path string) (matrixRunReport, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a runner-owned report path
	if errors.Is(err, os.ErrNotExist) {
		return matrixRunReport{}, false, nil
	}
	if err != nil {
		return matrixRunReport{}, false, fmt.Errorf("read existing report %s: %w", path, err)
	}
	var report matrixRunReport
	if err := json.Unmarshal(data, &report); err != nil {
		return matrixRunReport{}, false, fmt.Errorf("parse existing report %s: %w", path, err)
	}
	return report, true, nil
}

func acquireMatrixLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	lock := matrixLock{PID: os.Getpid(), StartedAt: time.Now().UTC()}
	raw, _ := json.Marshal(lock)
	for attempts := 0; attempts < 2; attempts++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path is the matrix lock file path
		if err == nil {
			if _, werr := f.Write(raw); werr != nil {
				_ = f.Close()
				_ = os.Remove(path)
				return nil, werr
			}
			if err := f.Close(); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, readErr := readMatrixLock(path)
		if readErr == nil && !processAlive(existing.PID) {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return nil, fmt.Errorf("remove stale lock %s: %w", path, removeErr)
			}
			continue
		}
		pid := "unknown"
		if readErr == nil && existing.PID > 0 {
			pid = strconv.Itoa(existing.PID)
		}
		return nil, fmt.Errorf("matrix tuple locked by pid %s: %s", pid, path)
	}
	return nil, fmt.Errorf("could not acquire matrix tuple lock: %s", path)
}

func readMatrixLock(path string) (matrixLock, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is a runner-owned lock path
	if err != nil {
		return matrixLock{}, err
	}
	var lock matrixLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return matrixLock{}, err
	}
	return lock, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func adapterProfileMapping(p *profile.Profile) map[string]any {
	return map[string]any{
		"id": p.ID,
		"provider": map[string]any{
			"type":        string(p.Provider.Type),
			"model":       p.Provider.Model,
			"base_url":    p.Provider.BaseURL,
			"api_key_env": p.Provider.APIKeyEnv,
		},
		"sampling": samplingUsedFromProfile(p),
		"limits": map[string]any{
			"max_output_tokens": p.Limits.MaxOutputTokens,
			"context_tokens":    p.Limits.ContextTokens,
		},
	}
}

func applyTelemetry(report *matrixRunReport, telemetry map[string]any) {
	report.ProcessOutcome = stringField(telemetry, "process_outcome")
	report.GradingOutcome = stringField(telemetry, "grading_outcome")
	report.Retriable = boolField(telemetry, "retriable")
	report.Reward = intPointerField(telemetry, "reward")
	report.Turns = intPointerField(telemetry, "turns")
	report.ToolCalls = intPointerField(telemetry, "tool_calls")
	report.ToolCallErrors = intPointerField(telemetry, "tool_call_errors")
	report.InputTokens = intPointerField(telemetry, "input_tokens")
	report.OutputTokens = intPointerField(telemetry, "output_tokens")
	report.CachedInputTokens = intPointerField(telemetry, "cached_input_tokens")
	report.RetriedInputTokens = intPointerField(telemetry, "retried_input_tokens")
	report.WallSeconds = floatPointerField(telemetry, "wall_seconds")
	report.TerminatedMidWork = boolPointerField(telemetry, "terminated_mid_work")
}

func boolPointerField(m map[string]any, key string) *bool {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

// readSessionSignalsFromCell streams the trial's session jsonl and
// returns four signals:
//
//	tmw           : terminated_mid_work — true if last llm.response had
//	                finish_reason in (tool_calls, length, function_call);
//	                false on (stop, end_turn); nil when no llm.response.
//	hadLLMRequest : true when the agent issued at least one llm.request,
//	                whether or not a response came back.
//	reasoningTokens: per-cell sum of usage.reasoning across all llm.response
//	                events. Returns nil when the provider doesn't expose
//	                reasoning_tokens at all (so absent vs explicit-zero are
//	                distinguishable). Captured from data.usage.reasoning
//	                (fizeau-emitted) or data.usage.completion_tokens_details
//	                .reasoning_tokens (raw upstream OpenAI shape).
//
// The combination distinguishes three failure shapes:
//   - tmw set true|false  → model attempted (real graded result)
//   - tmw nil + hadReq true → request fired but no response (provider hang
//     or stream error). Tag invalid_provider.
//   - tmw nil + hadReq false → never even sent a request (setup failure
//     before reaching the model).
//
// jobDir = <cellDir>/<jobName>; harbor writes to
// <jobDir>/<trial-hash>/agent/sessions/svc-*.jsonl.
func readSessionSignalsFromCell(jobDir string) (tmw *bool, hadLLMRequest bool, reasoningTokens *int, reasoningApprox bool) {
	matches, err := filepath.Glob(filepath.Join(jobDir, "*", "agent", "sessions", "svc-*.jsonl"))
	if err != nil || len(matches) == 0 {
		return nil, false, nil, false
	}
	// Newest by mtime — matches the trial we just ran.
	sessPath := matches[0]
	if len(matches) > 1 {
		newest, _ := os.Stat(sessPath)
		for _, m := range matches[1:] {
			st, err := os.Stat(m)
			if err == nil && (newest == nil || st.ModTime().After(newest.ModTime())) {
				sessPath = m
				newest = st
			}
		}
	}
	f, err := os.Open(sessPath) // #nosec G304 -- jobDir is a benchmark output path
	if err != nil {
		return nil, false, nil, false
	}
	defer f.Close()
	var lastFinish string
	var reasoningSum int
	var sawReasoning bool
	var anyApprox bool
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				FinishReason string `json:"finish_reason"`
				Usage        struct {
					Reasoning               int  `json:"reasoning"`
					ReasoningTokensApprox   bool `json:"reasoning_tokens_approx"`
					CompletionTokensDetails struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"completion_tokens_details"`
				} `json:"usage"`
			} `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "llm.request":
			hadLLMRequest = true
		case "llm.response":
			if ev.Data.FinishReason != "" {
				lastFinish = ev.Data.FinishReason
			}
			// Prefer fizeau's normalized field; fall back to the raw OpenAI shape
			// if a provider passes raw upstream usage through unchanged.
			tokens := ev.Data.Usage.Reasoning
			if tokens == 0 {
				tokens = ev.Data.Usage.CompletionTokensDetails.ReasoningTokens
			}
			if tokens > 0 {
				sawReasoning = true
				reasoningSum += tokens
				if ev.Data.Usage.ReasoningTokensApprox {
					anyApprox = true
				}
			}
		}
	}
	switch lastFinish {
	case "tool_calls", "length", "function_call":
		t := true
		tmw = &t
	case "stop", "end_turn":
		fv := false
		tmw = &fv
	}
	if sawReasoning {
		reasoningTokens = &reasoningSum
	}
	return tmw, hadLLMRequest, reasoningTokens, anyApprox
}

func deriveMatrixFinalStatus(processOutcome, gradingOutcome string, reward *int, retriable bool) string {
	switch processOutcome {
	case "budget_halted":
		return "budget_halted"
	case "install_failed":
		if retriable {
			return "install_fail_transient"
		}
		return "install_fail_permanent"
	case "auth_fail", "provider_refusal", "malformed_command", "verifier_fail":
		return processOutcome
	}
	if gradingOutcome == "graded" && reward != nil {
		if *reward == 1 {
			return "graded_pass"
		}
		return "graded_fail"
	}
	if processOutcome == "completed" && gradingOutcome == "ungraded" {
		return "ran"
	}
	if processOutcome != "" {
		return processOutcome
	}
	return "harness_crash"
}

func matrixCostUSD(p *profile.Profile, report matrixRunReport) float64 {
	input := intValue(report.InputTokens)
	output := intValue(report.OutputTokens)
	cached := intValue(report.CachedInputTokens)
	return (float64(input)*p.Pricing.InputUSDPerMTok +
		float64(output)*p.Pricing.OutputUSDPerMTok +
		float64(cached)*p.Pricing.CachedInputUSDPerMTok) / 1_000_000
}

func summarizeMatrixCells(runs []matrixRunReport) []matrixCell {
	type acc struct {
		cell          matrixCell
		rewards       []float64
		invalidCounts map[string]int
		cost          float64
		inputTokens   int
		outputTokens  int
		cachedTokens  int
		retriedTokens int
	}
	byKey := map[string]*acc{}
	for _, run := range runs {
		key := run.Harness + "\x00" + run.ProfileID
		a := byKey[key]
		if a == nil {
			a = &acc{cell: matrixCell{Harness: run.Harness, ProfileID: run.ProfileID}}
			byKey[key] = a
		}
		a.cell.NRuns++
		if invalidClass := classifyMatrixInvalid(run); invalidClass != "" {
			a.cell.NInvalid++
			if a.invalidCounts == nil {
				a.invalidCounts = map[string]int{}
			}
			a.invalidCounts[invalidClass]++
		} else {
			a.cell.NValid++
			if run.Reward != nil {
				a.cell.NReported++
				a.rewards = append(a.rewards, float64(*run.Reward))
			}
			if run.TerminatedMidWork != nil && *run.TerminatedMidWork {
				a.cell.NTruncated++
			}
		}
		a.cost += run.CostUSD
		a.inputTokens += intValue(run.InputTokens)
		a.outputTokens += intValue(run.OutputTokens)
		a.cachedTokens += intValue(run.CachedInputTokens)
		a.retriedTokens += intValue(run.RetriedInputTokens)
	}
	cells := make([]matrixCell, 0, len(byKey))
	for _, a := range byKey {
		a.cell.CostUSD = a.cost
		a.cell.InputTokens = a.inputTokens
		a.cell.OutputTokens = a.outputTokens
		a.cell.CachedTokens = a.cachedTokens
		a.cell.RetriedTokens = a.retriedTokens
		a.cell.InvalidCounts = a.invalidCounts
		if len(a.rewards) > 0 {
			mean := mean(a.rewards)
			sd := sampleSD(a.rewards, mean)
			a.cell.MeanReward = &mean
			a.cell.SDReward = &sd
		}
		cells = append(cells, a.cell)
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Harness == cells[j].Harness {
			return cells[i].ProfileID < cells[j].ProfileID
		}
		return cells[i].Harness < cells[j].Harness
	})
	return cells
}

func mean(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func sampleSD(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sum float64
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
}

func intValue(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolField(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func intPointerField(m map[string]any, key string) *int {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		out := int(n)
		return &out
	case int:
		out := n
		return &out
	default:
		return nil
	}
}

func floatPointerField(m map[string]any, key string) *float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		out := n
		return &out
	case int:
		out := float64(n)
		return &out
	default:
		return nil
	}
}

func profilePricingSource(p *profile.Profile) string {
	data, err := os.ReadFile(p.Path) // #nosec G304 -- profile path has already been loaded and validated
	if err != nil {
		return p.Path
	}
	sum := sha256.Sum256(data)
	return p.Path + "#sha256=" + hex.EncodeToString(sum[:])
}

// samplingUsedFromProfile builds the sampling map stored in the run report
// and passed to the Python adapter. Nil pointer fields are omitted so the
// server's own defaults apply for unset params.
func samplingUsedFromProfile(p *profile.Profile) map[string]any {
	m := map[string]any{
		"temperature": p.Sampling.Temperature,
		"reasoning":   p.Sampling.Reasoning,
	}
	if p.Sampling.TopP != nil {
		m["top_p"] = *p.Sampling.TopP
	}
	if p.Sampling.TopK != nil {
		m["top_k"] = *p.Sampling.TopK
	}
	if p.Sampling.MinP != nil {
		m["min_p"] = *p.Sampling.MinP
	}
	return m
}

// samplingEnvPairs returns "FIZEAU_X=value" strings for each non-nil sampling
// field in the profile, for forwarding via harbor --ae flags.
func samplingEnvPairs(p *profile.Profile) []string {
	var pairs []string
	if !profileUsesNativeOpenAIDefaultSamplingOnly(p) {
		pairs = append(pairs, fmt.Sprintf("FIZEAU_TEMPERATURE=%g", p.Sampling.Temperature))
	}
	if p.Sampling.TopP != nil && !profileUsesNativeOpenAIDefaultSamplingOnly(p) {
		pairs = append(pairs, fmt.Sprintf("FIZEAU_TOP_P=%g", *p.Sampling.TopP))
	}
	if p.Sampling.TopK != nil {
		pairs = append(pairs, fmt.Sprintf("FIZEAU_TOP_K=%d", *p.Sampling.TopK))
	}
	if p.Sampling.MinP != nil {
		pairs = append(pairs, fmt.Sprintf("FIZEAU_MIN_P=%g", *p.Sampling.MinP))
	}
	if r := strings.TrimSpace(p.Sampling.Reasoning); r != "" {
		// Propagate the profile's declared reasoning level (low/medium/high/off).
		// Operators should not need to set FIZEAU_REASONING manually for
		// standard sweeps — the profile is the source of truth. Fiz's
		// openai provider decides per-model whether to honour it.
		pairs = append(pairs, fmt.Sprintf("FIZEAU_REASONING=%s", r))
	}
	return pairs
}

func profileUsesNativeOpenAIDefaultSamplingOnly(p *profile.Profile) bool {
	return p.Provider.Type == profile.ProviderOpenAI &&
		strings.HasPrefix(strings.ToLower(p.Provider.Model), "gpt-5")
}

func fizeauProviderEnv(p *profile.Profile) string {
	if p.Provider.Type == profile.ProviderOpenAICompat && strings.Contains(p.Provider.BaseURL, "openrouter") {
		return string(profile.ProviderOpenRouter)
	}
	if p.Provider.Type == profile.ProviderOpenAICompat && strings.Contains(p.Provider.BaseURL, "vidar:1235") {
		return string(profile.ProviderOMLX)
	}
	return string(p.Provider.Type)
}

func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// queryModelServerInfo attempts to fetch model metadata from a lmstudio
// /api/v0/models/<id> endpoint. Returns nil if the server is not lmstudio
// or the request fails.
func queryModelServerInfo(p *profile.Profile) *profile.ModelServerInfo {
	base := strings.TrimRight(p.Provider.BaseURL, "/")
	// Only try lmstudio-style endpoints (port 1234 conventional, or path /api/v0 present).
	if !strings.Contains(base, ":1234") && !strings.Contains(base, "/api/v0") {
		return nil
	}
	// Strip trailing /v1 to get the base server URL.
	apiBase := strings.TrimSuffix(base, "/v1")
	modelID := p.Provider.Model
	url := apiBase + "/api/v0/models/" + modelID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()
	var info struct {
		Quantization        string `json:"quantization"`
		LoadedContextLength int    `json:"loaded_context_length"`
		MaxContextLength    int    `json:"max_context_length"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil
	}
	return &profile.ModelServerInfo{
		Quantization:        info.Quantization,
		LoadedContextLength: info.LoadedContextLength,
		MaxContextLength:    info.MaxContextLength,
		Source:              url,
	}
}

// extractCellRuntimeProps extracts server-reported runtime properties for the
// platform identified by p.Provider.Type. Called once per cell after preflight
// and before the bench run. On failure it logs the error and returns a Props
// record with ExtractionFailed set — it never fails the cell.
func extractCellRuntimeProps(ctx context.Context, p *profile.Profile) *evidence.RuntimeProps {
	if ctx == nil {
		ctx = context.Background()
	}
	lane := runtimeprops.LaneInfo{
		Runtime: string(p.Provider.Type),
		BaseURL: p.Provider.BaseURL,
		Model:   p.Provider.Model,
	}
	props, err := runtimeprops.Extract(ctx, lane)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[matrix] runtime_props extraction failed for %s (%s): %v\n",
			p.ID, p.Provider.Type, err)
	}
	return &props
}
