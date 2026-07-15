package serviceimpl

import (
	"strings"
	"testing"
)

func TestProviderCredentialMissing(t *testing.T) {
	const location = "providers.openrouter.api_key (or OPENROUTER_API_KEY env)"
	validKey := "sk-or-" + strings.Repeat("a", 14)

	tests := []struct {
		name      string
		names     []string
		providers map[string]ProviderEntry
		want      map[string]string
	}{
		{name: "nil inputs"},
		{name: "empty inputs", names: []string{}, providers: map[string]ProviderEntry{}},
		{name: "missing entry", names: []string{"openrouter"}, providers: map[string]ProviderEntry{}},
		{
			name:      "empty key",
			names:     []string{"openrouter"},
			providers: map[string]ProviderEntry{"openrouter": {Type: "openrouter"}},
			want:      map[string]string{"openrouter": location},
		},
		{
			name:      "trimmed empty key",
			names:     []string{"openrouter"},
			providers: map[string]ProviderEntry{"openrouter": {Type: "openrouter", APIKey: " \t\n "}},
			want:      map[string]string{"openrouter": location},
		},
		{
			name:      "mixed case whitespace type",
			names:     []string{"openrouter"},
			providers: map[string]ProviderEntry{"openrouter": {Type: "  OpenRouter \t"}},
			want:      map[string]string{"openrouter": location},
		},
		{
			name:      "custom provider location",
			names:     []string{"router-prod"},
			providers: map[string]ProviderEntry{"router-prod": {Type: "openrouter"}},
			want: map[string]string{
				"router-prod": "providers.router-prod.api_key (or OPENROUTER_API_KEY env)",
			},
		},
		{
			name:      "non openrouter",
			names:     []string{"anthropic"},
			providers: map[string]ProviderEntry{"anthropic": {Type: "anthropic"}},
		},
		{
			name:      "empty type defaults to openai",
			names:     []string{"default"},
			providers: map[string]ProviderEntry{"default": {}},
		},
		{
			name:      "valid key",
			names:     []string{"openrouter"},
			providers: map[string]ProviderEntry{"openrouter": {Type: "openrouter", APIKey: validKey}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProviderCredentialMissing(tc.names, tc.providers)
			if len(got) != len(tc.want) {
				t.Fatalf("ProviderCredentialMissing() = %v, want %v", got, tc.want)
			}
			for name, wantLocation := range tc.want {
				if got[name] != wantLocation {
					t.Fatalf("ProviderCredentialMissing()[%q] = %q, want %q", name, got[name], wantLocation)
				}
			}
			if len(tc.want) == 0 && got != nil {
				t.Fatalf("ProviderCredentialMissing() = %#v, want nil", got)
			}
		})
	}

	t.Run("fresh result", func(t *testing.T) {
		names := []string{"openrouter"}
		providers := map[string]ProviderEntry{"openrouter": {Type: "openrouter"}}
		first := ProviderCredentialMissing(names, providers)
		first["openrouter"] = "mutated"
		second := ProviderCredentialMissing(names, providers)
		if got := second["openrouter"]; got != location {
			t.Fatalf("result map shared between calls: got %q, want %q", got, location)
		}
	})
}

func TestOpenRouterAPIKeyWellFormed(t *testing.T) {
	prefix := "sk-or-"
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty"},
		{name: "whitespace", raw: " \t\n "},
		{name: "wrong prefix", raw: "not-or-" + strings.Repeat("a", 20)},
		{name: "case sensitive prefix", raw: "SK-OR-" + strings.Repeat("a", 20)},
		{name: "19 byte boundary", raw: prefix + strings.Repeat("a", 13)},
		{name: "20 byte boundary", raw: prefix + strings.Repeat("a", 14), want: true},
		{name: "bare placeholder", raw: "${OPENROUTER_API_KEY}"},
		{name: "long partial placeholder", raw: "sk-or-v1-${KEY_SUFFIX_UNSET}-padding"},
		{name: "valid key", raw: "sk-or-v1-credential-test-key", want: true},
		{name: "trimmed valid key", raw: " \tsk-or-v1-credential-test-key\n ", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := OpenRouterAPIKeyWellFormed(tc.raw); got != tc.want {
				t.Fatalf("OpenRouterAPIKeyWellFormed(%q) = %t, want %t", tc.raw, got, tc.want)
			}
		})
	}
}
