package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Offline auth usability classes for Claude surfaces. These match the
// CONTRACT-003 credential vocabulary used at execution time so preflight and
// Execute terminal failures share the same failure-class strings.
const (
	// AuthUsabilityMissing means no credential file and no env token were found.
	// #nosec G101 -- stable failure-class vocabulary, not a credential.
	AuthUsabilityMissing = "credential_missing"
	// AuthUsabilityInvalid means a credentials file is present but has no usable
	// OAuth access/refresh token material (or is unreadable as JSON).
	// #nosec G101 -- stable failure-class vocabulary, not a credential.
	AuthUsabilityInvalid = FailureClassCredentialInvalid
)

// AuthUsability is the result of a cheap offline Claude auth probe. Class is
// empty when the session looks usable enough to attempt Execute; otherwise it
// is AuthUsabilityMissing or AuthUsabilityInvalid. Diagnostic is operator-
// safe (no token material).
type AuthUsability struct {
	Class      string
	Diagnostic string
}

// ClaudeAuthUsabilityProbe is a zero-network probe used before Execute.
// Tests inject fixed results; production uses ProbeClaudeAuthUsability.
type ClaudeAuthUsabilityProbe func() AuthUsability

// ProbeClaudeAuthUsability inspects env vars and the Claude credentials file
// without contacting the network. Order:
//  1. Env tokens (ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / CLAUDE_CODE_OAUTH_TOKEN) → usable
//  2. Credentials file with non-empty access or refresh token → usable
//  3. Credentials file present but empty/invalid OAuth material → credential_invalid
//  4. No env and no credentials file → usable (soft)
//
// Missing credentials do not hard-fail by default: CI and unit tests that mock
// the Binary without host credentials must stay green offline. Callers that
// need fail-fast on missing inject a ClaudeAuthUsabilityProbe (see tests) or
// use ProbeClaudeAuthUsabilityStrict.
//
// Home or config-root resolution failures are treated as usable so hermetic
// unit tests and unusual hosts are not hard-failed by a probe meant for
// operator machines.
func ProbeClaudeAuthUsability() AuthUsability {
	return probeClaudeAuthUsability(false)
}

// ProbeClaudeAuthUsabilityStrict is like ProbeClaudeAuthUsability but treats a
// missing credentials file (and no env token) as credential_missing.
func ProbeClaudeAuthUsabilityStrict() AuthUsability {
	return probeClaudeAuthUsability(true)
}

func probeClaudeAuthUsability(strictMissing bool) AuthUsability {
	if envClaudeAuthPresent() {
		return AuthUsability{}
	}
	configRoot, err := claudeAuthConfigRoot()
	if err != nil {
		return AuthUsability{}
	}
	path := filepath.Join(configRoot, ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if !strictMissing {
				return AuthUsability{}
			}
			return AuthUsability{
				Class:      AuthUsabilityMissing,
				Diagnostic: "claude credentials missing (" + path + "); run claude auth / claude login",
			}
		}
		return AuthUsability{
			Class:      AuthUsabilityInvalid,
			Diagnostic: "claude credentials unreadable; re-authenticate Claude Code (claude auth / claude login); then retry",
		}
	}
	if !claudeCredentialsHaveOAuthToken(data) {
		return AuthUsability{
			Class:      AuthUsabilityInvalid,
			Diagnostic: "claude credentials present but OAuth tokens missing or empty; re-authenticate Claude Code (claude auth / claude login); then retry",
		}
	}
	return AuthUsability{}
}

func envClaudeAuthPresent() bool {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func claudeAuthConfigRoot() (string, error) {
	if configured, exists := os.LookupEnv("CLAUDE_CONFIG_DIR"); exists {
		configured = strings.TrimSpace(configured)
		if configured == "" || !filepath.IsAbs(configured) {
			return "", os.ErrInvalid
		}
		return filepath.Clean(configured), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", os.ErrNotExist
	}
	return filepath.Join(home, ".claude"), nil
}

func claudeCredentialsHaveOAuthToken(data []byte) bool {
	var document map[string]any
	if json.Unmarshal(data, &document) != nil || document == nil {
		return false
	}
	oauth, ok := document["claudeAiOauth"].(map[string]any)
	if !ok {
		return false
	}
	return nonEmptyStringField(oauth["accessToken"]) || nonEmptyStringField(oauth["refreshToken"])
}

func nonEmptyStringField(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}
