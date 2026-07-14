package website

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const terminalBenchTaskBase = "https://www.tbench.ai/registry/terminal-bench-core/head/"

type benchmarkDataManifest struct {
	Artifacts []struct {
		Format string `json:"format"`
		Kind   string `json:"kind"`
		Rows   int    `json:"rows"`
	} `json:"artifacts"`
}

type workbenchReadyState struct {
	Failed bool   `json:"failed"`
	Ready  bool   `json:"ready"`
	Rows   string `json:"rows"`
	Status string `json:"status"`
}

type workbenchSnapshot struct {
	ActiveFilters           []string `json:"activeFilters"`
	CombinationRowCount     int      `json:"combinationRowCount"`
	CombinationTaskSort     string   `json:"combinationTaskSort"`
	CombinationTaskLinkHref string   `json:"combinationTaskLinkHref"`
	CompareA                string   `json:"compareA"`
	CompareB                string   `json:"compareB"`
	ComparisonGapText       string   `json:"comparisonGapText"`
	ComparisonGapSort       string   `json:"comparisonGapSort"`
	ComparisonRowCount      int      `json:"comparisonRowCount"`
	ComparisonTaskLinkHref  string   `json:"comparisonTaskLinkHref"`
	CurrentRoute            string   `json:"currentRoute"`
	LocationHash            string   `json:"locationHash"`
	LegacyLaneFilterPresent bool     `json:"legacyLaneFilterPresent"`
	ModelOptionCount        int      `json:"modelOptionCount"`
	ModelOptions            []string `json:"modelOptions"`
	ProviderOptionCount     int      `json:"providerOptionCount"`
	ProviderOptions         []string `json:"providerOptions"`
	ResultOptionCount       int      `json:"resultOptionCount"`
	Rows                    string   `json:"rows"`
	SummaryChartCount       int      `json:"summaryChartCount"`
	SummaryRows             string   `json:"summaryRows"`
	SettingsColumnCount     int      `json:"settingsColumnCount"`
	SettingsOpen            bool     `json:"settingsOpen"`
	Status                  string   `json:"status"`
	TaskOptionCount         int      `json:"taskOptionCount"`
	VisibleColumns          []string `json:"visibleColumns"`
}

