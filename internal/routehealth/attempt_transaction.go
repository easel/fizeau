package routehealth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

// AttemptTransaction supplies the state and callbacks for one route-attempt
// transaction. The callbacks keep root service configuration, catalog keys,
// and persistence paths outside this package while routehealth owns ordering.
type AttemptTransaction struct {
	Store         *Store
	RecordOptions RecordOptions
	Persist       func() error
	Dispatch      func(provider, endpoint string, err error)
}

// RecordAttempt updates process-local route health before invoking persistence.
func RecordAttempt(tx AttemptTransaction, attempt Attempt) error {
	if tx.Store == nil {
		return fmt.Errorf("route attempt store is required")
	}
	if err := tx.Store.RecordAttemptWithOptions(attempt, tx.RecordOptions); err != nil {
		return err
	}
	if tx.Persist != nil {
		return tx.Persist()
	}
	return nil
}

// ObserveFinalAttempt converts one harness final and applies its route-health
// effects in a fixed transaction order: memory, persistence, then eligible
// dispatch feedback. A persistence failure is captured and returned without
// suppressing dispatch feedback.
func ObserveFinalAttempt(tx AttemptTransaction, final harnesses.FinalData, mode FinalEvidenceMode) error {
	attempt, ok := AttemptFromFinal(final, mode)
	if !ok {
		return nil
	}
	if tx.Store == nil {
		return fmt.Errorf("route attempt store is required")
	}
	if err := tx.Store.RecordAttemptWithOptions(attempt, tx.RecordOptions); err != nil {
		return err
	}
	var persistErr error
	if tx.Persist != nil {
		persistErr = tx.Persist()
	}
	if tx.Dispatch != nil {
		if provider, endpoint, dispatchErr := DispatchFailureFromAttempt(attempt); dispatchErr != nil {
			tx.Dispatch(provider, endpoint, dispatchErr)
		}
	}
	return persistErr
}

// DispatchEndpoint is the API-neutral endpoint projection needed to select
// catalog cache URLs for dispatch feedback.
type DispatchEndpoint struct {
	Name    string
	BaseURL string
}

// DispatchProvider is the API-neutral provider projection needed for
// dispatch feedback. RecordCatalog is supplied by the root facade so concrete
// catalog key and cache types stay outside routehealth.
type DispatchProvider struct {
	BaseURL       string
	Endpoints     []DispatchEndpoint
	RecordCatalog func(baseURL string, err error)
}

// DispatchFeedback contains the injected ports used to apply one confirmed
// dispatch reachability failure.
type DispatchFeedback struct {
	IsReachabilityFailure func(error) bool
	LookupProvider        func(provider string) (DispatchProvider, bool)
	RecordProbe           func(provider, endpoint string, success bool, at time.Time)
	PersistProbe          func() error
	Now                   func() time.Time
}

// Record applies independently-confirmed reachability feedback to catalog and
// probe stores. Probe persistence errors are deliberately ignored: the route
// attempt transaction's earlier persistence result remains authoritative.
func (f DispatchFeedback) Record(provider, endpoint string, err error) {
	if err == nil || f.IsReachabilityFailure == nil || !f.IsReachabilityFailure(err) {
		return
	}
	providerName := strings.TrimSpace(provider)
	endpointName := strings.TrimSpace(endpoint)
	if base, embeddedEndpoint, ok := splitProviderRef(providerName); ok {
		providerName = base
		if endpointName == "" {
			endpointName = embeddedEndpoint
		}
	}

	now := time.Now().UTC()
	if f.Now != nil {
		now = f.Now().UTC()
	}

	if providerName != "" && f.LookupProvider != nil {
		if configured, ok := f.LookupProvider(providerName); ok && configured.RecordCatalog != nil {
			for _, baseURL := range DispatchProviderBaseURLs(configured, endpointName) {
				configured.RecordCatalog(baseURL, err)
			}
		}
	}

	if providerName != "" && f.RecordProbe != nil {
		f.RecordProbe(providerName, endpointName, false, now)
		if f.PersistProbe != nil {
			_ = f.PersistProbe()
		}
	}
}

// DispatchProviderBaseURLs returns configured catalog URLs for an endpoint.
// Empty endpoint feedback covers the primary URL followed by every named URL.
// An exact or unknown endpoint falls back to the primary URL only when no
// nonblank exact URL was found.
func DispatchProviderBaseURLs(provider DispatchProvider, endpoint string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(baseURL string) {
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			return
		}
		if _, duplicate := seen[baseURL]; duplicate {
			return
		}
		seen[baseURL] = struct{}{}
		out = append(out, baseURL)
	}
	if endpoint == "" {
		add(provider.BaseURL)
		for _, configured := range provider.Endpoints {
			add(configured.BaseURL)
		}
		return out
	}
	for _, configured := range provider.Endpoints {
		if configured.Name == endpoint {
			add(configured.BaseURL)
		}
	}
	if len(out) == 0 {
		add(provider.BaseURL)
	}
	return out
}

// DispatchFailureFromAttempt performs the stable failure-class half of the
// two-stage reachability gate. DispatchFeedback.Record independently applies
// the injected diagnostic/error classifier before mutating state.
func DispatchFailureFromAttempt(attempt Attempt) (string, string, error) {
	if strings.TrimSpace(attempt.Status) == "" || strings.TrimSpace(attempt.Provider) == "" {
		return "", "", nil
	}
	if Succeeded(strings.ToLower(strings.TrimSpace(attempt.Status))) {
		return "", "", nil
	}
	if !IsDispatchFailureClass(attempt.Reason) {
		return "", "", nil
	}
	message := strings.TrimSpace(attempt.Error)
	if message == "" {
		return "", "", nil
	}
	provider := strings.TrimSpace(attempt.Provider)
	endpoint := strings.TrimSpace(attempt.Endpoint)
	if base, embeddedEndpoint, ok := splitProviderRef(provider); ok {
		provider = base
		if endpoint == "" {
			endpoint = embeddedEndpoint
		}
	}
	return provider, endpoint, errors.New(message)
}
