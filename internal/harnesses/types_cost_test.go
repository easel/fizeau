package harnesses

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFinalDataCostJSONRoundTrip(t *testing.T) {
	t.Run("unknown omits cost", func(t *testing.T) {
		encoded := marshalFinalDataForTest(t, FinalData{
			Status:          "success",
			FinalCostSource: CostSourceUnknown,
		})
		wire := decodeFinalDataWireForTest(t, encoded)
		if _, ok := wire["cost_usd"]; ok {
			t.Fatalf("cost_usd unexpectedly present in %s", encoded)
		}
		if got := string(wire["cost_source"]); got != `"unknown"` {
			t.Fatalf("cost_source = %s, want unknown", got)
		}

		var decoded FinalData
		unmarshalFinalDataForTest(t, encoded, &decoded)
		if decoded.FinalCostUSD != nil || decoded.CostUSD != 0 || decoded.FinalCostSource != CostSourceUnknown {
			t.Fatalf("decoded cost = (%v, %v, %q), want (nil, 0, unknown)", decoded.FinalCostUSD, decoded.CostUSD, decoded.FinalCostSource)
		}
	})

	t.Run("reported zero is present", func(t *testing.T) {
		zero := 0.0
		encoded := marshalFinalDataForTest(t, FinalData{
			FinalCostUSD:    &zero,
			FinalCostSource: CostSourceReported,
		})
		wire := decodeFinalDataWireForTest(t, encoded)
		if got, ok := wire["cost_usd"]; !ok || string(got) != "0" {
			t.Fatalf("cost_usd = %s (present %v), want present zero", got, ok)
		}

		var decoded FinalData
		unmarshalFinalDataForTest(t, encoded, &decoded)
		if decoded.FinalCostUSD == nil || *decoded.FinalCostUSD != 0 || decoded.CostUSD != 0 || decoded.FinalCostSource != CostSourceReported {
			t.Fatalf("decoded cost = (%v, %v, %q), want (0 pointer, 0, reported)", decoded.FinalCostUSD, decoded.CostUSD, decoded.FinalCostSource)
		}
	})

	t.Run("configured positive", func(t *testing.T) {
		cost := 1.25
		encoded := marshalFinalDataForTest(t, FinalData{
			FinalCostUSD:    &cost,
			FinalCostSource: CostSourceConfigured,
		})

		var decoded FinalData
		unmarshalFinalDataForTest(t, encoded, &decoded)
		if decoded.FinalCostUSD == nil || *decoded.FinalCostUSD != cost || decoded.CostUSD != cost || decoded.FinalCostSource != CostSourceConfigured {
			t.Fatalf("decoded cost = (%v, %v, %q), want (%v pointer, %v, configured)", decoded.FinalCostUSD, decoded.CostUSD, decoded.FinalCostSource, cost, cost)
		}
	})

	t.Run("authoritative pointer takes precedence and mirrors", func(t *testing.T) {
		cost := 2.5
		encoded := marshalFinalDataForTest(t, FinalData{
			FinalCostUSD:    &cost,
			FinalCostSource: CostSourceReported,
			CostUSD:         99,
		})
		wire := decodeFinalDataWireForTest(t, encoded)
		if got := string(wire["cost_usd"]); got != "2.5" {
			t.Fatalf("cost_usd = %s, want authoritative 2.5", got)
		}

		var decoded FinalData
		unmarshalFinalDataForTest(t, encoded, &decoded)
		if decoded.FinalCostUSD == nil || *decoded.FinalCostUSD != cost || decoded.CostUSD != cost {
			t.Fatalf("decoded cost = (%v, %v), want mirrored %v", decoded.FinalCostUSD, decoded.CostUSD, cost)
		}
	})

	t.Run("legacy decode and re-encode", func(t *testing.T) {
		var decoded FinalData
		unmarshalFinalDataForTest(t, []byte(`{"status":"success","cost_usd":3.75}`), &decoded)
		if decoded.FinalCostUSD != nil || decoded.CostUSD != 3.75 || decoded.FinalCostSource != CostSourceUnknown {
			t.Fatalf("legacy decoded cost = (%v, %v, %q), want (nil, 3.75, unknown)", decoded.FinalCostUSD, decoded.CostUSD, decoded.FinalCostSource)
		}

		wire := decodeFinalDataWireForTest(t, marshalFinalDataForTest(t, decoded))
		if got := string(wire["cost_usd"]); got != "3.75" {
			t.Fatalf("re-encoded cost_usd = %s, want 3.75", got)
		}
		if got := string(wire["cost_source"]); got != `"unknown"` {
			t.Fatalf("re-encoded cost_source = %s, want unknown", got)
		}
	})

	for _, source := range []string{`"invalid"`, `""`, `null`, `17`} {
		t.Run("invalid source "+source, func(t *testing.T) {
			payload := []byte(`{"cost_usd":0,"cost_source":` + source + `}`)
			var decoded FinalData
			unmarshalFinalDataForTest(t, payload, &decoded)
			if decoded.FinalCostSource != CostSourceUnknown {
				t.Fatalf("FinalCostSource = %q, want unknown", decoded.FinalCostSource)
			}
			if decoded.FinalCostUSD == nil || *decoded.FinalCostUSD != 0 {
				t.Fatalf("FinalCostUSD = %v, want present zero", decoded.FinalCostUSD)
			}
		})
	}

	t.Run("all fields round trip and Extra remains local", func(t *testing.T) {
		cost := 4.5
		inputTokens := 10
		outputTokens := 6
		fresh := true
		extra := map[string]string{"request_id": "req-1"}
		original := FinalData{
			Status:         "failed",
			Outcome:        SessionOutcomeFailed,
			Cause:          TerminalCauseCleanupFailed,
			Stage:          SessionStageCleanup,
			PrimaryOutcome: SessionOutcomeSuccess,
			PrimaryCause:   TerminalCauseCompleted,
			PrimaryStage:   SessionStageProvider,
			ExitCode:       7,
			Error:          "cleanup failed",
			FinalText:      "done",
			DurationMS:     1234,
			Usage: &FinalUsage{
				InputTokens:  &inputTokens,
				OutputTokens: &outputTokens,
				Source:       "result",
				Fresh:        &fresh,
				CapturedAt:   "2026-07-16T12:00:00Z",
			},
			Warnings: []FinalWarning{{
				Code:    "usage_conflict",
				Message: "sources differed",
				Sources: []UsageSourceEvidence{{
					Source: "result",
					Usage:  &UsageTokenCounts{InputTokens: &inputTokens},
				}},
			}},
			FinalCostUSD:    &cost,
			FinalCostSource: CostSourceConfigured,
			CostUSD:         cost,
			SessionLogPath:  "/tmp/session.jsonl",
			RoutingActual: &RoutingActual{
				Harness:            "synthetic",
				Provider:           "provider",
				ServerInstance:     "local",
				Model:              "model",
				FallbackChainFired: []string{"fallback"},
				FailureClass:       "cleanup",
				Power:              8,
			},
			Reasoning: &ReasoningActual{
				Harness:            "synthetic",
				RequestedReasoning: "high",
				ResolvedReasoning:  "medium",
				Source:             "configured",
				DiscoverySource:    "test",
				Reason:             "supported",
				Warning:            "downgraded",
				SupportedReasoning: []string{"low", "medium"},
			},
			Extra: extra,
		}

		encoded := marshalFinalDataForTest(t, original)
		wire := decodeFinalDataWireForTest(t, encoded)
		if _, ok := wire["Extra"]; ok {
			t.Fatalf("Extra unexpectedly serialized in %s", encoded)
		}
		if _, ok := wire["extra"]; ok {
			t.Fatalf("extra unexpectedly serialized in %s", encoded)
		}

		decoded := FinalData{Extra: extra}
		unmarshalFinalDataForTest(t, encoded, &decoded)
		if !reflect.DeepEqual(decoded, original) {
			t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", decoded, original)
		}
	})
}

func marshalFinalDataForTest(t *testing.T, data FinalData) []byte {
	t.Helper()
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return encoded
}

func unmarshalFinalDataForTest(t *testing.T, encoded []byte, data *FinalData) {
	t.Helper()
	if err := json.Unmarshal(encoded, data); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", encoded, err)
	}
}

func decodeFinalDataWireForTest(t *testing.T, encoded []byte) map[string]json.RawMessage {
	t.Helper()
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode wire %s: %v", encoded, err)
	}
	return wire
}
