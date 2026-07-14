package serviceimpl

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
)

func TestBuildRoutingInputsPreservesSnapshotEvidence(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)
	baseSuccess := map[string]float64{"local/model": 0.75}
	base := routing.Inputs{
		ProviderSuccessRate: baseSuccess,
		Now:                 time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
	}

	input := RoutingInputsInput{
		Base: base,
		Harnesses: []routing.HarnessEntry{
			{
				Name:                "fiz",
				Available:           true,
				AutoRoutingEligible: true,
				SupportsTools:       true,
			},
			{
				Name:                "claude",
				Surface:             "claude",
				Available:           true,
				AutoRoutingEligible: true,
				IsSubscription:      true,
				DefaultModel:        "sonnet-4.6",
				SupportedModels:     []string{"sonnet"},
			},
		},
		Providers: map[string]ProviderEntry{
			"local": {
				Type:                "lmstudio",
				BaseURL:             "http://local.invalid/v1",
				ServerInstance:      "local-default",
				Model:               "plain-model",
				Billing:             modelcatalog.BillingModelFixed,
				IncludeByDefault:    false,
				IncludeByDefaultSet: true,
				ContextWindow:       131072,
				Endpoints: []ProviderEndpoint{
					{Name: "alpha", BaseURL: "http://alpha.invalid/v1", ServerInstance: "alpha-instance"},
					{Name: "beta", BaseURL: "http://beta.invalid/v1", ServerInstance: "beta-instance"},
				},
			},
			"remote": {
				Type:                "openrouter",
				BaseURL:             "https://remote.invalid/v1",
				Model:               "priced-model",
				Billing:             modelcatalog.BillingModelPerToken,
				IncludeByDefault:    true,
				IncludeByDefaultSet: true,
			},
			"broken": {
				Type:        "openai",
				Model:       "priced-model",
				ConfigError: "bad config",
			},
		},
		ProviderNames:    []string{"local", "remote", "broken"},
		HasServiceConfig: true,
		Snapshot: modelsnapshot.ModelSnapshot{Models: []modelsnapshot.KnownModel{
			{
				Provider:        "local",
				ProviderType:    "lmstudio",
				ID:              "dflash",
				CatalogID:       "priced-model",
				EndpointName:    "alpha",
				EndpointBaseURL: "http://alpha.invalid/v1",
				ServerInstance:  "alpha-instance",
				ContextWindow:   65536,
			},
			{
				Provider:        "local",
				ProviderType:    "lmstudio",
				ID:              "plain-model",
				EndpointName:    "beta",
				EndpointBaseURL: "http://beta.invalid/v1",
				ServerInstance:  "beta-instance",
				ContextWindow:   32768,
			},
			{
				Provider:            "remote",
				ProviderType:        "openrouter",
				ID:                  "priced-model",
				EndpointName:        "remote",
				EndpointBaseURL:     "https://remote.invalid/v1",
				ContextWindow:       65536,
				ContextWindowSource: routing.ContextSourceProviderAPI,
			},
			{Provider: "broken", ID: "priced-model"},
			{Provider: "not-configured", ID: "priced-model"},
			{Provider: "local", Harness: "codex", ID: "priced-model"},
		}},
		Catalog:                 cat,
		LocalCostUSDPer1kTokens: 0.0042,
	}

	got := BuildRoutingInputs(input)
	if got.ProviderSuccessRate["local/model"] != baseSuccess["local/model"] || !got.Now.Equal(base.Now) {
		t.Fatalf("base routing evidence was not preserved: %#v", got)
	}
	if len(got.Harnesses) != 2 {
		t.Fatalf("harness count = %d, want 2", len(got.Harnesses))
	}

	fiz := got.Harnesses[0]
	if fiz.Name != "fiz" || !fiz.Available || !fiz.AutoRoutingEligible {
		t.Fatalf("fiz harness state = %#v, want available/auto-routable", fiz)
	}
	if len(fiz.Providers) != 3 {
		t.Fatalf("fiz providers = %d, want 3: %#v", len(fiz.Providers), fiz.Providers)
	}
	if fiz.Providers[0].Name != "local@alpha" || fiz.Providers[1].Name != "local@beta" || fiz.Providers[2].Name != "remote" {
		t.Fatalf("provider ordering/names = %q/%q/%q, want local@alpha/local@beta/remote", fiz.Providers[0].Name, fiz.Providers[1].Name, fiz.Providers[2].Name)
	}

	alpha := fiz.Providers[0]
	if alpha.EndpointName != "alpha" || alpha.ServerInstance != "alpha-instance" || !alpha.DiscoveryAttempted {
		t.Fatalf("alpha endpoint evidence = %#v", alpha)
	}
	if alpha.CatalogIDByModel["dflash"] != "priced-model" {
		t.Fatalf("alpha catalog alias map = %#v, want dflash -> priced-model", alpha.CatalogIDByModel)
	}
	if !containsString(alpha.DiscoveredIDs, "dflash") || !containsString(alpha.DiscoveredIDs, "plain-model") {
		t.Fatalf("alpha discovered/default IDs = %#v, want dflash and configured plain-model", alpha.DiscoveredIDs)
	}
	if alpha.ContextWindows["dflash"] != 131072 || alpha.ContextWindowSources["dflash"] != routing.ContextSourceProviderConfig {
		t.Fatalf("alpha context evidence = %d/%q, want 131072/%q", alpha.ContextWindows["dflash"], alpha.ContextWindowSources["dflash"], routing.ContextSourceProviderConfig)
	}
	if !alpha.ExcludeFromDefaultRouting {
		t.Fatal("alpha provider lost include_by_default=false evidence")
	}
	if alpha.ActualCashSpend || alpha.CostSource != routing.CostSourceUserConfig || !floatNearRoutingInput(alpha.CostUSDPer1kTokens, 0.0042) {
		t.Fatalf("alpha local cost evidence = %#v", alpha)
	}

	remote := fiz.Providers[2]
	if !remote.ActualCashSpend || remote.CostSource != routing.CostSourceCatalog || !floatNearRoutingInput(remote.CostUSDPer1kTokens, 0.003) {
		t.Fatalf("remote catalog cost evidence = %#v, want metered 0.003", remote)
	}
	if remote.ContextWindows["priced-model"] != 65536 || remote.ContextWindowSources["priced-model"] != routing.ContextSourceProviderAPI {
		t.Fatalf("remote context evidence = %d/%q, want 65536/provider_api", remote.ContextWindows["priced-model"], remote.ContextWindowSources["priced-model"])
	}

	claude := got.Harnesses[1]
	if len(claude.AutoRoutingModels) != 2 || claude.AutoRoutingModels[0] != "sonnet-4.6" || claude.AutoRoutingModels[1] != "haiku-5.5" {
		t.Fatalf("claude tier models = %#v, want power-sorted sonnet then haiku", claude.AutoRoutingModels)
	}
	if len(claude.Providers) != 1 || claude.Providers[0].Billing != modelcatalog.BillingModelSubscription || claude.Providers[0].ActualCashSpend {
		t.Fatalf("claude subscription projection = %#v", claude.Providers)
	}
	if got.ModelEligibility == nil {
		t.Fatal("ModelEligibility is nil")
	}
	haiku, ok := got.ModelEligibility("haiku")
	if !ok || !haiku.ExactPinOnly || haiku.AutoRoutable {
		t.Fatalf("haiku eligibility = %#v/%t, want exact-pin-only", haiku, ok)
	}
	priced, ok := got.ModelEligibility("priced-model")
	if !ok || priced.Power != 8 {
		t.Fatalf("priced-model eligibility = %#v/%t, want power 8", priced, ok)
	}
	if got.ReasoningResolver == nil {
		t.Fatal("ReasoningResolver is nil")
	}
	if reasoning, ok := got.ReasoningResolver("default", "claude"); !ok || reasoning != "high" {
		t.Fatalf("default claude reasoning = %q/%t, want high/true", reasoning, ok)
	}
}

