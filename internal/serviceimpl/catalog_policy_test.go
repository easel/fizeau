package serviceimpl

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
)

func TestRoutingPolicyForName(t *testing.T) {
	cat := loadCatalogPolicyTestCatalog(t)
	tests := []struct {
		name              string
		cat               *modelcatalog.Catalog
		policy            string
		wantRouting       string
		wantPowerName     string
		wantFailureKind   CatalogPolicyFailureKind
		wantFailurePolicy string
	}{
		{name: "empty", cat: cat, wantRouting: "", wantPowerName: ""},
		{
			name:              "whitespace remains raw in failure evidence",
			cat:               cat,
			policy:            "   ",
			wantRouting:       "",
			wantPowerName:     "   ",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: "   ",
		},
		{name: "canonical", cat: cat, policy: " default ", wantRouting: "default", wantPowerName: "default"},
		{name: "custom", cat: cat, policy: " custom ", wantRouting: "custom", wantPowerName: "custom"},
		{
			name:              "unknown remains raw in power and failure evidence",
			cat:               cat,
			policy:            " missing ",
			wantRouting:       "missing",
			wantPowerName:     " missing ",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: " missing ",
		},
		{
			name:              "canonical routing name survives nil catalog",
			policy:            " smart ",
			wantRouting:       "smart",
			wantPowerName:     " smart ",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: " smart ",
		},
		{
			name:              "unknown nil catalog",
			policy:            " custom ",
			wantRouting:       "custom",
			wantPowerName:     " custom ",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: " custom ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, failure := EvaluateCatalogPolicy(test.cat, CatalogPolicyRequest{Policy: test.policy})
			if result.RoutingPolicy != test.wantRouting {
				t.Fatalf("RoutingPolicy=%q, want %q", result.RoutingPolicy, test.wantRouting)
			}
			if result.PowerPolicy.PolicyName != test.wantPowerName {
				t.Fatalf("PowerPolicy.PolicyName=%q, want %q", result.PowerPolicy.PolicyName, test.wantPowerName)
			}
			assertCatalogPolicyFailure(t, failure, test.wantFailureKind, test.wantFailurePolicy, "", "")
		})
	}
}

func TestProviderPreferenceForPolicy(t *testing.T) {
	cat := loadCatalogPolicyTestCatalog(t)
	tests := []struct {
		name              string
		cat               *modelcatalog.Catalog
		policy            string
		wantPreference    string
		wantFailureKind   CatalogPolicyFailureKind
		wantReplacement   string
		wantFailurePolicy string
	}{
		{name: "empty without catalog", policy: "", wantPreference: routing.ProviderPreferenceLocalFirst},
		{name: "default", cat: cat, policy: "default", wantPreference: routing.ProviderPreferenceLocalFirst},
		{name: "trimmed cheap", cat: cat, policy: " cheap ", wantPreference: routing.ProviderPreferenceLocalFirst},
		{name: "smart", cat: cat, policy: "smart", wantPreference: routing.ProviderPreferenceSubscriptionFirst},
		{name: "air gapped", cat: cat, policy: "air-gapped", wantPreference: routing.ProviderPreferenceLocalOnly},
		{
			name:              "deprecated medium",
			cat:               cat,
			policy:            "code-medium",
			wantFailureKind:   CatalogPolicyFailureDeprecatedPolicy,
			wantReplacement:   "default",
			wantFailurePolicy: "code-medium",
		},
		{
			name:              "deprecated high",
			cat:               cat,
			policy:            "code-high",
			wantFailureKind:   CatalogPolicyFailureDeprecatedPolicy,
			wantReplacement:   "smart",
			wantFailurePolicy: "code-high",
		},
		{
			name:              "trimmed deprecated name remains unknown",
			cat:               cat,
			policy:            " code-high ",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: " code-high ",
		},
		{
			name:              "unknown",
			cat:               cat,
			policy:            "missing",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: "missing",
		},
		{
			name:              "nil catalog",
			policy:            "default",
			wantFailureKind:   CatalogPolicyFailureUnknownPolicy,
			wantFailurePolicy: "default",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, failure := EvaluateCatalogPolicy(test.cat, CatalogPolicyRequest{Policy: test.policy})
			if result.ProviderPreference != test.wantPreference {
				t.Fatalf("ProviderPreference=%q, want %q", result.ProviderPreference, test.wantPreference)
			}
			assertCatalogPolicyFailure(t, failure, test.wantFailureKind, test.wantFailurePolicy, test.wantReplacement, "")
		})
	}

	t.Run("unsupported preference", func(t *testing.T) {
		result, failure := evaluateCatalogPolicy(cat, CatalogPolicyRequest{Policy: "custom"}, func(string) string {
			return "sideways"
		})
		if result.ProviderPreference != "" {
			t.Fatalf("ProviderPreference=%q, want empty on failure", result.ProviderPreference)
		}
		assertCatalogPolicyFailure(
			t,
			failure,
			CatalogPolicyFailureUnsupportedProviderPreference,
			"custom",
			"",
			"sideways",
		)
	})
}

