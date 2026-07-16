package fizeau

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/session"
)

func TestServiceFinalCostPresenceJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		cost       *float64
		source     CostSource
		wantCost   *float64
		wantSource CostSource
	}{
		{name: "unknown", source: CostSourceUnknown, wantSource: CostSourceUnknown},
		{name: "known zero", cost: float64Pointer(0), source: CostSourceReported, wantCost: float64Pointer(0), wantSource: CostSourceReported},
		{name: "positive reported", cost: float64Pointer(1.25), source: CostSourceReported, wantCost: float64Pointer(1.25), wantSource: CostSourceReported},
		{name: "positive configured", cost: float64Pointer(2.5), source: CostSourceConfigured, wantCost: float64Pointer(2.5), wantSource: CostSourceConfigured},
		{name: "invalid source", cost: float64Pointer(3.75), source: CostSource("estimated"), wantSource: CostSourceUnknown},
		{name: "negative producer", cost: float64Pointer(-1), source: CostSourceReported, wantSource: CostSourceUnknown},
		{name: "non finite producer", cost: float64Pointer(math.Inf(1)), source: CostSourceReported, wantSource: CostSourceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			final := ServiceFinalData{Status: "success", CostUSD: tt.cost, CostSource: tt.source}
			cost, source := final.CostMeasurement()
			assertPublicCost(t, cost, source, tt.wantCost, tt.wantSource)
			if cost != nil && cost == final.CostUSD {
				t.Fatal("CostMeasurement returned the final's owned pointer")
			}

			raw, err := json.Marshal(final)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if !bytes.Contains(raw, []byte(`"cost_source":"`+string(tt.wantSource)+`"`)) {
				t.Fatalf("mandatory normalized cost_source missing from %s", raw)
			}
			hasCost := bytes.Contains(raw, []byte(`"cost_usd"`))
			if hasCost != (tt.wantCost != nil) {
				t.Fatalf("cost_usd presence = %v, want %v in %s", hasCost, tt.wantCost != nil, raw)
			}
			if bytes.Contains(raw, []byte(`"cost_usd":-`)) {
				t.Fatalf("negative producer amount escaped normalization: %s", raw)
			}

			var decoded ServiceFinalData
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			assertPublicCost(t, decoded.CostUSD, decoded.CostSource, tt.wantCost, tt.wantSource)
			decodedMeasurement, decodedSource := decoded.CostMeasurement()
			assertPublicCost(t, decodedMeasurement, decodedSource, tt.wantCost, tt.wantSource)
			if decodedMeasurement != nil && decodedMeasurement == decoded.CostUSD {
				t.Fatal("CostMeasurement did not clone the decoded final amount")
			}
		})
	}

	for _, input := range []string{
		`{"status":"success","cost_usd":4.5}`,
		`{"status":"success","cost_usd":4.5,"cost_source":"unknown"}`,
		`{"status":"success","cost_source":"reported"}`,
		`{"status":"success","cost_usd":-1,"cost_source":"reported"}`,
		`{"status":"success","cost_usd":4.5,"cost_source":"estimated"}`,
	} {
		t.Run("invalid input "+input, func(t *testing.T) {
			var final ServiceFinalData
			if err := json.Unmarshal([]byte(input), &final); err != nil {
				t.Fatalf("UnmarshalJSON: %v", err)
			}
			if final.CostUSD != nil || final.CostSource != CostSourceUnknown {
				t.Fatalf("invalid input promoted cost: %v/%q", final.CostUSD, final.CostSource)
			}
			raw, err := json.Marshal(final)
			if err != nil {
				t.Fatalf("marshal normalized final: %v", err)
			}
			if bytes.Contains(raw, []byte(`"cost_usd"`)) || !bytes.Contains(raw, []byte(`"cost_source":"unknown"`)) {
				t.Fatalf("invalid input re-emitted numeric cost: %s", raw)
			}
		})
	}
}

func TestDrainExecutePreservesFinalCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		cost       *float64
		source     CostSource
		wantCost   *float64
		wantSource CostSource
	}{
		{name: "unknown", source: CostSourceUnknown, wantSource: CostSourceUnknown},
		{name: "known zero", cost: float64Pointer(0), source: CostSourceReported, wantCost: float64Pointer(0), wantSource: CostSourceReported},
		{name: "positive", cost: float64Pointer(1.75), source: CostSourceConfigured, wantCost: float64Pointer(1.75), wantSource: CostSourceConfigured},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalRaw, err := json.Marshal(ServiceFinalData{
				Status:     "success",
				CostUSD:    tt.cost,
				CostSource: tt.source,
			})
			if err != nil {
				t.Fatalf("marshal final: %v", err)
			}
			events := make(chan ServiceEvent, 1)
			events <- ServiceEvent{Type: harnesses.EventType(ServiceEventTypeFinal), Data: finalRaw}
			close(events)

			result, err := DrainExecute(context.Background(), events)
			if err != nil {
				t.Fatalf("DrainExecute: %v", err)
			}
			assertPublicCost(t, result.CostUSD, result.CostSource, tt.wantCost, tt.wantSource)
			assertPublicCost(t, result.Final.CostUSD, result.Final.CostSource, tt.wantCost, tt.wantSource)
			if result.CostUSD != nil && result.CostUSD == result.Final.CostUSD {
				t.Fatal("drain result aliases the final payload's amount")
			}
			if result.CostUSD != nil {
				want := *result.CostUSD
				*result.Final.CostUSD = 99
				if *result.CostUSD != want {
					t.Fatalf("drain result changed after final mutation: got %v, want %v", *result.CostUSD, want)
				}
			}
		})
	}
}

func TestExecuteOverridePreservesFinalCostPresence(t *testing.T) {
	tests := []struct {
		name       string
		cost       *float64
		source     CostSource
		wantCost   *float64
		wantSource CostSource
	}{
		{name: "unknown", source: CostSourceUnknown, wantSource: CostSourceUnknown},
		{name: "known zero", cost: float64Pointer(0), source: CostSourceConfigured, wantCost: float64Pointer(0), wantSource: CostSourceConfigured},
		{name: "positive", cost: float64Pointer(3.5), source: CostSourceReported, wantCost: float64Pointer(3.5), wantSource: CostSourceReported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalRaw, err := json.Marshal(ServiceFinalData{
				Status:     "success",
				CostUSD:    tt.cost,
				CostSource: tt.source,
				DurationMS: 7,
			})
			if err != nil {
				t.Fatalf("marshal final: %v", err)
			}
			finalEvent := ServiceEvent{
				Type:     harnesses.EventType(ServiceEventTypeFinal),
				Sequence: 2,
				Time:     time.Now().UTC(),
				Data:     finalRaw,
			}
			overrideEvent, payload, ok := makeOverrideEvent(&overrideContext{}, "session", finalEvent, nil)
			if !ok || payload.Outcome == nil {
				t.Fatal("makeOverrideEvent did not produce an outcome")
			}
			assertPublicCost(t, payload.Outcome.CostUSD, payload.Outcome.CostSource, tt.wantCost, tt.wantSource)

			var decoded ServiceOverrideData
			if err := json.Unmarshal(overrideEvent.Data, &decoded); err != nil {
				t.Fatalf("decode override event: %v", err)
			}
			if decoded.Outcome == nil {
				t.Fatal("decoded override outcome is nil")
			}
			assertPublicCost(t, decoded.Outcome.CostUSD, decoded.Outcome.CostSource, tt.wantCost, tt.wantSource)
			if payload.Outcome.CostUSD != nil && payload.Outcome.CostUSD == decoded.Outcome.CostUSD {
				t.Fatal("override payload aliases its decoded wire projection")
			}
			if payload.Outcome.CostUSD != nil {
				want := *payload.Outcome.CostUSD
				*decoded.Outcome.CostUSD = 99
				if *payload.Outcome.CostUSD != want {
					t.Fatalf("override payload changed after decoded mutation: got %v, want %v", *payload.Outcome.CostUSD, want)
				}
			}
		})
	}

	for _, input := range []string{
		`{"status":"success","cost_usd":8}`,
		`{"status":"success","cost_usd":-1,"cost_source":"reported"}`,
	} {
		var legacy ServiceOverrideOutcome
		if err := json.Unmarshal([]byte(input), &legacy); err != nil {
			t.Fatalf("legacy override decode: %v", err)
		}
		if legacy.CostUSD != nil || legacy.CostSource != CostSourceUnknown {
			t.Fatalf("legacy override promoted cost: %v/%q", legacy.CostUSD, legacy.CostSource)
		}
	}
}

