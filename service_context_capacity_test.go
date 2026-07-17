package fizeau

import (
	"context"
	"reflect"
	"testing"
	"time"

	agentcore "github.com/easel/fizeau/internal/core"
	"github.com/easel/fizeau/internal/harnesses"
	serviceimpl "github.com/easel/fizeau/internal/serviceimpl"
)

type contextCapacityRouteProvider struct {
	calls int
}

func (p *contextCapacityRouteProvider) Chat(context.Context, []agentcore.Message, []agentcore.ToolDef, agentcore.Options) (agentcore.Response, error) {
	p.calls++
	return agentcore.Response{Content: "unexpected dispatch"}, nil
}

type contextCapacityRouteFixture struct {
	final        harnesses.FinalData
	origin       serviceimpl.TerminalOrigin
	capacity     harnesses.ContextCapacityData
	coreEvents   []agentcore.Event
	resolveCalls map[string]int
	providers    map[string]*contextCapacityRouteProvider
}

func runContextCapacityRouteFixture(t *testing.T) contextCapacityRouteFixture {
	t.Helper()
	rootDecision := RouteDecision{
		Harness:        "fiz",
		Provider:       "alpha",
		Endpoint:       "west",
		ServerInstance: "server-alpha",
		Model:          "model-alpha",
		Reason:         "selected",
		Candidates: []RouteCandidate{
			{Harness: "fiz", Provider: "alpha", Endpoint: "west", ServerInstance: "server-alpha", Model: "model-alpha", Eligible: true},
			{Harness: "fiz", Provider: "beta", Endpoint: "east", ServerInstance: "server-beta", Model: "model-beta", Eligible: true},
		},
	}
	publicDecision := serviceRoutingDecisionDataFromDecision(ServiceExecuteRequest{}, rootDecision, "capacity-route")
	if publicDecision.Provider != "alpha" || publicDecision.Endpoint != "west" || len(publicDecision.Candidates) != 2 {
		t.Fatalf("public routing decision lost selected route or candidates: %#v", publicDecision)
	}
	if publicDecision.Candidates[0].Provider != "alpha" || publicDecision.Candidates[0].Endpoint != "west" ||
		publicDecision.Candidates[1].Provider != "beta" || publicDecision.Candidates[1].Endpoint != "east" {
		t.Fatalf("public candidate order/identity changed: %#v", publicDecision.Candidates)
	}

	fixture := contextCapacityRouteFixture{
		resolveCalls: map[string]int{},
		providers: map[string]*contextCapacityRouteProvider{
			"alpha": {},
			"beta":  {},
		},
	}
	serviceimpl.RunNative(context.Background(), serviceimpl.NativeRequest{
		Prompt:                "x",
		Permissions:           "unrestricted",
		MaxTokens:             10,
		SelectedContextWindow: 2,
		Decision: serviceimpl.NativeDecision{
			Harness:               rootDecision.Harness,
			Provider:              rootDecision.Provider,
			ServerInstance:        rootDecision.ServerInstance,
			Model:                 rootDecision.Model,
			SelectedContextWindow: 2,
			SelectedContextSource: "fixture",
			Candidates: []serviceimpl.NativeRouteCandidate{
				{Provider: "alpha", Endpoint: "west", ServerInstance: "server-alpha", Model: "model-alpha", Eligible: true},
				{Provider: "beta", Endpoint: "east", ServerInstance: "server-beta", Model: "model-beta", Eligible: true},
			},
		},
		Started: time.Now(),
	}, serviceimpl.NativeCallbacks{
		ResolveProvider: func(req serviceimpl.NativeProviderRequest) serviceimpl.NativeProviderResolution {
			fixture.resolveCalls[req.Provider]++
			providerName := req.Provider
			if providerName == "alpha@west" {
				providerName = "alpha"
			}
			if providerName == "beta@east" {
				providerName = "beta"
			}
			return serviceimpl.NativeProviderResolution{
				Provider: fixture.providers[providerName],
				Name:     providerName,
				Model:    req.Model,
			}
		},
		Compactor: func(string) agentcore.Compactor {
			return func(_ context.Context, input agentcore.CompactionInput, _ agentcore.Provider) ([]agentcore.Message, *agentcore.CompactionResult, error) {
				return input.History, nil, nil
			}
		},
		ObserveAgentEvent: func(event agentcore.Event) {
			fixture.coreEvents = append(fixture.coreEvents, event)
		},
		EmitEvent: func(eventType harnesses.EventType, payload any) {
			if eventType != harnesses.EventTypeContextCapacity {
				return
			}
			mapped, ok := payload.(harnesses.ContextCapacityData)
			if !ok {
				t.Fatalf("context-capacity payload type = %T", payload)
			}
			fixture.capacity = mapped
		},
		Finalize: func(final harnesses.FinalData, origin serviceimpl.TerminalOrigin) {
			fixture.origin = origin
			fixture.final = serviceimpl.ClassifyTerminalFinal(final, origin, nil)
		},
	})
	return fixture
}

