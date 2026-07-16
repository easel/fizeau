package comparison

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/easel/fizeau"
)

type benchmarkCostAggregate struct {
	total    float64
	source   fizeau.CostSource
	count    int
	allKnown bool
}

func (a *benchmarkCostAggregate) add(cost *float64, source fizeau.CostSource) {
	if a.count == 0 {
		a.allKnown = true
	}
	a.count++

	amount, normalizedSource := normalizeComparisonCost(cost, source)
	if amount == nil {
		a.allKnown = false
		return
	}
	a.total += *amount
	if normalizedSource == fizeau.CostSourceReported || a.source == fizeau.CostSourceUnknown {
		a.source = normalizedSource
	}
}

func (a benchmarkCostAggregate) measurement() (*float64, fizeau.CostSource) {
	if a.count == 0 || !a.allKnown {
		return nil, fizeau.CostSourceUnknown
	}
	total := a.total
	return &total, a.source
}

// LoadBenchmarkSuite reads a benchmark suite from a JSON file.
func LoadBenchmarkSuite(path string) (*BenchmarkSuite, error) {
	// #nosec G304 -- path is the benchmark suite file the operator passed on
	// the command line; loading it is the explicit purpose of this function.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading benchmark suite: %w", err)
	}
	var suite BenchmarkSuite
	if err := json.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("parsing benchmark suite: %w", err)
	}
	return &suite, nil
}

// RunBenchmark executes all prompts in a suite against all arms.
// The run function is called once per (arm, prompt) pair.
func RunBenchmark(run RunFunc, suite *BenchmarkSuite) (*BenchmarkResult, error) {
	result := &BenchmarkResult{
		Suite:     suite.Name,
		Version:   suite.Version,
		Timestamp: time.Now().UTC(),
		Arms:      suite.Arms,
	}

	for _, prompt := range suite.Prompts {
		promptText := prompt.Prompt
		if promptText == "" && prompt.PromptFile != "" {
			data, err := os.ReadFile(prompt.PromptFile)
			if err != nil {
				return nil, fmt.Errorf("reading prompt file %s: %w", prompt.PromptFile, err)
			}
			promptText = string(data)
		}

		harnesses := make([]string, len(suite.Arms))
		armModels := make(map[int]string, len(suite.Arms))
		armLabels := make(map[int]string, len(suite.Arms))
		for i, arm := range suite.Arms {
			harnesses[i] = arm.Harness
			if arm.Model != "" {
				armModels[i] = arm.Model
			}
			armLabels[i] = arm.Label
		}

		compareOpts := CompareOptions{
			Prompt:    promptText,
			Harnesses: harnesses,
			ArmModels: armModels,
			ArmLabels: armLabels,
			Sandbox:   suite.Sandbox,
			PostRun:   suite.PostRun,
		}

		record, err := RunCompare(run, compareOpts)
		if err != nil {
			return nil, fmt.Errorf("prompt %s: %w", prompt.ID, err)
		}

		result.Comparisons = append(result.Comparisons, *record)
	}

	result.Summary = summarizeBenchmark(result)
	return result, nil
}

// summarizeBenchmark computes per-arm aggregates.
func summarizeBenchmark(result *BenchmarkResult) BenchmarkSummary {
	summary := BenchmarkSummary{
		TotalPrompts: len(result.Comparisons),
	}

	armStats := make(map[string]*BenchmarkArmSummary)
	armCosts := make(map[string]*benchmarkCostAggregate)
	armOrder := make([]string, len(result.Arms))
	for i, arm := range result.Arms {
		label := arm.Label
		armOrder[i] = label
		armStats[label] = &BenchmarkArmSummary{Label: label, CostSource: fizeau.CostSourceUnknown}
		armCosts[label] = &benchmarkCostAggregate{source: fizeau.CostSourceUnknown}
	}

	for _, cmp := range result.Comparisons {
		for _, arm := range cmp.Arms {
			stats, ok := armStats[arm.Harness]
			if !ok {
				continue
			}
			if arm.ExitCode == 0 {
				stats.Completed++
			} else {
				stats.Failed++
			}
			stats.TotalTokens += arm.Tokens
			armCosts[arm.Harness].add(arm.CostUSD, arm.CostSource)
			stats.AvgDurationMS += arm.DurationMS
		}
	}

	for _, label := range armOrder {
		stats := armStats[label]
		stats.TotalCostUSD, stats.CostSource = armCosts[label].measurement()
		total := stats.Completed + stats.Failed
		if total > 0 {
			stats.AvgDurationMS = stats.AvgDurationMS / total
		}
		summary.Arms = append(summary.Arms, *stats)
	}

	return summary
}

// SaveBenchmarkResult writes a benchmark result to a JSON file.
func SaveBenchmarkResult(path string, result *BenchmarkResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}
