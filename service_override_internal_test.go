package fizeau

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// coincidenceFakeService returns a *service backed by a fakeServiceConfig
// that exposes a single provider so ResolveRoute deterministically picks
// that one provider regardless of pin. It anchors the coincidental-agreement
// test's stripped-auto resolution.
func coincidenceFakeService(t *testing.T) *service {
	t.Helper()
	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-04-30T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  model-a:
    family: test
    status: active
    power: 5
    surfaces: {agent.openai: model-a}
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: "model-a"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	return publicRouteTraceService(sc)
}

// TestOverrideEventCoincidentalAgreement covers AC #3 in full: when the
// pin matches what auto-routing would have picked anyway, the override
// event still fires AND match_per_axis is true on every overridden axis.
// Synthesis is real — a fakeServiceConfig with a single provider means
// the stripped auto resolution lands on the same Harness/Provider/Model
// the user pinned.
func TestOverrideEventCoincidentalAgreement(t *testing.T) {
	svc := coincidenceFakeService(t)

	// Pin Provider only. Stripped auto resolution still picks "local"
	// because it is the sole configured provider — coincidental agreement.
	req := ServiceExecuteRequest{
		Harness:  "fiz",
		Provider: "local",
		Model:    "model-a",
	}
	octx := svc.buildOverrideContext(context.Background(), req)
	if octx == nil {
		t.Fatal("buildOverrideContext returned nil for pinned request")
	}
	if !equalAxisSets(octx.payload.AxesOverridden, []string{overrideAxisHarness, overrideAxisProvider, overrideAxisModel}) {
		t.Fatalf("axes_overridden: got %v, want all three", octx.payload.AxesOverridden)
	}
	// Auto decision must be the very same thing the user pinned.
	if octx.payload.AutoDecision.Provider != "local" {
		t.Fatalf("auto provider: got %q, want %q", octx.payload.AutoDecision.Provider, "local")
	}
	if octx.payload.AutoDecision.Harness != "fiz" {
		t.Fatalf("auto harness: got %q, want %q", octx.payload.AutoDecision.Harness, "fiz")
	}
	if octx.payload.AutoDecision.Model != "model-a" {
		t.Fatalf("auto model: got %q, want %q", octx.payload.AutoDecision.Model, "model-a")
	}
	// match_per_axis must be true everywhere — coincidental agreement.
	for _, axis := range octx.payload.AxesOverridden {
		if !octx.payload.MatchPerAxis[axis] {
			t.Fatalf("match_per_axis[%s] = false; want true (auto=%+v pin=%+v)",
				axis, octx.payload.AutoDecision, octx.payload.UserPin)
		}
	}
	// Event still fires: makeOverrideEvent must succeed. We construct a
	// minimal final event with non-zero Sequence so the override event
	// gets a real preceding sequence number.
	finalRaw, _ := json.Marshal(ServiceFinalData{Status: "success", DurationMS: 1})
	finalEv := ServiceEvent{
		Type:     harnesses.EventTypeFinal,
		Sequence: 5,
		Time:     time.Now().UTC(),
		Data:     finalRaw,
	}
	ev, _, ok := makeOverrideEvent(octx, "test-session", finalEv, nil)
	if !ok {
		t.Fatal("makeOverrideEvent returned ok=false on coincidental agreement")
	}
	if string(ev.Type) != ServiceEventTypeOverride {
		t.Fatalf("override event type: got %q, want %q", ev.Type, ServiceEventTypeOverride)
	}
	if ev.Sequence >= finalEv.Sequence {
		t.Fatalf("override Sequence=%d must precede final Sequence=%d", ev.Sequence, finalEv.Sequence)
	}
}

// TestRejectedOverrideOnUnknownProvider covers AC #6's "unknown provider"
// branch. With ServiceConfig present but the pinned provider name absent,
// resolveExecuteRoute must surface ErrUnknownProvider, which in turn must
// be classified as an explicit-pin error and produce a rejected_override
// event (no override event, no channel).
func TestRejectedOverrideOnUnknownProvider(t *testing.T) {
	sc := &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {Type: "test", BaseURL: "http://127.0.0.1:9999/v1", Model: "model-a"},
		},
		names:       []string{"local"},
		defaultName: "local",
	}
	svc := publicRouteTraceService(sc)

	ch, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Prompt:   "hi",
		Harness:  "fiz",
		Provider: "definitely-not-configured",
	})
	if err == nil {
		t.Fatal("expected typed pin error for unknown provider, got nil")
	}
	if ch != nil {
		t.Fatalf("expected nil channel for typed pin error, got %#v", ch)
	}
	var unknown *ErrUnknownProvider
	if !errors.As(err, &unknown) {
		t.Fatalf("errors.As ErrUnknownProvider: got %T %v", err, err)
	}
	if unknown.Provider != "definitely-not-configured" {
		t.Fatalf("ErrUnknownProvider.Provider: got %q", unknown.Provider)
	}
	rejected, ok := AsRejectedOverride(err)
	if !ok {
		t.Fatalf("AsRejectedOverride: expected wrapper carrying rejected_override payload, got %T %v", err, err)
	}
	if !equalAxisSets(rejected.AxesOverridden, []string{overrideAxisHarness, overrideAxisProvider}) {
		t.Fatalf("rejected.axes_overridden: got %v", rejected.AxesOverridden)
	}
	if rejected.UserPin.Provider != "definitely-not-configured" {
		t.Fatalf("rejected.user_pin.provider: got %q", rejected.UserPin.Provider)
	}
	if rejected.Outcome != nil {
		t.Fatalf("rejected_override must not carry outcome: got %+v", rejected.Outcome)
	}
}

// TestIsExplicitPinError_ClassifiesUnknownProvider locks in the contract
// that the unknown-provider typed error participates in the pin-error
// classification used by Execute to decide rejected_override emission.
func TestIsExplicitPinError_ClassifiesUnknownProvider(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unknown provider direct", &ErrUnknownProvider{Provider: "x"}, true},
		{"unknown provider wrapped", errors.Join(errors.New("ctx"), &ErrUnknownProvider{Provider: "x"}), true},
		{"orphan model", &ErrHarnessModelIncompatible{Harness: "h", Model: "m"}, true},
		{"policy requirement conflict", &ErrPolicyRequirementUnsatisfied{Policy: "smart", AttemptedPin: "Harness=fiz"}, true},
		{"plain error", errors.New("nope"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExplicitPinError(tc.err); got != tc.want {
				t.Fatalf("isExplicitPinError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func equalAxisSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gs := make(map[string]bool, len(got))
	for _, g := range got {
		gs[g] = true
	}
	for _, w := range want {
		if !gs[w] {
			return false
		}
	}
	return true
}
