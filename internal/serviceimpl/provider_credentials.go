package serviceimpl

import (
	"fmt"
	"strings"
)

// ProviderCredentialMissing returns configured providers whose credentials
// are absent or malformed, keyed by provider name with the operator-facing
// configuration location as evidence. Only OpenRouter requires a credential
// at this routing boundary.
func ProviderCredentialMissing(providerNames []string, providers map[string]ProviderEntry) map[string]string {
	var missing map[string]string
	for _, name := range providerNames {
		provider, ok := providers[name]
		if !ok || NormalizeProviderType(provider.Type) != "openrouter" {
			continue
		}
		if OpenRouterAPIKeyWellFormed(provider.APIKey) {
			continue
		}
		if missing == nil {
			missing = make(map[string]string)
		}
		missing[name] = fmt.Sprintf("providers.%s.api_key (or OPENROUTER_API_KEY env)", name)
	}
	return missing
}

// OpenRouterAPIKeyWellFormed reports whether raw plausibly resembles an
// OpenRouter API key. It deliberately performs only local shape validation;
// server-side credential validity is classified by the credit probe.
func OpenRouterAPIKeyWellFormed(raw string) bool {
	const (
		expectedPrefix  = "sk-or-"
		minPlausibleLen = 20
	)
	key := strings.TrimSpace(raw)
	return key != "" &&
		!strings.Contains(key, "${") &&
		strings.HasPrefix(key, expectedPrefix) &&
		len(key) >= minPlausibleLen
}