// @covers US-008-AC1
func TestBenchmarkWorkbenchSmoke(t *testing.T) {
	buildDir := t.TempDir()
	listener, baseURL := reserveListener(t)
	buildWebsiteWithBaseURL(t, buildDir, baseURL)
	expectedRows := readWorkbenchRowCount(t, buildDir)
	serveStaticDir(t, buildDir, listener)

	chromePath := ensureChromium(t)
	browserCtx := newBrowserContext(t, chromePath)

	workbenchURL := baseURL + "benchmarks/explorer/"
	waitForWorkbenchReady(t, browserCtx, workbenchURL)

	snapshot := captureWorkbenchSnapshot(t, browserCtx)
	if got := parseCount(t, snapshot.Rows); got != expectedRows {
		t.Fatalf("unexpected initial row count: got %d, want %d", got, expectedRows)
	}
	if snapshot.CurrentRoute != "summary" || snapshot.LocationHash != "#summary" {
		t.Fatalf("expected default summary route, got route=%q hash=%q", snapshot.CurrentRoute, snapshot.LocationHash)
	}
	if got := parseCount(t, snapshot.SummaryRows); got != expectedRows {
		t.Fatalf("unexpected summary row count: got %d, want %d", got, expectedRows)
	}
	if snapshot.SummaryChartCount == 0 {
		t.Fatal("expected summary charts to render")
	}
	if snapshot.Status == "" || !strings.Contains(snapshot.Status, "rows loaded") {
		t.Fatalf("expected loaded status, got %q", snapshot.Status)
	}
	if snapshot.ResultOptionCount < 4 {
		t.Fatalf("expected result-state options to populate, got %d", snapshot.ResultOptionCount)
	}
	if snapshot.TaskOptionCount < 2 {
		t.Fatalf("expected task picker options to populate, got %d", snapshot.TaskOptionCount)
	}
	if snapshot.ModelOptionCount < 2 || len(snapshot.ModelOptions) == 0 {
		t.Fatalf("expected model picker options to populate, got %d (%v)", snapshot.ModelOptionCount, snapshot.ModelOptions)
	}
	if snapshot.ProviderOptionCount < 2 || len(snapshot.ProviderOptions) == 0 {
		t.Fatalf("expected provider enum filter options to populate, got %d (%v)", snapshot.ProviderOptionCount, snapshot.ProviderOptions)
	}
	if snapshot.ComparisonRowCount == 0 {
		t.Fatal("expected pairwise comparison rows to render")
	}
	if snapshot.ComparisonGapText == "" || snapshot.ComparisonGapText == "-" {
		t.Fatalf("expected a rendered pairwise gap cell, got %q", snapshot.ComparisonGapText)
	}
	if snapshot.CompareA == "" || snapshot.CompareB == "" || snapshot.CompareA == snapshot.CompareB {
		t.Fatalf("expected distinct comparison families, got %q vs %q", snapshot.CompareA, snapshot.CompareB)
	}
	if snapshot.CombinationRowCount == 0 {
		t.Fatal("expected task combination aggregate rows to render")
	}
	if snapshot.CombinationTaskLinkHref == "" || !strings.HasPrefix(snapshot.CombinationTaskLinkHref, terminalBenchTaskBase) {
		t.Fatalf("expected aggregate task links to point at Terminal-Bench, got %q", snapshot.CombinationTaskLinkHref)
	}

	click(t, browserCtx, "[data-bw-route=\"data\"]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "data" && strings.HasPrefix(current.LocationHash, "#data") && len(current.VisibleColumns) > 0
	})
	snapshot = captureWorkbenchSnapshot(t, browserCtx)
	if len(snapshot.VisibleColumns) == 0 {
		t.Fatal("expected perspective viewer to expose visible columns")
	}
	expectedDefaultColumns := []string{
		"task",
		"result_state",
		"model_display_name",
		"quant_display",
		"engine",
		"effective_gpu_model",
		"effective_gpu_ram_gb",
		"profile_prefill_tps_est",
		"decode_tps_est",
		"turns",
		"input_tokens",
		"output_tokens",
		"reasoning_tokens",
		"total_tokens",
		"cost_usd",
		"wall_seconds",
		"provider_type",
		"provider_surface",
		"model",
		"model_quant",
		"machine",
		"rep",
		"started_at",
		"finished_at",
	}
	for _, column := range expectedDefaultColumns {
		if !contains(snapshot.VisibleColumns, column) {
			t.Fatalf("expected %s to be visible by default, got %v", column, snapshot.VisibleColumns)
		}
	}
	if !sameStrings(snapshot.VisibleColumns, expectedDefaultColumns) {
		t.Fatalf("expected default column order %v, got %v", expectedDefaultColumns, snapshot.VisibleColumns)
	}
	if len(snapshot.VisibleColumns) > len(expectedDefaultColumns) {
		t.Fatalf("expected focused default columns, got %d columns: %v", len(snapshot.VisibleColumns), snapshot.VisibleColumns)
	}
	if contains(snapshot.VisibleColumns, "terminalbench_task_url") {
		t.Fatalf("terminalbench_task_url must stay hidden by default, got %v", snapshot.VisibleColumns)
	}
	for _, column := range []string{
		"terminalbench_task_url",
		"raw_report_json",
		"search_text",
		"task_subsets",
		"passed",
		"grader_passed",
		"final_status",
		"invalid_class",
		"harness",
		"harness_label",
	} {
		if contains(snapshot.VisibleColumns, column) {
			t.Fatalf("%s must stay hidden by default, got %v", column, snapshot.VisibleColumns)
		}
	}
	if snapshot.LegacyLaneFilterPresent {
		t.Fatal("raw database filter UI must use profile terminology, not lane terminology")
	}

	poisonLegacyViewerConfig(t, browserCtx)
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "data" &&
			contains(current.VisibleColumns, "result_state") &&
			!contains(current.VisibleColumns, "result-state") &&
			strings.Contains(current.Status, "rows loaded")
	})

	click(t, browserCtx, "[data-bw-open-config]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.SettingsOpen && current.SettingsColumnCount > 0
	})
	removeViewerColumn(t, browserCtx, "model_display_name")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.SettingsOpen &&
			!contains(current.VisibleColumns, "model_display_name") &&
			len(current.VisibleColumns) == len(expectedDefaultColumns)-1
	})
	click(t, browserCtx, "[data-bw-open-config]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return !current.SettingsOpen
	})
	setSelectValue(t, browserCtx, "[data-bw-model]", snapshot.ModelOptions[0])
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) < expectedRows && !contains(current.VisibleColumns, "model_display_name")
	})
	setSelectValue(t, browserCtx, "[data-bw-model]", "")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) == expectedRows && !contains(current.VisibleColumns, "model_display_name")
	})

	customColumns := []string{"task", "result_state", "profile_prefill_tps_est", "decode_tps_est"}
	setViewerColumns(t, browserCtx, customColumns)
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return sameStrings(current.VisibleColumns, customColumns)
	})
	clickRawGridHeaderSort(t, browserCtx, "result_state")
	clickRawGridHeaderSort(t, browserCtx, "profile_prefill_tps_est")
	clickRawGridHeaderSort(t, browserCtx, "decode_tps_est")

	setSelectValue(t, browserCtx, "[data-bw-model]", snapshot.ModelOptions[0])
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) < expectedRows && sameStrings(current.VisibleColumns, customColumns)
	})

	click(t, browserCtx, "[data-bw-reset-view]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return contains(current.VisibleColumns, "profile_prefill_tps_est") && len(current.VisibleColumns) == len(expectedDefaultColumns)
	})
	click(t, browserCtx, "[data-bw-clear-filters]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) == expectedRows && !hasFilterPrefix(current.ActiveFilters, "Model:")
	})

	setSelectValue(t, browserCtx, "[data-bw-compare-dimension]", "task")
	click(t, browserCtx, "[data-bw-route=\"compare\"]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "compare" &&
			strings.HasPrefix(current.LocationHash, "#compare") &&
			strings.Contains(current.LocationHash, "dim=task")
	})
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.ComparisonTaskLinkHref != ""
	})
	snapshot = captureWorkbenchSnapshot(t, browserCtx)
	if !strings.HasPrefix(snapshot.ComparisonTaskLinkHref, terminalBenchTaskBase) {
		t.Fatalf("expected pairwise task links to point at Terminal-Bench, got %q", snapshot.ComparisonTaskLinkHref)
	}
	click(t, browserCtx, "[data-bw-comparison] th:nth-child(4) button")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.ComparisonGapSort == "ascending" &&
			strings.Contains(current.LocationHash, "sort=gap_pp%3Aasc")
	})
	comparisonBookmark := captureWorkbenchSnapshot(t, browserCtx).LocationHash

	click(t, browserCtx, "[data-bw-route=\"combinations\"]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "combinations" && strings.HasPrefix(current.LocationHash, "#combinations")
	})
	click(t, browserCtx, "[data-bw-combinations] th:first-child button")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CombinationTaskSort == "ascending" &&
			strings.Contains(current.LocationHash, "sort=task%3Aasc")
	})

	click(t, browserCtx, "[data-bw-route=\"data\"]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "data"
	})
	setSelectValue(t, browserCtx, "[data-bw-model]", snapshot.ModelOptions[0])
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) < expectedRows && hasFilterPrefix(current.ActiveFilters, "Model:")
	})

	click(t, browserCtx, "[data-bw-clear-filters]")
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) == expectedRows && !hasFilterPrefix(current.ActiveFilters, "Model:")
	})

	setSelectValue(t, browserCtx, "[data-bw-filter-field=\"provider_type\"]", snapshot.ProviderOptions[0])
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return parseCount(t, current.Rows) < expectedRows &&
			hasFilterPrefix(current.ActiveFilters, "Provider:") &&
			strings.Contains(current.LocationHash, "f.provider_type=")
	})
	rawBookmark := captureWorkbenchSnapshot(t, browserCtx).LocationHash

	waitForWorkbenchReady(t, browserCtx, workbenchURL+comparisonBookmark)
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "compare" &&
			current.ComparisonGapSort == "ascending" &&
			current.ComparisonTaskLinkHref != "" &&
			strings.Contains(current.LocationHash, "dim=task")
	})

	waitForWorkbenchReady(t, browserCtx, workbenchURL+rawBookmark)
	waitForCondition(t, browserCtx, 30*time.Second, func(current workbenchSnapshot) bool {
		return current.CurrentRoute == "data" &&
			parseCount(t, current.Rows) < expectedRows &&
			hasFilterPrefix(current.ActiveFilters, "Provider:")
	})
}