func TestRoutingCostProjectionPreservesPolicyAndSubscriptionCurve(t *testing.T) {
	cat := loadRoutingInputsTestCatalog(t)

	fixedUnknown := routing.ProviderEntry{DefaultModel: "plain-model"}
	ApplyEndpointRoutingCost(&fixedUnknown, ProviderEntry{Type: "lmstudio"}, cat, 0)
	if fixedUnknown.ActualCashSpend || fixedUnknown.CostSource != routing.CostSourceUnknown || fixedUnknown.CostUSDPer1kTokens != 0 {
		t.Fatalf("fixed unknown cost = %#v", fixedUnknown)
	}

	fixedConfigured := routing.ProviderEntry{DefaultModel: "plain-model"}
	ApplyEndpointRoutingCost(&fixedConfigured, ProviderEntry{Type: "lmstudio"}, cat, 0.0042)
	if fixedConfigured.ActualCashSpend || fixedConfigured.CostSource != routing.CostSourceUserConfig || !floatNearRoutingInput(fixedConfigured.CostUSDPer1kTokens, 0.0042) {
		t.Fatalf("fixed configured cost = %#v", fixedConfigured)
	}

	metered := routing.ProviderEntry{DefaultModel: "priced-model"}
	ApplyEndpointRoutingCost(&metered, ProviderEntry{Type: "openrouter"}, cat, 0)
	if !metered.ActualCashSpend || metered.CostSource != routing.CostSourceCatalog || !floatNearRoutingInput(metered.CostUSDPer1kTokens, 0.003) {
		t.Fatalf("metered catalog cost = %#v", metered)
	}

	lowQuotaUse := routing.HarnessEntry{
		Name:              "claude",
		IsSubscription:    true,
		DefaultModel:      "sonnet-4.6",
		QuotaPercentUsed:  10,
		AutoRoutingModels: []string{"sonnet-4.6", "haiku-5.5"},
	}
	highQuotaUse := lowQuotaUse
	highQuotaUse.QuotaPercentUsed = 95
	ApplySubscriptionRoutingCost(&lowQuotaUse, cat)
	ApplySubscriptionRoutingCost(&highQuotaUse, cat)
	if len(lowQuotaUse.Providers) != 1 || len(highQuotaUse.Providers) != 1 {
		t.Fatalf("subscription provider counts = %d/%d, want 1/1", len(lowQuotaUse.Providers), len(highQuotaUse.Providers))
	}
	lowCost := lowQuotaUse.Providers[0]
	highCost := highQuotaUse.Providers[0]
	if !floatNearRoutingInput(lowCost.CostUSDPer1kTokens, 0.009) || !floatNearRoutingInput(highCost.CostUSDPer1kTokens, 0.009) {
		t.Fatalf("subscription shadow costs = %v/%v, want flat 0.009", lowCost.CostUSDPer1kTokens, highCost.CostUSDPer1kTokens)
	}
	if lowCost.CostSource != routing.CostSourceSubscription || lowCost.ActualCashSpend {
		t.Fatalf("subscription billing evidence = %#v", lowCost)
	}
	if !floatNearRoutingInput(lowCost.CostUSDPer1kTokensByModel["sonnet-4.6"], 0.009) || !floatNearRoutingInput(lowCost.CostUSDPer1kTokensByModel["haiku-5.5"], 0.0015) {
		t.Fatalf("subscription per-tier costs = %#v", lowCost.CostUSDPer1kTokensByModel)
	}

	// Unknown subscription defaults retain the legacy policy fallback. The
	// default policy resolves to the highest-powered agent.openai model.
	fallback := routing.HarnessEntry{Name: "codex", IsSubscription: true, DefaultModel: "missing-model"}
	ApplySubscriptionRoutingCost(&fallback, cat)
	if len(fallback.Providers) != 1 || !floatNearRoutingInput(fallback.Providers[0].CostUSDPer1kTokens, 0.003) {
		t.Fatalf("subscription fallback policy cost = %#v, want 0.003", fallback.Providers)
	}

	curve := NormalizeSubscriptionCostCurve(nil)
	wantCurve := SubscriptionCostCurve{
		FreeUntilPercent:   70,
		LowUntilPercent:    80,
		MediumUntilPercent: 90,
		LowMultiplier:      0.1,
		MediumMultiplier:   0.3,
		HighMultiplier:     1.2,
	}
	if curve != wantCurve {
		t.Fatalf("default subscription curve = %#v, want %#v", curve, wantCurve)
	}
	baseCost := 0.01
	for _, tc := range []struct {
		used int
		want float64
	}{
		{used: 70, want: 0},
		{used: 75, want: 0.001},
		{used: 85, want: 0.003},
		{used: 92, want: 0.012},
	} {
		if got := SubscriptionEffectiveCostUSDPer1kTokens(baseCost, tc.used, curve); !floatNearRoutingInput(got, tc.want) {
			t.Fatalf("legacy curve at %d%% = %v, want %v", tc.used, got, tc.want)
		}
	}
}

