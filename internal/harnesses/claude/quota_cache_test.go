package claude

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClaudeAccount() *harnesses.AccountInfo {
	return &harnesses.AccountInfo{PlanType: "Claude Max"}
}

func TestClaudeQuotaCachePathXDG(t *testing.T) {
	t.Setenv(claudeQuotaCacheEnv, "")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	path, err := claudeQuotaCachePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/xdg-state", "fizeau", "claude-quota.json"), path)
}

func TestClaudeQuotaCachePathHomeFallback(t *testing.T) {
	t.Setenv(claudeQuotaCacheEnv, "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/tmp/fake-home")
	path, err := claudeQuotaCachePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/fake-home", ".local", "state", "fizeau", "claude-quota.json"), path)
}

func TestClaudeQuotaCachePathEnvOverride(t *testing.T) {
	t.Setenv(claudeQuotaCacheEnv, "/tmp/override/cq.json")
	path, err := claudeQuotaCachePath()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override/cq.json", path)
}

func TestClaudeQuotaSnapshotRoundTrip(t *testing.T) {
	captured := time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC)
	original := claudeQuotaSnapshot{
		CapturedAt:        captured,
		FiveHourRemaining: 7500,
		FiveHourLimit:     10000,
		WeeklyRemaining:   40000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded claudeQuotaSnapshot
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.True(t, decoded.CapturedAt.Equal(original.CapturedAt))
	assert.Equal(t, original.FiveHourRemaining, decoded.FiveHourRemaining)
	assert.Equal(t, original.FiveHourLimit, decoded.FiveHourLimit)
	assert.Equal(t, original.WeeklyRemaining, decoded.WeeklyRemaining)
	assert.Equal(t, original.WeeklyLimit, decoded.WeeklyLimit)
	assert.Equal(t, original.Source, decoded.Source)
}

func TestWriteClaudeQuotaAtomicAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "claude-quota.json")

	snap := claudeQuotaSnapshot{
		CapturedAt:        time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
		FiveHourRemaining: 8000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   60000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}

	require.NoError(t, writeClaudeQuota(path, snap))

	// Read back and compare.
	loaded, ok := readClaudeQuotaFrom(path)
	require.True(t, ok)
	require.NotNil(t, loaded)
	assert.True(t, loaded.CapturedAt.Equal(snap.CapturedAt))
	assert.Equal(t, snap.FiveHourRemaining, loaded.FiveHourRemaining)
	assert.Equal(t, snap.FiveHourLimit, loaded.FiveHourLimit)
	assert.Equal(t, snap.WeeklyRemaining, loaded.WeeklyRemaining)
	assert.Equal(t, snap.WeeklyLimit, loaded.WeeklyLimit)
	assert.Equal(t, snap.Source, loaded.Source)

	// Verify mode is 0600.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())

	// Verify the containing directory is not world-readable.
	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o750), dirInfo.Mode().Perm())

	// No leftover tmp file.
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "tmp file should not remain after rename")

	// Overwriting the same path works (atomic replace).
	snap.FiveHourRemaining = 100
	require.NoError(t, writeClaudeQuota(path, snap))
	loaded2, ok := readClaudeQuotaFrom(path)
	require.True(t, ok)
	assert.Equal(t, 100, loaded2.FiveHourRemaining)
}

func TestReadClaudeQuotaMissingReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")
	snap, ok := readClaudeQuotaFrom(path)
	assert.False(t, ok)
	assert.Nil(t, snap)
}

func TestReadClaudeQuotaCorruptReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	snap, ok := readClaudeQuotaFrom(path)
	assert.False(t, ok)
	assert.Nil(t, snap)
}

func TestReadClaudeQuotaUsesDefaultCachePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-quota.json")
	t.Setenv(claudeQuotaCacheEnv, path)

	snap := claudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 1,
		FiveHourLimit:     2,
		WeeklyRemaining:   3,
		WeeklyLimit:       4,
		Source:            "pty",
	}
	require.NoError(t, writeClaudeQuota(path, snap))

	loaded, ok := readClaudeQuota()
	require.True(t, ok)
	require.NotNil(t, loaded)
	assert.Equal(t, 1, loaded.FiveHourRemaining)
	assert.Equal(t, "pty", loaded.Source)
}

