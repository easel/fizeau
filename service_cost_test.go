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

func TestServiceFinalCostCompatibilityBridge(t *testing.T) {
	t.Run("final normalization and wire presence", func(t *testing.T) {
		tests := []struct {
			name       string
			cost       float64
			source     CostSource
			wantCost   *float64
			wantSource CostSource
		}{
			{name: "unknown", source: CostSourceUnknown, wantSource: CostSourceUnknown},
			{name: "known zero", source: CostSourceReported, wantCost: float64Pointer(0), wantSource: CostSourceReported},
			{name: "positive reported", cost: 1.25, source: CostSourceReported, wantCost: float64Pointer(1.25), wantSource: CostSourceReported},
			{name: "positive configured", cost: 2.5, source: CostSourceConfigured, wantCost: float64Pointer(2.5), wantSource: CostSourceConfigured},
			{name: "invalid source", cost: 3.75, source: CostSource("estimated"), wantSource: CostSourceUnknown},
			{name: "negative sentinel", cost: -1, source: CostSourceReported, wantSource: CostSourceUnknown},
			{name: "non finite", cost: math.Inf(1), source: CostSourceReported, wantSource: CostSourceUnknown},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				final := ServiceFinalData{Status: "success", CostUSD: tt.cost, CostSource: tt.source}
				gotCost, gotSource := final.CostMeasurement()
				assertPublicCost(t, gotCost, gotSource, tt.wantCost, tt.wantSource)

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

				var decoded ServiceFinalData
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatalf("UnmarshalJSON: %v", err)
				}
				decodedCost, decodedSource := decoded.CostMeasurement()
				assertPublicCost(t, decodedCost, decodedSource, tt.wantCost, tt.wantSource)
			})
		}
	})

	t.Run("legacy and invalid input stay unknown", func(t *testing.T) {
		inputs := []string{
			`{"status":"success","cost_usd":4.5}`,
			`{"status":"success","cost_usd":4.5,"cost_source":"unknown"}`,
			`{"status":"success","cost_source":"reported"}`,
			`{"status":"success","cost_usd":-1,"cost_source":"reported"}`,
			`{"status":"success","cost_usd":4.5,"cost_source":"estimated"}`,
		}
		for _, input := range inputs {
			var final ServiceFinalData
			if err := json.Unmarshal([]byte(input), &final); err != nil {
				t.Fatalf("UnmarshalJSON(%s): %v", input, err)
			}
			cost, source := final.CostMeasurement()
			assertPublicCost(t, cost, source, nil, CostSourceUnknown)
		}
	})

	t.Run("drain clones normalized measurement", func(t *testing.T) {
		finalRaw, err := json.Marshal(ServiceFinalData{
			Status:     "success",
			CostUSD:    0,
			CostSource: CostSourceReported,
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
		if result.CostUSD != 0 || result.CostSource != CostSourceReported {
			t.Fatalf("drained cost = %v/%q, want 0/reported", result.CostUSD, result.CostSource)
		}
		result.Final.CostUSD = 99
		if result.CostUSD != 0 {
			t.Fatalf("drain retained alias to final cost: got %v", result.CostUSD)
		}
	})

	t.Run("override copies normalized measurement", func(t *testing.T) {
		finalRaw, err := json.Marshal(ServiceFinalData{
			Status:     "success",
			CostUSD:    0,
			CostSource: CostSourceConfigured,
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
		if payload.Outcome.CostUSD != 0 || payload.Outcome.CostSource != CostSourceConfigured {
			t.Fatalf("override cost = %v/%q, want 0/configured", payload.Outcome.CostUSD, payload.Outcome.CostSource)
		}
		if !bytes.Contains(overrideEvent.Data, []byte(`"cost_usd":0`)) ||
			!bytes.Contains(overrideEvent.Data, []byte(`"cost_source":"configured"`)) {
			t.Fatalf("known-zero override wire lost measurement: %s", overrideEvent.Data)
		}

		var legacy ServiceOverrideOutcome
		if err := json.Unmarshal([]byte(`{"status":"success","cost_usd":8}`), &legacy); err != nil {
			t.Fatalf("legacy override decode: %v", err)
		}
		if cost, source := legacy.costMeasurement(); cost != nil || source != CostSourceUnknown {
			t.Fatalf("legacy override promoted cost: %v/%q", cost, source)
		}
	})

	t.Run("session projection preserves typed source", func(t *testing.T) {
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
		publicRaw, err := json.Marshal(public)
		if err != nil {
			t.Fatalf("marshal public session projection: %v", err)
		}
		if !bytes.Contains(publicRaw, []byte(`"cost_usd":0`)) ||
			!bytes.Contains(publicRaw, []byte(`"cost_source":"reported"`)) {
			t.Fatalf("public session wire lost known zero: %s", publicRaw)
		}

		sessionCases := []struct {
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
		for _, tt := range sessionCases {
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
				if tt.cost != nil && decoded.CostUSD == tt.cost {
					t.Fatal("session projection retained caller-owned cost pointer")
				}
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
	})
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
