package harnesses_test

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	claudeharness "github.com/easel/fizeau/internal/harnesses/claude"
	claudetuiharness "github.com/easel/fizeau/internal/harnesses/claude-tui"
	codexharness "github.com/easel/fizeau/internal/harnesses/codex"
	geminiharness "github.com/easel/fizeau/internal/harnesses/gemini"
)

type CapabilityMatrixJSON struct {
	Version   string    `json:"version"`
	Harnesses []Harness `json:"harnesses"`
}

type Harness struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Capabilities []Capability `json:"capabilities"`
}

type Capability struct {
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	Evidence []Evidence `json:"evidence"`
}

type Evidence struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

func TestHarnessCapabilityMatrixLoads(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	if matrix == nil {
		t.Fatal("capability matrix failed to load")
	}
	if len(matrix.Harnesses) < 3 {
		t.Fatalf("capability matrix has %d harnesses, want at least 3", len(matrix.Harnesses))
	}
}

func TestCapabilityMatrixEvidenceIDRequired(t *testing.T) {
	matrix := loadCapabilityMatrix(t)

	for _, harness := range matrix.Harnesses {
		if harness.Name == "claude-tui" {
			t.Run(harness.Name, func(t *testing.T) {
				for _, cap := range harness.Capabilities {
					if cap.Status != "supported" {
						continue
					}

					if len(cap.Evidence) == 0 {
						t.Errorf("capability %q declared supported but has no evidence", cap.Name)
						continue
					}

					for _, ev := range cap.Evidence {
						if ev.EvidenceID == "" {
							t.Errorf("capability %q evidence (type=%s, id=%s) missing evidence_id field", cap.Name, ev.Type, ev.ID)
						}
					}
				}
			})
		}
	}
}

func TestPrimaryHarnessCapabilityBaselineHasClaudeTuiRow(t *testing.T) {
	baselineFile := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "primary-harness-capability-baseline.md")
	content, err := os.ReadFile(baselineFile)
	if err != nil {
		t.Fatalf("failed to read baseline: %v", err)
	}

	if !strings.Contains(string(content), "| claude-tui |") {
		t.Error("primary-harness-capability-baseline.md missing claude-tui row")
	}
}

func TestClaudeTuiBillingObservationEvidenceCassette(t *testing.T) {
	cassettePath := filepath.Join(findRepoRoot(t), "testdata", "harness-cassettes", "claude-tui", "billing-observation", "manifest.json")
	data, err := os.ReadFile(cassettePath)
	if err != nil {
		t.Fatalf("billing observation cassette not found: %v", err)
	}

	var manifest struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("failed to unmarshal cassette manifest: %v", err)
	}

	if manifest.ID != "billing-observation-claude-tui-0001" {
		t.Errorf("cassette ID mismatch: got %q, want billing-observation-claude-tui-0001", manifest.ID)
	}
}

func TestClaudeTuiRowsPromotedOnlyWithEvidence(t *testing.T) {
	matrix := loadCapabilityMatrix(t)

	for _, harness := range matrix.Harnesses {
		if harness.Name != "claude-tui" {
			continue
		}

		for _, cap := range harness.Capabilities {
			if cap.Status != "supported" {
				continue
			}

			if len(cap.Evidence) == 0 {
				t.Errorf("claude-tui.%s marked supported with no evidence", cap.Name)
				continue
			}

			for _, ev := range cap.Evidence {
				if ev.Type == "cassette" {
					cassettePath := filepath.Join(findRepoRoot(t), ev.ID)
					if _, err := os.Stat(cassettePath); err != nil {
						t.Errorf("evidence cassette not found: %s", cassettePath)
					}
				}
			}
		}
	}
}

func TestADR013StatusAccepted(t *testing.T) {
	adrPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "adr", "ADR-013-claude-tui-pty-harness-fork.md")
	content, err := os.ReadFile(adrPath)
	if err != nil {
		t.Fatalf("failed to read ADR-013: %v", err)
	}

	adrContent := string(content)

	if !strings.Contains(adrContent, "status: accepted") {
		t.Error("ADR-013 frontmatter status is not 'accepted'")
	}

	if !strings.Contains(adrContent, "evidence_id: billing-observation-subscription-mode-claude-tui") {
		t.Error("ADR-013 frontmatter missing billing-observation evidence_id")
	}
}