func TestReadClaudeQuotaDoesNotReadLegacyEnvPath(t *testing.T) {
	dir := t.TempDir()

	newPath := filepath.Join(dir, "new-claude-quota.json")
	legacyPath := filepath.Join(dir, "old-claude-quota.json")

	t.Setenv(claudeQuotaCacheEnv, newPath)
	t.Setenv("DDX_CLAUDE_QUOTA_CACHE", legacyPath)

	legacySnap := claudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 9999,
		FiveHourLimit:     10000,
		WeeklyRemaining:   65000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}
	require.NoError(t, writeClaudeQuota(legacyPath, legacySnap))

	loaded, ok := readClaudeQuota()
	assert.False(t, ok, "legacy DDx cache env must not be read")
	assert.Nil(t, loaded)
}

func TestReadClaudeQuotaUsesNewPathOnly(t *testing.T) {
	dir := t.TempDir()

	newPath := filepath.Join(dir, "new-claude-quota.json")
	legacyPath := filepath.Join(dir, "old-claude-quota.json")

	t.Setenv(claudeQuotaCacheEnv, newPath)
	t.Setenv("DDX_CLAUDE_QUOTA_CACHE", legacyPath)

	newSnap := claudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 1111,
		FiveHourLimit:     10000,
		WeeklyRemaining:   20000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}
	legacySnap := claudeQuotaSnapshot{
		CapturedAt:        time.Now().UTC(),
		FiveHourRemaining: 9999,
		FiveHourLimit:     10000,
		WeeklyRemaining:   65000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}
	require.NoError(t, writeClaudeQuota(newPath, newSnap))
	require.NoError(t, writeClaudeQuota(legacyPath, legacySnap))

	loaded, ok := readClaudeQuota()
	require.True(t, ok)
	require.NotNil(t, loaded)
	assert.Equal(t, 1111, loaded.FiveHourRemaining, "new path should take precedence over old path")
}

func TestIsClaudeQuotaFresh(t *testing.T) {
	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		snapshot   *claudeQuotaSnapshot
		staleAfter time.Duration
		wantFresh  bool
	}{
		{
			name:      "nil snapshot",
			snapshot:  nil,
			wantFresh: false,
		},
		{
			name: "zero captured_at",
			snapshot: &claudeQuotaSnapshot{
				CapturedAt: time.Time{},
			},
			wantFresh: false,
		},
		{
			name: "fresh within default ttl",
			snapshot: &claudeQuotaSnapshot{
				CapturedAt: now.Add(-2 * time.Minute),
			},
			wantFresh: true,
		},
		{
			name: "stale past default ttl",
			snapshot: &claudeQuotaSnapshot{
				CapturedAt: now.Add(-20 * time.Minute),
			},
			wantFresh: false,
		},
		{
			name: "fresh under custom ttl",
			snapshot: &claudeQuotaSnapshot{
				CapturedAt: now.Add(-1 * time.Hour),
			},
			staleAfter: 2 * time.Hour,
			wantFresh:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isClaudeQuotaFresh(tc.snapshot, now, tc.staleAfter)
			assert.Equal(t, tc.wantFresh, got)
		})
	}
}

func TestClaudeQuotaSnapshotAge(t *testing.T) {
	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	// Nil snapshot.
	assert.Equal(t, time.Duration(0), claudeQuotaSnapshotAge(nil, now))
	// Zero time.
	assert.Equal(t, time.Duration(0), claudeQuotaSnapshotAge(&claudeQuotaSnapshot{}, now))
	// Future captured_at.
	future := &claudeQuotaSnapshot{CapturedAt: now.Add(time.Hour)}
	assert.Equal(t, time.Duration(0), claudeQuotaSnapshotAge(future, now))
	// Past captured_at.
	past := &claudeQuotaSnapshot{CapturedAt: now.Add(-3 * time.Minute)}
	assert.Equal(t, 3*time.Minute, claudeQuotaSnapshotAge(past, now))
}