// @covers US-008-AC2
func TestBenchmarkWorkbenchInteractionCoverage(t *testing.T) {
	buildDir := t.TempDir()
	listener, baseURL := reserveListener(t)
	buildWebsiteWithBaseURL(t, buildDir, baseURL)
	serveStaticDir(t, buildDir, listener)

	chromePath := ensureChromium(t)
	browserCtx := newBrowserContext(t, chromePath)

	workbenchURL := baseURL + "benchmarks/explorer/"
	waitForWorkbenchReady(t, browserCtx, workbenchURL)

	var report struct {
		RawControls       []string `json:"rawControls"`
		RawEnumFilters    []string `json:"rawEnumFilters"`
		Presets           []string `json:"presets"`
		CompareControls   []string `json:"compareControls"`
		CompareDimensions []string `json:"compareDimensions"`
		CompareFilters    []string `json:"compareFilters"`
		CompareSorts      []string `json:"compareSorts"`
		Combination       []string `json:"combination"`
		CombinationSorts  []string `json:"combinationSorts"`
		GridConfig        []string `json:"gridConfig"`
	}
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(workbenchInteractionCoverageJS, &report, awaitPromise)); err != nil {
		t.Fatalf("exercise benchmark workbench interactions: %v", err)
	}

	requiredRawControls := []string{"search", "result-state", "task", "model", "engine", "gpu", "max-ram", "passed-only", "clear-filters"}
	for _, control := range requiredRawControls {
		if !contains(report.RawControls, control) {
			t.Fatalf("raw control %q was not exercised; report=%+v", control, report)
		}
	}
	requiredPresets := []string{"all", "passing-test", "passing-test-gpu", "passing-test-ram", "recent-failures"}
	for _, preset := range requiredPresets {
		if !contains(report.Presets, preset) {
			t.Fatalf("preset %q was not exercised; report=%+v", preset, report)
		}
	}
	for _, control := range []string{"baseline", "compare", "max-ram"} {
		if !contains(report.CompareControls, control) {
			t.Fatalf("comparison control %q was not exercised; report=%+v", control, report)
		}
	}
	for _, control := range []string{"task", "model", "gpu", "passed-only"} {
		if !contains(report.Combination, control) {
			t.Fatalf("combination control %q was not exercised; report=%+v", control, report)
		}
	}
	if len(report.RawEnumFilters) < 10 {
		t.Fatalf("expected broad raw enum filter coverage, got %d: %+v", len(report.RawEnumFilters), report)
	}
	if len(report.CompareDimensions) < 10 {
		t.Fatalf("expected broad comparison dimension coverage, got %d: %+v", len(report.CompareDimensions), report)
	}
	if len(report.CompareFilters) < 6 {
		t.Fatalf("expected broad comparison filter coverage, got %d: %+v", len(report.CompareFilters), report)
	}
	if len(report.CompareSorts) < 10 {
		t.Fatalf("expected comparison sort header coverage, got %d: %+v", len(report.CompareSorts), report)
	}
	if len(report.CombinationSorts) < 10 {
		t.Fatalf("expected combination sort header coverage, got %d: %+v", len(report.CombinationSorts), report)
	}
	for _, item := range []string{"sort", "group_by", "filter"} {
		if !contains(report.GridConfig, item) {
			t.Fatalf("Perspective grid config %q was not exercised; report=%+v", item, report)
		}
	}
	var workbenchEnhanced bool
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`Boolean(document.querySelector('[data-benchmark-workbench] .bench-table-shell'))`, &workbenchEnhanced)); err != nil {
		t.Fatalf("check workbench table enhancer scope: %v", err)
	}
	if workbenchEnhanced {
		t.Fatal("legacy benchmark table enhancer wrapped a workbench table")
	}

	benchmarksURL := baseURL + "benchmarks/"
	var explorerHref string
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(benchmarksURL),
		chromedp.Evaluate(`document.querySelector('a[href$="explorer/"], a[href*="/benchmarks/explorer/"]')?.href ?? ""`, &explorerHref),
	); err != nil {
		t.Fatalf("open benchmark index: %v", err)
	}
	if !strings.Contains(explorerHref, "/benchmarks/explorer/") {
		t.Fatalf("expected /benchmarks/ to link to explorer, got %q", explorerHref)
	}

	var reportTableEnhanced bool
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(baseURL+"benchmarks/terminal-bench-2-1/"),
		chromedp.WaitVisible(`.bench-table-shell`, chromedp.ByQuery),
		chromedp.Evaluate(`Boolean(document.querySelector('.bench-table-shell'))`, &reportTableEnhanced),
	); err != nil {
		t.Fatalf("open benchmark report page: %v", err)
	}
	if !reportTableEnhanced {
		t.Fatal("expected legacy benchmark report tables to remain enhanced")
	}
}

