package routehealth

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestRecordAttemptPersistsAfterMemoryUpdate(t *testing.T) {
	store := NewStore()
	persistErr := errors.New("persist route health")
	recordedAt := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	var trace []string
	err := RecordAttempt(AttemptTransaction{
		Store:         store,
		RecordOptions: RecordOptions{ExactSuccessClear: true},
		Persist: func() error {
			trace = append(trace, "persist")
			records := store.ActiveAttempts(recordedAt, time.Minute)
			if len(records) != 1 || records[0].Key.Provider != "vendor" {
				t.Fatalf("records visible to persistence = %+v, want in-memory vendor attempt", records)
			}
			return persistErr
		},
	}, Attempt{Harness: "fiz", Provider: " vendor ", Status: "failed", Timestamp: recordedAt})
	if err != persistErr {
		t.Fatalf("RecordAttempt error = %v, want original persistence error %v", err, persistErr)
	}
	if !reflect.DeepEqual(trace, []string{"persist"}) {
		t.Fatalf("callback trace = %v, want [persist]", trace)
	}

	for _, tc := range []struct {
		name      string
		attempt   Attempt
		wantError string
	}{
		{name: "missing identity", attempt: Attempt{Status: "failed"}, wantError: "route attempt requires harness or provider"},
		{name: "missing status", attempt: Attempt{Provider: "vendor"}, wantError: "route attempt status is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalidStore := NewStore()
			persistCalls := 0
			err := RecordAttempt(AttemptTransaction{
				Store: invalidStore,
				Persist: func() error {
					persistCalls++
					return errors.New("must not persist invalid attempt")
				},
			}, tc.attempt)
			if err == nil || err.Error() != tc.wantError {
				t.Fatalf("RecordAttempt error = %v, want original validation error %q", err, tc.wantError)
			}
			if persistCalls != 0 {
				t.Fatalf("persistence calls = %d, want zero after validation failure", persistCalls)
			}
			if records := invalidStore.ActiveAttempts(time.Now().UTC(), time.Minute); len(records) != 0 {
				t.Fatalf("invalid attempt mutated store: %+v", records)
			}
		})
	}
}

func TestObserveFinalAttemptAppliesDispatchAfterPersistenceError(t *testing.T) {
	store := NewStore()
	probeStore := NewProbeStore()
	firstErr := errors.New("first route snapshot failure")
	secondErr := errors.New("later probe snapshot failure")
	now := time.Date(2026, 7, 15, 1, 30, 0, 0, time.UTC)
	var trace []string
	var dispatchedErr error
	feedback := DispatchFeedback{
		IsReachabilityFailure: func(error) bool {
			trace = append(trace, "classify")
			return true
		},
		LookupProvider: func(provider string) (DispatchProvider, bool) {
			trace = append(trace, "lookup:"+provider)
			return DispatchProvider{
				BaseURL: "https://primary.invalid/v1",
				Endpoints: []DispatchEndpoint{{
					Name:    "primary",
					BaseURL: "https://primary.invalid/v1",
				}},
				RecordCatalog: func(baseURL string, err error) {
					if err != dispatchedErr {
						t.Fatalf("catalog error = %v, want exact dispatch error %v", err, dispatchedErr)
					}
					trace = append(trace, "catalog:"+baseURL)
				},
			}, true
		},
		RecordProbe: func(provider, endpoint string, success bool, at time.Time) {
			trace = append(trace, "probe:"+provider+"@"+endpoint)
			probeStore.RecordProbe(provider, endpoint, success, at)
		},
		PersistProbe: func() error {
			trace = append(trace, "persist-probe")
			return secondErr
		},
		Now: func() time.Time {
			trace = append(trace, "now")
			return now
		},
	}
	err := ObserveFinalAttempt(AttemptTransaction{
		Store:         store,
		RecordOptions: RecordOptions{ExactSuccessClear: true},
		Persist: func() error {
			trace = append(trace, "persist")
			if records := store.ActiveAttempts(time.Now().UTC(), time.Minute); len(records) != 1 {
				t.Fatalf("persistence observed records = %+v, want one", records)
			}
			return firstErr
		},
		Dispatch: func(provider, endpoint string, err error) {
			trace = append(trace, "dispatch")
			dispatchedErr = err
			feedback.Record(provider, endpoint, err)
		},
	}, failedTransactionFinal("transport", "dial tcp 192.0.2.1:443: i/o timeout"), FinalEvidenceTypedOnly)
	if err != firstErr {
		t.Fatalf("ObserveFinalAttempt error = %v, want original persistence error %v (not %v)", err, firstErr, secondErr)
	}
	wantTrace := []string{
		"persist",
		"dispatch",
		"classify",
		"now",
		"lookup:vendor",
		"catalog:https://primary.invalid/v1",
		"probe:vendor@primary",
		"persist-probe",
	}
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("callback trace = %v, want %v", trace, wantTrace)
	}
	probe, ok := probeStore.LastProbe("vendor", "primary")
	if !ok || probe.LastProbeSuccess || !probe.LastProbeAt.Equal(now) {
		t.Fatalf("exact probe = %+v, ok=%v, want failed probe at %v", probe, ok, now)
	}
}