func TestRoutePowerPolicyForRequest(t *testing.T) {
	cat := loadCatalogPolicyTestCatalog(t)

	t.Run("catalog and caller bounds intersect", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:   "default",
			MinPower: 4,
			MaxPower: 10,
		})
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		want := CatalogPowerPolicy{PolicyName: "default", MinPower: 5, MaxPower: 8}
		if result.PowerPolicy != want || result.MinPower != 5 || result.MaxPower != 8 {
			t.Fatalf("result=%#v, want power policy %#v and enforced bounds 5..8", result, want)
		}
	})

	t.Run("tighter caller bounds win", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:   "default",
			MinPower: 7,
			MaxPower: 7,
		})
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		if result.PowerPolicy.MinPower != 7 || result.PowerPolicy.MaxPower != 7 || result.MinPower != 7 || result.MaxPower != 7 {
			t.Fatalf("result=%#v, want effective and enforced bounds 7..7", result)
		}
	})

	t.Run("conflicting explicit bounds are not newly validated", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:   "default",
			MinPower: 9,
			MaxPower: 4,
		})
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		if result.PowerPolicy.MinPower != 9 || result.PowerPolicy.MaxPower != 4 || result.MinPower != 9 || result.MaxPower != 4 {
			t.Fatalf("result=%#v, want conflicting bounds preserved as 9..4", result)
		}
	})

	t.Run("unknown policy preserves request evidence", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:   "missing",
			MinPower: 2,
			MaxPower: 11,
		})
		assertCatalogPolicyFailure(t, failure, CatalogPolicyFailureUnknownPolicy, "missing", "", "")
		want := CatalogPowerPolicy{PolicyName: "missing", MinPower: 2, MaxPower: 11}
		if result.PowerPolicy != want || result.MinPower != 2 || result.MaxPower != 11 {
			t.Fatalf("result=%#v, want unknown-policy evidence %#v and bounds 2..11", result, want)
		}
	})

	t.Run("explicit model pin keeps caller bounds", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:   "default",
			Model:    "pinned-model",
			MinPower: 2,
			MaxPower: 11,
		})
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		want := CatalogPowerPolicy{PolicyName: "default", MinPower: 5, MaxPower: 8}
		if result.PowerPolicy != want || result.MinPower != 2 || result.MaxPower != 11 {
			t.Fatalf("result=%#v, want policy evidence %#v and pinned bounds 2..11", result, want)
		}
	})

	t.Run("air gapped requirements compose defensively", func(t *testing.T) {
		requestRequire := []string{"request-first", "request-second"}
		request := CatalogPolicyRequest{
			Policy:  "air-gapped",
			Require: requestRequire,
		}
		result, failure := EvaluateCatalogPolicy(cat, request)
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		wantRequire := []string{"no_remote", "request-first", "request-second"}
		if !result.AllowLocal || !reflect.DeepEqual(result.Require, wantRequire) {
			t.Fatalf("result=%#v, want AllowLocal and Require=%v", result, wantRequire)
		}
		requestRequire[0] = "mutated-request"
		if !reflect.DeepEqual(result.Require, wantRequire) {
			t.Fatalf("result.Require changed with request slice: %v", result.Require)
		}
		result.Require[0] = "mutated-result"
		again, againFailure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{Policy: "air-gapped"})
		if againFailure != nil {
			t.Fatalf("second failure=%#v, want nil", againFailure)
		}
		if !reflect.DeepEqual(again.Require, []string{"no_remote"}) {
			t.Fatalf("catalog requirements aliased prior result: %v", again.Require)
		}
	})

	t.Run("request allow local survives restrictive policy", func(t *testing.T) {
		result, failure := EvaluateCatalogPolicy(cat, CatalogPolicyRequest{
			Policy:     "custom",
			AllowLocal: true,
		})
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		if !result.AllowLocal {
			t.Fatal("AllowLocal=false, want request AllowLocal OR restrictive policy")
		}
	})
}

func assertCatalogPolicyFailure(t *testing.T, failure *CatalogPolicyFailure, kind CatalogPolicyFailureKind, policy, replacement, preference string) {
	t.Helper()
	if kind == "" {
		if failure != nil {
			t.Fatalf("failure=%#v, want nil", failure)
		}
		return
	}
	if failure == nil {
		t.Fatalf("failure=nil, want kind %q", kind)
	}
	if failure.Kind != kind || failure.Policy != policy || failure.ReplacementPolicy != replacement || failure.ProviderPreference != preference {
		t.Fatalf(
			"failure=%#v, want kind=%q policy=%q replacement=%q preference=%q",
			failure,
			kind,
			policy,
			replacement,
			preference,
		)
	}
}

func loadCatalogPolicyTestCatalog(t *testing.T) *modelcatalog.Catalog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	manifest := `
version: 5
generated_at: 2026-07-15T00:00:00Z
catalog_version: catalog-policy-test
policies:
  default:
    min_power: 5
    max_power: 8
    allow_local: true
    require: [no_remote]
  cheap:
    min_power: 1
    max_power: 5
    allow_local: true
  smart:
    min_power: 7
    max_power: 10
    allow_local: false
  air-gapped:
    min_power: 1
    max_power: 5
    allow_local: true
    require: [no_remote]
  custom:
    min_power: 3
    max_power: 9
    allow_local: false
    require: [no_remote]
models:
  catalog-policy-model:
    family: test
    status: active
    power: 5
    surfaces:
      agent.openai: catalog-policy-model
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
