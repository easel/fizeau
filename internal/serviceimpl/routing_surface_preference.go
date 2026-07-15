package serviceimpl

import (
	"strings"

	"github.com/easel/fizeau/internal/routing"
)

// RoutingSurfacePreference returns the surface-to-harness preference selected
// by the caller-supplied kill-switch value. Recognized truthy values disable
// the built-in preference with an explicit non-nil empty map; every other
// value retains a fresh copy of the routing default.
func RoutingSurfacePreference(rawKillSwitch string) map[string]string {
	switch strings.ToLower(strings.TrimSpace(rawKillSwitch)) {
	case "1", "true", "yes", "on":
		return map[string]string{}
	default:
		return routing.DefaultSurfacePreference()
	}
}
