package modelcatalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/easel/fizeau/internal/reasoning"
	"github.com/easel/fizeau/internal/sampling"
)

// Surface identifies a consumer-specific concrete model naming surface.
type Surface string

const (
	SurfaceAgentOpenAI    Surface = "agent.openai"
	SurfaceAgentAnthropic Surface = "agent.anthropic"
	SurfaceCodex          Surface = "codex"
	SurfaceClaudeCode     Surface = "claude-code"
	SurfaceGemini         Surface = "gemini"
	SurfaceGrok           Surface = "grok"
	SurfaceEmbeddedOpenAI Surface = "embedded.openai"
)

// ResolveOptions configures compatibility model-reference resolution.
type ResolveOptions struct {
	Surface         Surface
	AllowDeprecated bool
}

// Catalog exposes the loaded v5 model catalog.
type Catalog struct {
	manifest    manifest
	manifestSrc string
}

// Metadata describes the loaded manifest.
type Metadata struct {
	ManifestSource  string
	ManifestVersion int
	CatalogVersion  string
}

// Policy describes one canonical catalog routing policy.
type Policy struct {
	Name       string
	MinPower   int
	MaxPower   int
	AllowLocal bool
	Require    []string
}

// Provider describes one catalog-declared provider system.
type Provider struct {
	Name             string
	Type             string
	IncludeByDefault bool
	Billing          BillingModel
}

// SurfacePolicy is kept as a narrow compatibility container for callers that
// have not yet moved reasoning defaults from surface projections to model entries.
type SurfacePolicy struct {
	ReasoningDefault       reasoning.Reasoning
	PlacementOrder         []string
	MaxInputCostPerMTokUSD *float64
	FailurePolicy          string
}

// ResolvedTarget is the compatibility output for model-reference resolution.
// In v5 CanonicalID is either a policy name or canonical model ID; there is no
// catalog alias concept behind it.
type ResolvedTarget struct {
	Ref                string
	Profile            string
	Family             string
	CanonicalID        string
	ConcreteModel      string
	SurfacePolicy      SurfacePolicy
	Deprecated         bool
	Replacement        string
	CatalogVersion     string
	ManifestSource     string
	ManifestVersion    int
	CostInputPerM      float64
	CostOutputPerM     float64
	CostCacheReadPerM  float64
	CostCacheWritePerM float64
	ContextWindow      int
	SWEBenchVerified   float64
	LiveCodeBench      float64
	BenchmarkAsOf      string
	OpenRouterRefID    string
}

// ModelEligibility describes whether a catalog model can participate in
// unpinned automatic routing or only explicit model pins.
type ModelEligibility struct {
	Power        int
	ExactPinOnly bool
	AutoRoutable bool
}

// Metadata returns the loaded manifest metadata for inspection surfaces.
func (c *Catalog) Metadata() Metadata {
	return Metadata{
		ManifestSource:  c.manifestSrc,
		ManifestVersion: c.manifest.Version,
		CatalogVersion:  c.manifest.CatalogVersion,
	}
}

// Policies returns all canonical policies in deterministic order.
func (c *Catalog) Policies() []Policy {
	names := make([]string, 0, len(c.manifest.Policies))
	for name := range c.manifest.Policies {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Policy, 0, len(names))
	for _, name := range names {
		policy, _ := c.Policy(name)
		out = append(out, policy)
	}
	return out
}

// Policy returns one canonical policy definition.
func (c *Catalog) Policy(name string) (Policy, bool) {
	name = strings.TrimSpace(name)
	entry, ok := c.manifest.Policies[name]
	if !ok {
		return Policy{}, false
	}
	return Policy{
		Name:       name,
		MinPower:   entry.MinPower,
		MaxPower:   entry.MaxPower,
		AllowLocal: entry.AllowLocal,
		Require:    append([]string(nil), entry.Require...),
	}, true
}

// Providers returns all catalog-declared provider systems in deterministic order.
func (c *Catalog) Providers() []Provider {
	names := make([]string, 0, len(c.manifest.Providers))
	for name := range c.manifest.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Provider, 0, len(names))
	for _, name := range names {
		entry := c.manifest.Providers[name]
		billing := BillingModel(entry.Billing)
		if billing == BillingModelUnknown {
			billing = BillingForProviderSystem(entry.Type)
			if billing == BillingModelUnknown {
				billing = BillingForHarness(entry.Type)
			}
		}
		out = append(out, Provider{
			Name:             name,
			Type:             entry.Type,
			IncludeByDefault: entry.IncludeByDefault,
			Billing:          billing,
		})
	}
	return out
}

