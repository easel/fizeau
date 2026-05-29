package fizeau

import (
	"context"
	"os/exec"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
)

type testServiceOption func(*service)

func newTestService(t testing.TB, opts ServiceOptions, options ...testServiceOption) *service {
	t.Helper()

	registry := harnesses.NewRegistry()
	// Default to reporting no subprocess CLI binaries on PATH so the test
	// service's harness discovery is hermetic regardless of which agent CLIs are
	// installed on the host. Tests that need a harness discoverable override
	// registry.LookPath themselves (see stubSubscriptionHarnessLookPath).
	registry.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }

	svc := &service{
		opts:             opts,
		registry:         registry,
		harnessInstances: defaultHarnessInstances(),
	}
	for _, option := range options {
		option(svc)
	}
	return svc
}

func canceledRefreshContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestNewTestServiceInitializesCommonRuntimeState(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	if svc.registry == nil {
		t.Fatal("registry is nil")
	}
}
