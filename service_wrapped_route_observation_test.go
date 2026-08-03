package fizeau

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/discoverycache"
	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
)

func TestWrappedHarnessTypedFailureRecordsResolvedRoute(t *testing.T) {
	requirePOSIXWrappedHarnessTest(t)
	binDir := t.TempDir()
	writeWrappedHarnessTestScript(t, binDir, "claude", `#!/bin/sh
printf '%s\n' 'Failed to authenticate: OAuth session expired and could not be refreshed' >&2
exit 1
`)
	t.Setenv("PATH", binDir)
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-claude-quota.json"))

	svc := newProductionWrappedObservationService(t, "")
	final, _ := executeWrappedObservationRequest(t, svc, ServiceExecuteRequest{
		Prompt:        "exercise typed route failure observation",
		Harness:       "claude",
		Provider:      "vendor@primary",
		Model:         "claude-sonnet-4-6",
		Permissions:   "safe",
		SessionLogDir: t.TempDir(),
	})
	if final.Status != "failed" || final.RoutingActual == nil {
		t.Fatalf("final = %+v, want failed final with routing evidence", final)
	}
	if final.RoutingActual.FailureClass != "credential_invalid" {
		t.Fatalf("failure class = %q, want credential_invalid", final.RoutingActual.FailureClass)
	}
	if !strings.Contains(final.Error, "re-authenticate Claude Code") {
		t.Fatalf("final error = %q, want operator re-auth remediation", final.Error)
	}
	if final.RoutingActual.ServerInstance != "server-a" {
		t.Fatalf("server instance = %q, want server-a", final.RoutingActual.ServerInstance)
	}

	records := svc.activeRouteAttempts(time.Now().UTC(), time.Minute)
	if len(records) != 1 {
		t.Fatalf("active route attempts = %+v, want exactly one", records)
	}
	want := routehealth.Key{
		Harness:        final.RoutingActual.Harness,
		Provider:       "vendor",
		Endpoint:       "primary",
		ServerInstance: final.RoutingActual.ServerInstance,
		Model:          final.RoutingActual.Model,
	}
	if records[0].Key != want {
		t.Fatalf("recorded route = %+v, want exact delivered route %+v", records[0].Key, want)
	}
	if records[0].Reason != "credential_invalid" || records[0].Status != "failed" {
		t.Fatalf("recorded attempt = %+v, want failed credential_invalid", records[0])
	}
}

func TestFailedWrappedRouteIsExcludedFromNextUnpinnedDecision(t *testing.T) {
	requirePOSIXWrappedHarnessTest(t)
	binDir := t.TempDir()
	claudePath := writeWrappedHarnessTestScript(t, binDir, "claude", `#!/bin/sh
printf '%s\n' 'Failed to authenticate' 'Could not refresh auth token' >&2
exit 1
`)
	codexPath := writeWrappedHarnessTestScript(t, binDir, "codex", `#!/bin/sh
printf '%s\n' '{"type":"output","item":{"type":"agent_message","text":"sibling route completed"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}'
`)
	t.Setenv("PATH", binDir)
	t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "1")
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-claude-quota.json"))
	t.Setenv("FIZEAU_CODEX_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-codex-quota.json"))

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-14T00:00:00Z
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
  smart:
    min_power: 1
    max_power: 10
    allow_local: false
