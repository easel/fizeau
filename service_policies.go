package fizeau

import (
	"context"

	"github.com/easel/fizeau/internal/modelcatalog"
	"github.com/easel/fizeau/internal/serviceimpl"
)

func (s *service) ListPolicies(_ context.Context) ([]PolicyInfo, error) {
	cat, err := modelcatalog.Default()
	if err != nil {
		return nil, err
	}
	meta := cat.Metadata()
	policies := cat.Policies()
	out := make([]PolicyInfo, 0, len(policies))

	for _, policy := range policies {
		out = append(out, adaptServiceImplPolicyInfo(serviceimpl.PolicyInfoFromCatalog(policy, meta)))
	}

	return out, nil
}

func policyInfoFromCatalog(policy modelcatalog.Policy, meta modelcatalog.Metadata) PolicyInfo {
	return adaptServiceImplPolicyInfo(serviceimpl.PolicyInfoFromCatalog(policy, meta))
}
