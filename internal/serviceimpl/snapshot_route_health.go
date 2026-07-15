package serviceimpl

import (
	"time"

	"github.com/easel/fizeau/internal/modelsnapshot"
	"github.com/easel/fizeau/internal/routehealth"
)

// ProviderCooldownsFromSnapshotErrors adapts snapshot discovery-source
// metadata to the routing-health projection. Classification, provider-name
// matching, recency, and TTL behavior remain owned by routehealth.
func ProviderCooldownsFromSnapshotErrors(snapshot modelsnapshot.ModelSnapshot, providerNames []string, now time.Time, ttl time.Duration) map[string]time.Time {
	return routehealth.ProviderCooldownsFromSnapshotErrors(
		snapshotRouteHealthSources(snapshot),
		providerNames,
		now,
		ttl,
	)
}

func snapshotRouteHealthSources(snapshot modelsnapshot.ModelSnapshot) []routehealth.SnapshotSource {
	if len(snapshot.Sources) == 0 {
		return nil
	}
	sources := make([]routehealth.SnapshotSource, 0, len(snapshot.Sources))
	for name, meta := range snapshot.Sources {
		sources = append(sources, routehealth.SnapshotSource{
			Name:            name,
			Error:           meta.Error,
			LastRefreshedAt: meta.LastRefreshedAt,
		})
	}
	return sources
}
