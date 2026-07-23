package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const grokAuthPathEnv = "FIZEAU_GROK_AUTH"

// grokAuthPath returns the local grok CLI auth file used for account
// metadata. The grok CLI stores OIDC-scoped credentials at
// ~/.grok/auth.json.
func grokAuthPath() (string, error) {
	if path := os.Getenv(grokAuthPathEnv); path != "" {
		return path, nil
	}
	if home := os.Getenv("GROK_HOME"); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grok", "auth.json"), nil
}

// grokAuthEntry mirrors one credential entry in ~/.grok/auth.json. The file
// is a map keyed by "<issuer>::<client_id>"; only non-secret metadata is
// decoded.
type grokAuthEntry struct {
	AuthMode      string `json:"auth_mode"`
	Email         string `json:"email"`
	TeamID        string `json:"team_id"`
	PrincipalType string `json:"principal_type"`
	ExpiresAt     string `json:"expires_at"`
}

// readGrokAccount extracts non-secret account metadata from grok auth state.
func readGrokAccount() (*harnesses.AccountInfo, bool) {
	path, err := grokAuthPath()
	if err != nil {
		return nil, false
	}
	return readGrokAccountFrom(path)
}

// readGrokAccountFrom reads auth.json and extracts email and plan metadata.
// The grok auth file carries no subscription-tier claim, so the plan type is
// derived from the auth mode: OIDC/session credentials imply a Grok
// subscription (the CLI requires SuperGrok / X Premium+ for session auth),
// while API-key entries are metered.
func readGrokAccountFrom(path string) (*harnesses.AccountInfo, bool) {
	data, err := safefs.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var entries map[string]grokAuthEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false
	}
	var best *grokAuthEntry
	var bestExpiry time.Time
	for key := range entries {
		entry := entries[key]
		if entry.Email == "" && entry.AuthMode == "" {
			continue
		}
		expiry, _ := time.Parse(time.RFC3339Nano, entry.ExpiresAt)
		if best == nil || expiry.After(bestExpiry) {
			e := entry
			best = &e
			bestExpiry = expiry
		}
	}
	if best == nil {
		return nil, false
	}
	account := &harnesses.AccountInfo{
		Email:    strings.TrimSpace(best.Email),
		PlanType: grokPlanTypeForAuthMode(best.AuthMode),
	}
	if strings.TrimSpace(best.TeamID) != "" {
		account.OrgName = "team " + strings.TrimSpace(best.TeamID)
	}
	if account.Email == "" && account.PlanType == "" {
		return nil, false
	}
	return account, true
}

func grokPlanTypeForAuthMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "oidc", "session":
		return "Grok subscription"
	case "api_key", "api-key", "apikey":
		return "xAI API key"
	case "":
		return ""
	default:
		return "Grok " + strings.TrimSpace(mode)
	}
}

// grokAccountSupportsAutoRouting reports whether the account evidence
// indicates a subsidized subscription pool suitable for automatic routing.
func grokAccountSupportsAutoRouting(account *harnesses.AccountInfo) bool {
	return account != nil && account.PlanType == "Grok subscription"
}