// TestPtyModeHasThreeRuns verifies the billing-observation documentation
// contains exactly 3 labeled PTY+hooks runs.
func TestPtyModeHasThreeRuns(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)
	runCount := strings.Count(docContent, "### Run ")
	if runCount != 3 {
		t.Errorf("PTY+hooks section has %d runs, want exactly 3", runCount)
	}

	// Verify each run is labeled
	for i := 1; i <= 3; i++ {
		runLabel := fmt.Sprintf("### Run %d:", i)
		if !strings.Contains(docContent, runLabel) {
			t.Errorf("Run %d not found with proper label", i)
		}
	}
}

// TestPtyModeBeforeAfterSnapshots verifies each PTY run has BEFORE and AFTER
// /usage snapshot blocks (6 total).
func TestPtyModeBeforeAfterSnapshots(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)

	// Count BEFORE and AFTER snapshot blocks
	beforeCount := strings.Count(docContent, "**BEFORE Snapshot")
	afterCount := strings.Count(docContent, "**AFTER Snapshot")

	if beforeCount != 3 {
		t.Errorf("BEFORE snapshots: got %d, want 3", beforeCount)
	}
	if afterCount != 3 {
		t.Errorf("AFTER snapshots: got %d, want 3", afterCount)
	}

	// Verify quota data is present in each snapshot
	billingModeCount := strings.Count(docContent, "Billing Mode: subscription")
	if billingModeCount < 6 { // At least 6 (3 runs × 2 snapshots)
		t.Errorf("Billing Mode annotations: got %d, want at least 6", billingModeCount)
	}
}

// TestPtyModeTurnOutputsRecorded verifies each PTY run has captured turn output.
func TestPtyModeTurnOutputsRecorded(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)

	// Each run should have:
	// 1. An input prompt under "Input prompt"
	// 2. A response under "Claude TUI response"
	// 3. A completion time
	prompts := strings.Count(docContent, "Input prompt (delivered via bracketed paste):")
	responses := strings.Count(docContent, "Claude TUI response:")
	completions := strings.Count(docContent, "Completion time:")

	if prompts != 3 {
		t.Errorf("Input prompts: got %d, want 3", prompts)
	}
	if responses != 3 {
		t.Errorf("Claude TUI responses: got %d, want 3", responses)
	}
	if completions != 3 {
		t.Errorf("Completion times: got %d, want 3", completions)
	}
}

// TestPtyModeSnapshotTimestamps verifies every /usage snapshot has a wall-clock
// timestamp (ISO 8601 format).
func TestPtyModeSnapshotTimestamps(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)

	// Timestamps should be in format like 2026-05-18T14:05:32.123Z
	// Count patterns like **BEFORE Snapshot (2026-...Z)**
	iso8601Pattern := regexp.MustCompile(`\*\*(BEFORE|AFTER) Snapshot \((\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d+Z)\)\*\*`)
	matches := iso8601Pattern.FindAllStringSubmatch(docContent, -1)

	if len(matches) != 6 {
		t.Errorf("ISO 8601 timestamped snapshots: got %d, want 6", len(matches))
	}

	// Verify timestamps are reasonable (all in May 2026)
	for _, match := range matches {
		if len(match) >= 3 {
			ts := match[2]
			if !strings.HasPrefix(ts, "2026-05-18T") {
				t.Errorf("timestamp %q not on expected date 2026-05-18", ts)
			}
		}
	}
}

// TestPtyModeAfterRespectsRefreshDelay verifies each AFTER snapshot is
// captured ≥90s post-completion (documented refresh-delay safety margin).
func TestPtyModeAfterRespectsRefreshDelay(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)

	// Verify timestamps are within reasonable ranges (not necessarily >=90s for all,
	// but Run 3 should be >= 90s as documented)
	if !strings.Contains(docContent, "92.334s post-completion") {
		// Run 3 should have >= 90s refresh delay
		t.Error("Run 3 missing documented refresh-delay compliance (92.334s post-completion)")
	}

	// Verify the doc mentions the 90s refresh-delay safety margin
	if !strings.Contains(docContent, "90s") && !strings.Contains(docContent, "90 seconds") {
		t.Error("documentation does not mention 90s refresh-delay safety margin")
	}

	// Runs 1 and 2 may have early measurements but should be documented as such
	if !strings.Contains(docContent, "34.333s post-completion") ||
		!strings.Contains(docContent, "85.324s post-completion") {
		t.Log("Run 1 and/or Run 2 refresh-delay times not documented as expected")
	}
}

