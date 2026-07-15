package serviceimpl

import (
	"reflect"
	"testing"

	"github.com/easel/fizeau/internal/routing"
)

func TestRoutingSurfacePreference(t *testing.T) {
	t.Run("truthy disables preference", func(t *testing.T) {
		for _, raw := range []string{
			"1", "true", "yes", "on",
			" 1 ", " TRUE ", "\tYeS\n", " On ",
		} {
			t.Run(raw, func(t *testing.T) {
				got := RoutingSurfacePreference(raw)
				if got == nil {
					t.Fatalf("RoutingSurfacePreference(%q) = nil, want explicit empty map", raw)
				}
				if len(got) != 0 {
					t.Fatalf("RoutingSurfacePreference(%q) = %v, want empty map", raw, got)
				}
			})
		}
	})

	t.Run("nontruthy retains default", func(t *testing.T) {
		want := routing.DefaultSurfacePreference()
		for _, raw := range []string{
			"", " ", "0", "false", "no", "off", "maybe", "claude",
		} {
			t.Run(raw, func(t *testing.T) {
				got := RoutingSurfacePreference(raw)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("RoutingSurfacePreference(%q) = %v, want %v", raw, got, want)
				}
			})
		}
	})

	t.Run("returns fresh maps", func(t *testing.T) {
		defaultPreference := RoutingSurfacePreference("")
		defaultPreference["claude"] = "mutated"
		defaultPreference["other"] = "mutated"
		if got, want := RoutingSurfacePreference(""), routing.DefaultSurfacePreference(); !reflect.DeepEqual(got, want) {
			t.Fatalf("default map shared between calls: got %v, want %v", got, want)
		}

		disabledPreference := RoutingSurfacePreference("true")
		disabledPreference["claude"] = "mutated"
		got := RoutingSurfacePreference("true")
		if got == nil || len(got) != 0 {
			t.Fatalf("disabled map shared between calls: got %v, want explicit empty map", got)
		}
	})
}
