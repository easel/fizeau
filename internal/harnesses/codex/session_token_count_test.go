package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexSessionsRootUsesOverrideAndCodeXHome(t *testing.T) {
	override := filepath.Join(t.TempDir(), "override")
	t.Setenv(codexSessionsRootEnv, override)
	if got, err := codexSessionsRoot(); err != nil || got != override {
		t.Fatalf("override root: got %q err=%v, want %q", got, err, override)
	}

	t.Setenv(codexSessionsRootEnv, "")
	home := filepath.Join(t.TempDir(), "codex-home")
	t.Setenv("CODEX_HOME", home)
	if got, err := codexSessionsRoot(); err != nil || got != filepath.Join(home, "sessions") {
		t.Fatalf("CODEX_HOME root: got %q err=%v", got, err)
	}
}

func TestReadCodexQuotaFromSessionTokenCounts_NewestFresh(t *testing.T) {
	now := mustTime(t, "2026-04-22T02:00:00Z")
	root := t.TempDir()
	t.Setenv(codexSessionsRootEnv, root)
	writeSessionJSONL(t, filepath.Join(root, "old.jsonl"), now.Add(-10*time.Minute), tokenCountLine("2026-04-22T01:50:00Z", `"used_percent":20`))
	writeSessionJSONL(t, filepath.Join(root, "nested", "new.jsonl"), now.Add(-1*time.Minute), tokenCountLine("2026-04-22T01:59:30Z", `"used_percent":4.5`))

	snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now))
	if !ok {
		t.Fatal("expected fresh token_count quota snapshot")
	}
	if !snap.CapturedAt.Equal(mustTime(t, "2026-04-22T01:59:30Z")) {
		t.Fatalf("CapturedAt: got %s", snap.CapturedAt.Format(time.RFC3339Nano))
	}
	if snap.Source != "codex_session_token_count" {
		t.Fatalf("Source: got %q", snap.Source)
	}
	if snap.Account == nil || snap.Account.PlanType != "ChatGPT Pro" {
		t.Fatalf("Account: got %#v", snap.Account)
	}
	if len(snap.Windows) != 2 {
		t.Fatalf("Windows: got %#v", snap.Windows)
	}
	primary := snap.Windows[0]
	if primary.Name != "5h" || primary.WindowMinutes != 300 || primary.UsedPercent != 4.5 || primary.LimitID != "codex" || primary.LimitName != "primary credits" {
		t.Fatalf("primary window: got %#v", primary)
	}
	if primary.ResetsAtUnix != 1776840333 || primary.ResetsAt == "" {
		t.Fatalf("primary reset not preserved: %#v", primary)
	}
	secondary := snap.Windows[1]
	if secondary.Name != "7d" || secondary.WindowMinutes != 10080 || secondary.UsedPercent != 2 {
		t.Fatalf("secondary window: got %#v", secondary)
	}
}

func TestReadCodexQuotaFromSessionTokenCounts_FallbackMtimeAndStale(t *testing.T) {
	now := mustTime(t, "2026-04-22T02:00:00Z")
	root := t.TempDir()
	t.Setenv(codexSessionsRootEnv, root)
	fileMTime := now.Add(-5 * time.Minute)
	writeSessionJSONL(t, filepath.Join(root, "fresh.jsonl"), fileMTime, tokenCountLine("not-a-time", `"used_percent":10`))

	snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now))
	if !ok {
		t.Fatal("expected mtime-derived fresh snapshot")
	}
	if !snap.CapturedAt.Equal(fileMTime) {
		t.Fatalf("CapturedAt: got %s, want mtime %s", snap.CapturedAt, fileMTime)
	}

	staleRoot := t.TempDir()
	t.Setenv(codexSessionsRootEnv, staleRoot)
	writeSessionJSONL(t, filepath.Join(staleRoot, "stale.jsonl"), now.Add(-20*time.Minute), tokenCountLine("", `"used_percent":10`))
	if snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now)); ok || snap != nil {
		t.Fatalf("stale evidence should be ignored: snap=%#v ok=%v", snap, ok)
	}
}

func TestReadCodexQuotaFromSessionTokenCounts_BoundsAndSymlinks(t *testing.T) {
	now := mustTime(t, "2026-04-22T02:00:00Z")
	root := t.TempDir()
	t.Setenv(codexSessionsRootEnv, root)
	writeSessionJSONL(t, filepath.Join(root, "older-valid.jsonl"), now.Add(-2*time.Minute), tokenCountLine("2026-04-22T01:58:00Z", `"used_percent":10`))
	writeSessionJSONL(t, filepath.Join(root, "newer-malformed.jsonl"), now.Add(-1*time.Minute), `{"type":"event_msg","payload":{"type":"token_count","info":{"rate_limits":{"primary":{"used_percent":"bad","window_minutes":300}}}}}`)

	if snap, ok := readCodexQuotaFromSessionTokenCounts(
		WithCodexSessionTokenCountNow(now),
		WithCodexSessionTokenCountLimits(1, defaultCodexSessionMaxBytesPerFile, defaultCodexSessionMaxLineBytes),
	); ok || snap != nil {
		t.Fatalf("maxFiles=1 should only inspect newest malformed file: snap=%#v ok=%v", snap, ok)
	}
	if snap, ok := readCodexQuotaFromSessionTokenCounts(
		WithCodexSessionTokenCountNow(now),
		WithCodexSessionTokenCountLimits(2, defaultCodexSessionMaxBytesPerFile, defaultCodexSessionMaxLineBytes),
	); !ok || snap == nil {
		t.Fatalf("maxFiles=2 should reach older valid file: snap=%#v ok=%v", snap, ok)
	}

	tooLargeRoot := t.TempDir()
	t.Setenv(codexSessionsRootEnv, tooLargeRoot)
	writeSessionJSONL(t, filepath.Join(tooLargeRoot, "large.jsonl"), now.Add(-time.Minute), tokenCountLine("2026-04-22T01:59:00Z", `"used_percent":10`))
	if snap, ok := readCodexQuotaFromSessionTokenCounts(
		WithCodexSessionTokenCountNow(now),
		WithCodexSessionTokenCountLimits(10, 10, defaultCodexSessionMaxLineBytes),
	); ok || snap != nil {
		t.Fatalf("oversized file should be skipped: snap=%#v ok=%v", snap, ok)
	}

	symlinkRoot := t.TempDir()
	target := filepath.Join(symlinkRoot, "target.jsonl")
	writeSessionJSONL(t, target, now.Add(-time.Minute), tokenCountLine("2026-04-22T01:59:00Z", `"used_percent":10`))
	link := filepath.Join(symlinkRoot, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	t.Setenv(codexSessionsRootEnv, symlinkRoot)
	if snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now)); ok || snap != nil {
		t.Fatalf("symlinked session file should be skipped: snap=%#v ok=%v", snap, ok)
	}
}

