package harnesses

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/reasoning"
)

const EmbeddedDiscoverySource = "embedded-cassette"

type ModelDiscoveryLoader func(harnessName, source string) (ModelDiscoverySnapshot, error)

type discoveryCacheKey struct {
	harnessName string
	source      string
}

type discoveryCacheEntry struct {
	snapshot ModelDiscoverySnapshot
	err      error
}

// ModelDiscoveryCache memoizes in-process discovery snapshots by harness and
// source. The embedded source is populated from the registry's cassette-backed
// model lists; live PTY refresh is intentionally out of scope here.
type ModelDiscoveryCache struct {
	mu      sync.Mutex
	loader  ModelDiscoveryLoader
	entries map[discoveryCacheKey]discoveryCacheEntry
}

func NewModelDiscoveryCache(loader ModelDiscoveryLoader) *ModelDiscoveryCache {
	if loader == nil {
		loader = LoadEmbeddedModelDiscoverySnapshot
	}
	return &ModelDiscoveryCache{
		loader:  loader,
		entries: make(map[discoveryCacheKey]discoveryCacheEntry),
	}
}

func (c *ModelDiscoveryCache) Snapshot(harnessName, source string) (ModelDiscoverySnapshot, error) {
	if c == nil {
		c = NewModelDiscoveryCache(nil)
	}
	key := discoveryCacheKey{
		harnessName: strings.TrimSpace(harnessName),
		source:      strings.TrimSpace(source),
	}
	if key.source == "" {
		key.source = EmbeddedDiscoverySource
	}
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return entry.snapshot, entry.err
	}
	loader := c.loader
	c.mu.Unlock()

	snapshot, err := loader(key.harnessName, key.source)

	c.mu.Lock()
	c.entries[key] = discoveryCacheEntry{snapshot: snapshot, err: err}
	c.mu.Unlock()
	return snapshot, err
}

var defaultDiscoveryCache = NewModelDiscoveryCache(nil)

func CachedModelDiscoverySnapshot(harnessName, source string) (ModelDiscoverySnapshot, error) {
	return defaultDiscoveryCache.Snapshot(harnessName, source)
}

func LoadEmbeddedModelDiscoverySnapshot(harnessName, source string) (ModelDiscoverySnapshot, error) {
	return ModelDiscoverySnapshot{}, fmt.Errorf("embedded model discovery is not supported; harness discovery is live-only via PTY")
}