models:
  codex-route:
    family: gpt
    status: active
    power: 8
    cost_input_per_m: 5
    cost_output_per_m: 5
    surfaces:
      codex: codex-route
  claude-route:
    family: claude-sonnet
    status: active
    power: 9
    cost_input_per_m: 5
    cost_output_per_m: 5
    surfaces:
      claude-code: claude-route
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))
	stubSubprocessHarnessModelIDs(t, map[string][]string{
		"codex":      {"codex-route"},
		"claude":     {"claude-route"},
		"claude-tui": {"claude-route"},
	})

	public, err := New(ServiceOptions{
		ServiceConfig:       &fakeServiceConfig{healthCooldown: time.Minute, workDir: t.TempDir()},
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := public.(*service)
	svc.registry.LookPath = func(file string) (string, error) {
		switch file {
		case "codex":
			return codexPath, nil
		case "claude":
			return claudePath, nil
		default:
			return "", os.ErrNotExist
		}
	}

	var (
		dispatchMu sync.Mutex
		dispatched []string
	)
	svc.subprocessDispatchObserver = func(runner harnesses.Harness) {
		dispatchMu.Lock()
		dispatched = append(dispatched, runner.Info().Name)
		dispatchMu.Unlock()
	}

	first, firstEvents := executeWrappedObservationRequest(t, svc, ServiceExecuteRequest{
		Prompt:        "first independent request",
		Policy:        "smart",
		Permissions:   "safe",
		SessionLogDir: t.TempDir(),
	})
	if first.Status != "failed" || first.RoutingActual == nil || first.RoutingActual.Harness != "claude" {
		t.Fatalf("first final = %+v, want failed Claude route", first)
	}
	if got := routingDecisionHarness(t, firstEvents); got != "claude" {
		t.Fatalf("first routing decision = %q, want claude", got)
	}
	firstDecision := wrappedRoutingDecision(t, firstEvents)
	firstClaude := wrappedRoutingCandidate(t, firstDecision, "claude")
	firstCodex := wrappedRoutingCandidate(t, firstDecision, "codex")
	if !firstClaude.Eligible || !firstCodex.Eligible {
		t.Fatalf("first candidates should both be eligible: claude=%+v codex=%+v", firstClaude, firstCodex)
	}
	dispatchMu.Lock()
	firstDispatches := append([]string(nil), dispatched...)
	dispatchMu.Unlock()
	if len(firstDispatches) != 1 || firstDispatches[0] != "claude" {
		t.Fatalf("first Execute dispatches = %v, want one Claude invocation and no same-request retry", firstDispatches)
	}

	second, secondEvents := executeWrappedObservationRequest(t, svc, ServiceExecuteRequest{
		Prompt:        "second independent request",
		Policy:        "smart",
		Permissions:   "safe",
		SessionLogDir: t.TempDir(),
	})
	if second.Status != "success" || second.RoutingActual == nil || second.RoutingActual.Harness != "codex" {
		t.Fatalf("second final = %+v, want successful Codex sibling", second)
	}
	if got := routingDecisionHarness(t, secondEvents); got != "codex" {
		t.Fatalf("second routing decision = %q, want codex sibling", got)
	}
	secondDecision := wrappedRoutingDecision(t, secondEvents)
	secondClaude := wrappedRoutingCandidate(t, secondDecision, "claude")
	secondCodex := wrappedRoutingCandidate(t, secondDecision, "codex")
	if !secondClaude.Eligible || !secondCodex.Eligible {
		t.Fatalf("exact cooldown must stay soft: claude=%+v codex=%+v", secondClaude, secondCodex)
	}
	if secondClaude.Score >= firstClaude.Score {
		t.Fatalf("failed Claude score was not demoted: first=%v second=%v", firstClaude.Score, secondClaude.Score)
	}
	if secondCodex.Score != firstCodex.Score {
		t.Fatalf("sibling Codex score changed: first=%v second=%v", firstCodex.Score, secondCodex.Score)
	}
	dispatchMu.Lock()
	allDispatches := append([]string(nil), dispatched...)
	dispatchMu.Unlock()
	if len(allDispatches) != 2 || allDispatches[0] != "claude" || allDispatches[1] != "codex" {
		t.Fatalf("independent Execute dispatches = %v, want [claude codex]", allDispatches)
	}
}

func TestSemanticHarnessFailureDoesNotPoisonRouteHealth(t *testing.T) {
	requirePOSIXWrappedHarnessTest(t)
	binDir := t.TempDir()
	writeWrappedHarnessTestScript(t, binDir, "claude", `#!/bin/sh
printf '%s\n' 'ordinary task failed: validator rejected the requested change' >&2
exit 1
`)
	t.Setenv("PATH", binDir)
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-claude-quota.json"))

	svc := newProductionWrappedObservationService(t, "")
	final, _ := executeWrappedObservationRequest(t, svc, ServiceExecuteRequest{
		Prompt:        "exercise semantic task failure",
		Harness:       "claude",
		Provider:      "vendor@primary",
		Model:         "claude-sonnet-4-6",
		Permissions:   "safe",
		SessionLogDir: t.TempDir(),
	})
	if final.Status != "failed" || final.Cause != harnesses.TerminalCauseHarnessFailed {
		t.Fatalf("final = %+v, want harness_failed terminal", final)
	}
	if final.RoutingActual == nil || final.RoutingActual.FailureClass != "unknown" {
		t.Fatalf("routing actual = %+v, want typed unknown evidence", final.RoutingActual)
	}
	if records := svc.activeRouteAttempts(time.Now().UTC(), time.Minute); len(records) != 0 {
		t.Fatalf("semantic failure poisoned route health: %+v", records)
	}
}

func TestWrappedFailureObservationUsesTwoStageReachabilityGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		class    string
		error    string
		wantGate bool
	}{
		{name: "availability without reachability diagnostic stays soft", class: "availability", error: "binary not found"},
		{name: "protocol without reachability diagnostic stays soft", class: "protocol", error: "bad response framing"},
		{name: "transport without reachability diagnostic stays soft", class: "transport", error: "transport adapter rejected the request"},
		{name: "credential network-looking diagnostic stays soft", class: "credential_invalid", error: "credential refresh failed after connection reset"},
		{name: "quota HTTP-looking diagnostic stays soft", class: "quota_exhausted", error: "HTTP 503 while reading quota exhaustion evidence"},
		{name: "dispatch class plus reachability diagnostic hard gates", class: "transport", error: "dial tcp 192.0.2.1:443: i/o timeout", wantGate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{providerProbe: routehealth.NewProbeStore()}
			err := svc.observeRouteAttemptFromFinal(harnesses.FinalData{
				Status: "failed",
				Error:  tc.error,
				RoutingActual: &harnesses.RoutingActual{
					Harness:        "claude",
					Provider:       "vendor@primary",
					ServerInstance: "server-a",
					Model:          "model-a",
					FailureClass:   tc.class,
				},
			})
			if err != nil {
				t.Fatalf("observeRouteAttemptFromFinal: %v", err)
			}
			records := svc.activeRouteAttempts(time.Now().UTC(), time.Minute)
			if len(records) != 1 {
				t.Fatalf("typed class %q recorded attempts = %+v, want one", tc.class, records)
			}
			wantKey := (routehealth.Key{Harness: "claude", Provider: "vendor", Endpoint: "primary", ServerInstance: "server-a", Model: "model-a"})
			if records[0].Key != wantKey || records[0].Reason != tc.class {
				t.Fatalf("record = %+v, want exact key %+v and class %q", records[0], wantKey, tc.class)
			}
			probe, probed := svc.providerProbe.LastProbe("vendor", "primary")
			if probed != tc.wantGate {
				t.Fatalf("probe hard gate present = %v, want %v; probe=%+v", probed, tc.wantGate, probe)
			}
			if probed && probe.LastProbeSuccess {
				t.Fatalf("hard-gate probe unexpectedly successful: %+v", probe)
			}
		})
	}
}

