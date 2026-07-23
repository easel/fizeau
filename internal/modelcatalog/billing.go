package modelcatalog

import "strings"

// BillingModel describes how a provider or harness is paid for.
type BillingModel string

const (
	BillingModelUnknown      BillingModel = ""
	BillingModelFixed        BillingModel = "fixed"
	BillingModelPerToken     BillingModel = "per_token"
	BillingModelSubscription BillingModel = "subscription"
)

// BillingForProviderSystem returns the catalog's built-in billing model for a
// known provider system. Unknown systems return BillingModelUnknown so user
// manifests can require an explicit provider billing field.
func BillingForProviderSystem(system string) BillingModel {
	switch normalizeBillingKey(system) {
	case "lmstudio", "llama-server", "ds4", "omlx", "vllm", "rapid-mlx", "ollama", "lucebox":
		return BillingModelFixed
	case "openai", "openrouter", "anthropic", "google", "xai":
		return BillingModelPerToken
	default:
		return BillingModelUnknown
	}
}

// BillingForHarness returns the billing model for built-in harnesses whose
// usage is governed by subscription/account limits rather than per-token user
// provider configuration.
func BillingForHarness(harness string) BillingModel {
	switch normalizeBillingKey(harness) {
	case "claude", "codex", "gemini", "grok":
		return BillingModelSubscription
	default:
		return BillingModelUnknown
	}
}

func knownProviderSystem(system string) bool {
	return BillingForProviderSystem(system) != BillingModelUnknown || BillingForHarness(system) != BillingModelUnknown
}

func knownBillingModel(model BillingModel) bool {
	switch model {
	case BillingModelFixed, BillingModelPerToken, BillingModelSubscription:
		return true
	default:
		return false
	}
}

func normalizeBillingKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
