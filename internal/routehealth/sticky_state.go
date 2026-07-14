package routehealth

import (
	"strings"
	"time"

	"github.com/easel/fizeau/internal/provider/utilization"
	"github.com/easel/fizeau/internal/routing"
)

// StickyRequest is the internal sticky-route input needed to update lease
// state and surface public sticky evidence.
type StickyRequest struct {
	StickyKey      string
	Harness        string
	Provider       string
	Endpoint       string
	ServerInstance string
	Model          string
}

// StickyDecision is the internal sticky-route evidence projected back onto the
// root RouteDecision type.
type StickyDecision struct {
	KeyPresent     bool
	Assignment     string
	ServerInstance string
	Reason         string
	Bonus          float64
}

// StickyState owns sticky route leases plus endpoint utilization samples.
type StickyState struct {
	leases      *LeaseStore
	utilization *UtilizationStore
}

// NewStickyState returns an empty sticky-route state bundle.
func NewStickyState() *StickyState {
	return &StickyState{
		leases:      NewLeaseStore(),
		utilization: NewUtilizationStore(),
	}
}

func (s *StickyState) leaseStore() *LeaseStore {
	if s == nil {
		return nil
	}
	if s.leases == nil {
		s.leases = NewLeaseStore()
	}
	return s.leases
}

func (s *StickyState) utilizationStore() *UtilizationStore {
	if s == nil {
		return nil
	}
	if s.utilization == nil {
		s.utilization = NewUtilizationStore()
	}
	return s.utilization
}

// ApplyStickyLease updates sticky-route state for the selected candidate and
// returns the sticky evidence the root package should surface publicly.
func (s *StickyState) ApplyStickyLease(now time.Time, req StickyRequest) StickyDecision {
	if strings.TrimSpace(req.StickyKey) == "" {
		return StickyDecision{}
	}

	decision := StickyDecision{KeyPresent: true}
	if req.Harness != "fiz" || strings.TrimSpace(req.Provider) == "" {
		decision.Assignment = "not_applicable"
		return decision
	}

	baseProvider := strings.TrimSpace(req.Provider)
	if base, _, ok := splitProviderRef(baseProvider); ok {
		baseProvider = base
	}
	if baseProvider == "" || strings.TrimSpace(req.Model) == "" {
		return decision
	}

	selectedServer := normalizeStickyServerInstance(req.Provider, req.Endpoint, req.ServerInstance)
	if selectedServer == "" {
		decision.Assignment = "none"
		return decision
	}

	key := NormalizeLeaseKey(req.StickyKey)
	store := s.leaseStore()
	if lease, ok := store.Live(now, key); ok && lease.Endpoint == selectedServer {
		decision.Assignment = "reused"
		decision.ServerInstance = selectedServer
		decision.Bonus = routing.StickyAffinityBonus
		decision.Reason = "live sticky lease reused"
	} else if ok {
		decision.Assignment = "moved"
		decision.ServerInstance = selectedServer
		decision.Reason = "sticky server instance lost to a stronger candidate"
	} else {
		decision.Assignment = "acquired"
		decision.ServerInstance = selectedServer
		decision.Reason = "new sticky lease acquired"
	}

	store.Acquire(now, DefaultLeaseTTL, key, baseProvider, selectedServer, req.Model)
	return decision
}

// RecordUtilization retains the latest provider endpoint/model utilization
// sample for routing and public evidence projection.
func (s *StickyState) RecordUtilization(provider, endpoint, model string, sample utilization.EndpointUtilization) {
	s.utilizationStore().Record(provider, endpoint, model, sample)
}

// UtilizationSample returns the latest provider endpoint/model utilization
// sample, including the provider-wide fallback owned by the utilization store.
func (s *StickyState) UtilizationSample(provider, endpoint, model string) (utilization.EndpointUtilization, bool) {
	return s.utilizationStore().Sample(provider, endpoint, model)
}

// EndpointLoadResolver returns the routing-engine load resolver that combines
// lease counts with fresh endpoint utilization.
func (s *StickyState) EndpointLoadResolver(now time.Time) func(provider, endpoint, model string) (routing.EndpointLoad, bool) {
	if s == nil {
		return nil
	}
	leaseStore := s.leaseStore()
	utilStore := s.utilizationStore()
	return func(provider, endpoint, model string) (routing.EndpointLoad, bool) {
		leaseCounts := leaseStore.LeaseCounts(now, provider, model)
		loads := utilStore.EndpointLoads(provider, model, leaseCounts)
		load, ok := loads[endpoint]
		if !ok {
			if count, ok := leaseCounts[endpoint]; ok {
				return routing.EndpointLoad{
					LeaseCount:       count,
					NormalizedLoad:   float64(count),
					UtilizationFresh: false,
				}, true
			}
			return routing.EndpointLoad{}, false
		}
		return routing.EndpointLoad{
			LeaseCount:           load.LeaseCount,
			NormalizedLoad:       load.NormalizedLoad,
			UtilizationFresh:     load.UtilizationFresh,
			UtilizationSaturated: load.UtilizationSaturated,
		}, true
	}
}

// StickyServerInstanceResolver returns the sticky-key to server-instance lookup
// used during candidate scoring.
func (s *StickyState) StickyServerInstanceResolver(now time.Time) func(stickyKey string) (string, bool) {
	if s == nil {
		return nil
	}
	store := s.leaseStore()
	return func(stickyKey string) (string, bool) {
		if strings.TrimSpace(stickyKey) == "" {
			return "", false
		}
		lease, ok := store.Live(now, NormalizeLeaseKey(stickyKey))
		if !ok || lease.Endpoint == "" {
			return "", false
		}
		return lease.Endpoint, true
	}
}

func normalizeStickyServerInstance(provider, endpoint, serverInstance string) string {
	if server := strings.TrimSpace(serverInstance); server != "" {
		return server
	}
	if endpoint = strings.TrimSpace(endpoint); endpoint != "" {
		return endpoint
	}
	if _, endpoint, ok := splitProviderRef(provider); ok {
		return endpoint
	}
	return strings.TrimSpace(provider)
}
