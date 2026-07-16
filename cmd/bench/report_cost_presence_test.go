package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/easel/fizeau"
	"github.com/easel/fizeau/internal/comparison"
)

func TestBenchmarkReportCostPresence(t *testing.T) {
	zero := 0.0
	positive := 1.234567
	unknownAmount := 99.0
	invalidAmount := 2.0
	result := &comparison.BenchmarkResult{
		Suite:   "cost-presence",
		Version: "1",
		Summary: comparison.BenchmarkSummary{
			TotalPrompts: 1,
			Arms: []comparison.BenchmarkArmSummary{
				{Label: "unknown", Completed: 1, TotalTokens: 10, TotalCostUSD: &unknownAmount, CostSource: fizeau.CostSourceUnknown, AvgDurationMS: 5},
				{Label: "zero", Completed: 1, TotalTokens: 20, TotalCostUSD: &zero, CostSource: fizeau.CostSourceReported, AvgDurationMS: 6},
				{Label: "positive", Completed: 1, TotalTokens: 30, TotalCostUSD: &positive, CostSource: fizeau.CostSourceConfigured, AvgDurationMS: 7},
				{Label: "invalid", Completed: 1, TotalTokens: 40, TotalCostUSD: &invalidAmount, CostSource: fizeau.CostSource("estimated"), AvgDurationMS: 8},
			},
		},
	}

	table := captureBenchmarkReport(t, func() { printSummaryTable(result) })
	assertBenchmarkTableCost(t, table, "unknown", "n/a")
	assertBenchmarkTableCost(t, table, "zero", "0.000000")
	assertBenchmarkTableCost(t, table, "positive", "1.234567")
	assertBenchmarkTableCost(t, table, "invalid", "n/a")

	markdown := captureBenchmarkReport(t, func() { printMarkdownReport(result) })
	for _, want := range []string{
		"| unknown | 1 | 0 | 10 | n/a | 5 |",
		"| zero | 1 | 0 | 20 | 0.000000 | 6 |",
		"| positive | 1 | 0 | 30 | 1.234567 | 7 |",
		"| invalid | 1 | 0 | 40 | n/a | 8 |",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("Markdown report missing %q:\n%s", want, markdown)
		}
	}
}

func captureBenchmarkReport(t *testing.T, render func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w

	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&output, r)
		done <- copyErr
	}()

	render()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = oldStdout
	if err := <-done; err != nil {
		t.Fatalf("capture stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return output.String()
}

func assertBenchmarkTableCost(t *testing.T, output, label, want string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 6 && fields[0] == label {
			if fields[4] != want {
				t.Fatalf("table cost for %s = %q, want %q; row %q", label, fields[4], want, line)
			}
			return
		}
	}
	t.Fatalf("table report missing row %q:\n%s", label, output)
}