func TestWrappedHTTP5xxObservationGatesOnlyExactEndpoint(t *testing.T) {
	cacheRoot := tempDiscoveryCacheDir(t)
	t.Setenv("FIZEAU_CACHE_DIR", cacheRoot)
	cache := &discoverycache.Cache{Root: cacheRoot}
	const modelID = "shared-model"
	primaryURL := "http://127.0.0.1:18011/v1"
	siblingURL := "http://127.0.0.1:18012/v1"
	capturedAt := time.Now().UTC().Add(-30 * time.Second)
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("local", "primary", primaryURL, "server-a"), capturedAt, []string{modelID})
	writeSnapshotDiscoveryFixture(t, cache, testDiscoverySourceName("local", "sibling", siblingURL, "server-b"), capturedAt, []string{modelID})

	catalog := loadRoutingFixtureCatalog(t, `
version: 5
generated_at: 2026-07-14T00:00:00Z
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
    allow_local: true
models:
  shared-model:
    family: shared
    status: active
    power: 6
    context_window: 16384
    surfaces:
      embedded-openai: shared-model
`)
	t.Cleanup(replaceRoutingCatalogForTest(t, catalog))

	svc := newResolveRouteProbeTestService(t, &fakeServiceConfig{
		providers: map[string]ServiceProviderEntry{
			"local": {
				Type: "lmstudio",
				Endpoints: []ServiceProviderEndpoint{
					{Name: "primary", BaseURL: primaryURL, ServerInstance: "server-a"},
					{Name: "sibling", BaseURL: siblingURL, ServerInstance: "server-b"},
				},
				Model:               modelID,
				Billing:             BillingModelFixed,
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
		},
		names:       []string{"local"},
		defaultName: "local",
	}, func(_ context.Context, provider, _ string) bool {
		t.Fatalf("route hot path invoked aliveness prober for %s", provider)
		return false
	})
	before := time.Now().UTC().Add(-time.Second)
	svc.providerProbe.RecordProbe("local", "primary", true, before)
	svc.providerProbe.RecordProbe("local", "sibling", true, before)

	if err := svc.observeRouteAttemptFromFinal(harnesses.FinalData{
		Status: "failed",
		Error:  "HTTP 502 Bad Gateway",
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "fiz",
			Provider:       "local@primary",
			ServerInstance: "server-a",
			Model:          modelID,
			FailureClass:   "protocol",
		},
	}); err != nil {
		t.Fatalf("observeRouteAttemptFromFinal: %v", err)
	}

	primaryProbe, ok := svc.providerProbe.LastProbe("local", "primary")
	if !ok || primaryProbe.LastProbeSuccess {
		t.Fatalf("primary probe = %+v, ok=%v; want failed 5xx reachability evidence", primaryProbe, ok)
	}
	siblingProbe, ok := svc.providerProbe.LastProbe("local", "sibling")
	if !ok || !siblingProbe.LastProbeSuccess {
		t.Fatalf("sibling probe = %+v, ok=%v; exact feedback must preserve sibling health", siblingProbe, ok)
	}

	decision, err := svc.ResolveRoute(context.Background(), RouteRequest{Model: modelID})
	if err != nil {
		t.Fatalf("ResolveRoute: %v", err)
	}
	if decision == nil || decision.Provider != "local@sibling" {
		t.Fatalf("decision = %#v, want healthy sibling local@sibling", decision)
	}
	var sawPrimary, sawSibling bool
	for _, candidate := range decision.Candidates {
		switch candidate.Provider {
		case "local@primary":
			sawPrimary = true
			if candidate.Eligible || candidate.FilterReason != FilterReasonEndpointUnreachable {
				t.Fatalf("failed primary candidate = %#v, want exact endpoint_unreachable gate", candidate)
			}
		case "local@sibling":
			sawSibling = true
			if !candidate.Eligible {
				t.Fatalf("healthy sibling candidate = %#v, want eligible", candidate)
			}
		}
	}
	if !sawPrimary || !sawSibling {
		t.Fatalf("endpoint candidates missing: primary=%v sibling=%v candidates=%#v", sawPrimary, sawSibling, decision.Candidates)
	}
}

