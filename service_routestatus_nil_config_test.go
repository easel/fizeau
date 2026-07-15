package fizeau

import (
	"context"
	"testing"
)

func TestRouteStatusNilServiceConfigAdapter(t *testing.T) {
	// The external test package registers the config loader, which adapts even
	// an empty config into a non-nil ServiceConfig. Suppress it narrowly so this
	// test reaches the literal nil-config adapter branch.
	previousLoader := loadServiceConfig
	loadServiceConfig = nil
	t.Cleanup(func() { loadServiceConfig = previousLoader })

	refreshCtx, cancel := context.WithCancel(context.Background())
	cancel()
	publicService, err := New(ServiceOptions{QuotaRefreshContext: refreshCtx})
	if err != nil {
		t.Fatalf("New with nil ServiceConfig: %v", err)
	}

	report, err := publicService.RouteStatus(context.Background())
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if report == nil {
		t.Fatal("RouteStatus returned a nil report")
	}
	if len(report.Routes) != 0 {
		t.Fatalf("Routes = %#v, want no routes for nil ServiceConfig", report.Routes)
	}
	if report.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt is zero")
	}
}
