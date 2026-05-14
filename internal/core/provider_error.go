package core

import (
	"errors"
	"net"
	"regexp"
)

// overflowPattern matches error messages that indicate the request exceeded the
// model's context window.
var overflowPattern = regexp.MustCompile(
	`(?i)context.?length|token.?limit|maximum.?context|context.?window|` +
		`too.?long|exceeds.*limit|reduce.*length|reduce.*message`,
)

// IsContextOverflowError reports whether err indicates the request exceeded
// the model's context window.
func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	return overflowPattern.MatchString(err.Error())
}

// transientPattern matches error messages that indicate a transient condition
// (network blips, rate limits, server overload) that is safe to retry.
var transientPattern = regexp.MustCompile(
	`(?i)overloaded|rate.?limit|too many requests|\b(?:429|500|502|503|504)\b|` +
		`service.?unavailable|server.?error|internal.?error|` +
		`network.?error|connection.?error|connection.?refused|` +
		`other side closed|fetch failed|socket hang.?up|` +
		`ended without|timed?.?out|timeout|i/o timeout|` +
		`EOF|connection reset|broken pipe`,
)

// fatalPattern matches error messages that indicate a permanent failure
// (authentication/authorization). These should never be retried even if they
// also match a transient pattern.
var fatalPattern = regexp.MustCompile(
	`(?i)\b(?:401|403)\b|unauthorized|forbidden|invalid.?api.?key|authentication`,
)

// IsDialError reports whether err represents a TCP-level connection failure
// at dial time. These errors originate from a *net.OpError with Op=="dial"
// (ECONNREFUSED, ETIMEDOUT, EHOSTUNREACH, and related connect-phase failures).
//
// Dial errors are not retried: the endpoint is currently unreachable and
// repeated attempts on the same provider within the backoff window will not
// recover a dead host. Contrast with in-flight 5xx errors, which may succeed
// on retry.
func IsDialError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// IsTransientError reports whether err is a transient provider error
// that is safe to retry (network issues, rate limits, server overload).
// Returns false for fatal errors (auth failures, bad requests, etc.).
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Provider-capability errors are deterministic for a given provider+model
	// combination. Retrying just burns the same call repeatedly with the same
	// outcome, so classify them as non-transient regardless of substring shape.
	if errors.Is(err, ErrProviderCapabilityMissing) {
		return false
	}
	msg := err.Error()
	if fatalPattern.MatchString(msg) {
		return false
	}
	return transientPattern.MatchString(msg)
}
