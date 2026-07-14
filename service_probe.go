package fizeau

import (
	"context"
	"errors"
	"strings"

	"github.com/easel/fizeau/internal/provider/openai"
	"github.com/easel/fizeau/internal/sdk/openaicompat"
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
	if isServerError(msg) || isNetworkFailure(err) {
		return nil, &openai.ReachabilityError{
			Endpoint:   baseURL,
			Operation:  "probe_models",
			StatusCode: extractStatusCode(msg),
			Cause:      err,
		}
	}
	return nil, err
}

// isServerError returns true when the error message indicates a 5xx
// response from the discovery endpoint.
func isServerError(msg string) bool {
	for _, prefix := range []string{"HTTP 500", "HTTP 501", "HTTP 502", "HTTP 503", "HTTP 504", "HTTP 505"} {
		if strings.Contains(msg, prefix) {
			return true
		}
	}
	return false
}

// isNetworkFailure returns true when err looks like a transport-level
// failure (dial, timeout, TLS, connection reset) — the ReachabilityError
// classification applies.
func isNetworkFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	markers := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"tls: handshake",
		"TLS handshake",
		"i/o timeout",
		"dial tcp",
		"broken pipe",
	}
	for _, m := range markers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	// Explicitly NOT classifying context.DeadlineExceeded as network —
	// that's the caller's own deadline.
	return errors.Is(err, errDialishNetworkError)
}

// extractStatusCode pulls the status code out of the "HTTP NNN:" prefix
// used by openaicompat.DiscoverModels. Returns 0 when no code is found.
func extractStatusCode(msg string) int {
	const prefix = "HTTP "
	idx := strings.Index(msg, prefix)
	if idx < 0 {
		return 0
	}
	tail := msg[idx+len(prefix):]
	if len(tail) < 3 {
		return 0
	}
	code := 0
	for i := 0; i < 3 && i < len(tail); i++ {
		c := tail[i]
		if c < '0' || c > '9' {
			return 0
		}
		code = code*10 + int(c-'0')
	}
	return code
}

// errDialishNetworkError is a sentinel for errors.Is tests. Callers may
// wrap their own network errors with this to ensure isNetworkFailure
// picks them up even when the message doesn't match one of the literal
// markers.
var errDialishNetworkError = errors.New("agent: network failure")