func TestSessionEndCostPresenceJSONRoundTrip(t *testing.T) {
	zero := 0.0
	internalRaw, err := json.Marshal(session.SessionEndData{
		CostUSD:    &zero,
		CostSource: harnesses.CostSourceReported,
	})
	if err != nil {
		t.Fatalf("marshal internal session projection: %v", err)
	}
	var public SessionEndData
	if err := json.Unmarshal(internalRaw, &public); err != nil {
		t.Fatalf("decode public session projection: %v", err)
	}
	if public.CostUSD == nil || *public.CostUSD != 0 || public.CostSource != CostSourceReported {
		t.Fatalf("session cost = %v/%q, want 0/reported", public.CostUSD, public.CostSource)
	}

	tests := []struct {
		name       string
		cost       *float64
		source     CostSource
		wantCost   *float64
		wantSource CostSource
	}{
		{name: "unknown", wantSource: CostSourceUnknown},
		{name: "positive reported", cost: float64Pointer(1.5), source: CostSourceReported, wantCost: float64Pointer(1.5), wantSource: CostSourceReported},
		{name: "positive configured", cost: float64Pointer(2.5), source: CostSourceConfigured, wantCost: float64Pointer(2.5), wantSource: CostSourceConfigured},
		{name: "invalid source", cost: float64Pointer(3.5), source: CostSource("estimated"), wantSource: CostSourceUnknown},
		{name: "negative", cost: float64Pointer(-1), source: CostSourceReported, wantSource: CostSourceUnknown},
		{name: "nan", cost: float64Pointer(math.NaN()), source: CostSourceReported, wantSource: CostSourceUnknown},
		{name: "positive infinity", cost: float64Pointer(math.Inf(1)), source: CostSourceConfigured, wantSource: CostSourceUnknown},
		{name: "negative infinity", cost: float64Pointer(math.Inf(-1)), source: CostSourceReported, wantSource: CostSourceUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(SessionEndData{CostUSD: tt.cost, CostSource: tt.source})
			if err != nil {
				t.Fatalf("marshal session: %v", err)
			}
			var decoded SessionEndData
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal session: %v", err)
			}
			assertPublicCost(t, decoded.CostUSD, decoded.CostSource, tt.wantCost, tt.wantSource)
		})
	}

	for _, input := range []string{
		`{"cost_usd":3.5}`,
		`{"cost_usd":3.5,"cost_source":"unknown"}`,
		`{"cost_usd":-1,"cost_source":"reported"}`,
		`{"cost_source":"configured"}`,
	} {
		var legacy SessionEndData
		if err := json.Unmarshal([]byte(input), &legacy); err != nil {
			t.Fatalf("decode session input %s: %v", input, err)
		}
		if legacy.CostUSD != nil || legacy.CostSource != CostSourceUnknown {
			t.Fatalf("session input %s promoted invalid cost: %v/%q", input, legacy.CostUSD, legacy.CostSource)
		}
		normalizedRaw, err := json.Marshal(legacy)
		if err != nil {
			t.Fatalf("marshal normalized session input %s: %v", input, err)
		}
		if bytes.Contains(normalizedRaw, []byte(`"cost_usd"`)) ||
			!bytes.Contains(normalizedRaw, []byte(`"cost_source":"unknown"`)) {
			t.Fatalf("session input %s emitted invalid measurement: %s", input, normalizedRaw)
		}
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}

func assertPublicCost(t *testing.T, gotCost *float64, gotSource CostSource, wantCost *float64, wantSource CostSource) {
	t.Helper()
	if gotSource != wantSource {
		t.Fatalf("cost source = %q, want %q", gotSource, wantSource)
	}
	if gotCost == nil || wantCost == nil {
		if gotCost != nil || wantCost != nil {
			t.Fatalf("cost = %v, want %v", gotCost, wantCost)
		}
		return
	}
	if *gotCost != *wantCost {
		t.Fatalf("cost = %v, want %v", *gotCost, *wantCost)
	}
	if gotCost == wantCost {
		t.Fatal("cost measurement returned caller-owned pointer")
	}
}