// UnknownReferenceError indicates that a reference is not known to the catalog.
type UnknownReferenceError struct {
	Ref string
}

func (e *UnknownReferenceError) Error() string {
	return fmt.Sprintf("modelcatalog: unknown reference %q", e.Ref)
}

// MissingSurfaceError indicates that a model cannot be projected to the requested surface.
type MissingSurfaceError struct {
	CanonicalID string
	Surface     Surface
}

func (e *MissingSurfaceError) Error() string {
	return fmt.Sprintf("modelcatalog: reference %q has no mapping for surface %q", e.CanonicalID, e.Surface)
}

// DeprecatedTargetError indicates that a deprecated or stale model was resolved in strict mode.
type DeprecatedTargetError struct {
	CanonicalID       string
	Status            string
	Replacement       string
	SuggestedProfile  string
	SuggestedMinPower int
	SuggestedMaxPower int
}

func (e *DeprecatedTargetError) Error() string {
	if e.Replacement == "" {
		return fmt.Sprintf("modelcatalog: reference %q is %s", e.CanonicalID, e.Status)
	}
	return fmt.Sprintf("modelcatalog: reference %q is %s; use %q", e.CanonicalID, e.Status, e.Replacement)
}

// Current resolves a policy to its selected concrete model for a surface.
func (c *Catalog) Current(policy string, opts ResolveOptions) (ResolvedTarget, error) {
	return c.Resolve(policy, opts)
}

// Resolve resolves a canonical policy or model ID to a concrete model ID.
func (c *Catalog) Resolve(ref string, opts ResolveOptions) (ResolvedTarget, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ResolvedTarget{}, &UnknownReferenceError{Ref: ref}
	}
	if opts.Surface == "" {
		return ResolvedTarget{}, &MissingSurfaceError{CanonicalID: ref, Surface: opts.Surface}
	}
	if policy, ok := c.policyForReference(ref); ok {
		return c.resolvePolicy(ref, policy, opts)
	}
	if entry, ok := c.manifest.Models[ref]; ok && surfaceModelID(ref, entry, opts.Surface) == "" {
		return ResolvedTarget{}, &MissingSurfaceError{CanonicalID: ref, Surface: opts.Surface}
	}
	if modelID, entry, ok := c.lookupModelVariant(ref); ok && surfaceModelID(modelID, entry, opts.Surface) == "" {
		return ResolvedTarget{}, &MissingSurfaceError{CanonicalID: modelID, Surface: opts.Surface}
	}
	modelID, entry, concrete, ok := c.modelForSurface(ref, opts.Surface)
	if !ok {
		return ResolvedTarget{}, &UnknownReferenceError{Ref: ref}
	}
	return c.resolvedFromModel(ref, "", modelID, concrete, entry, opts)
}

func (c *Catalog) resolvePolicy(ref string, policy Policy, opts ResolveOptions) (ResolvedTarget, error) {
	displayName := policyDisplayNameForRef(ref, policy.Name)
	candidates := c.policyCandidates(policy, opts.Surface)
	if len(candidates) == 0 {
		return ResolvedTarget{}, &MissingSurfaceError{CanonicalID: displayName, Surface: opts.Surface}
	}
	best := candidates[0]
	return c.resolvedFromModel(ref, displayName, best.modelID, best.concrete, best.entry, opts)
}

type policyCandidate struct {
	modelID  string
	concrete string
	entry    ModelEntry
}

func (c *Catalog) policyCandidates(policy Policy, surface Surface) []policyCandidate {
	ids := make([]string, 0, len(c.manifest.Models))
	for id := range c.manifest.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]policyCandidate, 0, len(ids))
	for _, id := range ids {
		entry := c.manifest.Models[id]
		if !entry.AutoRoutable() {
			continue
		}
		if policy.MinPower > 0 && entry.Power < policy.MinPower {
			continue
		}
		if policy.MaxPower > 0 && entry.Power > policy.MaxPower {
			continue
		}
		if requiresNoRemote(policy) && !isLocalDeployment(entry.DeploymentClass) {
			continue
		}
		if !policy.AllowLocal && isLocalDeployment(entry.DeploymentClass) {
			continue
		}
		concrete := surfaceModelID(id, entry, surface)
		if concrete == "" {
			continue
		}
		out = append(out, policyCandidate{modelID: id, concrete: concrete, entry: entry})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].entry.Power != out[j].entry.Power {
			return out[i].entry.Power > out[j].entry.Power
		}
		if out[i].entry.CostInputPerM != out[j].entry.CostInputPerM {
			return out[i].entry.CostInputPerM < out[j].entry.CostInputPerM
		}
		return out[i].modelID < out[j].modelID
	})
	return out
}

