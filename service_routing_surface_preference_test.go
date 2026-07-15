package fizeau

import (
	"context"
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routing"
)

func TestRoutingInputsWiresSurfacePreference(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", "")
		svc := newTestService(t, ServiceOptions{})

		inputs, _ := svc.routingInputs(context.Background(), nil, modelsnapshot.RefreshNone)
		if got, want := inputs.SurfacePreference, routing.DefaultSurfacePreference(); !reflect.DeepEqual(got, want) {
			t.Fatalf("routingInputs SurfacePreference = %v, want %v", got, want)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("FIZEAU_DISABLE_CLAUDE_TUI_DEFAULT", " true ")
		svc := newTestService(t, ServiceOptions{})

		inputs, _ := svc.routingInputs(context.Background(), nil, modelsnapshot.RefreshNone)
		if inputs.SurfacePreference == nil {
			t.Fatal("routingInputs SurfacePreference = nil, want explicit empty map")
		}
		if len(inputs.SurfacePreference) != 0 {
			t.Fatalf("routingInputs SurfacePreference = %v, want empty map", inputs.SurfacePreference)
		}
	})
}