func TestObserveFinalAttemptRejectsInvalidEvidenceBeforeCallbacks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		final harnesses.FinalData
		mode  FinalEvidenceMode
	}{
		{name: "missing routing actual", final: harnesses.FinalData{Status: "failed", Error: "dial tcp: timeout"}, mode: FinalEvidenceTypedOnly},
		{name: "unknown evidence mode", final: failedTransactionFinal("transport", "dial tcp: timeout"), mode: FinalEvidenceMode(99)},
		{name: "semantic class", final: failedTransactionFinal("unknown", "dial tcp: timeout"), mode: FinalEvidenceTypedOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore()
			var trace []string
			err := ObserveFinalAttempt(AttemptTransaction{
				Store: store,
				Persist: func() error {
					trace = append(trace, "persist")
					return nil
				},
				Dispatch: func(string, string, error) {
					trace = append(trace, "dispatch")
				},
			}, tc.final, tc.mode)
			if err != nil {
				t.Fatalf("ObserveFinalAttempt error = %v, want nil rejection", err)
			}
			if len(trace) != 0 {
				t.Fatalf("invalid evidence callback trace = %v, want none", trace)
			}
			if records := store.ActiveAttempts(time.Now().UTC(), time.Minute); len(records) != 0 {
				t.Fatalf("invalid evidence mutated store: %+v", records)
			}
		})
	}
}

