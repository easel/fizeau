package serviceimpl

import (
	"strings"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routehealth"
	"github.com/easel/fizeau/internal/routing"
)

// CatalogPolicyRequest is the API-neutral subset of a route request needed to
// resolve catalog policy, power evidence, and routing bounds.
type CatalogPolicyRequest struct {
	Policy     string
	Model      string
	MinPower   int
	MaxPower   int
	AllowLocal bool
	Require    []string
}

// CatalogPowerPolicy records the effective policy name and bounds used as
// decision evidence.
type CatalogPowerPolicy struct {
	PolicyName string
	MinPower   int
	MaxPower   int
}

// CatalogPolicyResult is the API-neutral catalog-policy projection consumed
// by the root service adapter.
type CatalogPolicyResult struct {
	RoutingPolicy      string
	ProviderPreference string
	PowerPolicy        CatalogPowerPolicy
	MinPower           int
	MaxPower           int
	AllowLocal         bool
	Require            []string
}

// CatalogPolicyFailureKind identifies a catalog-policy failure without
// constructing a root-package error.
type CatalogPolicyFailureKind string

const (
	CatalogPolicyFailureUnknownPolicy                 CatalogPolicyFailureKind = "unknown_policy"
	CatalogPolicyFailureDeprecatedPolicy              CatalogPolicyFailureKind = "deprecated_policy"
	CatalogPolicyFailureUnsupportedProviderPreference CatalogPolicyFailureKind = "unsupported_provider_preference"
)

// CatalogPolicyFailure contains the data the root adapter needs to construct
// its exact public error.
type CatalogPolicyFailure struct {
	Kind               CatalogPolicyFailureKind
	Policy             string
	ReplacementPolicy  string
	ProviderPreference string
}

// EvaluateCatalogPolicy resolves one request's catalog policy, provider
// preference, effective power evidence, enforced bounds, and policy
// requirements. Generic power merging remains owned by routehealth.
func EvaluateCatalogPolicy(cat *modelcatalog.Catalog, req CatalogPolicyRequest) (CatalogPolicyResult, *CatalogPolicyFailure) {
	return evaluateCatalogPolicy(cat, req, providerPreferenceForPolicyName)
}

type providerPreferenceLookup func(string) string

func evaluateCatalogPolicy(cat *modelcatalog.Catalog, req CatalogPolicyRequest, preferenceForName providerPreferenceLookup) (CatalogPolicyResult, *CatalogPolicyFailure) {
	policy, _, policyFound := lookupCatalogPolicy(cat, req.Policy)
	power := routehealth.EffectivePowerPolicy(routehealth.PowerRequest{
		Policy:   req.Policy,
		Model:    req.Model,
		MinPower: req.MinPower,
		MaxPower: req.MaxPower,
	}, func(name string) (routehealth.PolicySpec, bool) {
		matched, policyName, ok := lookupCatalogPolicy(cat, name)
		if !ok {
			return routehealth.PolicySpec{}, false
		}
		return routehealth.PolicySpec{
			Name:     policyName,
			MinPower: matched.MinPower,
			MaxPower: matched.MaxPower,
		}, true
	})
	minPower, maxPower := routehealth.PowerBoundsForRequest(routehealth.PowerRequest{
		Policy:   req.Policy,
		Model:    req.Model,
		MinPower: req.MinPower,
		MaxPower: req.MaxPower,
	}, power)

	result := CatalogPolicyResult{
		RoutingPolicy: routingPolicyForName(cat, req.Policy),
		PowerPolicy: CatalogPowerPolicy{
			PolicyName: power.PolicyName,
			MinPower:   power.MinPower,
			MaxPower:   power.MaxPower,
		},
		MinPower:   minPower,
		MaxPower:   maxPower,
		AllowLocal: req.AllowLocal,
		Require:    append([]string(nil), req.Require...),
	}
	if policyFound {
		result.AllowLocal = result.AllowLocal || policy.AllowLocal
		result.Require = append(append([]string(nil), policy.Require...), req.Require...)
	}

	preference, failure := catalogProviderPreference(cat, req.Policy, policyFound, preferenceForName)
	result.ProviderPreference = preference
	return result, failure
}

func routingPolicyForName(cat *modelcatalog.Catalog, name string) string {
	name = strings.TrimSpace(name)
	switch name {
	case "":
		return ""
	case "cheap", "default", "smart", "air-gapped":
		return name
	}
	if cat == nil {
		return name
	}
	_, policyName, ok := lookupCatalogPolicy(cat, name)
	if !ok {
		return name
	}
	return policyName
}

func catalogProviderPreference(cat *modelcatalog.Catalog, policy string, policyFound bool, preferenceForName providerPreferenceLookup) (string, *CatalogPolicyFailure) {
	if policy == "" {
		return routing.ProviderPreferenceLocalFirst, nil
	}
	switch policy {
	case "code-medium":
		return "", &CatalogPolicyFailure{
			Kind:              CatalogPolicyFailureDeprecatedPolicy,
			Policy:            policy,
			ReplacementPolicy: "default",
		}
	case "code-high":
		return "", &CatalogPolicyFailure{
			Kind:              CatalogPolicyFailureDeprecatedPolicy,
			Policy:            policy,
			ReplacementPolicy: "smart",
		}
	}
	if cat == nil || !policyFound {
		return "", &CatalogPolicyFailure{
			Kind:   CatalogPolicyFailureUnknownPolicy,
			Policy: policy,
		}
	}

	preference := preferenceForName(policy)
	switch preference {
	case routing.ProviderPreferenceLocalOnly, routing.ProviderPreferenceSubscriptionOnly,
		routing.ProviderPreferenceLocalFirst, routing.ProviderPreferenceSubscriptionFirst:
		return preference, nil
	default:
		return "", &CatalogPolicyFailure{
			Kind:               CatalogPolicyFailureUnsupportedProviderPreference,
			Policy:             policy,
			ProviderPreference: preference,
		}
	}
}

func lookupCatalogPolicy(cat *modelcatalog.Catalog, name string) (modelcatalog.Policy, string, bool) {
	if cat == nil {
		return modelcatalog.Policy{}, "", false
	}
	name = strings.TrimSpace(name)
	if policy, ok := cat.Policy(name); ok {
		return policy, policy.Name, true
	}
	return modelcatalog.Policy{}, "", false
}

func providerPreferenceForPolicyName(name string) string {
	switch strings.TrimSpace(name) {
	case "air-gapped":
		return routing.ProviderPreferenceLocalOnly
	case "smart":
		return routing.ProviderPreferenceSubscriptionFirst
	case "default", "cheap":
		return routing.ProviderPreferenceLocalFirst
	default:
		return routing.ProviderPreferenceLocalFirst
	}
}

// PolicyForName remains temporarily for the root adapter. The root-wiring
// child removes this compatibility entrypoint after migrating its callers.
func PolicyForName(cat *modelcatalog.Catalog, name string) (modelcatalog.Policy, string, bool) {
	return lookupCatalogPolicy(cat, name)
}

// ProviderPreferenceForPolicyName remains temporarily for the root adapter.
// The root-wiring child removes this compatibility entrypoint after migrating
// its caller.
func ProviderPreferenceForPolicyName(name string) string {
	return providerPreferenceForPolicyName(name)
}
