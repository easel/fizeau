package fizeau_test

import (
	"context"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestPublicPowerBoundsValidationContract(t *testing.T) {
	_ = fizeau.ServiceExecuteRequest{MinPower: 3, MaxPower: 8}
	_ = fizeau.RouteRequest{MinPower: 3, MaxPower: 8}

	for _, test := range []struct {
		name string
		min  int
		max  int
	}{
		{name: "unset"},
		{name: "min only", min: 5},
		{name: "max only", max: 8},
		{name: "range", min: 3, max: 8},
		{name: "same", min: 7, max: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := fizeau.ValidatePowerBounds(test.min, test.max); err != nil {
				t.Fatalf("ValidatePowerBounds(%d, %d) = %v", test.min, test.max, err)
			}
		})
	}

	for _, test := range []struct {
		name string
		min  int
		max  int
		want string
	}{
		{name: "negative min", min: -1, want: "invalid MinPower -1: must be >= 0"},
		{name: "negative max", max: -1, want: "invalid MaxPower -1: must be >= 0"},
		{name: "max below min", min: 8, max: 3, want: "invalid power bounds: MaxPower 3 must be >= MinPower 8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := fizeau.ValidatePowerBounds(test.min, test.max); err == nil || err.Error() != test.want {
				t.Fatalf("ValidatePowerBounds(%d, %d) = %v, want %q", test.min, test.max, err, test.want)
			}
		})
	}

	service := newProviderFacade(t, &providerFacadeConfig{})
	events, err := service.Execute(context.Background(), fizeau.ServiceExecuteRequest{
		Prompt: "must fail before dispatch", MinPower: 9, MaxPower: 4,
	})
	if events != nil || err == nil || err.Error() != "invalid power bounds: MaxPower 4 must be >= MinPower 9" {
		t.Fatalf("Execute events=%#v error=%v, want exact invalid-power failure", events, err)
	}
	_, err = service.ResolveRoute(context.Background(), fizeau.RouteRequest{MinPower: -1})
	if err == nil || err.Error() != "invalid MinPower -1: must be >= 0" {
		t.Fatalf("ResolveRoute error=%v, want exact invalid MinPower failure", err)
	}
}
