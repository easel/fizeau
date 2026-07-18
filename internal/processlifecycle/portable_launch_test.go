package processlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type portableLaunchTestRecipe struct{ secret string }

func (portableLaunchTestRecipe) PortableRuntimeNamespaceRecipe() {}

func TestPortableActivationIdentityDriftPreventsGatedReleaseAndReleasesLease(t *testing.T) {
	drift := errors.New("activation identity changed")
	prepared := testPrepared()
	released := make(chan struct{}, 1)
	wrapped := revalidatingPreparedBoundary{
		PreparedBoundary: prepared,
		revalidate:       func() error { return drift },
		releaseLease:     func() { released <- struct{}{} },
	}
	_, err := Acquire(context.Background(), testOptions(&fakeClock{now: time.Now()}), NewMemoryRegistry(), testPlatform(), wrapped)
	if !errors.Is(err, drift) {
		t.Fatalf("Acquire() error = %v, want identity drift", err)
	}
	if prepared.released || prepared.started {
		t.Fatalf("identity drift released the gated launcher: released=%v started=%v", prepared.released, prepared.started)
	}
	if !prepared.aborted {
		t.Fatal("identity drift did not abort the prepared boundary")
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("identity drift did not release the portable root lease")
	}
	select {
	case <-released:
		t.Fatal("identity drift released the portable root lease more than once")
	default:
	}
}

func TestPortableActivationIdentityRevalidationAllowsGatedRelease(t *testing.T) {
	prepared := testPrepared()
	wrapped := revalidatingPreparedBoundary{
		PreparedBoundary: prepared,
		revalidate:       func() error { return nil },
	}
	if _, err := Acquire(context.Background(), testOptions(&fakeClock{now: time.Now()}), NewMemoryRegistry(), testPlatform(), wrapped); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if !prepared.released || !prepared.started || prepared.aborted {
		t.Fatalf("normal identity did not release gated launcher: released=%v started=%v aborted=%v", prepared.released, prepared.started, prepared.aborted)
	}
}

func TestPortableLaunchAttachmentOwnsExactChildSpecification(t *testing.T) {
	const secret = "portable-recipe-secret-must-not-leak"
	attachment, err := NewPortableLaunchAttachment(
		"/guest/runtime/bin/tool", []string{"run", "request"},
		[]string{"HOME=/guest/home", "TOKEN=not-the-recipe-secret"},
		portableLaunchTestRecipe{secret: secret},
	)
	if err != nil {
		t.Fatalf("NewPortableLaunchAttachment: %v", err)
	}

	text := attachment.String()
	encoded, err := json.Marshal(attachment)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	for _, forbidden := range []string{"/guest/runtime/bin/tool", "request", "TOKEN", secret} {
		if strings.Contains(text, forbidden) || strings.Contains(string(encoded), forbidden) {
			t.Fatalf("diagnostic leaked %q: text=%q json=%s", forbidden, text, encoded)
		}
	}

	target, err := attachment.commandForPTY("/guest/runtime/bin/tool", []string{"run", "request"}, []string{"HOME=/guest/home", "TOKEN=not-the-recipe-secret"})
	if err != nil {
		t.Fatalf("commandForPTY: %v", err)
	}
	if err := validatePortableLaunchTarget(target, attachment); err != nil {
		t.Fatalf("validatePortableLaunchTarget: %v", err)
	}
}

func TestPortableLaunchAttachmentFailsClosed(t *testing.T) {
	recipe := portableLaunchTestRecipe{}
	for _, tc := range []struct {
		name        string
		command     string
		arguments   []string
		environment []string
		recipe      PortableLaunchRecipe
	}{
		{name: "relative command", command: "tool", environment: []string{}},
		{name: "aliased command", command: "/guest/runtime/../tool", environment: []string{}},
		{name: "inherited environment", command: "/guest/tool", environment: nil},
		{name: "duplicate environment", command: "/guest/tool", environment: []string{"HOME=/a", "HOME=/b"}},
		{name: "malformed environment", command: "/guest/tool", environment: []string{"HOME"}},
		{name: "nul argument", command: "/guest/tool", arguments: []string{"bad\x00argument"}, environment: []string{}},
		{name: "missing recipe", command: "/guest/tool", environment: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := tc.recipe
			if tc.name != "missing recipe" {
				candidate = recipe
			}
			if _, err := NewPortableLaunchAttachment(tc.command, tc.arguments, tc.environment, candidate); err == nil {
				t.Fatal("NewPortableLaunchAttachment accepted unsafe input")
			}
		})
	}
}

func TestPortableLaunchAttachmentRejectsAliasedTarget(t *testing.T) {
	attachment, err := NewPortableLaunchAttachment("/guest/runtime/bin/tool", []string{"request"}, []string{"HOME=/guest/home"}, portableLaunchTestRecipe{})
	if err != nil {
		t.Fatalf("NewPortableLaunchAttachment: %v", err)
	}
	target, err := attachment.commandForPTY("/guest/runtime/bin/tool", []string{"request"}, []string{"HOME=/guest/home"})
	if err != nil {
		t.Fatalf("commandForPTY: %v", err)
	}
	target.Args[0] = "/guest/runtime/bin/alias"
	if err := validatePortableLaunchTarget(target, attachment); err == nil {
		t.Fatal("validatePortableLaunchTarget accepted aliased argv[0]")
	}
	if _, err := attachment.commandForPTY("tool", []string{"request"}, []string{"HOME=/guest/home"}); err == nil {
		t.Fatal("commandForPTY accepted PATH-resolved command alias")
	}
}
