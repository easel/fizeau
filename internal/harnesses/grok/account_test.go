package grok

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// grokAuthCapturedShape mirrors the real ~/.grok/auth.json structure
// (grok 0.2.106): a map keyed by "<issuer>::<client_id>" with non-secret
// metadata alongside credentials (redacted here).
const grokAuthCapturedShape = `{
  "https://auth.x.ai::b1a00492-073a-47ea-816f-4c329264a828": {
    "key": "REDACTED",
    "auth_mode": "oidc",
    "create_time": "2026-07-01T00:00:00Z",
    "user_id": "u-123",
    "email": "user@example.com",
    "principal_type": "User",
    "team_id": "t-456",
    "refresh_token": "REDACTED",
    "expires_at": "2026-07-23T23:56:25.052138440Z",
    "oidc_issuer": "https://auth.x.ai",
    "oidc_client_id": "b1a00492-073a-47ea-816f-4c329264a828"
  }
}`

func writeGrokAuthFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadGrokAccountFromOIDCAuth(t *testing.T) {
	path := writeGrokAuthFixture(t, grokAuthCapturedShape)
	account, ok := readGrokAccountFrom(path)
	if !ok || account == nil {
		t.Fatal("expected account from OIDC auth fixture")
	}
	if account.Email != "user@example.com" {
		t.Errorf("Email = %q", account.Email)
	}
	if account.PlanType != "Grok subscription" {
		t.Errorf("PlanType = %q, want Grok subscription", account.PlanType)
	}
	if account.OrgName != "team t-456" {
		t.Errorf("OrgName = %q", account.OrgName)
	}
	if !grokAccountSupportsAutoRouting(account) {
		t.Error("OIDC subscription account should support auto-routing")
	}
}

func TestReadGrokAccountAPIKeyMode(t *testing.T) {
	path := writeGrokAuthFixture(t, `{
  "https://auth.x.ai::client": {"auth_mode": "api_key", "email": "user@example.com", "expires_at": "2026-07-23T00:00:00Z"}
}`)
	account, ok := readGrokAccountFrom(path)
	if !ok || account == nil {
		t.Fatal("expected account")
	}
	if account.PlanType != "xAI API key" {
		t.Errorf("PlanType = %q", account.PlanType)
	}
	if grokAccountSupportsAutoRouting(account) {
		t.Error("API-key account must not support subscription auto-routing")
	}
}

func TestReadGrokAccountMissingFile(t *testing.T) {
	if _, ok := readGrokAccountFrom(filepath.Join(t.TempDir(), "absent.json")); ok {
		t.Fatal("expected no account for missing file")
	}
}

func TestReadGrokAccountMalformed(t *testing.T) {
	path := writeGrokAuthFixture(t, "not json")
	if _, ok := readGrokAccountFrom(path); ok {
		t.Fatal("expected no account for malformed file")
	}
}

func TestReadGrokAccountPicksLatestExpiry(t *testing.T) {
	older := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	newer := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	path := writeGrokAuthFixture(t, `{
  "https://auth.x.ai::old": {"auth_mode": "oidc", "email": "old@example.com", "expires_at": "`+older+`"},
  "https://auth.x.ai::new": {"auth_mode": "oidc", "email": "new@example.com", "expires_at": "`+newer+`"}
}`)
	account, ok := readGrokAccountFrom(path)
	if !ok || account == nil {
		t.Fatal("expected account")
	}
	if account.Email != "new@example.com" {
		t.Errorf("Email = %q, want the freshest credential entry", account.Email)
	}
}

func TestGrokAccountSnapshotUnauthenticatedColdPath(t *testing.T) {
	t.Setenv(grokAuthPathEnv, filepath.Join(t.TempDir(), "absent.json"))
	snap := readGrokAccountSnapshot(time.Now())
	if !snap.Unauthenticated || snap.Authenticated {
		t.Fatalf("snapshot = %+v, want unauthenticated", snap)
	}
}