type RunnerModelResolution struct {
	HarnessName       string `json:"harness_name"`
	RequestedModel    string `json:"requested_model,omitempty"`
	ResolvedModel     string `json:"resolved_model"`
	PriorDefaultModel string `json:"prior_default_model,omitempty"`
	Source            string `json:"source"`
	Surface           string `json:"surface"`
	Warning           string `json:"warning,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ExplicitPin       bool   `json:"explicit_pin,omitempty"`
}

type modelCandidate struct {
	model  string
	parsed modelcatalog.ParsedModel
	power  int
	order  int
}

func ResolveRunnerModel(harnessName string, surface modelcatalog.Surface, requestedModel, fallbackDefault string) RunnerModelResolution {
	return resolveRunnerModel(defaultDiscoveryCache, harnessName, surface, requestedModel, fallbackDefault)
}

func ResolveRunnerModelWithCache(cache *ModelDiscoveryCache, harnessName string, surface modelcatalog.Surface, requestedModel, fallbackDefault string) RunnerModelResolution {
	if cache == nil {
		cache = defaultDiscoveryCache
	}
	return resolveRunnerModel(cache, harnessName, surface, requestedModel, fallbackDefault)
}

func resolveRunnerModel(cache *ModelDiscoveryCache, harnessName string, surface modelcatalog.Surface, requestedModel, fallbackDefault string) RunnerModelResolution {
	resolution := RunnerModelResolution{
		HarnessName:       harnessName,
		RequestedModel:    requestedModel,
		PriorDefaultModel: fallbackDefault,
		Surface:           string(surface),
		Source:            EmbeddedDiscoverySource,
	}

	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel != "" {
		resolution.ResolvedModel = requestedModel
		resolution.ExplicitPin = true
		snapshot, err := cache.Snapshot(harnessName, EmbeddedDiscoverySource)
		if err != nil {
			resolution.Warning = fmt.Sprintf("model discovery unavailable for explicit pin validation: %v", err)
			resolution.Reason = "explicit_pin_discovery_unavailable"
			return resolution
		}
		resolution.Source = snapshot.Source
		if !stringInSliceFold(requestedModel, snapshot.Models) {
			resolution.Warning = fmt.Sprintf("explicit model pin %q was not present in %s snapshot", requestedModel, snapshot.Source)
			resolution.Reason = "explicit_pin_absent_from_snapshot"
		}
		return resolution
	}

	snapshot, err := cache.Snapshot(harnessName, EmbeddedDiscoverySource)
	if err != nil {
		resolution.ResolvedModel = fallbackDefault
		resolution.Warning = fmt.Sprintf("model discovery lookup failed; using fallback default %q: %v", fallbackDefault, err)
		resolution.Reason = "discovery_lookup_failed"
		return resolution
	}
	resolution.Source = snapshot.Source

	catalog, err := modelcatalog.Default()
	if err != nil {
		resolution.ResolvedModel = fallbackDefault
		resolution.Warning = fmt.Sprintf("model catalog load failed; using fallback default %q: %v", fallbackDefault, err)
		resolution.Reason = "catalog_load_failed"
		return resolution
	}

	candidates := autoRoutableSnapshotCandidates(catalog, snapshot, surface)
	if len(candidates) == 0 {
		resolution.ResolvedModel = fallbackDefault
		resolution.Warning = fmt.Sprintf("model discovery snapshot %s had no auto-routable catalog candidates; using fallback default %q", snapshot.Source, fallbackDefault)
		resolution.Reason = "no_auto_routable_discovery_candidate"
		return resolution
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if cmp := candidates[i].parsed.Compare(candidates[j].parsed); cmp != 0 {
			return cmp < 0
		}
		if candidates[i].power != candidates[j].power {
			return candidates[i].power > candidates[j].power
		}
		return candidates[i].order < candidates[j].order
	})

	resolution.ResolvedModel = candidates[0].model
	if resolution.ResolvedModel != fallbackDefault {
		resolution.Reason = "discovery_default_changed"
	}
	return resolution
}

func autoRoutableSnapshotCandidates(catalog *modelcatalog.Catalog, snapshot ModelDiscoverySnapshot, surface modelcatalog.Surface) []modelCandidate {
	seen := make(map[string]bool)
	candidates := make([]modelCandidate, 0, len(snapshot.Models))
	for i, modelID := range snapshot.Models {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		eligibility, ok := catalog.ModelEligibility(modelID)
		if !ok || !eligibility.AutoRoutable {
			continue
		}
		resolved, err := catalog.Resolve(modelID, modelcatalog.ResolveOptions{Surface: surface})
		if err != nil || resolved.ConcreteModel == "" {
			continue
		}
		if seen[resolved.ConcreteModel] {
			continue
		}
		seen[resolved.ConcreteModel] = true
		candidates = append(candidates, modelCandidate{
			model:  resolved.ConcreteModel,
			parsed: modelcatalog.Parse(resolved.ConcreteModel),
			power:  eligibility.Power,
			order:  i,
		})
	}
	return candidates
}

func stringInSliceFold(needle string, haystack []string) bool {
	for _, candidate := range haystack {
		if strings.EqualFold(strings.TrimSpace(candidate), needle) {
			return true
		}
	}
	return false
}

func RunnerDefaultResolutionEvent(resolution RunnerModelResolution, metadata map[string]string, seq *int64) Event {
	raw, err := json.Marshal(resolution)
	if err != nil {
		raw = []byte(`{"warning":"marshal runner default resolution"}`)
	}
	ev := Event{
		Type:     EventTypeRoutingDecision,
		Time:     time.Now().UTC(),
		Metadata: metadata,
		Data:     raw,
	}
	if seq != nil {
		ev.Sequence = *seq
		*seq++
	}
	return ev
}

func ShouldEmitRunnerDefaultResolution(resolution RunnerModelResolution) bool {
	return resolution.Warning != "" ||
		(!resolution.ExplicitPin && resolution.PriorDefaultModel != "" && resolution.ResolvedModel != "" && resolution.ResolvedModel != resolution.PriorDefaultModel)
}

func ResolveRunnerReasoning(harnessName, requestedReasoning string) ReasoningActual {
	return ResolveRunnerReasoningWithCache(defaultDiscoveryCache, harnessName, requestedReasoning)
}

func ResolveRunnerReasoningWithCache(cache *ModelDiscoveryCache, harnessName, requestedReasoning string) ReasoningActual {
	if cache == nil {
		cache = defaultDiscoveryCache
	}
	resolution := ReasoningActual{
		Harness:            strings.TrimSpace(harnessName),
		RequestedReasoning: strings.TrimSpace(requestedReasoning),
		Source:             string(reasoning.ResolutionSourceCaller),
	}
	policy, err := reasoning.ParseString(requestedReasoning)
	if err != nil {
		resolution.ResolvedReasoning = requestedReasoning
		return resolution
	}
	if policy.Kind == reasoning.KindUnset || policy.Kind == reasoning.KindAuto {
		resolution.Source = string(reasoning.ResolutionSourceDefault)
	}

	snapshot, err := cache.Snapshot(harnessName, EmbeddedDiscoverySource)
	if err != nil || len(snapshot.ReasoningLevels) == 0 {
		resolution.ResolvedReasoning = adapterReasoningPolicy(policy, requestedReasoning)
		return resolution
	}
	resolution.DiscoverySource = snapshot.Source
	resolution.SupportedReasoning = append([]string(nil), snapshot.ReasoningLevels...)

	supported, err := reasoning.ResolveAgainstSupportedLevels(policy, snapshot.ReasoningLevels)
	if err != nil {
		resolution.ResolvedReasoning = adapterReasoningPolicy(policy, requestedReasoning)
		return resolution
	}
	resolution.ResolvedReasoning = adapterReasoningPolicy(supported.Policy, requestedReasoning)
	resolution.Source = string(supported.Source)
	resolution.Reason = supported.Reason
	resolution.Warning = supported.Warning
	return resolution
}

func adapterReasoningPolicy(policy reasoning.Policy, fallback string) string {
	switch policy.Kind {
	case reasoning.KindUnset, reasoning.KindAuto, reasoning.KindOff:
		return ""
	case reasoning.KindTokens:
		if policy.Tokens == 0 {
			return ""
		}
		return string(policy.Value)
	case reasoning.KindNamed:
		return string(policy.Value)
	default:
		return fallback
	}
}

func RunnerReasoningResolutionEvent(resolution ReasoningActual, metadata map[string]string, seq *int64) Event {
	raw, err := json.Marshal(resolution)
	if err != nil {
		raw = []byte(`{"warning":"marshal runner reasoning resolution"}`)
	}
	ev := Event{
		Type:     EventTypeRoutingDecision,
		Time:     time.Now().UTC(),
		Metadata: metadata,
		Data:     raw,
	}
	if seq != nil {
		ev.Sequence = *seq
		*seq++
	}
	return ev
}

func ShouldEmitRunnerReasoningResolution(resolution ReasoningActual) bool {
	return resolution.ResolvedReasoning != "" || resolution.Warning != ""
}

func LogRunnerReasoningWarning(resolution ReasoningActual) {
	if resolution.Warning == "" {
		return
	}
	slog.Warn("harness reasoning effort snapped",
		"harness", resolution.Harness,
		"requested_reasoning", resolution.RequestedReasoning,
		"resolved_reasoning", resolution.ResolvedReasoning,
		"reason", resolution.Reason,
		"discovery_source", resolution.DiscoverySource,
		"supported_reasoning", resolution.SupportedReasoning,
	)
}
