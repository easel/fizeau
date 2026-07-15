package fizeau

import (
	"context"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/routingquality"
)

// TestUsageReportRoutingQualityNoLogDir is the narrow root adapter seam between
// the public UsageReport projection and the private in-memory fallback. Session
// log scanning and ring aggregation mechanics are owned by their internal
// packages.
func TestUsageReportRoutingQualityNoLogDir(t *testing.T) {
	svc := &service{routingQuality: routingquality.NewStore(routingquality.DefaultStoreCap)}
	// Pre-load the ring with one no-override and one override request so
	// the metric is computable.
	svc.routingQuality.RecordRequest(time.Now().UTC(), nil)
	ov := ServiceOverrideData{
		AxesOverridden: []string{"model"},
		MatchPerAxis:   map[string]bool{"model": false},
	}
	svc.routingQuality.RecordRequest(time.Now().UTC(), toRoutingQualityOverride(ov))

	rep, err := svc.UsageReport(context.Background(), UsageReportOptions{})
	if err != nil {
		t.Fatalf("UsageReport: %v", err)
	}
	if rep.RoutingQuality.TotalRequests != 2 {
		t.Fatalf("TotalRequests = %d, want 2 (ring fallback dropped)", rep.RoutingQuality.TotalRequests)
	}
	if rep.RoutingQuality.TotalOverrides != 1 {
		t.Fatalf("TotalOverrides = %d, want 1", rep.RoutingQuality.TotalOverrides)
	}
}