func requiresNoRemote(policy Policy) bool {
	for _, requirement := range policy.Require {
		if requirement == "no_remote" {
			return true
		}
	}
	return false
}

func isLocalDeployment(deploymentClass string) bool {
	switch deploymentClass {
	case deploymentClassLocalFree, deploymentClassCommunitySelfHosted:
		return true
	default:
		return false
	}
}

func (c *Catalog) resolvedFromModel(ref, policyName, modelID, concrete string, entry ModelEntry, opts ResolveOptions) (ResolvedTarget, error) {
	status := normalizedStatus(entry.Status)
	if status != statusActive && !opts.AllowDeprecated {
		return ResolvedTarget{}, &DeprecatedTargetError{CanonicalID: modelID, Status: status}
	}
	return ResolvedTarget{
		Ref:                ref,
		Profile:            policyName,
		Family:             entry.Family,
		CanonicalID:        canonicalIDForResolved(policyName, modelID),
		ConcreteModel:      concrete,
		SurfacePolicy:      SurfacePolicy{ReasoningDefault: entry.ReasoningDefault},
		Deprecated:         status != statusActive,
		CatalogVersion:     c.manifest.CatalogVersion,
		ManifestSource:     c.manifestSrc,
		ManifestVersion:    c.manifest.Version,
		CostInputPerM:      entry.inputCostPerM(),
		CostOutputPerM:     entry.outputCostPerM(),
		CostCacheReadPerM:  entry.CostCacheReadPerM,
		CostCacheWritePerM: entry.CostCacheWritePerM,
		ContextWindow:      entry.ContextWindow,
		SWEBenchVerified:   entry.SWEBenchVerified,
		LiveCodeBench:      entry.LiveCodeBench,
		BenchmarkAsOf:      entry.BenchmarkAsOf,
		OpenRouterRefID:    entry.openRouterID(),
	}, nil
}

func canonicalIDForResolved(policyName, modelID string) string {
	if policyName != "" {
		return policyName
	}
	return modelID
}

func (c *Catalog) modelForSurface(ref string, surface Surface) (string, ModelEntry, string, bool) {
	if entry, ok := c.manifest.Models[ref]; ok {
		concrete := surfaceModelID(ref, entry, surface)
		return ref, entry, concrete, concrete != ""
	}
	for modelID, entry := range c.manifest.Models {
		for _, concrete := range entry.Surfaces {
			if concrete == ref {
				return modelID, entry, concrete, true
			}
		}
	}
	if modelID, entry, ok := c.lookupModelVariant(ref); ok {
		concrete := surfaceModelID(modelID, entry, surface)
		return modelID, entry, concrete, concrete != ""
	}
	return "", ModelEntry{}, "", false
}

func surfaceModelID(modelID string, entry ModelEntry, surface Surface) string {
	if surface == "" {
		return ""
	}
	if concrete := entry.Surfaces[string(surface)]; concrete != "" {
		return concrete
	}
	if _, ok := entry.Surfaces[""]; ok {
		return modelID
	}
	return ""
}

// AllConcreteModels returns a map from concrete model ID to canonical model ID
// for every active model that has a mapping for the given surface.
func (c *Catalog) AllConcreteModels(surface Surface) map[string]string {
	ids := make([]string, 0, len(c.manifest.Models))
	for id := range c.manifest.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make(map[string]string)
	for _, id := range ids {
		entry := c.manifest.Models[id]
		if normalizedStatus(entry.Status) != statusActive {
			continue
		}
		if concrete := surfaceModelID(id, entry, surface); concrete != "" {
			out[concrete] = id
		}
	}
	return out
}

// CandidatesFor returns the ordered concrete model IDs for a policy or model.
func (c *Catalog) CandidatesFor(surface Surface, key string) []string {
	if policy, ok := c.policyForReference(key); ok {
		candidates := c.policyCandidates(policy, surface)
		out := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			out = append(out, candidate.concrete)
		}
		return out
	}
	if _, _, concrete, ok := c.modelForSurface(key, surface); ok {
		return []string{concrete}
	}
	return nil
}

func (c *Catalog) policyForReference(ref string) (Policy, bool) {
	if policy, ok := c.Policy(ref); ok {
		return policy, true
	}
	return Policy{}, false
}

func policyDisplayNameForRef(ref, policyName string) string {
	return policyName
}

// SamplingProfile returns the catalog-defined sampling-parameter bundle for
// the given profile name (e.g., "code"). The second return value is false
// when the profile is not declared in the manifest. See ADR-007.
func (c *Catalog) SamplingProfile(name string) (sampling.Profile, bool) {
	p, ok := c.manifest.SamplingProfiles[name]
	return p, ok
}