func reserveListener(t *testing.T) (net.Listener, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for static site: %v", err)
	}
	return listener, "http://" + listener.Addr().String() + "/"
}

func serveStaticDir(t *testing.T, dir string, listener net.Listener) {
	t.Helper()

	server := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
}

func buildWebsiteWithBaseURL(t *testing.T, dest, baseURL string) {
	t.Helper()

	hugoPath, err := exec.LookPath("hugo")
	if err != nil {
		t.Fatalf("find hugo: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, hugoPath,
		"--quiet",
		"--baseURL", baseURL,
		"--destination", dest,
	)
	cmd.Dir = websiteRoot(t)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build website: %v\n%s", err, output)
	}
}

func readWorkbenchRowCount(t *testing.T, buildDir string) int {
	t.Helper()

	path := filepath.Join(buildDir, "data", "benchmark-data-manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read benchmark manifest: %v", err)
	}

	var manifest benchmarkDataManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode benchmark manifest: %v", err)
	}

	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == "cell_rows" && artifact.Format == "parquet" {
			return artifact.Rows
		}
	}

	t.Fatalf("find parquet cell_rows artifact in %s", path)
	return 0
}

func newBrowserContext(t *testing.T, chromePath string) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.WSURLReadTimeout(90*time.Second),
		)...,
	)
	t.Cleanup(allocCancel)

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	t.Cleanup(browserCancel)
	return browserCtx
}

func waitForWorkbenchReady(t *testing.T, ctx context.Context, pageURL string) {
	t.Helper()

	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 1400, chromedp.EmulateScale(1)),
		chromedp.Navigate(pageURL),
	); err != nil {
		t.Fatalf("open benchmark workbench: %v", err)
	}

	deadline := time.Now().Add(90 * time.Second)
	var last workbenchReadyState
	for time.Now().Before(deadline) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(workbenchReadyJS, &last)); err != nil {
			t.Fatalf("read workbench readiness: %v", err)
		}
		if last.Failed {
			t.Fatalf("benchmark workbench failed to load: %s", last.Status)
		}
		if last.Ready {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for benchmark workbench: status=%q rows=%q", last.Status, last.Rows)
}

func captureWorkbenchSnapshot(t *testing.T, ctx context.Context) workbenchSnapshot {
	t.Helper()

	var snapshot workbenchSnapshot
	if err := chromedp.Run(ctx, chromedp.Evaluate(workbenchSnapshotJS, &snapshot, awaitPromise)); err != nil {
		t.Fatalf("capture workbench snapshot: %v", err)
	}
	return snapshot
}

func waitForCondition(t *testing.T, ctx context.Context, timeout time.Duration, predicate func(workbenchSnapshot) bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last workbenchSnapshot
	for time.Now().Before(deadline) {
		last = captureWorkbenchSnapshot(t, ctx)
		if predicate(last) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for workbench condition: rows=%q filters=%v status=%q", last.Rows, last.ActiveFilters, last.Status)
}

func setSelectValue(t *testing.T, ctx context.Context, selector, value string) {
	t.Helper()

	script := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) {
			throw new Error("missing element: " + %q);
		}
		el.value = %q;
		el.dispatchEvent(new Event('change', { bubbles: true }));
		return el.value;
	})()`, selector, selector, value)

	var applied string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &applied)); err != nil {
		t.Fatalf("set %s to %q: %v", selector, value, err)
	}
	if applied != value {
		t.Fatalf("set %s to %q, got %q", selector, value, applied)
	}
}

func click(t *testing.T, ctx context.Context, selector string) {
	t.Helper()

	script := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el) {
			throw new Error("missing element: " + %q);
		}
		el.click();
		return true;
	})()`, selector, selector)

	if err := chromedp.Run(ctx, chromedp.Evaluate(script, nil)); err != nil {
		t.Fatalf("click %s: %v", selector, err)
	}
}

func clickViewerShadow(t *testing.T, ctx context.Context, selector string) {
	t.Helper()

	script := fmt.Sprintf(`(() => {
		const viewer = document.querySelector('[data-bw-viewer]');
		const el = viewer?.shadowRoot?.querySelector(%q);
		if (!el) {
			throw new Error("missing viewer shadow element: " + %q);
		}
		el.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, view: window }));
		el.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true, view: window }));
		el.click();
		return true;
	})()`, selector, selector)

	if err := chromedp.Run(ctx, chromedp.Evaluate(script, nil)); err != nil {
		t.Fatalf("click viewer shadow %s: %v", selector, err)
	}
}

