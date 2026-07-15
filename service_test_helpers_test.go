package fizeau

import (
	"context"
	"os/exec"
	"testing"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routehealth"
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

func resetProviderProbeForTest(svc *service) {
	svc.providerProbe = routehealth.NewProbeStore()
	svc.aliveness = routehealth.NewAlivenessCoordinator(routehealth.AlivenessCoordinatorOptions{
		Store:   svc.providerProbe,
		Prober:  routehealth.AlivenessProber(svc.opts.AlivenessProber),
		Persist: svc.persistRouteHealthSnapshot,
	})
}

func canceledRefreshContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// stubSubscriptionHarnessLookPath makes the given binaries discoverable via the
// registry's LookPath seam and reports every other binary as missing, so the
// test is hermetic regardless of which CLIs are installed on the host.
func stubSubscriptionHarnessLookPath(svc *service, available ...string) {
	set := make(map[string]struct{}, len(available))
	for _, name := range available {
		set[name] = struct{}{}
	}
	svc.registry.LookPath = func(file string) (string, error) {
		if _, ok := set[file]; ok {
			return "/usr/local/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
}

// stubSubprocessHarnessModelIDs replaces the package-level model-ID resolver
// with a hermetic map for the duration of the test, so tests outside model
// inventory do not depend on launching real CLIs via PTY.
func stubSubprocessHarnessModelIDs(t *testing.T, byHarness map[string][]string) {
	t.Helper()
	prev := subprocessHarnessModelIDs
	t.Cleanup(func() { subprocessHarnessModelIDs = prev })
	subprocessHarnessModelIDs = func(name string, _ harnesses.HarnessConfig) []string {
		return byHarness[name]
	}
}

func TestNewTestServiceInitializesCommonRuntimeState(t *testing.T) {
	svc := newTestService(t, ServiceOptions{})
	if svc.registry == nil {
		t.Fatal("registry is nil")
	}
}
