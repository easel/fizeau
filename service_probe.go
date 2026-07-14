package fizeau

import (
	"context"
	"strings"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/sdk/openaicompat"
	serviceimpl "github.com/easel/fizeau/internal/serviceimpl"
)

// QuotaRecoveryProber reports whether a quota_exhausted provider has recovered.
// A nil error indicates the provider is back online; a non-nil error keeps it
// in quota_exhausted with an extended retry_after.
type QuotaRecoveryProber func(ctx context.Context, provider string) error

// probeOpenAIModels calls GET /v1/models against baseURL and classifies
// failures into the three shapes the catalog cache understands:
//
//   - *openai.ReachabilityError for 5xx / transport failures (endpoint
//     unreachable); cache sets an UnreachableCooldown.
//   - errDiscoveryUnsupported for 404 / endpoints that don't expose
//     /v1/models; cache marks DiscoverySupported=false and callers
//     fall back to passthrough model naming.
//   - other errors (401/403 auth, unexpected body) are returned as-is;
//     the cache records them but doesn't mark the endpoint unreachable.
//
// The returned IDs are whatever the server returned in its `data[].id`
// list, preserving server-provided order.
func probeOpenAIModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	ids, err := openaicompat.DiscoverModels(ctx, baseURL, apiKey)
	if err == nil {
		return ids, nil
	}
	msg := err.Error()
	// openaicompat.DiscoverModels returns errors prefixed with
	// "HTTP <code>: <body>" for non-2xx responses.
	if strings.Contains(msg, "HTTP 404") {
		return nil, ErrDiscoveryUnsupported()
	}
	if serviceimpl.IsServerError(msg) || serviceimpl.IsNetworkFailure(err) {
		return nil, &openai.ReachabilityError{
			Endpoint:   baseURL,
			Operation:  "probe_models",
			StatusCode: serviceimpl.ExtractStatusCode(msg),
			Cause:      err,
		}
	}
	return nil, err
}

// extractStatusCode pulls the status code out of the "HTTP NNN:" prefix
// used by openaicompat.DiscoverModels. Returns 0 when no code is found.
func extractStatusCode(msg string) int {
	return serviceimpl.ExtractStatusCode(msg)
}
