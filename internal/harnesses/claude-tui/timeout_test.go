package claudetui

import (
	"testing"
	"time"
)

func TestEffectiveTurnTimeoutHonorsRequestTimeout(t *testing.T) {
	if got := effectiveTurnTimeout(17 * time.Minute); got != 17*time.Minute {
		t.Fatalf("effectiveTurnTimeout(request) = %v, want 17m", got)
	}
	if got := effectiveTurnTimeout(0); got != 5*time.Minute {
		t.Fatalf("effectiveTurnTimeout(default) = %v, want 5m", got)
	}
}