func TestDispatchFailureFromAttempt(t *testing.T) {
	tests := []struct {
		name         string
		attempt      Attempt
		wantProvider string
		wantEndpoint string
		wantError    string
	}{
		{
			name:         "qualified provider is split",
			attempt:      Attempt{Provider: " vendor@embedded ", Status: " FAILED ", Reason: " transport ", Error: " dial tcp: timeout "},
			wantProvider: "vendor",
			wantEndpoint: "embedded",
			wantError:    "dial tcp: timeout",
		},
		{
			name:         "explicit endpoint wins",
			attempt:      Attempt{Provider: "vendor@embedded", Endpoint: " explicit ", Status: "failed", Reason: "protocol", Error: "HTTP 502"},
			wantProvider: "vendor",
			wantEndpoint: "explicit",
			wantError:    "HTTP 502",
		},
		{
			name:         "availability class",
			attempt:      Attempt{Provider: "vendor", Status: "failed", Reason: "availability", Error: "not available"},
			wantProvider: "vendor",
			wantError:    "not available",
		},
		{name: "success", attempt: Attempt{Provider: "vendor", Status: "success", Reason: "transport", Error: "dial tcp: timeout"}},
		{name: "credential is not dispatch failure", attempt: Attempt{Provider: "vendor", Status: "failed", Reason: "credential_invalid", Error: "connection reset while refreshing credential"}},
		{name: "quota is not dispatch failure", attempt: Attempt{Provider: "vendor", Status: "failed", Reason: "quota_exhausted", Error: "HTTP 503 while reading quota"}},
		{name: "missing diagnostic", attempt: Attempt{Provider: "vendor", Status: "failed", Reason: "transport"}},
		{name: "missing provider", attempt: Attempt{Status: "failed", Reason: "transport", Error: "dial tcp: timeout"}},
		{name: "missing status", attempt: Attempt{Provider: "vendor", Reason: "transport", Error: "dial tcp: timeout"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, endpoint, err := DispatchFailureFromAttempt(tc.attempt)
			if provider != tc.wantProvider || endpoint != tc.wantEndpoint {
				t.Fatalf("target = %q@%q, want %q@%q", provider, endpoint, tc.wantProvider, tc.wantEndpoint)
			}
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantError {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestDispatchProviderBaseURLsExactFallbackAndDeduplicate(t *testing.T) {
	provider := DispatchProvider{
		BaseURL: " https://primary.invalid/v1 ",
		Endpoints: []DispatchEndpoint{
			{Name: "primary", BaseURL: "https://primary.invalid/v1"},
			{Name: "primary", BaseURL: " https://a.invalid/v1 "},
			{Name: "primary", BaseURL: "https://a.invalid/v1"},
			{Name: "sibling", BaseURL: "https://b.invalid/v1"},
			{Name: "blank", BaseURL: "  "},
		},
	}
	tests := []struct {
		name     string
		endpoint string
		provider DispatchProvider
		want     []string
	}{
		{name: "empty covers all in stable order", provider: provider, want: []string{"https://primary.invalid/v1", "https://a.invalid/v1", "https://b.invalid/v1"}},
		{name: "exact includes every matching URL", endpoint: "primary", provider: provider, want: []string{"https://primary.invalid/v1", "https://a.invalid/v1"}},
		{name: "exact sibling", endpoint: "sibling", provider: provider, want: []string{"https://b.invalid/v1"}},
		{name: "blank exact falls back primary", endpoint: "blank", provider: provider, want: []string{"https://primary.invalid/v1"}},
		{name: "unknown falls back primary", endpoint: "unknown", provider: provider, want: []string{"https://primary.invalid/v1"}},
		{name: "endpoint names remain exact", endpoint: " primary ", provider: provider, want: []string{"https://primary.invalid/v1"}},
		{name: "blank primary can return empty", endpoint: "unknown", provider: DispatchProvider{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DispatchProviderBaseURLs(tc.provider, tc.endpoint); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("base URLs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDispatchFeedbackRecordsExactProbe(t *testing.T) {
	t.Run("exact endpoint preserves sibling and callback order", func(t *testing.T) {
		probeStore := NewProbeStore()
		now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
		siblingAt := now.Add(-time.Minute)
		probeStore.RecordProbe("vendor", "sibling", true, siblingAt)
		var trace []string
		dispatchErr := errors.New("dial tcp: timeout")
		feedback := DispatchFeedback{
			IsReachabilityFailure: func(error) bool {
				trace = append(trace, "classify")
				return true
			},
			LookupProvider: func(provider string) (DispatchProvider, bool) {
				trace = append(trace, "lookup:"+provider)
				return DispatchProvider{
					BaseURL: "https://fallback.invalid/v1",
					Endpoints: []DispatchEndpoint{
						{Name: "primary", BaseURL: "https://primary.invalid/v1"},
						{Name: "sibling", BaseURL: "https://sibling.invalid/v1"},
					},
					RecordCatalog: func(baseURL string, err error) {
						if err != dispatchErr {
							t.Fatalf("catalog error = %v, want exact dispatch error %v", err, dispatchErr)
						}
						trace = append(trace, "catalog:"+baseURL)
					},
				}, true
			},
			RecordProbe: func(provider, endpoint string, success bool, at time.Time) {
				trace = append(trace, "probe:"+provider+"@"+endpoint)
				probeStore.RecordProbe(provider, endpoint, success, at)
			},
			PersistProbe: func() error {
				trace = append(trace, "persist")
				return errors.New("ignored probe persistence failure")
			},
			Now: func() time.Time {
				trace = append(trace, "now")
				return now
			},
		}
		feedback.Record(" vendor@embedded ", " primary ", dispatchErr)
		wantTrace := []string{
			"classify",
			"now",
			"lookup:vendor",
			"catalog:https://primary.invalid/v1",
			"probe:vendor@primary",
			"persist",
		}
		if !reflect.DeepEqual(trace, wantTrace) {
			t.Fatalf("callback trace = %v, want %v", trace, wantTrace)
		}
		primary, ok := probeStore.LastProbe("vendor", "primary")
		if !ok || primary.LastProbeSuccess || !primary.LastProbeAt.Equal(now) {
			t.Fatalf("primary probe = %+v, ok=%v, want exact failure at %v", primary, ok, now)
		}
		sibling, ok := probeStore.LastProbe("vendor", "sibling")
		if !ok || !sibling.LastProbeSuccess || !sibling.LastProbeAt.Equal(siblingAt) {
			t.Fatalf("sibling probe changed = %+v, ok=%v", sibling, ok)
		}
	})

	t.Run("diagnostic classifier rejects without side effects", func(t *testing.T) {
		var trace []string
		feedback := DispatchFeedback{
			IsReachabilityFailure: func(error) bool {
				trace = append(trace, "classify")
				return false
			},
			LookupProvider: func(string) (DispatchProvider, bool) {
				trace = append(trace, "lookup")
				return DispatchProvider{}, true
			},
			RecordProbe: func(string, string, bool, time.Time) { trace = append(trace, "probe") },
			PersistProbe: func() error {
				trace = append(trace, "persist")
				return nil
			},
			Now: func() time.Time { trace = append(trace, "now"); return time.Now() },
		}
		feedback.Record("vendor", "primary", errors.New("HTTP 401 invalid api key"))
		if !reflect.DeepEqual(trace, []string{"classify"}) {
			t.Fatalf("callback trace = %v, want classifier only", trace)
		}
	})

	for _, class := range []string{"credential_invalid", "quota_exhausted", "capability"} {
		t.Run(class+" class blocks catalog and probe", func(t *testing.T) {
			store := NewStore()
			var persistCalls, lookupCalls, catalogCalls, probeCalls int
			feedback := DispatchFeedback{
				IsReachabilityFailure: func(error) bool { return true },
				LookupProvider: func(string) (DispatchProvider, bool) {
					lookupCalls++
					return DispatchProvider{RecordCatalog: func(string, error) { catalogCalls++ }}, true
				},
				RecordProbe: func(string, string, bool, time.Time) { probeCalls++ },
			}
			err := ObserveFinalAttempt(AttemptTransaction{
				Store: store,
				Persist: func() error {
					persistCalls++
					return nil
				},
				Dispatch: feedback.Record,
			}, failedTransactionFinal(class, "connection reset with HTTP 503 evidence"), FinalEvidenceTypedOnly)
			if err != nil {
				t.Fatalf("ObserveFinalAttempt: %v", err)
			}
			if persistCalls != 1 {
				t.Fatalf("persist calls = %d, want route-attempt persistence", persistCalls)
			}
			if lookupCalls != 0 || catalogCalls != 0 || probeCalls != 0 {
				t.Fatalf("dispatch side effects = lookup:%d catalog:%d probe:%d, want zero", lookupCalls, catalogCalls, probeCalls)
			}
			records := store.ActiveAttempts(time.Now().UTC(), time.Minute)
			if len(records) != 1 || records[0].Reason != class {
				t.Fatalf("route-attempt feedback = %+v, want one %s record", records, class)
			}
		})
	}
}

func failedTransactionFinal(class, diagnostic string) harnesses.FinalData {
	return harnesses.FinalData{
		Status: "failed",
		Error:  strings.TrimSpace(diagnostic),
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "fiz",
			Provider:       "vendor@primary",
			Model:          "model-a",
			ServerInstance: "server-a",
			FailureClass:   class,
		},
	}
}
