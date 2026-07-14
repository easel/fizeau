package fizeau

import (
	"errors"
	"strings"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/routing"
	"github.com/easel/fizeau/internal/serviceimpl"
)

// resolveModelConstraint resolves an explicit raw model pin to a single
// concrete model ID using the service's discovered provider inventory and
// catalog aliases.
//
// Matching order:
//  1. discovered/provider concrete models and catalog-resolved concrete IDs
//  2. configured provider or harness default models
//
// A unique match returns the resolved concrete model ID. Multiple matches
// return ErrModelConstraintAmbiguous. No matches return
// ErrModelConstraintNoMatch with nearby candidate evidence.
func (s *service) resolveModelConstraint(reqHarness, reqProvider, reqModel string, in routing.Inputs, cat *modelcatalog.Catalog) (string, []RouteCandidate, error) {
	result, err := serviceimpl.ResolveModelConstraint(serviceimpl.ModelConstraintRequest{
		Harness:  reqHarness,
		Provider: reqProvider,
		Model:    reqModel,
	}, in, cat)
	return result.Model, modelCandidatesToRouteCandidates(result.Candidates), adaptModelConstraintError(err)
}

func adaptModelConstraintError(err error) error {
	var internal *serviceimpl.ModelConstraintError
	if !errors.As(err, &internal) {
		return err
	}
	candidates := append([]string(nil), internal.Candidates...)
	switch internal.Kind {
	case serviceimpl.ModelConstraintErrorAmbiguous:
		return &ErrModelConstraintAmbiguous{Model: internal.Model, Candidates: candidates}
	case serviceimpl.ModelConstraintErrorNoMatch:
		return &ErrModelConstraintNoMatch{Model: internal.Model, Candidates: candidates}
	default:
		return err
	}
}

func modelCandidatesToRouteCandidates(ids []string) []RouteCandidate {
	if len(ids) == 0 {
		return nil
	}
	out := make([]RouteCandidate, 0, len(ids))
	seen := make(map[string]struct{})
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, RouteCandidate{Model: id, Reason: "model candidate"})
	}
	return out
}