func removeViewerColumn(t *testing.T, ctx context.Context, column string) {
	t.Helper()

	script := fmt.Sprintf(`(() => {
		const viewer = document.querySelector('[data-bw-viewer]');
		const columns = [...(viewer?.shadowRoot?.querySelectorAll('#active-columns .column-selector-column') ?? [])];
		const target = columns.find((el) => el.textContent.includes(%q));
		const active = target?.querySelector('span.is_column_active');
		if (!active) {
			const seen = columns.map((el) => el.textContent.trim()).join(', ');
			throw new Error("missing active viewer column " + %q + "; saw " + seen);
		}
		active.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, view: window }));
		active.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true, view: window }));
		active.click();
		return true;
	})()`, column, column)

	if err := chromedp.Run(ctx, chromedp.Evaluate(script, nil)); err != nil {
		t.Fatalf("remove viewer column %s: %v", column, err)
	}
}

func setViewerColumns(t *testing.T, ctx context.Context, columns []string) {
	t.Helper()

	raw, err := json.Marshal(columns)
	if err != nil {
		t.Fatalf("marshal columns: %v", err)
	}
	script := fmt.Sprintf(`(async () => {
		const viewer = document.querySelector('[data-bw-viewer]');
		if (!viewer) throw new Error('missing viewer');
		const config = await viewer.save();
		await viewer.restore({
			...config,
			columns: %s,
			sort: [],
			group_by: [],
			split_by: [],
			filter: [],
			settings: false,
		});
		viewer.dispatchEvent(new CustomEvent('perspective-config-update', { bubbles: true }));
		await viewer.flush?.();
		return (await viewer.save()).columns;
	})()`, string(raw))

	var applied []string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &applied, awaitPromise)); err != nil {
		t.Fatalf("set viewer columns to %v: %v", columns, err)
	}
	if !sameStrings(applied, columns) {
		t.Fatalf("set viewer columns to %v, got %v", columns, applied)
	}
}

func clickRawGridHeaderSort(t *testing.T, ctx context.Context, column string) {
	t.Helper()

	script := fmt.Sprintf(`(async () => {
		const root = document.querySelector('[data-benchmark-workbench]');
		const workbench = root?.benchmarkWorkbench;
		const viewer = root?.querySelector('[data-bw-viewer]');
		if (!workbench || !viewer) throw new Error('missing workbench or viewer');
		const datagrid = await workbench.findDatagridPlugin();
		const table = datagrid?.regular_table;
		if (!table) throw new Error('missing Perspective regular-table');
		await viewer.restore({
			...(await viewer.save()),
			sort: [],
			group_by: [],
			split_by: [],
			filter: [],
			settings: false,
		});
		await viewer.flush?.();
		await table.draw?.();
		const displayColumn = %q.replaceAll('_', '-');
		const header = [...table.querySelectorAll('thead th.psp-sort-enabled, thead th')]
			.find((cell) => {
				const meta = table.getMeta(cell);
				const seen = meta?.column_header?.at(-1);
				return meta?.type === 'column_header' && (seen === %q || seen === displayColumn);
			});
		if (!header) {
			const seen = [...table.querySelectorAll('thead th')].map((cell) => table.getMeta(cell)?.column_header?.join('/')).filter(Boolean);
			throw new Error('missing sortable header for %q; saw ' + seen.join(', '));
		}
		const PointerCtor = window.PointerEvent || MouseEvent;
		header.dispatchEvent(new PointerCtor('pointerdown', { bubbles: true, cancelable: true, view: window }));
		header.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true, view: window }));
		header.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true, view: window }));
		header.click();
		await new Promise((resolve) => setTimeout(resolve, 250));
		await viewer.flush?.();
		return await viewer.save();
	})()`, column, column, column)

	var config struct {
		Sort [][]string `json:"sort"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &config, awaitPromise)); err != nil {
		t.Fatalf("click raw grid header %q: %v", column, err)
	}
	for _, sort := range config.Sort {
		if len(sort) >= 2 && sort[0] == column {
			return
		}
	}
	t.Fatalf("clicking raw grid header %q did not update Perspective sort, got %v", column, config.Sort)
}

func poisonLegacyViewerConfig(t *testing.T, ctx context.Context) {
	t.Helper()

	script := `(async () => {
		const root = document.querySelector('[data-benchmark-workbench]');
		const workbench = root?.benchmarkWorkbench;
		if (!workbench) throw new Error('missing benchmark workbench instance');
		workbench.gridConfigReady = true;
		workbench.gridConfig = {
			plugin: 'Datagrid',
			columns: ['task', 'result-state'],
			sort: [['result-state', 'desc']],
			group_by: [],
			split_by: [],
			filter: [],
			settings: false,
		};
		await workbench.reloadRaw();
		const config = await workbench.viewer.save();
		return {...config, configJson: JSON.stringify(config)};
	})()`

	var config struct {
		Columns    []string `json:"columns"`
		ConfigJSON string   `json:"configJson"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &config, awaitPromise)); err != nil {
		t.Fatalf("reload workbench with legacy viewer config: %v", err)
	}
	if !contains(config.Columns, "result_state") {
		t.Fatalf("expected legacy result-state column to normalize to result_state, got %v (config=%s)", config.Columns, config.ConfigJSON)
	}
	if contains(config.Columns, "result-state") {
		t.Fatalf("legacy result-state column leaked into saved viewer config: %v", config.Columns)
	}
}