// SamplingProfileNames returns the names of all sampling profiles declared
// in the manifest, sorted lexicographically. Used by `catalog show` and
// other ops surfaces to advertise what profiles are available.
func (c *Catalog) SamplingProfileNames() []string {
	if len(c.manifest.SamplingProfiles) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.manifest.SamplingProfiles))
	for name := range c.manifest.SamplingProfiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModelSamplingControl returns the SamplingControl string for the model ID,
// or "" when the model is not declared in the catalog or carries no
// explicit value (treated as the default "client_settable" by callers).
// Implements the sampling.CatalogLookup interface.
func (c *Catalog) ModelSamplingControl(modelID string) string {
	entry, ok := c.LookupModel(modelID)
	if !ok {
		return ""
	}
	return entry.SamplingControl
}

// LookupModel returns the ModelEntry for the given model ID.
func (c *Catalog) LookupModel(id string) (ModelEntry, bool) {
	if entry, ok := c.manifest.Models[id]; ok {
		return entry, true
	}
	for _, entry := range c.manifest.Models {
		for _, concrete := range entry.Surfaces {
			if concrete == id {
				return entry, true
			}
		}
	}
	if _, entry, ok := c.lookupModelVariant(id); ok {
		return entry, true
	}
	return ModelEntry{}, false
}