func loadRoutingInputsTestCatalog(t *testing.T) *modelcatalog.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	manifest := `
version: 5
generated_at: 2026-07-14T00:00:00Z
catalog_version: routing-inputs-test
policies:
  default:
    min_power: 5
    max_power: 8
    allow_local: true
models:
  priced-model:
    family: priced
    status: active
    power: 8
    cost_input_per_m: 2
    cost_output_per_m: 4
    context_window: 262144
    reasoning_default: medium
    surfaces:
      agent.openai: priced-model
  plain-model:
    family: plain
    status: active
    power: 5
    context_window: 32768
    no_tools: true
    surfaces:
      agent.openai: plain-model
  claude-sonnet-4.6:
    family: claude
    status: active
    power: 8
    cost_input_per_m: 3
    cost_output_per_m: 15
    context_window: 200000
    reasoning_default: high
    surfaces:
      claude-code: sonnet-4.6
  claude-haiku-5.5:
    family: claude
    status: active
    power: 3
    exact_pin_only: true
    cost_input_per_m: 1
    cost_output_per_m: 2
    context_window: 200000
    reasoning_default: low
    surfaces:
      claude-code: haiku-5.5
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write catalog fixture: %v", err)
	}
	cat, err := modelcatalog.Load(modelcatalog.LoadOptions{ManifestPath: path, RequireExternal: true})
	if err != nil {
		t.Fatalf("load catalog fixture: %v", err)
	}
	return cat
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func floatNearRoutingInput(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}