func TestServiceContextCapacityRetainsSingleRouteTrace(t *testing.T) {
	fixture := runContextCapacityRouteFixture(t)
	if fixture.origin != serviceimpl.TerminalOriginContextCapacity {
		t.Fatalf("terminal origin = %v, want context capacity", fixture.origin)
	}
	if fixture.capacity.Action != agentcore.ContextCapacityActionRejected || fixture.capacity.CallKind != agentcore.ContextCapacityCallMain {
		t.Fatalf("capacity event = %#v, want rejected main call", fixture.capacity)
	}
	for _, event := range fixture.coreEvents {
		if event.Type == agentcore.EventLLMRequest || event.Type == agentcore.EventLLMResponse {
			t.Fatalf("pre-dispatch rejection emitted %s", event.Type)
		}
	}
	if fixture.providers["alpha"].calls != 0 || fixture.providers["beta"].calls != 0 {
		t.Fatalf("provider calls alpha/beta = %d/%d, want 0/0", fixture.providers["alpha"].calls, fixture.providers["beta"].calls)
	}
	if fixture.resolveCalls["beta"] != 0 || fixture.resolveCalls["beta@east"] != 0 {
		t.Fatalf("unselected beta was resolved: %#v", fixture.resolveCalls)
	}
	actual := fixture.final.RoutingActual
	if actual == nil || actual.Provider != "alpha" || actual.Model != "model-alpha" || actual.ServerInstance != "server-alpha" {
		t.Fatalf("final routing actual = %#v, want selected alpha route", actual)
	}
	if len(actual.FallbackChainFired) != 0 {
		t.Fatalf("pre-dispatch rejection fabricated attempted providers: %#v", actual.FallbackChainFired)
	}
	if fixture.final.ContextCapacity == nil || *fixture.final.ContextCapacity != fixture.capacity {
		t.Fatalf("final capacity = %#v, event = %#v", fixture.final.ContextCapacity, fixture.capacity)
	}
}

func TestServiceContextCapacityAcceptedExecuteProjection(t *testing.T) {
	t.Setenv("FIZEAU_CACHE_DIR", t.TempDir())
	t.Cleanup(replaceRoutingCatalogForTest(t, explicitNativeContextCatalog(t)))
	svc := newTestService(t, ServiceOptions{ServiceConfig: &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"alpha": {
				Type: "lmstudio", BaseURL: "http://127.0.0.1:1/v1",
				Model: "known-context-model", ContextWindow: 2,
			},
		},
		names: []string{"alpha"}, defaultName: "alpha",
	}})
	svc.hub = serviceimpl.NewSessionHub()

	events, err := svc.Execute(context.Background(), ServiceExecuteRequest{
		Harness: "fiz", Provider: "alpha", Model: "known-context-model",
		Prompt: "<summary>x</summary>", Permissions: "unrestricted",
	})
	if err != nil || events == nil {
		t.Fatalf("accepted Execute = (%v, %v), want event stream and nil error", events, err)
	}
	var (
		capacity      *ServiceContextCapacityData
		final         *ServiceFinalData
		capacityIndex = -1
		finalIndex    = -1
		index         int
	)
	for event := range events {
		decoded, decodeErr := DecodeServiceEvent(event)
		if decodeErr != nil {
			t.Fatalf("DecodeServiceEvent(%q): %v", event.Type, decodeErr)
		}
		if decoded.ContextCapacity != nil {
			capacityIndex = index
			capacity = decoded.ContextCapacity
		}
		if decoded.Final != nil {
			finalIndex = index
			final = decoded.Final
		}
		index++
	}
	if capacity == nil || final == nil || final.ContextCapacity == nil {
		t.Fatalf("accepted Execute capacity/final = %#v/%#v", capacity, final)
	}
	if capacity.Action != ServiceContextCapacityRejected || capacity.CallKind != ServiceContextCapacityMain {
		t.Fatalf("accepted Execute capacity = %#v", capacity)
	}
	if final.Outcome != SessionOutcomeFailed || final.Cause != TerminalCauseContextCapacityExceeded || final.Stage != SessionStageToolLoop {
		t.Fatalf("accepted Execute terminal tuple = %q/%q/%q", final.Outcome, final.Cause, final.Stage)
	}
	if *final.ContextCapacity != *capacity || capacityIndex >= finalIndex {
		t.Fatalf("accepted Execute event/final mismatch or order: capacity=%#v final=%#v indexes=%d/%d", capacity, final.ContextCapacity, capacityIndex, finalIndex)
	}
	if records := svc.activeRouteAttempts(time.Now(), time.Minute); len(records) != 0 {
		t.Fatalf("accepted capacity rejection poisoned route health: %+v", records)
	}
}

func TestServiceContextCapacityPublicMappingEveryField(t *testing.T) {
	neutral := harnesses.ContextCapacityData{
		Action: "a", CallKind: "b", TurnIndex: 3, AttemptIndex: 4,
		ContextWindow: 5, EffectiveContextWindow: 6, EstimatedInputTokens: 7,
		RequestedMaxTokens: 8, EffectiveMaxTokens: 9, AvailableOutputTokens: 10,
	}
	got := serviceContextCapacityDataFromHarness(neutral)
	neutralType := reflect.TypeOf(neutral)
	publicType := reflect.TypeOf(got)
	if neutralType.NumField() != 10 || publicType.NumField() != 10 {
		t.Fatalf("neutral/public context-capacity field counts = %d/%d, want 10/10", neutralType.NumField(), publicType.NumField())
	}
	for index := 0; index < neutralType.NumField(); index++ {
		if neutralType.Field(index).Name != publicType.Field(index).Name ||
			neutralType.Field(index).Tag.Get("json") != publicType.Field(index).Tag.Get("json") {
			t.Fatalf("neutral/public field %d drift: %#v vs %#v", index, neutralType.Field(index), publicType.Field(index))
		}
	}
	want := ServiceContextCapacityData{
		Action: ServiceContextCapacityAction("a"), CallKind: ServiceContextCapacityCallKind("b"),
		TurnIndex: 3, AttemptIndex: 4, ContextWindow: 5, EffectiveContextWindow: 6,
		EstimatedInputTokens: 7, RequestedMaxTokens: 8, EffectiveMaxTokens: 9,
		AvailableOutputTokens: 10,
	}
	if got != want {
		t.Fatalf("public context-capacity mapping = %#v, want %#v", got, want)
	}
}
