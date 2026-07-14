package serviceimpl

import (
	"fmt"
	"strings"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/modelmatch"
	"github.com/easel/fizeau/internal/routing"
)

// ModelConstraintRequest is the API-neutral view of an explicit model pin.
type ModelConstraintRequest struct {
	Harness  string
	Provider string
	Model    string
}

// ModelConstraintResult contains either the resolved model or the candidate
// evidence considered when resolution failed.
type ModelConstraintResult struct {
	Model      string
	Candidates []string
}

// ModelConstraintErrorKind identifies why an explicit model pin could not be
// resolved. The root facade projects this implementation detail onto its
// public error types.
type ModelConstraintErrorKind string

const (
	ModelConstraintErrorAmbiguous ModelConstraintErrorKind = "ambiguous"
	ModelConstraintErrorNoMatch   ModelConstraintErrorKind = "no_match"
)

// ModelConstraintError retains the normalized request and candidate evidence
// needed by the root facade's public error projection.
type ModelConstraintError struct {
	Kind       ModelConstraintErrorKind
	Model      string
	Candidates []string
}

func (e *ModelConstraintError) Error() string {
	switch e.Kind {
	case ModelConstraintErrorAmbiguous:
		if len(e.Candidates) == 0 {
			return fmt.Sprintf("ambiguous model %q", e.Model)
		}
		return fmt.Sprintf("ambiguous model %q: candidates: %s", e.Model, strings.Join(e.Candidates, ", "))
	case ModelConstraintErrorNoMatch:
		if len(e.Candidates) == 0 {
			return fmt.Sprintf("no matching model for %q", e.Model)
		}
		return fmt.Sprintf("no matching model for %q; nearby candidates: %s", e.Model, strings.Join(e.Candidates, ", "))
	default:
		return fmt.Sprintf("model constraint %q could not be resolved", e.Model)
	}
}

// ResolveModelConstraint resolves an explicit raw model pin to a single
// concrete model ID using discovered provider inventory and catalog aliases.
// Concrete discovered/catalog models take precedence over configured defaults.
func ResolveModelConstraint(req ModelConstraintRequest, in routing.Inputs, cat *modelcatalog.Catalog) (ModelConstraintResult, error) {
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		return ModelConstraintResult{}, nil
	}

	concreteCandidates := collectConcreteModelCandidates(req.Harness, req.Provider, req.Model, in, cat)
	if resolved, err := resolveSingleModelMatch(req.Model, concreteCandidates); err != nil {
		return ModelConstraintResult{Candidates: cloneModelCandidates(concreteCandidates)}, err
	} else if resolved != "" {
		return ModelConstraintResult{Model: resolved}, nil
	}

	defaultCandidates := collectDefaultModelCandidates(req.Harness, req.Provider, in)
	evidence := append(cloneModelCandidates(concreteCandidates), defaultCandidates...)
	if resolved, err := resolveSingleModelMatch(req.Model, defaultCandidates); err != nil {
		return ModelConstraintResult{Candidates: evidence}, err
	} else if resolved != "" {
		return ModelConstraintResult{Model: resolved}, nil
	}

	return ModelConstraintResult{Candidates: evidence}, &ModelConstraintError{
		Kind:       ModelConstraintErrorNoMatch,
		Model:      req.Model,
		Candidates: cloneModelCandidates(evidence),
	}
}

func collectConcreteModelCandidates(reqHarness, reqProvider, reqModel string, in routing.Inputs, cat *modelcatalog.Catalog) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	harnessPin := canonicalHarnessPin(reqHarness)
	providerPin := modelConstraintProviderPin(reqProvider)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, h := range in.Harnesses {
		if harnessPin != "" && harnessPin != canonicalHarnessPin(h.Name) {
			continue
		}
		for _, p := range h.Providers {
			if providerPin != "" && providerPin != candidateProviderIdentity(h, p) {
				continue
			}
			for _, id := range p.DiscoveredIDs {
				add(id)
			}
		}
	}

	if cat != nil {
		seenSurface := make(map[modelcatalog.Surface]struct{})
		for _, h := range in.Harnesses {
			if harnessPin != "" && harnessPin != canonicalHarnessPin(h.Name) {
				continue
			}
			surface := modelcatalog.Surface(h.Surface)
			if surface == "" {
				continue
			}
			if _, ok := seenSurface[surface]; ok {
				continue
			}
			seenSurface[surface] = struct{}{}
			if resolved, err := cat.Resolve(reqModel, modelcatalog.ResolveOptions{
				Surface:         surface,
				AllowDeprecated: true,
			}); err == nil && resolved.ConcreteModel != "" {
				add(resolved.ConcreteModel)
			}
		}
	}

	return out
}

func collectDefaultModelCandidates(reqHarness, reqProvider string, in routing.Inputs) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	harnessPin := canonicalHarnessPin(reqHarness)
	providerPin := modelConstraintProviderPin(reqProvider)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}

	for _, h := range in.Harnesses {
		if harnessPin != "" && harnessPin != canonicalHarnessPin(h.Name) {
			continue
		}
		if providerPin == "" {
			add(h.DefaultModel)
		}
		for _, p := range h.Providers {
			if providerPin != "" && providerPin != candidateProviderIdentity(h, p) {
				continue
			}
			add(p.DefaultModel)
		}
	}
	return out
}

func resolveSingleModelMatch(reqModel string, candidates []string) (string, error) {
	matches := modelmatch.Match(reqModel, candidates)
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", &ModelConstraintError{
			Kind:       ModelConstraintErrorAmbiguous,
			Model:      reqModel,
			Candidates: cloneModelCandidates(matches),
		}
	}
}

func modelConstraintProviderPin(provider string) string {
	provider = strings.TrimSpace(provider)
	base, endpoint, ok := strings.Cut(provider, "@")
	if ok && base != "" && endpoint != "" {
		return base
	}
	return provider
}

func canonicalHarnessPin(harness string) string {
	if harness == "local" {
		return "fiz"
	}
	return harness
}

func candidateProviderIdentity(h routing.HarnessEntry, p routing.ProviderEntry) string {
	if p.Name != "" {
		return p.Name
	}
	return h.Name
}

func cloneModelCandidates(candidates []string) []string {
	if len(candidates) == 0 {
		return nil
	}
	return append([]string(nil), candidates...)
}