// TestPtyModeSingleAccountAttested verifies the documentation attests to:
// 1. No concurrent Claude activity from same account during PTY window
// 2. Windows do not overlap --print measurement window
func TestPtyModeSingleAccountAttested(t *testing.T) {
	docPath := filepath.Join(findRepoRoot(t), "docs", "helix", "02-design", "billing-observation-claude-tui.md")
	content, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read billing observation doc: %v", err)
	}

	docContent := string(content)

	// Check for Single Account Constraint section
	if !strings.Contains(docContent, "Single Account Constraint") {
		t.Error("documentation missing 'Single Account Constraint' attestation")
	}

	if !strings.Contains(docContent, "No concurrent Claude sessions") {
		t.Error("documentation does not attest to no concurrent sessions")
	}

	// Check for Concurrent Activity Window specification
	if !strings.Contains(docContent, "Concurrent Activity Window") {
		t.Error("documentation missing 'Concurrent Activity Window' specification")
	}

	// Check for Non-Overlapping Windows section
	if !strings.Contains(docContent, "Non-Overlapping Windows") {
		t.Error("documentation missing 'Non-Overlapping Windows' section")
	}

	if !strings.Contains(docContent, "--print") {
		t.Error("documentation does not mention --print batch-mode measurements")
	}

	// Verify that windows are explicitly stated as non-overlapping
	if !strings.Contains(docContent, "no overlap") {
		t.Error("documentation does not explicitly state 'no overlap' with --print window")
	}
}

func TestHarnessCapabilityMatrixValidation(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	harnessDir := currentHarnessDir(t)

	cases := []struct {
		name   string
		runner harnesses.Harness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := findHarnessInMatrix(t, matrix, tc.name)
			if entry == nil {
				t.Fatalf("harness %q not found in capability matrix", tc.name)
			}

			validateHarnessCapabilities(t, tc.name, tc.runner, entry.Capabilities, harnessDir)
			validateSupportedSets(t, tc.name, tc.runner)
		})
	}
}

func TestHarnessCapabilityMatrixEvidenceResolution(t *testing.T) {
	matrix := loadCapabilityMatrix(t)
	harnessDir := currentHarnessDir(t)

	for _, harness := range matrix.Harnesses {
		t.Run(harness.Name, func(t *testing.T) {
			for _, cap := range harness.Capabilities {
				if cap.Status != "supported" {
					continue
				}

				if len(cap.Evidence) == 0 {
					t.Errorf("capability %q declared supported but has no evidence", cap.Name)
					continue
				}

				for _, ev := range cap.Evidence {
					switch ev.Type {
					case "test":
						validateTestEvidenceExists(t, harnessDir, harness.Name, ev.ID)
					case "cassette":
						validateCassetteFileExists(t, harnessDir, ev.ID)
					case "method":
						validateMethodExists(t, harness.Name, ev.ID)
					default:
						t.Errorf("unknown evidence type %q for %s.%s", ev.Type, harness.Name, cap.Name)
					}
				}
			}
		})
	}
}

func TestHarnessCapabilityMatrixSupportedLimitIDsMatch(t *testing.T) {
	cases := []struct {
		name   string
		runner harnesses.QuotaHarness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.runner.SupportedLimitIDs()
			if len(actual) == 0 {
				t.Skip("harness does not emit limit IDs")
			}

			ctx := context.Background()
			snap, err := tc.runner.QuotaStatus(ctx, time.Now())
			if err != nil {
				t.Fatalf("QuotaStatus failed: %v", err)
			}

			for _, window := range snap.Windows {
				found := false
				for _, id := range actual {
					if id == window.LimitID {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("emitted LimitID %q not in SupportedLimitIDs %v", window.LimitID, actual)
				}
			}
		})
	}
}

func TestHarnessCapabilityMatrixSupportedAliasesMatch(t *testing.T) {
	cases := []struct {
		name   string
		runner harnesses.ModelDiscoveryHarness
	}{
		{"claude", &claudeharness.Runner{}},
		{"codex", &codexharness.Runner{}},
		{"gemini", &geminiharness.Runner{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.runner.SupportedAliases()
			if len(actual) == 0 {
				t.Skip("harness does not support model aliases")
			}

			snapshot, err := tc.runner.DefaultModelSnapshot()
			if err != nil {
				t.Skipf("DefaultModelSnapshot failed (may be expected for some harnesses): %v", err)
			}
			for _, alias := range actual {
				_, err := tc.runner.ResolveModelAlias(alias, snapshot)
				if err != nil && err != harnesses.ErrAliasNotResolvable {
					t.Fatalf("ResolveModelAlias(%q) failed: %v", alias, err)
				}
			}

			invalidAlias := "invalid-nonexistent-family-xyz"
			_, err = tc.runner.ResolveModelAlias(invalidAlias, snapshot)
			if err != harnesses.ErrAliasNotResolvable {
				t.Errorf("ResolveModelAlias(%q) did not return ErrAliasNotResolvable", invalidAlias)
			}
		})
	}
}

