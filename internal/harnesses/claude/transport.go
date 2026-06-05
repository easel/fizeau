package claude

import (
	"os"
	"strings"
)

// NativeTransportSelected reports whether the claude harness should use the
// native Anthropic Messages API transport.
//
// Only an explicit "native" value flips it on; every other value (including
// empty and "subprocess") keeps the default subprocess path.
func NativeTransportSelected() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("FIZEAU_CLAUDE_TRANSPORT")), "native")
}