func TestWrappedPersistenceErrorStillAppliesReachabilityFeedback(t *testing.T) {
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("block snapshot parent"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	svc := &service{
		opts:          ServiceOptions{PersistRouteHealth: filepath.Join(blockingFile, "routehealth.json")},
		providerProbe: routehealth.NewProbeStore(),
	}
	err := svc.observeRouteAttemptFromFinal(harnesses.FinalData{
		Status: "failed",
		Error:  "dial tcp 192.0.2.1:443: i/o timeout",
		RoutingActual: &harnesses.RoutingActual{
			Harness:        "fiz",
			Provider:       "vendor@primary",
			ServerInstance: "server-a",
			Model:          "model-a",
			FailureClass:   "transport",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "route health snapshot") {
		t.Fatalf("observation error = %v, want route health snapshot failure", err)
	}
	records := svc.activeRouteAttempts(time.Now().UTC(), time.Minute)
	if len(records) != 1 || records[0].Key.ServerInstance != "server-a" {
		t.Fatalf("persistence failure lost in-memory exact attempt: %+v", records)
	}
	probe, ok := svc.providerProbe.LastProbe("vendor", "primary")
	if !ok || probe.LastProbeSuccess {
		t.Fatalf("persistence failure suppressed reachability hard gate: %+v, ok=%v", probe, ok)
	}
}

func TestProductionRouteObservationFailureDoesNotSuppressTerminal(t *testing.T) {
	requirePOSIXWrappedHarnessTest(t)
	binDir := t.TempDir()
	writeWrappedHarnessTestScript(t, binDir, "claude", `#!/bin/sh
printf '%s\n' 'Failed to authenticate' 'Could not refresh auth token' >&2
exit 1
`)
	t.Setenv("PATH", binDir)
	t.Setenv("FIZEAU_CLAUDE_TRANSPORT", "subprocess")
	t.Setenv("FIZEAU_CLAUDE_QUOTA_CACHE", filepath.Join(t.TempDir(), "missing-claude-quota.json"))

	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("block snapshot parent"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	svc := newProductionWrappedObservationService(t, filepath.Join(blockingFile, "routehealth.json"))
	final, events := executeWrappedObservationRequest(t, svc, ServiceExecuteRequest{
		Prompt:        "exercise route observation persistence failure",
		Harness:       "claude",
		Provider:      "vendor@primary",
		Model:         "claude-sonnet-4-6",
		Permissions:   "safe",
		SessionLogDir: t.TempDir(),
	})
	if final.Status != "failed" || countWrappedFinalEvents(events) != 1 {
		t.Fatalf("terminal status/count = %q/%d, want failed/1", final.Status, countWrappedFinalEvents(events))
	}
	var observationWarnings []harnesses.FinalWarning
	for _, warning := range final.Warnings {
		if warning.Code == "route_observation_failed" {
			observationWarnings = append(observationWarnings, warning)
		}
	}
	if len(observationWarnings) != 1 {
		t.Fatalf("route observation warnings = %+v, want exactly one", observationWarnings)
	}
	if message := observationWarnings[0].Message; message == "" || len(message) > 2048 || !strings.Contains(message, "route final observation failed") {
		t.Fatalf("route observation warning is not bounded/actionable: %q (%d bytes)", message, len(message))
	}
	records := svc.activeRouteAttempts(time.Now().UTC(), time.Minute)
	if len(records) != 1 {
		t.Fatalf("persistence failure lost in-memory route attempt: %+v", records)
	}
	wantKey := routehealth.Key{
		Harness:        final.RoutingActual.Harness,
		Provider:       "vendor",
		Endpoint:       "primary",
		ServerInstance: final.RoutingActual.ServerInstance,
		Model:          final.RoutingActual.Model,
	}
	if records[0].Key != wantKey || records[0].Reason != final.RoutingActual.FailureClass {
		t.Fatalf("retained attempt = %+v, want delivered route %+v and class %q", records[0], wantKey, final.RoutingActual.FailureClass)
	}
}

func newProductionWrappedObservationService(t *testing.T, persistPath string) *service {
	t.Helper()
	public, err := New(ServiceOptions{
		ServiceConfig: &fakeServiceConfig{
			providers: map[string]ServiceProviderEntry{
				"vendor": {
					Type:           "anthropic",
					BaseURL:        "https://vendor.invalid",
					ServerInstance: "server-default",
					Endpoints: []ServiceProviderEndpoint{{
						Name:           "primary",
						BaseURL:        "https://vendor-primary.invalid",
						ServerInstance: "server-a",
					}},
					Model: "claude-sonnet-4-6",
				},
			},
			names:          []string{"vendor"},
			defaultName:    "vendor",
			healthCooldown: time.Minute,
			workDir:        t.TempDir(),
		},
		PersistRouteHealth:  persistPath,
		QuotaRefreshContext: canceledRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return public.(*service)
}

func executeWrappedObservationRequest(t *testing.T, svc *service, req ServiceExecuteRequest) (harnesses.FinalData, []ServiceEvent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ch, err := svc.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var (
		events []ServiceEvent
		finals []harnesses.FinalData
	)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				if len(finals) != 1 {
					t.Fatalf("final count = %d, want 1; events=%+v", len(finals), events)
				}
				return finals[0], events
			}
			events = append(events, event)
			if event.Type != harnesses.EventTypeFinal {
				continue
			}
			var final harnesses.FinalData
			if err := json.Unmarshal(event.Data, &final); err != nil {
				t.Fatalf("decode final: %v", err)
			}
			finals = append(finals, final)
		case <-ctx.Done():
			t.Fatalf("timed out draining Execute: %v", ctx.Err())
		}
	}
}

func routingDecisionHarness(t *testing.T, events []ServiceEvent) string {
	t.Helper()
	return wrappedRoutingDecision(t, events).Harness
}

func wrappedRoutingDecision(t *testing.T, events []ServiceEvent) ServiceRoutingDecisionData {
	t.Helper()
	for _, event := range events {
		if event.Type != harnesses.EventTypeRoutingDecision {
			continue
		}
		var decision ServiceRoutingDecisionData
		if err := json.Unmarshal(event.Data, &decision); err != nil {
			t.Fatalf("decode routing decision: %v", err)
		}
		return decision
	}
	t.Fatalf("missing routing decision in %+v", events)
	return ServiceRoutingDecisionData{}
}

func wrappedRoutingCandidate(t *testing.T, decision ServiceRoutingDecisionData, harness string) ServiceRoutingDecisionCandidate {
	t.Helper()
	for _, candidate := range decision.Candidates {
		if candidate.Harness == harness {
			return candidate
		}
	}
	t.Fatalf("missing %s candidate in %+v", harness, decision.Candidates)
	return ServiceRoutingDecisionCandidate{}
}

func countWrappedFinalEvents(events []ServiceEvent) int {
	count := 0
	for _, event := range events {
		if event.Type == harnesses.EventTypeFinal {
			count++
		}
	}
	return count
}

func requirePOSIXWrappedHarnessTest(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake wrapped-harness scripts require POSIX shell")
	}
}

func writeWrappedHarnessTestScript(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write fake %s harness: %v", name, err)
	}
	return path
}
