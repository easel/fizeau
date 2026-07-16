package fizeau

import (
	"encoding/json"
	"math"
)

// CostSource identifies the provenance of a final session cost.
type CostSource string

const (
	CostSourceReported   CostSource = "reported"
	CostSourceConfigured CostSource = "configured"
	CostSourceUnknown    CostSource = "unknown"
)

// CostMeasurement returns the source-backed final cost as a fresh pointer.
// During the v0.15 compatibility migration, CostSource is the presence bit
// that distinguishes a known zero from an unknown scalar zero.
func (d ServiceFinalData) CostMeasurement() (*float64, CostSource) {
	return normalizePublicCost(d.CostUSD, d.CostSource)
}

func normalizePublicCost(cost float64, source CostSource) (*float64, CostSource) {
	return normalizePublicCostPointer(&cost, source)
}

func normalizePublicCostPointer(cost *float64, source CostSource) (*float64, CostSource) {
	source = normalizePublicCostSource(source)
	if cost == nil || source == CostSourceUnknown || *cost < 0 || math.IsNaN(*cost) || math.IsInf(*cost, 0) {
		return nil, CostSourceUnknown
	}
	value := *cost
	return &value, source
}

func normalizePublicCostSource(source CostSource) CostSource {
	switch source {
	case CostSourceReported, CostSourceConfigured:
		return source
	default:
		return CostSourceUnknown
	}
}

func decodePublicCost(costRaw, sourceRaw json.RawMessage) (float64, CostSource, error) {
	amount, source, err := decodePublicCostMeasurement(costRaw, sourceRaw)
	if err != nil || amount == nil {
		return 0, source, err
	}
	return *amount, source, nil
}

func decodePublicCostMeasurement(costRaw, sourceRaw json.RawMessage) (*float64, CostSource, error) {
	if len(sourceRaw) == 0 || string(sourceRaw) == "null" {
		return nil, CostSourceUnknown, nil
	}
	var source CostSource
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		return nil, CostSourceUnknown, nil
	}
	if len(costRaw) == 0 || string(costRaw) == "null" {
		return nil, CostSourceUnknown, nil
	}
	var cost float64
	if err := json.Unmarshal(costRaw, &cost); err != nil {
		return nil, CostSourceUnknown, err
	}
	amount, normalizedSource := normalizePublicCost(cost, source)
	return amount, normalizedSource, nil
}

// MarshalJSON keeps the temporary scalar Go field while emitting the
// normative optional amount and mandatory provenance on the wire.
func (d ServiceFinalData) MarshalJSON() ([]byte, error) {
	type serviceFinalDataAlias ServiceFinalData
	cost, source := d.CostMeasurement()
	return json.Marshal(struct {
		serviceFinalDataAlias
		CostUSD    *float64   `json:"cost_usd,omitempty"`
		CostSource CostSource `json:"cost_source"`
	}{
		serviceFinalDataAlias: serviceFinalDataAlias(d),
		CostUSD:               cost,
		CostSource:            source,
	})
}

// UnmarshalJSON accepts legacy source-less finals without promoting their
// scalar cost to authoritative billing evidence.
func (d *ServiceFinalData) UnmarshalJSON(data []byte) error {
	type serviceFinalDataAlias ServiceFinalData
	decoded := serviceFinalDataAlias(*d)
	wire := struct {
		*serviceFinalDataAlias
		CostUSD    json.RawMessage `json:"cost_usd"`
		CostSource json.RawMessage `json:"cost_source"`
	}{
		serviceFinalDataAlias: &decoded,
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	cost, source, err := decodePublicCost(wire.CostUSD, wire.CostSource)
	if err != nil {
		return err
	}
	decoded.CostUSD = cost
	decoded.CostSource = source
	*d = ServiceFinalData(decoded)
	return nil
}

func (o ServiceOverrideOutcome) costMeasurement() (*float64, CostSource) {
	return normalizePublicCost(o.CostUSD, o.CostSource)
}

// MarshalJSON preserves known zero override costs and omits unknown amounts.
func (o ServiceOverrideOutcome) MarshalJSON() ([]byte, error) {
	type serviceOverrideOutcomeAlias ServiceOverrideOutcome
	cost, source := o.costMeasurement()
	return json.Marshal(struct {
		serviceOverrideOutcomeAlias
		CostUSD    *float64   `json:"cost_usd,omitempty"`
		CostSource CostSource `json:"cost_source"`
	}{
		serviceOverrideOutcomeAlias: serviceOverrideOutcomeAlias(o),
		CostUSD:                     cost,
		CostSource:                  source,
	})
}

// UnmarshalJSON normalizes invalid and legacy source-less override costs.
func (o *ServiceOverrideOutcome) UnmarshalJSON(data []byte) error {
	type serviceOverrideOutcomeAlias ServiceOverrideOutcome
	decoded := serviceOverrideOutcomeAlias(*o)
	wire := struct {
		*serviceOverrideOutcomeAlias
		CostUSD    json.RawMessage `json:"cost_usd"`
		CostSource json.RawMessage `json:"cost_source"`
	}{
		serviceOverrideOutcomeAlias: &decoded,
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	cost, source, err := decodePublicCost(wire.CostUSD, wire.CostSource)
	if err != nil {
		return err
	}
	decoded.CostUSD = cost
	decoded.CostSource = source
	*o = ServiceOverrideOutcome(decoded)
	return nil
}

// MarshalJSON keeps durable public session projections on the same normalized
// amount/provenance wire contract as final and override events.
func (d SessionEndData) MarshalJSON() ([]byte, error) {
	type sessionEndDataAlias SessionEndData
	cost, source := normalizePublicCostPointer(d.CostUSD, d.CostSource)
	return json.Marshal(struct {
		sessionEndDataAlias
		CostUSD    *float64   `json:"cost_usd,omitempty"`
		CostSource CostSource `json:"cost_source"`
	}{
		sessionEndDataAlias: sessionEndDataAlias(d),
		CostUSD:             cost,
		CostSource:          source,
	})
}

// UnmarshalJSON accepts legacy source-less session records without promoting
// their amount to authoritative billing evidence.
func (d *SessionEndData) UnmarshalJSON(data []byte) error {
	type sessionEndDataAlias SessionEndData
	decoded := sessionEndDataAlias(*d)
	wire := struct {
		*sessionEndDataAlias
		CostUSD    json.RawMessage `json:"cost_usd"`
		CostSource json.RawMessage `json:"cost_source"`
	}{
		sessionEndDataAlias: &decoded,
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	cost, source, err := decodePublicCostMeasurement(wire.CostUSD, wire.CostSource)
	if err != nil {
		return err
	}
	decoded.CostUSD = cost
	decoded.CostSource = source
	*d = SessionEndData(decoded)
	return nil
}