// ModelEligibility returns power and automatic-routing eligibility for a
// catalog model ID or any declared provider surface ID. Unknown models return
// ok=false; known models with missing/zero power remain exact-pin-capable but
// are not auto-routable.
func (c *Catalog) ModelEligibility(id string) (ModelEligibility, bool) {
	entry, ok := c.LookupModel(id)
	if !ok {
		for modelID, candidate := range c.manifest.Models {
			if strings.EqualFold(modelID, id) {
				entry = candidate
				ok = true
				break
			}
			for _, surfaceID := range candidate.Surfaces {
				if strings.EqualFold(surfaceID, id) {
					entry = candidate
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			return ModelEligibility{}, false
		}
	}
	return ModelEligibility{
		Power:        entry.Power,
		ExactPinOnly: entry.ExactPinOnly,
		AutoRoutable: entry.AutoRoutable(),
	}, true
}

// ContextWindowForModel returns the context window in tokens for the given
// concrete model ID, or 0 if the model is not in the catalog or has no
// context_window declared. Used as a fallback when the provider's live API
// does not expose its context window (e.g. LM Studio's /v1/models omits it).
// Matching is case-insensitive to accept both "qwen3.5-27b" and "Qwen3.5-27B".
func (c *Catalog) ContextWindowForModel(id string) int {
	if entry, ok := c.LookupModel(id); ok && entry.ContextWindow > 0 {
		return entry.ContextWindow
	}
	best := 0
	id = strings.TrimSpace(id)
	if id == "" {
		return 0
	}
	for modelID, entry := range c.manifest.Models {
		if strings.EqualFold(modelID, id) || sameModelVariant(id, modelID) {
			if entry.ContextWindow > best {
				best = entry.ContextWindow
			}
		}
		for _, concrete := range entry.Surfaces {
			if strings.EqualFold(concrete, id) || sameModelVariant(id, concrete) {
				if entry.ContextWindow > best {
					best = entry.ContextWindow
				}
			}
		}
	}
	if best > 0 {
		return best
	}
	return 0
}

// SupportsToolsForModel reports whether the given concrete model ID supports
// tool/function calling per the catalog. Returns true when the model is not
// in the catalog (caller assumes capable by default) and false only when the
// catalog explicitly marks the model with no_tools=true. Matching is
// case-insensitive to mirror ContextWindowForModel.
func (c *Catalog) SupportsToolsForModel(id string) bool {
	if c == nil {
		return true
	}
	if entry, ok := c.LookupModel(id); ok {
		return !entry.NoTools
	}
	return true
}

func (c *Catalog) lookupModelVariant(id string) (string, ModelEntry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ModelEntry{}, false
	}
	var (
		best     ModelEntry
		bestID   string
		bestSeen bool
	)
	for modelID, entry := range c.manifest.Models {
		matched := sameModelVariant(id, modelID)
		if !matched {
			for _, surfaceID := range entry.Surfaces {
				if sameModelVariant(id, surfaceID) {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		if !bestSeen || betterModelVariantMatch(entry, modelID, best, bestID) {
			best = entry
			bestID = modelID
			bestSeen = true
		}
	}
	if !bestSeen {
		return "", ModelEntry{}, false
	}
	return bestID, best, true
}

func betterModelVariantMatch(candidate ModelEntry, candidateID string, best ModelEntry, bestID string) bool {
	candidateStatus := normalizedStatus(candidate.Status)
	bestStatus := normalizedStatus(best.Status)
	if candidateStatus != bestStatus {
		return statusRank(candidateStatus) > statusRank(bestStatus)
	}
	if candidate.Power != best.Power {
		return candidate.Power > best.Power
	}
	if candidate.ContextWindow != best.ContextWindow {
		return candidate.ContextWindow > best.ContextWindow
	}
	if candidate.ExactPinOnly != best.ExactPinOnly {
		return !candidate.ExactPinOnly && best.ExactPinOnly
	}
	return candidateID < bestID
}

func statusRank(status string) int {
	switch normalizedStatus(status) {
	case statusActive:
		return 2
	case statusDeprecated:
		return 1
	case statusStale:
		return 0
	default:
		return -1
	}
}

func sameModelVariant(a, b string) bool {
	ca := catalogModelKey(a)
	cb := catalogModelKey(b)
	if ca == "" || cb == "" {
		return false
	}
	if ca == cb {
		return true
	}
	if sameNamedFamilyMajor(a, b) {
		return true
	}
	if len(ca) < 8 || len(cb) < 8 {
		return false
	}
	return strings.Contains(ca, cb) || strings.Contains(cb, ca)
}

func catalogModelKey(s string) string {
	s = strings.TrimSpace(s)
	if slash := strings.Index(s, "/"); slash > 0 {
		s = s[slash+1:]
	}
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sameNamedFamilyMajor(a, b string) bool {
	familyA, majorA := namedFamilyMajor(a)
	familyB, majorB := namedFamilyMajor(b)
	return familyA != "" && familyA == familyB && majorA > 0 && majorA == majorB
}

func namedFamilyMajor(s string) (string, int) {
	tokens := catalogModelTokens(s)
	if len(tokens) == 0 {
		return "", 0
	}
	family := ""
	for _, token := range tokens {
		switch token {
		case "opus", "sonnet", "haiku":
			family = token
		}
	}
	if family == "" {
		return "", 0
	}
	for _, token := range tokens {
		n, err := strconv.Atoi(token)
		if err == nil {
			return family, n
		}
	}
	return family, 0
}

func catalogModelTokens(s string) []string {
	s = strings.TrimSpace(s)
	if slash := strings.Index(s, "/"); slash > 0 {
		s = s[slash+1:]
	}
	s = strings.ToLower(s)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// CatalogModelPricing holds per-million-token costs for a model as sourced from the catalog.
type CatalogModelPricing struct {
	InputPerMTok   float64
	OutputPerMTok  float64
	CacheReadPerM  float64
	CacheWritePerM float64
}

// AllModels returns all per-model entries, keyed by model ID.
func (c *Catalog) AllModels() map[string]ModelEntry {
	if len(c.manifest.Models) == 0 {
		return make(map[string]ModelEntry)
	}
	out := make(map[string]ModelEntry, len(c.manifest.Models))
	for id, e := range c.manifest.Models {
		out[id] = e
	}
	return out
}

// PricingFor returns pricing for all active concrete models across all surfaces.
func (c *Catalog) PricingFor() map[string]CatalogModelPricing {
	result := make(map[string]CatalogModelPricing)
	for modelID, entry := range c.manifest.Models {
		input := entry.inputCostPerM()
		if input <= 0 {
			continue
		}
		result[modelID] = CatalogModelPricing{
			InputPerMTok:   input,
			OutputPerMTok:  entry.outputCostPerM(),
			CacheReadPerM:  entry.CostCacheReadPerM,
			CacheWritePerM: entry.CostCacheWritePerM,
		}
	}
	return result
}

func (m ModelEntry) inputCostPerM() float64 {
	if m.CostInputPerM != 0 {
		return m.CostInputPerM
	}
	return m.CostInputPerMTok
}

func (m ModelEntry) outputCostPerM() float64 {
	if m.CostOutputPerM != 0 {
		return m.CostOutputPerM
	}
	return m.CostOutputPerMTok
}

func (m ModelEntry) openRouterID() string {
	if m.OpenRouterID != "" {
		return m.OpenRouterID
	}
	return m.OpenRouterRefID
}

// AutoRoutable reports whether a model is eligible for unpinned automatic
// routing. Unknown-power and exact-pin-only entries remain usable by explicit
// model pin when live discovery confirms availability.
func (m ModelEntry) AutoRoutable() bool {
	return normalizedStatus(m.Status) == statusActive && m.Power > 0 && !m.ExactPinOnly
}