func TestDecideClaudeQuotaRouting(t *testing.T) {
	now := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)

	fresh := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Source:            "pty",
		Account:           testClaudeAccount(),
	}
	freshExhausted := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 0,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Source:            "pty",
		Account:           testClaudeAccount(),
	}
	freshWeeklyZero := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   0,
		WeeklyLimit:       70000,
		Source:            "pty",
		Account:           testClaudeAccount(),
	}
	freshExtraExhausted := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Windows: []harnesses.QuotaWindow{
			{Name: "Current session", LimitID: "session", UsedPercent: 10, State: "ok"},
			{Name: "Current week (all models)", LimitID: "weekly-all", UsedPercent: 42, State: "ok"},
			{Name: "Extra usage", LimitID: "extra", UsedPercent: 100, State: "exhausted"},
		},
		Source:  "pty",
		Account: testClaudeAccount(),
	}
	stale := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-20 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}
	freshMissingAccount := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Source:            "pty",
	}
	freshMissingSource := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 5000,
		FiveHourLimit:     10000,
		WeeklyRemaining:   50000,
		WeeklyLimit:       70000,
		Account:           testClaudeAccount(),
	}
	freshInvalidLimits := &claudeQuotaSnapshot{
		CapturedAt:        now.Add(-1 * time.Minute),
		FiveHourRemaining: 101,
		FiveHourLimit:     100,
		WeeklyRemaining:   50,
		WeeklyLimit:       100,
		Source:            "pty",
		Account:           testClaudeAccount(),
	}

	cases := []struct {
		name            string
		snapshot        *claudeQuotaSnapshot
		wantPrefer      bool
		wantPresent     bool
		wantFresh       bool
		wantReasonSubst string
	}{
		{
			name:            "missing snapshot -> fall back",
			snapshot:        nil,
			wantPrefer:      false,
			wantPresent:     false,
			wantFresh:       false,
			wantReasonSubst: "no cached snapshot",
		},
		{
			name:            "stale snapshot -> fall back",
			snapshot:        stale,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       false,
			wantReasonSubst: "stale",
		},
		{
			name:            "fresh with headroom -> prefer claude",
			snapshot:        fresh,
			wantPrefer:      true,
			wantPresent:     true,
			wantFresh:       true,
			wantReasonSubst: "headroom",
		},
		{
			name:            "fresh but 5h exhausted -> fall back",
			snapshot:        freshExhausted,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       true,
			wantReasonSubst: "exhausted",
		},
		{
			name:            "fresh but weekly exhausted -> fall back",
			snapshot:        freshWeeklyZero,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       true,
			wantReasonSubst: "exhausted",
		},
		{
			name:            "fresh but extra usage exhausted -> fall back",
			snapshot:        freshExtraExhausted,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       true,
			wantReasonSubst: "extra",
		},
		{
			name:            "fresh but missing account -> fall back",
			snapshot:        freshMissingAccount,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       false,
			wantReasonSubst: "missing account",
		},
		{
			name:            "fresh but missing source -> fall back",
			snapshot:        freshMissingSource,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       false,
			wantReasonSubst: "missing source",
		},
		{
			name:            "fresh but impossible remaining -> fall back",
			snapshot:        freshInvalidLimits,
			wantPrefer:      false,
			wantPresent:     true,
			wantFresh:       false,
			wantReasonSubst: "invalid 5h remaining",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideClaudeQuotaRouting(tc.snapshot, now, defaultClaudeQuotaStaleAfter)
			assert.Equal(t, tc.wantPrefer, d.PreferClaude)
			assert.Equal(t, tc.wantPresent, d.SnapshotPresent)
			assert.Equal(t, tc.wantFresh, d.Fresh)
			assert.Contains(t, d.Reason, tc.wantReasonSubst)
		})
	}
}

func TestClaudeQuotaExhaustedMessageMarksCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-quota.json")
	t.Setenv(claudeQuotaCacheEnv, path)
	now := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)

	ok := markClaudeQuotaExhaustedFromMessage("You're out of extra usage · resets May 7, 12am (America/New_York)", now)
	require.True(t, ok)

	snap, present := readClaudeQuotaFrom(path)
	require.True(t, present)
	dec := decideClaudeQuotaRouting(snap, now, defaultClaudeQuotaStaleAfter)
	assert.False(t, dec.PreferClaude)
	assert.True(t, dec.Fresh)
	assert.Contains(t, dec.Reason, "exhausted")
	assert.Contains(t, dec.Reason, "weekly-all")
}