func TestReadCodexQuotaFromSessionTokenCounts_NewestWithinFileAndPrivacy(t *testing.T) {
	now := mustTime(t, "2026-04-22T02:00:00Z")
	root := t.TempDir()
	t.Setenv(codexSessionsRootEnv, root)
	writeSessionJSONL(t, filepath.Join(root, "multi.jsonl"), now.Add(-time.Minute),
		tokenCountLine("2026-04-22T01:57:00Z", `"used_percent":30`),
		`{"type":"output","item":{"type":"agent_message","text":"secret prompt and response text must not be retained"}}`,
		tokenCountLine("2026-04-22T01:59:00Z", `"used_percent":7`),
	)

	snap, ok := readCodexQuotaFromSessionTokenCounts(WithCodexSessionTokenCountNow(now))
	if !ok {
		t.Fatal("expected newest token_count within file")
	}
	if !snap.CapturedAt.Equal(mustTime(t, "2026-04-22T01:59:00Z")) || snap.Windows[0].UsedPercent != 7 {
		t.Fatalf("newest token_count not selected: %#v", snap)
	}
	if snap.Account != nil && (snap.Account.Email != "" || snap.Account.OrgName != "") {
		t.Fatalf("private session content leaked into account: %#v", snap.Account)
	}
}

func writeSessionJSONL(t *testing.T, path string, mtime time.Time, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func tokenCountLine(timestamp string, primaryUsed string) string {
	return `{"type":"event_msg","timestamp":"` + timestamp + `","payload":{"type":"token_count","info":{"rate_limits":{"plan_type":"pro","primary":{` + primaryUsed + `,"window_minutes":300,"resets_at":1776840333,"limit_id":"codex","limit_name":"primary credits"},"secondary":{"used_percent":2,"window_minutes":10080,"resets_at":"1777400415","limit_id":"codex-weekly"}}}}}`
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UTC()
}

func TestCodexQuotaWindowFromRaw_DefaultLimitID(t *testing.T) {
	tests := []struct {
		name          string
		json          string
		expectLimitID string
		expectOK      bool
	}{
		{
			name:          "missing limit_id uses window-based default (5h)",
			json:          `{"window_minutes":300,"used_percent":50}`,
			expectLimitID: "codex",
			expectOK:      true,
		},
		{
			name:          "missing limit_id uses window-based default (7d)",
			json:          `{"window_minutes":10080,"used_percent":25}`,
			expectLimitID: "codex-weekly",
			expectOK:      true,
		},
		{
			name:          "null limit_id uses window-based default (5h)",
			json:          `{"window_minutes":300,"used_percent":60,"limit_id":null}`,
			expectLimitID: "codex",
			expectOK:      true,
		},
		{
			name:          "empty string limit_id uses window-based default (7d)",
			json:          `{"window_minutes":10080,"used_percent":15,"limit_id":""}`,
			expectLimitID: "codex-weekly",
			expectOK:      true,
		},
		{
			name:          "explicit limit_id is preserved (5h)",
			json:          `{"window_minutes":300,"used_percent":40,"limit_id":"custom"}`,
			expectLimitID: "custom",
			expectOK:      true,
		},
		{
			name:          "explicit limit_id is preserved (7d)",
			json:          `{"window_minutes":10080,"used_percent":30,"limit_id":"codex-weekly"}`,
			expectLimitID: "codex-weekly",
			expectOK:      true,
		},
		{
			name:          "unknown window uses default codex",
			json:          `{"window_minutes":60,"used_percent":50}`,
			expectLimitID: "codex",
			expectOK:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			window, ok := codexQuotaWindowFromRaw([]byte(tc.json))
			if ok != tc.expectOK {
				t.Fatalf("codexQuotaWindowFromRaw ok: got %v, want %v", ok, tc.expectOK)
			}
			if !ok {
				return
			}
			if window.LimitID == "" {
				t.Fatalf("LimitID must not be empty; got %q", window.LimitID)
			}
			if window.LimitID != tc.expectLimitID {
				t.Fatalf("LimitID: got %q, want %q", window.LimitID, tc.expectLimitID)
			}
		})
	}
}