func parseCount(t *testing.T, value string) int {
	t.Helper()

	clean := strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	n, err := strconv.Atoi(clean)
	if err != nil {
		t.Fatalf("parse count %q: %v", value, err)
	}
	return n
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func hasFilterPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func awaitPromise(params *runtime.EvaluateParams) *runtime.EvaluateParams {
	return params.WithAwaitPromise(true)
}

const workbenchInteractionCoverageJS = `(async () => {
  const root = document.querySelector('[data-benchmark-workbench]');
  const workbench = root?.benchmarkWorkbench;
  if (!root || !workbench) throw new Error('missing benchmark workbench instance');

  const report = {
    rawControls: [],
    rawEnumFilters: [],
    presets: [],
    compareControls: [],
    compareDimensions: [],
    compareFilters: [],
    compareSorts: [],
    combination: [],
    combinationSorts: [],
    gridConfig: [],
  };
  const sleep = (ms = 260) => new Promise((resolve) => setTimeout(resolve, ms));
  const assert = (condition, message) => {
    if (!condition) throw new Error(message);
  };
  const parseCount = (value) => Number(String(value || '').replace(/,/g, ''));
  const metricRows = () => parseCount(root.querySelector('[data-bw-metric="rows"]')?.textContent?.trim());
  const optionValues = (selector) => [...(root.querySelector(selector)?.options ?? [])].map((option) => option.value).filter(Boolean);
  const firstOptionValue = (selector) => optionValues(selector)[0] || '';
  const activeLabels = () => [...root.querySelectorAll('[data-bw-active-filters] span')].map((el) => el.textContent.trim());
  const activeRoute = () => root.querySelector('[data-bw-route][aria-current="page"]')?.dataset.bwRoute || '';
  const eventFor = (element) => element?.tagName === 'INPUT' && element.type !== 'checkbox' ? 'input' : 'change';

  async function waitLoaded(label) {
    for (let i = 0; i < 80; i++) {
      const status = root.querySelector('[data-bw-status]')?.textContent?.trim() || '';
      if (root.classList.contains('bench-workbench--error') || status.startsWith('Workbench failed:')) {
        throw new Error(label + ' failed: ' + status);
      }
      if (status.includes('rows loaded') && Number.isFinite(metricRows())) return;
      await sleep(100);
    }
    throw new Error(label + ' did not settle');
  }

  async function activate(route) {
    root.querySelector('[data-bw-route="' + route + '"]')?.click();
    await sleep();
    assert(activeRoute() === route, 'route did not activate: ' + route);
  }

  async function applyControl(selector, value, label, options = {}) {
    const element = root.querySelector(selector);
    assert(element, 'missing control: ' + selector);
    if (element.type === 'checkbox') {
      element.checked = Boolean(value);
    } else {
      element.value = String(value);
    }
    element.dispatchEvent(new Event(options.event || eventFor(element), { bubbles: true }));
    await sleep();
    if (options.reload === 'compare') {
      await workbench.loadComparison();
    } else if (options.reload === 'combination') {
      await workbench.loadCombinationAggregates();
    } else if (options.reload !== false) {
      await workbench.reloadRaw();
      await waitLoaded(label);
    }
  }

  async function clearRaw() {
    root.querySelector('[data-bw-clear-filters]')?.click();
    report.rawControls.push('clear-filters');
    await sleep();
    await workbench.reloadRaw();
    await waitLoaded('clear raw filters');
  }

  await activate('summary');
  assert(window.location.hash === '#summary', 'summary route hash missing');
  assert(root.querySelectorAll('.bench-workbench__pie, .bench-workbench__bar-row').length > 0, 'summary charts missing');

  await activate('data');
  await waitLoaded('raw route');
  const unfilteredRows = metricRows();
  assert(unfilteredRows > 0, 'raw route has no rows');

  const searchTerm = firstOptionValue('[data-bw-model]') || 'qwen';
  await applyControl('[data-bw-search]', searchTerm, 'raw search');
  assert(window.location.hash.includes('q='), 'search did not update hash');
  assert(activeLabels().some((label) => label.startsWith('Search:')), 'search did not render active filter');
  report.rawControls.push('search');
  await clearRaw();

  const rawSelects = [
    ['[data-bw-result-state]', 'result-state', 'outcome='],
    ['[data-bw-task]', 'task', 'task='],
    ['[data-bw-model]', 'model', 'model='],
    ['[data-bw-engine]', 'engine', 'engine='],
    ['[data-bw-gpu]', 'gpu', 'gpu='],
  ];
  for (const [selector, label, hashPart] of rawSelects) {
    const value = firstOptionValue(selector);
    assert(value, 'missing option for raw control: ' + label);
    await applyControl(selector, value, label);
    assert(window.location.hash.includes(hashPart), label + ' did not update hash');
    assert(activeLabels().length > 0, label + ' did not render active filter');
    report.rawControls.push(label);
    await clearRaw();
  }

  await applyControl('[data-bw-max-ram]', '24', 'raw max ram');
  assert(window.location.hash.includes('max_ram='), 'max RAM did not update hash');
  assert(activeLabels().some((label) => label.startsWith('Max GPU RAM:')), 'max RAM active filter missing');
  report.rawControls.push('max-ram');
  await clearRaw();

  await applyControl('[data-bw-passed-only]', true, 'raw passed only');
  assert(window.location.hash.includes('passed=1'), 'passed-only did not update hash');
  assert(activeLabels().includes('Passed only'), 'passed-only active filter missing');
  report.rawControls.push('passed-only');
  await clearRaw();

  for (const select of root.querySelectorAll('[data-bw-filter-field]')) {
    const key = select.dataset.bwFilterField;
    const value = [...select.options].map((option) => option.value).find(Boolean);
    if (!value) continue;
    select.value = value;
    select.dispatchEvent(new Event('change', { bubbles: true }));
    await sleep();
    await workbench.reloadRaw();
    await waitLoaded('raw enum filter ' + key);
    assert(window.location.hash.includes('f.' + encodeURIComponent(key) + '='), 'enum filter did not update hash: ' + key);
    report.rawEnumFilters.push(key);
    await clearRaw();
  }

  await applyControl('[data-bw-task]', firstOptionValue('[data-bw-task]'), 'preset task');
  await applyControl('[data-bw-gpu]', firstOptionValue('[data-bw-gpu]'), 'preset gpu');
  await applyControl('[data-bw-max-ram]', '24', 'preset max ram');
  for (const preset of ['all', 'passing-test', 'passing-test-gpu', 'passing-test-ram', 'recent-failures']) {
    root.querySelector('[data-bw-preset="' + preset + '"]')?.click();
    await sleep();
    await workbench.reloadRaw();
    await waitLoaded('preset ' + preset);
    assert(root.querySelector('[data-bw-preset="' + preset + '"]')?.getAttribute('aria-pressed') === 'true', 'preset not pressed: ' + preset);
    if (preset.startsWith('passing')) assert(root.querySelector('[data-bw-passed-only]')?.checked, 'passing preset did not enable pass-only');
    report.presets.push(preset);
  }
  await clearRaw();

  await activate('compare');
  const familyOptions = optionValues('[data-bw-compare-a]');
  assert(familyOptions.length >= 2, 'comparison needs at least two model families');
  await applyControl('[data-bw-compare-a]', familyOptions[0], 'compare baseline', { reload: 'compare' });
  await applyControl('[data-bw-compare-b]', familyOptions[1], 'compare candidate', { reload: 'compare' });
  assert(root.querySelector('[data-bw-compare-a]')?.value !== root.querySelector('[data-bw-compare-b]')?.value, 'comparison families match');
  report.compareControls.push('baseline', 'compare');
  await applyControl('[data-bw-compare-max-ram]', '9999', 'compare max ram', { reload: 'compare' });
  assert(window.location.hash.includes('max_ram='), 'comparison max RAM did not update hash');
  report.compareControls.push('max-ram');
  await applyControl('[data-bw-compare-max-ram]', '', 'clear compare max ram', { reload: 'compare' });

  for (const value of optionValues('[data-bw-compare-dimension]')) {
    await applyControl('[data-bw-compare-dimension]', value, 'comparison dimension ' + value, { reload: 'compare' });
    assert(root.querySelector('[data-bw-comparison]')?.textContent.trim(), 'comparison output missing for dimension ' + value);
    report.compareDimensions.push(value);
  }
  await applyControl('[data-bw-compare-dimension]', 'task_category', 'reset comparison dimension', { reload: 'compare' });

  for (const select of root.querySelectorAll('[data-bw-compare-filter-field]')) {
    const key = select.dataset.bwCompareFilterField;
    const value = [...select.options].map((option) => option.value).find(Boolean);
    if (!value) continue;
    await applyControl('[data-bw-compare-filter-field="' + key + '"]', value, 'compare filter ' + key, { reload: 'compare' });
    assert(window.location.hash.includes('cf.' + encodeURIComponent(key) + '='), 'compare filter did not update hash: ' + key);
    assert(root.querySelector('[data-bw-comparison]')?.textContent.trim(), 'compare filter output missing: ' + key);
    report.compareFilters.push(key);
    await applyControl('[data-bw-compare-filter-field="' + key + '"]', '', 'clear compare filter ' + key, { reload: 'compare' });
  }

  workbench.comparisonSort = { key: 'gap_pp', direction: 'desc' };
  await workbench.loadComparison();
  const compareSortKeys = [...root.querySelectorAll('[data-bw-comparison] button[data-bw-sort]')].map((button) => button.dataset.bwSort);
  assert(compareSortKeys.length > 0, 'comparison sort headers missing');
  for (const key of compareSortKeys) {
    root.querySelector('[data-bw-comparison] button[data-bw-sort="' + key + '"]')?.click();
    await sleep();
    const sortState = root.querySelector('[data-bw-comparison] button[data-bw-sort="' + key + '"]')?.closest('th')?.getAttribute('aria-sort');
    assert(sortState && sortState !== 'none', 'comparison sort did not activate: ' + key);
    report.compareSorts.push(key);
  }

  await activate('combinations');
  const comboControls = [
    ['[data-bw-combo-task]', 'task', 'task='],
    ['[data-bw-combo-model]', 'model', 'model='],
    ['[data-bw-combo-gpu]', 'gpu', 'gpu='],
  ];
  for (const [selector, label, hashPart] of comboControls) {
    const value = firstOptionValue(selector);
    assert(value, 'missing combination option: ' + label);
    await applyControl(selector, value, 'combination ' + label, { reload: 'combination' });
    assert(window.location.hash.includes(hashPart), 'combination ' + label + ' did not update hash');
    assert(root.querySelector('[data-bw-combinations]')?.textContent.trim(), 'combination output missing after ' + label);
    report.combination.push(label);
    await applyControl(selector, '', 'clear combination ' + label, { reload: 'combination' });
  }
  await applyControl('[data-bw-combo-passed-only]', true, 'combination passed only', { reload: 'combination' });
  assert(window.location.hash.includes('passed=1'), 'combination pass-only did not update hash');
  report.combination.push('passed-only');
  await applyControl('[data-bw-combo-passed-only]', false, 'clear combination passed only', { reload: 'combination' });

  const comboSortKeys = [...root.querySelectorAll('[data-bw-combinations] button[data-bw-sort]')].map((button) => button.dataset.bwSort);
  assert(comboSortKeys.length > 0, 'combination sort headers missing');
  for (const key of comboSortKeys) {
    root.querySelector('[data-bw-combinations] button[data-bw-sort="' + key + '"]')?.click();
    await sleep();
    const sortState = root.querySelector('[data-bw-combinations] button[data-bw-sort="' + key + '"]')?.closest('th')?.getAttribute('aria-sort');
    assert(sortState && sortState !== 'none', 'combination sort did not activate: ' + key);
    report.combinationSorts.push(key);
  }

  await activate('data');
  await workbench.loadGrid();
  const viewer = root.querySelector('[data-bw-viewer]');
  const config = await viewer.save();
  await viewer.restore({
    ...config,
    columns: ['task', 'result_state', 'model_display_name'],
    sort: [['result_state', 'desc']],
    group_by: ['model_display_name'],
    filter: [['result_state', '==', 'passed']],
    settings: false,
  });
  await viewer.flush?.();
  const saved = await viewer.save();
  assert(saved.sort?.some((entry) => entry[0] === 'result_state'), 'Perspective sort config missing');
  assert(saved.group_by?.includes('model_display_name'), 'Perspective group_by config missing');
  assert(saved.filter?.some((entry) => entry[0] === 'result_state'), 'Perspective filter config missing');
  report.gridConfig.push('sort', 'group_by', 'filter');
  root.querySelector('[data-bw-reset-view]')?.click();
  await sleep();
  await workbench.reloadRaw();
  await waitLoaded('reset after grid config');

  return report;
})()`

const workbenchReadyJS = `(() => {
  const root = document.querySelector('[data-benchmark-workbench]');
  const status = root?.querySelector('[data-bw-status]')?.textContent?.trim() ?? '';
  const rows = root?.querySelector('[data-bw-metric="rows"]')?.textContent?.trim() ?? '';
  return {
    ready: status.includes('rows loaded') && rows !== '' && rows !== '-',
    failed: Boolean(root?.classList.contains('bench-workbench--error')) || status.startsWith('Workbench failed:'),
    rows,
    status,
  };
})()`

const workbenchSnapshotJS = `(async () => {
  const root = document.querySelector('[data-benchmark-workbench]');
  const text = (selector) => root?.querySelector(selector)?.textContent?.trim() ?? '';
  const options = (selector) => [...(root?.querySelector(selector)?.options ?? [])];
  const values = (selector) => options(selector).map((option) => option.value).filter(Boolean);
  const viewer = root?.querySelector('[data-bw-viewer]');
  const currentRoute = root?.querySelector('[data-bw-route][aria-current="page"]')?.dataset.bwRoute ?? '';
  const config = currentRoute === 'data' && viewer
    ? await Promise.race([
        viewer.save(),
        new Promise((resolve) => setTimeout(() => resolve({ columns: [] }), 1200)),
      ])
    : { columns: [] };
  const comparisonTaskLink = root?.querySelector('[data-bw-comparison] tbody tr td:first-child a');
  const combinationTaskLink = root?.querySelector('[data-bw-combinations] tbody a');

  return {
    activeFilters: [...(root?.querySelectorAll('[data-bw-active-filters] span') ?? [])].map((el) => el.textContent.trim()),
    combinationRowCount: root?.querySelectorAll('[data-bw-combinations] tbody tr').length ?? 0,
    combinationTaskSort: root?.querySelector('[data-bw-combinations] th:first-child')?.getAttribute('aria-sort') ?? '',
    combinationTaskLinkHref: combinationTaskLink?.href ?? '',
    compareA: root?.querySelector('[data-bw-compare-a]')?.value ?? '',
    compareB: root?.querySelector('[data-bw-compare-b]')?.value ?? '',
    comparisonGapText: text('[data-bw-comparison] tbody tr td:nth-child(4)'),
    comparisonGapSort: root?.querySelector('[data-bw-comparison] th:nth-child(4)')?.getAttribute('aria-sort') ?? '',
    comparisonRowCount: root?.querySelectorAll('[data-bw-comparison] tbody tr').length ?? 0,
    comparisonTaskLinkHref: comparisonTaskLink?.href ?? '',
    currentRoute,
    locationHash: window.location.hash,
    legacyLaneFilterPresent: Boolean(root?.querySelector('[data-bw-filter-field="lane_label"], [data-bw-filter-field="internal_lane_id"]')),
    modelOptionCount: options('[data-bw-model]').length,
    modelOptions: values('[data-bw-model]'),
    providerOptionCount: options('[data-bw-filter-field="provider_type"]').length,
    providerOptions: values('[data-bw-filter-field="provider_type"]'),
    resultOptionCount: options('[data-bw-result-state]').length,
    rows: text('[data-bw-metric="rows"]'),
    summaryChartCount: root?.querySelectorAll('.bench-workbench__pie, .bench-workbench__bar-row').length ?? 0,
    summaryRows: text('[data-bw-summary-metric="rows"]'),
    settingsColumnCount: viewer?.shadowRoot?.querySelectorAll('#active-columns .column-selector-column, #sub-columns .column-selector-column').length ?? 0,
    settingsOpen: Boolean(viewer?.hasAttribute('settings')),
    status: text('[data-bw-status]'),
    taskOptionCount: options('[data-bw-task]').length,
    visibleColumns: config?.columns ?? [],
  };
})()`
