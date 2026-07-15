package serviceimpl

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routehealth"
)

func TestProviderCooldownsFromSnapshotErrorsAdaptsSnapshotSources(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if got := ProviderCooldownsFromSnapshotErrors(modelsnapshot.ModelSnapshot{}, nil, now, time.Minute); got != nil {
		t.Fatalf("nil snapshot adaptation = %v, want nil", got)
	}
	if got := ProviderCooldownsFromSnapshotErrors(modelsnapshot.ModelSnapshot{Sources: map[string]modelsnapshot.SourceMeta{}}, []string{"router"}, now, time.Minute); got != nil {
		t.Fatalf("empty snapshot adaptation = %v, want nil", got)
	}

	failedAt := now.Add(-10 * time.Second)
	authAt := now.Add(-5 * time.Second)
	snapshot := modelsnapshot.ModelSnapshot{
		Models: []modelsnapshot.KnownModel{{ID: "ignored-model"}},
		AsOf:   now.Add(-time.Hour),
		Sources: map[string]modelsnapshot.SourceMeta{
			"router-primary": {
				Error:           "dial tcp 10.0.0.8:443: connection refused",
				LastRefreshedAt: failedAt,
				Stale:           true,
			},
			"router-auth": {
				Error:           "unauthorized",
				LastRefreshedAt: authAt,
			},
		},
	}
	gotSources := snapshotRouteHealthSources(snapshot)
	sort.Slice(gotSources, func(i, j int) bool { return gotSources[i].Name < gotSources[j].Name })
	wantSources := []routehealth.SnapshotSource{
		{Name: "router-auth", Error: "unauthorized", LastRefreshedAt: authAt},
		{Name: "router-primary", Error: "dial tcp 10.0.0.8:443: connection refused", LastRefreshedAt: failedAt},
	}
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("adapted sources = %#v, want %#v", gotSources, wantSources)
	}

	got := ProviderCooldownsFromSnapshotErrors(snapshot, []string{"router"}, now, time.Minute)
	if len(got) != 1 || !got["router"].Equal(failedAt) {
		t.Fatalf("delegated cooldowns = %v, want router at %v", got, failedAt)
	}
}
