package comparison

import (
	"encoding/json"
	"math"

	"github.com/easel/fizeau"
)

func normalizeComparisonCost(cost *float64, source fizeau.CostSource) (*float64, fizeau.CostSource) {
	if cost == nil || *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
		return nil, fizeau.CostSourceUnknown
	}
	switch source {
	case fizeau.CostSourceReported, fizeau.CostSourceConfigured:
		amount := *cost
		return &amount, source
	default:
		return nil, fizeau.CostSourceUnknown
	}
}

func normalizeRunResultCost(result RunResult) (*float64, fizeau.CostSource) {
	return normalizeComparisonCost(&result.CostUSD, result.CostSource)
}

// MarshalJSON always emits normalized provenance and preserves known zero.
func (a ComparisonArm) MarshalJSON() ([]byte, error) {
	type comparisonArmAlias ComparisonArm
	normalized := a
	normalized.CostUSD, normalized.CostSource = normalizeComparisonCost(a.CostUSD, a.CostSource)
	return json.Marshal(comparisonArmAlias(normalized))
}

// UnmarshalJSON accepts legacy evidence but does not promote a source-less
// scalar cost to an authoritative measurement.
func (a *ComparisonArm) UnmarshalJSON(data []byte) error {
	type comparisonArmAlias ComparisonArm
	var decoded comparisonArmAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	arm := ComparisonArm(decoded)
	arm.CostUSD, arm.CostSource = normalizeComparisonCost(arm.CostUSD, arm.CostSource)
	*a = arm
	return nil
}

// MarshalJSON keeps aggregate cost presence and provenance normalized on the
// durable benchmark evidence boundary.
func (s BenchmarkArmSummary) MarshalJSON() ([]byte, error) {
	type benchmarkArmSummaryAlias BenchmarkArmSummary
	normalized := s
	normalized.TotalCostUSD, normalized.CostSource = normalizeComparisonCost(s.TotalCostUSD, s.CostSource)
	return json.Marshal(benchmarkArmSummaryAlias(normalized))
}

func (s *BenchmarkArmSummary) UnmarshalJSON(data []byte) error {
	type benchmarkArmSummaryAlias BenchmarkArmSummary
	var decoded benchmarkArmSummaryAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	summary := BenchmarkArmSummary(decoded)
	summary.TotalCostUSD, summary.CostSource = normalizeComparisonCost(summary.TotalCostUSD, summary.CostSource)
	*s = summary
	return nil
}
