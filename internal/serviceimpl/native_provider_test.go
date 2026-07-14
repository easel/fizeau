package serviceimpl

import "testing"

func TestBuildNativeProviderUsesRegisteredFactory(t *testing.T) {
	provider := BuildNativeProvider(NativeProviderBuildInput{
		Name: "claude-api",
		Entry: ProviderEntry{
			Type:   "anthropic",
			APIKey: "test-key",
			Model:  "claude-test",
		},
	})
	if provider == nil {
		t.Fatal("registered anthropic provider factory returned nil")
	}

	if got := BuildNativeProvider(NativeProviderBuildInput{
		Name:  "broken",
		Entry: ProviderEntry{Type: "anthropic", ConfigError: "invalid"},
	}); got != nil {
		t.Fatalf("invalid provider config built %T, want nil", got)
	}

	if got := BuildNativeProvider(NativeProviderBuildInput{
		Name:  "unknown",
		Entry: ProviderEntry{Type: "not-a-provider"},
	}); got != nil {
		t.Fatalf("unknown provider type built %T, want nil", got)
	}
}
