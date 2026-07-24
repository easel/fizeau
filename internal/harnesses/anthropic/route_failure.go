package anthropic

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Claude route-failure classes are stable CONTRACT-003 values. They describe
// evidence from the Claude surface that executed; they do not describe host
// login state or the health of a sibling Claude surface.
const (
	// #nosec G101 -- stable failure-class vocabulary, not a credential.
	FailureClassCredentialInvalid = "credential_invalid"
	FailureClassQuotaExhausted    = "quota_exhausted"
	FailureClassTransport         = "transport"
	FailureClassProtocol          = "protocol"
	FailureClassUnknown           = "unknown"

	MaxRouteFailureDiagnosticBytes = 2048
)

var (
	bearerTokenPattern = regexp.MustCompile(`(?i)\bbearer[ \t]+[^\s,;]+`)
	apiTokenPattern    = regexp.MustCompile(`(?i)\bsk-(?:ant-)?[a-z0-9_-]{8,}`)
	secretFieldPattern = regexp.MustCompile(
		`(?i)\b([a-z0-9_.-]*(?:api[ _-]?(?:key|token)|x-api-key|oauth[ _-]?(?:(?:access|refresh)[ _-]?)?token|(?:access|refresh)[ _-]?token)|account[ _-]?(?:id|identifier)|org(?:anization)?[ _-]?id)\b["']?([ \t]*(?::|=)[ \t]*|[ \t]+)["']?[^\s,;"']+`,
	)
	emailPattern      = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	accountPattern    = regexp.MustCompile(`(?i)\bacct[-_][a-z0-9_-]+\b`)
	httpStatusPattern = regexp.MustCompile(
		`(?i)\b(?:http(?:/[0-9.]+)?(?:[ \t]+status(?:[ \t]+code)?)?|response[ \t]+status(?:[ \t]+code)?|status[ \t]+response)[ \t]*[:=]?[ \t]*[1-5][0-9]{2}\b`,
	)
	httpUnauthorizedPattern = regexp.MustCompile(
		`(?i)\b(?:http(?:/[0-9.]+)?(?:[ \t]+status(?:[ \t]+code)?)?|response[ \t]+status(?:[ \t]+code)?|status[ \t]+response)[ \t]*[:=]?[ \t]*401\b`,
	)
)

// ClassifyClaudeRouteFailure classifies one execution-time diagnostic and
// returns a sanitized, bounded form suitable for a terminal event. Matching is
// deliberately ordered: quota evidence wins over credentials, credentials
// (including HTTP 401) win over generic protocol evidence, then transport,
// protocol, and unknown.
func ClassifyClaudeRouteFailure(diagnostic string) (failureClass, sanitizedDiagnostic string) {
	lower := strings.ToLower(diagnostic)

	failureClass = FailureClassUnknown
	switch {
	case containsAny(lower,
		"quota exhausted", "quota_exhausted", "usage limit reached",
		"approaching usage limit", "out of extra usage", "out of free messages",
		"credit balance is too low", "credit balance too low", "insufficient credit",
		"billing limit"):
		failureClass = FailureClassQuotaExhausted
	case containsAny(lower,
		"failed to authenticate", "could not refresh auth token", "authentication_error",
		"invalid api key", "invalid x-api-key", "oauth token has expired",
		"oauth token expired", "please run /login", "unauthorized", `"status":401`,
		"401 unauthorized") || httpUnauthorizedPattern.MatchString(lower):
		failureClass = FailureClassCredentialInvalid
	case containsAny(lower,
		"connection error", "network error", "fetch failed", "econnrefused",
		"connection refused", "connection reset", "connection timed out",
		"timeout connecting", "dial tcp", "no such host",
		"temporary failure in name resolution"):
		failureClass = FailureClassTransport
	case containsAny(lower,
		"protocol error", "invalid response", "malformed", "failed to parse",
		"parse error", "unexpected response", "unexpected eof", "bad request",
		"rate_limit_error", "rate limit exceeded", "service is temporarily unavailable",
		"overloaded",
		// Harness startup/consent drift: the peer did not present the expected
		// interface. These are emitted by the claude-tui startup watchdog and
		// consent state machine and must classify as protocol, not unknown, so
		// UI-drift failures are distinct from opaque EOFs.
		"ui may have changed", "exited before the prompt was submitted") ||
		httpStatusPattern.MatchString(lower):
		failureClass = FailureClassProtocol
	}

	return failureClass, SanitizeClaudeDiagnostic(diagnostic)
}

func containsAny(s string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// SanitizeClaudeDiagnostic removes credentials and account identifiers and
// bounds a diagnostic for terminal delivery without assigning a route-failure
// class. Generic adapter errors and cancellation paths use this helper so
// sanitization never implies route-health admission.
func SanitizeClaudeDiagnostic(diagnostic string) string {
	diagnostic = strings.TrimSpace(diagnostic)
	diagnostic = bearerTokenPattern.ReplaceAllString(diagnostic, "Bearer [REDACTED]")
	diagnostic = apiTokenPattern.ReplaceAllString(diagnostic, "[REDACTED]")
	diagnostic = secretFieldPattern.ReplaceAllString(diagnostic, "$1: [REDACTED]")
	diagnostic = emailPattern.ReplaceAllString(diagnostic, "[REDACTED]")
	diagnostic = accountPattern.ReplaceAllString(diagnostic, "[REDACTED]")
	return boundDiagnostic(diagnostic, MaxRouteFailureDiagnosticBytes)
}

func boundDiagnostic(diagnostic string, maxBytes int) string {
	if len(diagnostic) <= maxBytes {
		return diagnostic
	}
	const suffix = "...(truncated)"
	end := maxBytes - len(suffix)
	for end > 0 && !utf8.ValidString(diagnostic[:end]) {
		end--
	}
	return diagnostic[:end] + suffix
}
