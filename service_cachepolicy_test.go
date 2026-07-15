package fizeau_test

import (
	"context"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestPublicCachePolicyValidationContract(t *testing.T) {
	_ = fizeau.ServiceExecuteRequest{CachePolicy: fizeau.CachePolicyOff}
	_ = fizeau.RouteRequest{CachePolicy: fizeau.CachePolicyDefault}

	for _, valid := range []string{"", fizeau.CachePolicyDefault, fizeau.CachePolicyOff} {
		if err := fizeau.ValidateCachePolicy(valid); err != nil {
			t.Errorf("ValidateCachePolicy(%q) = %v, want nil", valid, err)
		}
	}
	for _, invalid := range []string{"on", "aggressive", "Default", "OFF", "auto"} {
		t.Run(invalid, func(t *testing.T) {
			want := `invalid CachePolicy "` + invalid + `": want "", "default", or "off"`
			if err := fizeau.ValidateCachePolicy(invalid); err == nil || err.Error() != want {
				t.Fatalf("ValidateCachePolicy(%q) = %v, want %q", invalid, err, want)
			}
		})
	}

	service := newProviderFacade(t, &providerFacadeConfig{})
	events, err := service.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt: "must fail before dispatch", CachePolicy: "aggressive",
	})
	if events != nil || err == nil || err.Error() != `invalid CachePolicy "aggressive": want "", "default", or "off"` {
		t.Fatalf("Execute events=%#v error=%v, want exact pre-dispatch CachePolicy failure", events, err)
	}
}
