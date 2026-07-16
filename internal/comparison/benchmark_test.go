package comparison

import (
	"path/filepath"
	"testing"

	"github.com/easel/fizeau"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadBenchmarkSuite(t *testing.T) {
	suite, err := LoadBenchmarkSuite(filepath.Join("testdata", "benchmark-feat019.json"))
	require.NoError(t, err)
	assert.Equal(t, "FEAT-019 Agent Evaluation Benchmark", suite.Name)
	assert.Equal(t, "1.0.0", suite.Version)
	assert.Len(t, suite.Arms, 6)
	assert.Len(t, suite.Prompts, 4)

	ids := map[string]bool{}
	for _, p := range suite.Prompts {
		ids[p.ID] = true
		assert.NotEmpty(t, p.Prompt, "prompt %s should have inline text", p.ID)
	}
	assert.True(t, ids["read-comprehension"])
	assert.True(t, ids["code-analysis"])
	assert.True(t, ids["cross-reference"])
	assert.True(t, ids["simple-coding"])
}

func TestRunBenchmarkWithMockRunner(t *testing.T) {
	mockRun := func(harness, model, prompt string) RunResult {
		return RunResult{
			Harness:      harness,
			Model:        "test-model",
			Output:       "benchmark answer",
			InputTokens:  100,
			OutputTokens: 20,
			Tokens:       120,
			ExitCode:     0,
		}
	}

	suite := &BenchmarkSuite{
		Name:    "test-suite",
		Version: "0.1",
		Arms: []BenchmarkArm{
			{Label: "fiz-test", Harness: "fiz", Tier: "smart"},
			{Label: "virtual-test", Harness: "virtual"},
		},
		Prompts: []BenchmarkPrompt{
			{ID: "p1", Name: "Test 1", Prompt: "first prompt"},
			{ID: "p2", Name: "Test 2", Prompt: "second prompt"},
		},
	}

	result, err := RunBenchmark(mockRun, suite)
	require.NoError(t, err)
	assert.Equal(t, "test-suite", result.Suite)
	assert.Len(t, result.Comparisons, 2)

	for _, cmp := range result.Comparisons {
		assert.Len(t, cmp.Arms, 2)
	}

	assert.Equal(t, 2, result.Summary.TotalPrompts)
	assert.Len(t, result.Summary.Arms, 2)

	fizStats := result.Summary.Arms[0]
	assert.Equal(t, "fiz-test", fizStats.Label)
	assert.Equal(t, 2, fizStats.Completed)
	assert.Equal(t, 0, fizStats.Failed)
}

func TestSummarizeBenchmark(t *testing.T) {
	result := &BenchmarkResult{
		Arms: []BenchmarkArm{
			{Label: "a"},
			{Label: "b"},
		},
		Comparisons: []ComparisonRecord{
			{
				Arms: []ComparisonArm{
					{Harness: "a", ExitCode: 0, Tokens: 100, CostUSD: testCostPointer(0.01), CostSource: fizeau.CostSourceReported, DurationMS: 5000},
					{Harness: "b", ExitCode: 0, Tokens: 200, CostUSD: testCostPointer(0.05), CostSource: fizeau.CostSourceReported, DurationMS: 3000},
				},
			},
			{
				Arms: []ComparisonArm{
					{Harness: "a", ExitCode: 0, Tokens: 150, CostUSD: testCostPointer(0.02), CostSource: fizeau.CostSourceReported, DurationMS: 4000},
					{Harness: "b", ExitCode: 1, Tokens: 0, CostUSD: testCostPointer(0), CostSource: fizeau.CostSourceReported, DurationMS: 1000},
				},
			},
		},
	}

	summary := summarizeBenchmark(result)
	assert.Equal(t, 2, summary.TotalPrompts)
	require.Len(t, summary.Arms, 2)

	a := summary.Arms[0]
	assert.Equal(t, "a", a.Label)
	assert.Equal(t, 2, a.Completed)
	assert.Equal(t, 250, a.TotalTokens)
	require.NotNil(t, a.TotalCostUSD)
	assert.InDelta(t, 0.03, *a.TotalCostUSD, 0.001)
	assert.Equal(t, fizeau.CostSourceReported, a.CostSource)
	assert.Equal(t, 4500, a.AvgDurationMS)

	b := summary.Arms[1]
	assert.Equal(t, "b", b.Label)
	assert.Equal(t, 1, b.Completed)
	assert.Equal(t, 1, b.Failed)
}