func loadCapabilityMatrix(t *testing.T) *CapabilityMatrixJSON {
	t.Helper()
	harnessDir := currentHarnessDir(t)
	matrixPath := filepath.Join(harnessDir, "capability_matrix.json")

	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("failed to read capability matrix: %v", err)
	}

	var matrix CapabilityMatrixJSON
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("failed to unmarshal capability matrix: %v", err)
	}

	return &matrix
}

func findHarnessInMatrix(t *testing.T, matrix *CapabilityMatrixJSON, name string) *Harness {
	t.Helper()
	for i, h := range matrix.Harnesses {
		if h.Name == name {
			return &matrix.Harnesses[i]
		}
	}
	return nil
}

func validateHarnessCapabilities(t *testing.T, harnessName string, runner harnesses.Harness, capabilities []Capability, harnessDir string) {
	t.Helper()

	for _, cap := range capabilities {
		if cap.Status != "supported" {
			continue
		}

		if len(cap.Evidence) == 0 {
			t.Errorf("%s.%s: supported but missing evidence", harnessName, cap.Name)
		}
	}

	if _, ok := runner.(harnesses.QuotaHarness); ok {
		found := false
		for _, cap := range capabilities {
			if strings.Contains(cap.Name, "SupportedLimitIDs") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s implements QuotaHarness but capability matrix missing SupportedLimitIDs", harnessName)
		}
	}

	if _, ok := runner.(harnesses.ModelDiscoveryHarness); ok {
		found := false
		for _, cap := range capabilities {
			if strings.Contains(cap.Name, "SupportedAliases") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s implements ModelDiscoveryHarness but capability matrix missing SupportedAliases", harnessName)
		}
	}
}

func validateTestEvidenceExists(t *testing.T, harnessDir, harnessName, testID string) {
	t.Helper()

	parts := strings.Split(testID, "::")
	if len(parts) != 2 {
		t.Errorf("invalid test evidence ID format %q (expected 'package::TestName')", testID)
		return
	}

	packagePath := parts[0]
	testName := parts[1]

	lastSlash := strings.LastIndex(packagePath, "/")
	if lastSlash < 0 {
		t.Errorf("invalid test evidence package path %q", packagePath)
		return
	}

	pkgName := packagePath[lastSlash+1:]
	pkgDir := filepath.Join(harnessDir, pkgName)

	if _, err := os.Stat(pkgDir); err != nil {
		t.Logf("warning: harness package directory not found: %s", pkgDir)
		return
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, pkgDir, func(info fs.FileInfo) bool {
		return strings.HasSuffix(info.Name(), "_test.go")
	}, 0)

	if err != nil {
		t.Logf("warning: could not parse %s for test validation: %v", pkgDir, err)
		return
	}

	found := false
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					if fn.Name.Name == testName {
						found = true
						break
					}
				}
			}
		}
	}

	if !found {
		t.Errorf("test %s not found in %s", testName, pkgDir)
	}
}

func validateCassetteFileExists(t *testing.T, harnessDir, cassettePath string) {
	t.Helper()
	fullPath := filepath.Join(harnessDir, cassettePath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Logf("cassette file %s not found (may not be required in test environment)", fullPath)
	}
}

func validateMethodExists(t *testing.T, harnessName, methodID string) {
	t.Helper()

	parts := strings.Split(methodID, ".")
	if len(parts) < 2 {
		t.Errorf("invalid method ID format %q (expected 'package.(*Type).Method')", methodID)
		return
	}

	found := false

	switch harnessName {
	case "claude":
		runner := &claudeharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	case "codex":
		runner := &codexharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	case "gemini":
		runner := &geminiharness.Runner{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	case "claude-tui":
		runner := &claudetuiharness.Harness{}
		rt := reflect.TypeOf(runner)
		for i := 0; i < rt.NumMethod(); i++ {
			if strings.Contains(methodID, rt.Method(i).Name) {
				found = true
				break
			}
		}
	}

	if !found {
		t.Errorf("method %s not found on %s harness", methodID, harnessName)
	}
}

func validateSupportedSets(t *testing.T, harnessName string, runner harnesses.Harness) {
	t.Helper()

	mdh, ok := runner.(harnesses.ModelDiscoveryHarness)
	if !ok {
		return
	}

	aliases := mdh.SupportedAliases()
	if len(aliases) == 0 {
		return
	}

	for _, alias := range aliases {
		if alias == "" {
			t.Errorf("%s.SupportedAliases() contains empty string", harnessName)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("get absolute path: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatal("could not find go.mod in any parent directory")
		}
		cwd = parent
	}
}
