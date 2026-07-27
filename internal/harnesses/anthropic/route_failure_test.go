package anthropic

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClassifyClaudeRouteFailure(t *testing.T) {
	tests := []struct {
		name       string
		diagnostic string
		want       string
	}{
		{name: "credential incident", diagnostic: "Failed to authenticate\nCould not refresh auth token", want: FailureClassCredentialInvalid},
		{name: "quota", diagnostic: "Claude usage limit reached", want: FailureClassQuotaExhausted},
		{name: "credit balance quota", diagnostic: "Credit balance is too low; HTTP 401", want: FailureClassQuotaExhausted},
		{name: "quota beats credential", diagnostic: "quota exhausted after Failed to authenticate", want: FailureClassQuotaExhausted},
		{name: "http 401 beats protocol", diagnostic: "protocol error: HTTP 401 Unauthorized", want: FailureClassCredentialInvalid},
		{name: "transport", diagnostic: "Connection error: ECONNREFUSED", want: FailureClassTransport},
		{name: "protocol", diagnostic: "protocol error: HTTP 500 invalid response", want: FailureClassProtocol},
		{name: "rate limit is protocol", diagnostic: "rate_limit_error: rate limit exceeded", want: FailureClassProtocol},
		{name: "overloaded is protocol", diagnostic: "Overloaded: service is temporarily unavailable", want: FailureClassProtocol},
		{name: "bad gateway is protocol", diagnostic: "HTTP 502 Bad Gateway", want: FailureClassProtocol},
		{name: "service unavailable is protocol", diagnostic: "HTTP 503 Service Unavailable", want: FailureClassProtocol},
		{name: "gateway timeout is protocol", diagnostic: "HTTP 504 Gateway Timeout", want: FailureClassProtocol},
		{name: "response status is protocol", diagnostic: "provider response status code: 500", want: FailureClassProtocol},
		{name: "pty closed before stop is protocol", diagnostic: "Claude TUI PTY closed before Stop hook; process exit code 143", want: FailureClassProtocol},
		{name: "startup pre-prompt exit 143 is protocol", diagnostic: "claude-tui startup: Claude exited before the prompt was submitted (last recognized screen: none); the startup UI may have changed; Claude TUI PTY closed before Stop hook; process exit code 143", want: FailureClassProtocol},
		{name: "pty closed by signal is protocol", diagnostic: "Claude TUI PTY closed before Stop hook; process terminated by signal", want: FailureClassProtocol},
		{name: "process status is unknown", diagnostic: "process exited with status code 1", want: FailureClassUnknown},
		{name: "unknown", diagnostic: "claude exited for an unrecognized reason", want: FailureClassUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _ := ClassifyClaudeRouteFailure(test.diagnostic)
			if got != test.want {
				t.Fatalf("class = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClassifyClaudeRouteFailureRedactsAndBoundsDiagnostic(t *testing.T) {
	input := strings.Join([]string{
		"Failed to authenticate",
		"Authorization: Bearer bearer-secret-value",
		"api_key=sk-ant-api03-super-secret-value",
		"api_token=opaque-api-secret-value",
		"refresh_token=refresh-secret-value",
		"account_id=acct-sensitive-123",
		`{"ANTHROPIC_API_KEY":"json-api-secret","OAUTH_REFRESH_TOKEN":"json-refresh-secret"}`,
		"OAUTH_ACCESS_TOKEN=oauth-access-secret",
		"oauth_token: oauth-generic-secret",
		"unlabelled account acct-raw-secret-456",
		"account owner alice@example.com",
		strings.Repeat("x", 4096),
	}, "\n")

	_, diagnostic := ClassifyClaudeRouteFailure(input)
	if len(diagnostic) > MaxRouteFailureDiagnosticBytes {
		t.Fatalf("diagnostic bytes = %d, want <= %d", len(diagnostic), MaxRouteFailureDiagnosticBytes)
	}
	for _, secret := range []string{
		"bearer-secret-value", "sk-ant-api03-super-secret-value",
		"opaque-api-secret-value", "refresh-secret-value", "acct-sensitive-123", "alice@example.com",
		"json-api-secret", "json-refresh-secret", "oauth-access-secret",
		"oauth-generic-secret", "acct-raw-secret-456",
	} {
		if strings.Contains(diagnostic, secret) {
			t.Errorf("diagnostic retained secret/account identifier %q", secret)
		}
	}
}

func TestSanitizeClaudeDiagnosticAdversarialQuotedAndNamespacedSecrets(t *testing.T) {
	input := strings.Join([]string{
		`{"authorization":"Bearer quoted-bearer-secret"}`,
		`{"api_key":"quoted-api-secret","refresh_token":"quoted-refresh-secret","account_id":"acct-json-secret"}`,
		`ANTHROPIC_API_KEY='env-anthropic-secret'`,
		`OAUTH_REFRESH_TOKEN="env-oauth-refresh-secret"`,
		`CLAUDE_CODE_OAUTH_TOKEN=namespaced-oauth-secret`,
		`VENDOR_API_KEY=namespaced-api-secret`,
		`access-token=generic-access-secret`,
		`refresh token generic-refresh-secret`,
		`raw account identifier acct-unlabelled-secret`,
	}, "\n")

	got := SanitizeClaudeDiagnostic(input)
	for _, secret := range []string{
		"quoted-bearer-secret", "quoted-api-secret", "quoted-refresh-secret",
		"acct-json-secret", "env-anthropic-secret", "env-oauth-refresh-secret",
		"namespaced-oauth-secret", "namespaced-api-secret",
		"generic-access-secret", "generic-refresh-secret", "acct-unlabelled-secret",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("sanitized diagnostic retained %q: %q", secret, got)
		}
	}
	if len(got) > MaxRouteFailureDiagnosticBytes {
		t.Fatalf("diagnostic bytes = %d, want <= %d", len(got), MaxRouteFailureDiagnosticBytes)
	}
}

func TestSanitizeClaudeDiagnosticBoundsAtUTF8Boundary(t *testing.T) {
	const suffix = "...(truncated)"
	cut := MaxRouteFailureDiagnosticBytes - len(suffix)
	input := strings.Repeat("a", cut-1) + "é" + strings.Repeat("界", 100)
	got := SanitizeClaudeDiagnostic(input)
	if len(got) > MaxRouteFailureDiagnosticBytes {
		t.Fatalf("diagnostic bytes = %d, want <= %d", len(got), MaxRouteFailureDiagnosticBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("bounded diagnostic is not valid UTF-8: %q", got[len(got)-32:])
	}
	if !strings.HasSuffix(got, suffix) {
		t.Fatalf("bounded diagnostic suffix = %q, want %q", got[len(got)-len(suffix):], suffix)
	}

	exact := strings.Repeat("a", MaxRouteFailureDiagnosticBytes)
	if gotExact := SanitizeClaudeDiagnostic(exact); gotExact != exact || !utf8.ValidString(gotExact) {
		t.Fatal("exact-boundary diagnostic changed or became invalid UTF-8")
	}
}

func TestClaudeSurfaceFailureClassificationParity(t *testing.T) {
	batchClass, _ := ClassifyClaudeRouteFailure("claude stderr: Failed to authenticate; Could not refresh auth token")
	tuiClass, _ := ClassifyClaudeRouteFailure("Failed to authenticate\nCould not refresh auth token")
	if batchClass != tuiClass || batchClass != FailureClassCredentialInvalid {
		t.Fatalf("batch class = %q, TUI class = %q, want %q", batchClass, tuiClass, FailureClassCredentialInvalid)
	}
}
