package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestGeminiPortableRuntimeMixedStateProjection(t *testing.T) {
	sources := writeGeminiPortableStateFixture(t, true, true)
	state, err := inspectGeminiPortableState(context.Background(), sources)
	if err != nil {
		t.Fatalf("inspectGeminiPortableState() error = %v", err)
	}
	wantAssets := []harnesses.PortableRuntimeAsset{
		geminiPortableStateTestAsset(t, sources.settings, geminiPortableSettingsTarget, harnesses.PortableRuntimeAssetConfig),
		geminiPortableStateTestAsset(t, sources.oauth, geminiPortableOAuthTarget, harnesses.PortableRuntimeAssetCredential),
		geminiPortableStateTestAsset(t, sources.accounts, geminiPortableAccountsTarget, harnesses.PortableRuntimeAssetCache),
		geminiPortableStateTestAsset(t, sources.quota, geminiPortableQuotaTarget, harnesses.PortableRuntimeAssetQuota),
	}
	if !reflect.DeepEqual(state.assets, wantAssets) {
		t.Fatalf("assets = %#v, want exact four-member schema %#v", state.assets, wantAssets)
	}
	wantProjection := []harnesses.PortableRuntimeStateProjection{{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: ".gemini"},
		Entries: []harnesses.PortableRuntimeStateProjectionEntry{
			{AssetTarget: "config/gemini/settings.json", Target: "settings.json"},
			{AssetTarget: "state/gemini/oauth_creds.json", Target: "oauth_creds.json"},
			{AssetTarget: "state/gemini/google_accounts.json", Target: "google_accounts.json"},
		},
	}}
	if !reflect.DeepEqual(state.stateProjections, wantProjection) {
		t.Fatalf("state projection = %#v, want %#v", state.stateProjections, wantProjection)
	}
	for _, asset := range state.assets {
		if asset.PathKind != harnesses.PortableRuntimePathFile || asset.Executable || strings.ContainsAny(asset.Target, "*?[{") {
			t.Fatalf("state member is not one exact non-executable file: %#v", asset)
		}
	}

	if err := validateGeminiPortableProjectedSettings([]byte(validGeminiPortableSettingsJSON())); err != nil {
		t.Fatalf("exact portable-safe settings rejected: %v", err)
	}
	unsafe := []string{
		geminiPortableSettingsWithExtra(`"extensions":{"enabled":true}`),
		geminiPortableSettingsWithExtra(`"mcpServers":{"private":{"command":"account-secret-command"}}`),
		geminiPortableSettingsWithExtra(`"hooks":{"BeforeAgent":[{"command":"account-secret-command"}]}`),
		geminiPortableSettingsWithExtra(`"skills":{"enabled":true}`),
		geminiPortableSettingsWithExtra(`"agents":{"private":true}`),
		geminiPortableSettingsWithExtra(`"browserHelpers":{"command":"account-secret-command"}`),
		geminiPortableSettingsWithExtra(`"commands":{"private":"account-secret-command"}`),
		geminiPortableSettingsWithExtra(`"policyPaths":["/private/account/policy"]`),
		geminiPortableSettingsWithExtra(`"context":{"fileName":"/private/account/context"}`),
		geminiPortableSettingsWithExtra(`"tools":{"futureExecutableSurface":"account-secret-command"}`),
		geminiPortableSettingsWithExtra(`"ui":{"theme":"future-safe-looking-key"}`),
		`{"security":{"auth":{"selectedType":"oauth-personal","futureCommand":"account-secret-command"}}}`,
	}
	for i, document := range unsafe {
		if err := validateGeminiPortableProjectedSettings([]byte(document)); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("unsafe settings case %d error = %v, want typed rejection", i, err)
		}
	}

	home := t.TempDir()
	productionSources := writeGeminiPortableStateFixtureAt(t, home, true, true)
	t.Setenv("HOME", home)
	t.Setenv(geminiQuotaCacheEnv, productionSources.quota)
	resolved, err := inspectGeminiPortableStateForHome(context.Background(), home)
	if err != nil {
		t.Fatalf("inspectGeminiPortableStateForHome() error = %v", err)
	}
	for _, asset := range resolved.assets {
		wantSource := map[string]string{
			geminiPortableSettingsTarget: filepath.Join(home, ".gemini", "settings.json"),
			geminiPortableOAuthTarget:    filepath.Join(home, ".gemini", "oauth_creds.json"),
			geminiPortableAccountsTarget: filepath.Join(home, ".gemini", "google_accounts.json"),
			geminiPortableQuotaTarget:    productionSources.quota,
		}[asset.Target]
		if asset.Source != wantSource {
			t.Fatalf("production source for %q = %q, want %q", asset.Target, asset.Source, wantSource)
		}
	}
	if normalized := normalizeGeminiPortableStateContribution(t, resolved); len(normalized.Assets) != 5 || len(normalized.StateProjections) != 1 {
		t.Fatalf("normalized production state integration = %#v", normalized)
	}
}

func TestGeminiPortableRuntimeAbsentOptionalState(t *testing.T) {
	for _, test := range []struct {
		name   string
		target string
		path   func(geminiPortableStateSources) string
	}{
		{name: "account selection", target: geminiPortableAccountsTarget, path: func(s geminiPortableStateSources) string { return s.accounts }},
		{name: "quota", target: geminiPortableQuotaTarget, path: func(s geminiPortableStateSources) string { return s.quota }},
	} {
		t.Run(test.name, func(t *testing.T) {
			sources := writeGeminiPortableStateFixture(t, true, true)
			if err := os.Remove(test.path(sources)); err != nil {
				t.Fatal(err)
			}
			first, err := inspectGeminiPortableState(context.Background(), sources)
			if err != nil {
				t.Fatal(err)
			}
			second, err := inspectGeminiPortableState(context.Background(), sources)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("optional absence is nondeterministic: first=%#v second=%#v", first, second)
			}
			if geminiPortableStateHasTarget(first, test.target) {
				t.Fatalf("absent optional state fabricated target %q", test.target)
			}
			for _, projection := range first.stateProjections {
				for _, entry := range projection.Entries {
					if entry.AssetTarget == test.target {
						t.Fatalf("absent optional state left dangling projection entry %#v", entry)
					}
				}
			}
			if normalized := normalizeGeminiPortableStateContribution(t, first); len(normalized.Assets) != 4 {
				t.Fatalf("normalized optional-absence contribution has %d assets, want executable plus three state assets", len(normalized.Assets))
			}
			if _, err := os.Lstat(test.path(sources)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("optional host file was fabricated: %v", err)
			}
		})
	}

	for _, test := range []struct {
		name string
		path func(geminiPortableStateSources) string
	}{
		{name: "settings", path: func(s geminiPortableStateSources) string { return s.settings }},
		{name: "OAuth credential", path: func(s geminiPortableStateSources) string { return s.oauth }},
	} {
		t.Run("required "+test.name, func(t *testing.T) {
			sources := writeGeminiPortableStateFixture(t, true, true)
			if err := os.Remove(test.path(sources)); err != nil {
				t.Fatal(err)
			}
			state, err := inspectGeminiPortableState(context.Background(), sources)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || len(state.assets) != 0 || len(state.stateProjections) != 0 {
				t.Fatalf("missing required %s = (%#v, %v), want empty typed failure", test.name, state, err)
			}
			if _, err := os.Lstat(test.path(sources)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("required host file was fabricated: %v", err)
			}
		})
	}
}

func TestGeminiPortableRuntimeMutableStateScope(t *testing.T) {
	sources := writeGeminiPortableStateFixture(t, true, true)
	before := snapshotGeminiPortableStateSources(t, sources)
	state, err := inspectGeminiPortableState(context.Background(), sources)
	if err != nil {
		t.Fatal(err)
	}

	writable := map[string]harnesses.PortableRuntimeAssetKind{
		geminiPortableOAuthTarget:    harnesses.PortableRuntimeAssetCredential,
		geminiPortableAccountsTarget: harnesses.PortableRuntimeAssetCache,
		geminiPortableQuotaTarget:    harnesses.PortableRuntimeAssetQuota,
	}
	seenWritable := make(map[string]struct{}, len(writable))
	for _, asset := range state.assets {
		wantKind, isWritable := writable[asset.Target]
		if isWritable && asset.Kind != wantKind {
			t.Fatalf("writable target %q has kind %q, want %q", asset.Target, asset.Kind, wantKind)
		}
		if isWritable {
			seenWritable[asset.Target] = struct{}{}
		}
		if !isWritable && (asset.Kind == harnesses.PortableRuntimeAssetCredential || asset.Kind == harnesses.PortableRuntimeAssetCache || asset.Kind == harnesses.PortableRuntimeAssetQuota) {
			t.Fatalf("undeclared state target is writable by kind: %#v", asset)
		}
	}
	if len(seenWritable) != len(writable) || len(state.assets) == 0 || state.assets[0].Kind != harnesses.PortableRuntimeAssetConfig || state.assets[0].Target != geminiPortableSettingsTarget {
		t.Fatalf("immutable/writable ownership drifted: %#v", state.assets)
	}

	guest := materializeGeminiPortableStateForTest(t, state)
	for target := range writable {
		path := geminiPortableEffectiveGuestPath(t, guest, state, target)
		if err := os.WriteFile(path, []byte("activation-owned refresh"), 0o600); err != nil {
			t.Fatalf("refresh writable target %q: %v", target, err)
		}
	}
	for _, sibling := range []string{
		filepath.Join(guest, "home", ".gemini", "session-state.json"),
		filepath.Join(guest, "state", "fizeau", "gemini-quota.lock"),
	} {
		if err := os.WriteFile(sibling, []byte("activation-owned sibling"), 0o600); err != nil {
			t.Fatalf("create declared sibling state: %v", err)
		}
	}
	settingsGuest := geminiPortableEffectiveGuestPath(t, guest, state, geminiPortableSettingsTarget)
	if info, err := os.Stat(settingsGuest); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("immutable settings mode = %v/%v, want no write bits", info, err)
	}
	if after := snapshotGeminiPortableStateSources(t, sources); !reflect.DeepEqual(after, before) {
		t.Fatalf("guest mutation changed host state: before=%#v after=%#v", before, after)
	}
}

func TestGeminiPortableRuntimeStateRedaction(t *testing.T) {
	const (
		token      = "account-secret-token"
		email      = "operator@example.invalid"
		assignment = "PRIVATE_TOKEN=account-secret-token"
		command    = "account-secret-command --token=account-secret-token"
	)
	cases := []struct {
		name       string
		selectPath func(geminiPortableStateSources) string
		data       string
		want       string
	}{
		{name: "settings", selectPath: func(s geminiPortableStateSources) string { return s.settings }, data: `{"security":{"auth":{"selectedType":"oauth-personal"}},"hooks":{"command":"` + command + `"}}`, want: "portable runtime asset closure incomplete: settings document is outside the portable-safe schema"},
		{name: "credentials", selectPath: func(s geminiPortableStateSources) string { return s.oauth }, data: `{"refresh_token":"` + token + `","command":"` + command + `"}`, want: "portable runtime asset closure incomplete: OAuth credential state is invalid"},
		{name: "account selection", selectPath: func(s geminiPortableStateSources) string { return s.accounts }, data: `{"active":["` + email + `"],"old":["` + token + `"]}`, want: "portable runtime asset closure incomplete: account selection state is invalid"},
		{name: "quota", selectPath: func(s geminiPortableStateSources) string { return s.quota }, data: `{"captured_at":"2026-07-16T12:00:00Z","source":"pty","windows":[],"detail":"` + assignment + `","command":"` + command + `"}`, want: "portable runtime asset closure incomplete: quota state is invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), email, "account-secret-state")
			sources := writeGeminiPortableStateFixtureAt(t, root, true, true)
			path := test.selectPath(sources)
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			state, err := inspectGeminiPortableState(context.Background(), sources)
			if !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) || len(state.assets) != 0 {
				t.Fatalf("malformed %s = (%#v, %v), want empty typed failure", test.name, state, err)
			}
			if err.Error() != test.want {
				t.Fatalf("malformed %s diagnostic = %q, want exact generic %q", test.name, err, test.want)
			}
			environmentOutput := fmt.Sprint(geminiPortableExecutionConstraints().Environment)
			assertGeminiPortableStateRedacted(t, err.Error()+environmentOutput,
				root, path, token, email, assignment, command, test.data, geminiPortableStateDigest([]byte(test.data)),
				"oauth-personal", "pty", "2026-07-16T12:00:00Z", "BeforeAgent", "account-secret", "operator@")
		})
	}
}

func TestGeminiPortableRuntimeStateFileDescriptorIdentity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "settings.json")
	replacement := filepath.Join(root, "replacement.json")
	original := []byte(`{"security":{"auth":{"selectedType":"oauth-personal"}}}`)
	other := []byte(`{"security":{"auth":{"selectedType":"oauth-personal"}},"ui":{}}`)
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, other, 0o600); err != nil {
		t.Fatal(err)
	}
	data, digest, present, err := readGeminiPortableStateFileWithHook(source, true, func() error {
		if err := os.Rename(source, filepath.Join(root, "opened.json")); err != nil {
			return err
		}
		return os.Rename(replacement, source)
	})
	if err == nil || err.Error() != "state identity changed during discovery" || data != nil || digest != "" || present {
		t.Fatalf("swapped state read = (%q, %q, %t, %v), want empty identity failure", data, digest, present, err)
	}
	assertGeminiPortableStateRedacted(t, err.Error(), string(original), string(other), geminiPortableStateDigest(original), geminiPortableStateDigest(other), source)

	stable := filepath.Join(root, "stable.json")
	if err := os.WriteFile(stable, original, 0o600); err != nil {
		t.Fatal(err)
	}
	data, digest, present, err = readGeminiPortableStateFile(stable, true)
	if err != nil || !present || !bytes.Equal(data, original) || digest != geminiPortableStateDigest(original) {
		t.Fatalf("stable state read = (%q, %q, %t, %v), want exact bytes and digest", data, digest, present, err)
	}
}

func TestGeminiPortableRuntimeOAuthAuthParity(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).UnixMilli()
	expired := now.Add(-time.Hour).UnixMilli()
	tests := []struct {
		name          string
		document      string
		accessToken   string
		refreshToken  string
		expiryDate    int64
		authenticated bool
	}{
		{name: "refresh token", document: `{"refresh_token":"refresh-secret"}`, refreshToken: "refresh-secret", authenticated: true},
		{name: "refresh token ignores elapsed access expiry", document: fmt.Sprintf(`{"refresh_token":"refresh-secret","access_token":"access-secret","expiry_date":%d}`, expired), accessToken: "access-secret", refreshToken: "refresh-secret", expiryDate: expired, authenticated: true},
		{name: "access token without expiry", document: `{"access_token":"access-secret"}`, accessToken: "access-secret", authenticated: true},
		{name: "unexpired access token", document: fmt.Sprintf(`{"access_token":"access-secret","expiry_date":%d}`, future), accessToken: "access-secret", expiryDate: future, authenticated: true},
		{name: "expired access token", document: fmt.Sprintf(`{"access_token":"access-secret","expiry_date":%d}`, expired), accessToken: "access-secret", expiryDate: expired},
		{name: "empty tokens", document: `{"access_token":"","refresh_token":""}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotAuth := geminiOAuthCredentialsAuthenticated(test.accessToken, test.refreshToken, test.expiryDate, now)
			if gotAuth != test.authenticated {
				t.Fatalf("authoritative auth result = %t, want %t", gotAuth, test.authenticated)
			}
			err := validateGeminiPortableOAuthAt([]byte(test.document), now)
			if (err == nil) != test.authenticated {
				t.Fatalf("portable auth validation error = %v, authoritative authenticated = %t", err, test.authenticated)
			}
		})
	}
	for _, document := range []string{
		`{"access_token":"access-secret","expiry_date":0}`,
		`{"access_token":"access-secret","expiry_date":-1}`,
		`{"access_token":"access-secret","expiry_date":"tomorrow"}`,
	} {
		if err := validateGeminiPortableOAuthAt([]byte(document), now); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
			t.Fatalf("invalid expiry %q error = %v, want typed rejection", document, err)
		}
	}
}

func TestGeminiPortableRuntimeExactStateSchema(t *testing.T) {
	validQuota := validGeminiPortableQuotaJSON("pty", "Pro", "gemini-pro")
	cases := []struct {
		name     string
		validate func([]byte) error
		data     string
	}{
		{name: "settings SECURITY", validate: validateGeminiPortableProjectedSettings, data: `{"SECURITY":{"auth":{"selectedType":"oauth-personal"}}}`},
		{name: "settings AUTH", validate: validateGeminiPortableProjectedSettings, data: `{"security":{"AUTH":{"selectedType":"oauth-personal"}}}`},
		{name: "settings SELECTEDTYPE", validate: validateGeminiPortableProjectedSettings, data: `{"security":{"auth":{"SELECTEDTYPE":"oauth-personal"}}}`},
		{name: "OAuth REFRESH_TOKEN", validate: validateGeminiPortableOAuth, data: `{"refresh_token":"refresh-secret","REFRESH_TOKEN":"other-secret"}`},
		{name: "accounts ACTIVE", validate: validateGeminiPortableAccounts, data: `{"active":"active@example.invalid","old":[],"ACTIVE":"other@example.invalid"}`},
		{name: "quota SOURCE", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"SOURCE":"pty"}`},
		{name: "quota window NAME", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"name":"Pro"`, `"name":"Pro","NAME":"Flash"`, 1)},
		{name: "quota account PLAN_TYPE", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"account":{"plan_type":"Gemini","PLAN_TYPE":"other"}}`},
		{name: "accounts null root", validate: validateGeminiPortableAccounts, data: `null`},
		{name: "accounts null old", validate: validateGeminiPortableAccounts, data: `{"old":null}`},
		{name: "accounts wrong active type", validate: validateGeminiPortableAccounts, data: `{"active":["active@example.invalid"],"old":[]}`},
		{name: "quota mismatched tier", validate: validateGeminiPortableQuota, data: validGeminiPortableQuotaJSON("pty", "Flash", "gemini-pro")},
		{name: "quota missing captured_at", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"captured_at":"2026-07-16T12:00:00Z",`, "", 1)},
		{name: "quota null captured_at", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"captured_at":"2026-07-16T12:00:00Z"`, `"captured_at":null`, 1)},
		{name: "quota null windows", validate: validateGeminiPortableQuota, data: `{"captured_at":"2026-07-16T12:00:00Z","windows":null,"source":"pty"}`},
		{name: "quota missing source", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `,"source":"pty"`, "", 1)},
		{name: "quota null source", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"source":"pty"`, `"source":null`, 1)},
		{name: "quota null detail", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"detail":null}`},
		{name: "quota wrong detail type", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"detail":7}`},
		{name: "quota null account", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"account":null}`},
		{name: "quota null account email", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"account":{"email":null}}`},
		{name: "quota wrong account plan type", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"account":{"plan_type":7}}`},
		{name: "quota null account org name", validate: validateGeminiPortableQuota, data: strings.TrimSuffix(validQuota, "}") + `,"account":{"org_name":null}}`},
		{name: "quota missing window name", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"name":"Pro",`, "", 1)},
		{name: "quota null window name", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"name":"Pro"`, `"name":null`, 1)},
		{name: "quota missing limit id", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `,"limit_id":"gemini-pro"`, "", 1)},
		{name: "quota null limit id", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"limit_id":"gemini-pro"`, `"limit_id":null`, 1)},
		{name: "quota null limit name", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"state":"ok"`, `"limit_name":null,"state":"ok"`, 1)},
		{name: "quota missing window minutes", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `,"window_minutes":0`, "", 1)},
		{name: "quota null window minutes", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"window_minutes":0`, `"window_minutes":null`, 1)},
		{name: "quota wrong window minutes type", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"window_minutes":0`, `"window_minutes":"0"`, 1)},
		{name: "quota missing used percent", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `,"used_percent":10`, "", 1)},
		{name: "quota null used percent", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"used_percent":10`, `"used_percent":null`, 1)},
		{name: "quota wrong used percent type", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"used_percent":10`, `"used_percent":"10"`, 1)},
		{name: "quota null resets at", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"state":"ok"`, `"resets_at":null,"state":"ok"`, 1)},
		{name: "quota null resets at unix", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"state":"ok"`, `"resets_at_unix":null,"state":"ok"`, 1)},
		{name: "quota wrong resets at unix type", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"state":"ok"`, `"resets_at_unix":"1","state":"ok"`, 1)},
		{name: "quota missing state", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `,"state":"ok"`, "", 1)},
		{name: "quota null state", validate: validateGeminiPortableQuota, data: strings.Replace(validQuota, `"state":"ok"`, `"state":null`, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate([]byte(test.data)); !errors.Is(err, harnesses.ErrPortableRuntimeClosureIncomplete) {
				t.Fatalf("case-variant/malformed document accepted: %s", test.data)
			}
		})
	}
	for _, source := range []string{"pty", "cassette"} {
		if err := validateGeminiPortableQuota([]byte(validGeminiPortableQuotaJSON(source, "Pro", "gemini-pro"))); err != nil {
			t.Fatalf("authoritative quota source %q rejected: %v", source, err)
		}
	}
	richQuota := strings.Replace(validQuota, `"state":"ok"`, `"limit_name":"model tier","resets_at":"later","resets_at_unix":1,"state":"ok"`, 1)
	richQuota = strings.TrimSuffix(richQuota, "}") + `,"account":{"email":"operator@example.invalid","plan_type":"Gemini","org_name":"example"},"detail":"captured"}`
	if err := validateGeminiPortableQuota([]byte(richQuota)); err != nil {
		t.Fatalf("writer-shaped quota with populated optional scalars rejected: %v", err)
	}
	for _, document := range []string{`{}`, `{"active":null}`, `{"old":[]}`, `{"active":"","old":[""]}`} {
		if err := validateGeminiPortableAccounts([]byte(document)); err != nil {
			t.Fatalf("retained account format %s rejected: %v", document, err)
		}
	}
}

func installGeminiPortableStateFixture(t *testing.T) {
	t.Helper()
	sources := writeGeminiPortableStateFixture(t, true, true)
	originalState := discoverGeminiPortableState
	originalUserConfiguration := inspectGeminiPortableRuntimeUserConfiguration
	discoverGeminiPortableState = func(ctx context.Context, _ string) (geminiPortableState, error) {
		return inspectGeminiPortableState(ctx, sources)
	}
	inspectGeminiPortableRuntimeUserConfiguration = func(_ string) error {
		return inspectGeminiPortableUserConfiguration(filepath.Dir(filepath.Dir(sources.settings)))
	}
	t.Cleanup(func() {
		discoverGeminiPortableState = originalState
		inspectGeminiPortableRuntimeUserConfiguration = originalUserConfiguration
	})
}

func writeGeminiPortableStateFixture(t *testing.T, accounts, quota bool) geminiPortableStateSources {
	t.Helper()
	return writeGeminiPortableStateFixtureAt(t, t.TempDir(), accounts, quota)
}

func writeGeminiPortableStateFixtureAt(t *testing.T, root string, accounts, quota bool) geminiPortableStateSources {
	t.Helper()
	geminiRoot := filepath.Join(root, ".gemini")
	sources := geminiPortableStateSources{
		settings: filepath.Join(geminiRoot, geminiPortableSettingsName),
		oauth:    filepath.Join(geminiRoot, geminiPortableOAuthName),
		accounts: filepath.Join(geminiRoot, geminiPortableAccountsName),
		quota:    filepath.Join(root, "state", "fizeau", "gemini-quota.json"),
	}
	writeGeminiPortableStateTestFile(t, sources.settings, validGeminiPortableSettingsJSON())
	writeGeminiPortableStateTestFile(t, sources.oauth, `{"access_token":"access-secret","refresh_token":"refresh-secret","scope":"openid email","token_type":"Bearer","expiry_date":4102444800000}`)
	if accounts {
		writeGeminiPortableStateTestFile(t, sources.accounts, `{"active":"active@example.invalid","old":["old@example.invalid"]}`)
	}
	if quota {
		if err := writeGeminiQuota(sources.quota, geminiQuotaSnapshot{
			CapturedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
			Source:     "pty",
			Windows: []harnesses.QuotaWindow{{
				Name: "Pro", LimitID: "gemini-pro", UsedPercent: 10, State: "ok",
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return sources
}

func validGeminiPortableSettingsJSON() string {
	return `{"security":{"auth":{"selectedType":"oauth-personal"}}}`
}

func geminiPortableSettingsWithExtra(extra string) string {
	return strings.TrimSuffix(validGeminiPortableSettingsJSON(), "}") + "," + extra + "}"
}

func validGeminiPortableQuotaJSON(source, name, limitID string) string {
	return fmt.Sprintf(`{"captured_at":"2026-07-16T12:00:00Z","windows":[{"name":%q,"limit_id":%q,"window_minutes":0,"used_percent":10,"state":"ok"}],"source":%q}`, name, limitID, source)
}

func normalizeGeminiPortableStateContribution(t *testing.T, state geminiPortableState) harnesses.PortableRuntimeContribution {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("portable runtime contribution validation is Linux-only")
	}
	executable := filepath.Join(t.TempDir(), "gemini-state-fixture")
	if err := os.WriteFile(executable, []byte("fixture executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := harnesses.PortableRuntimeFileDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	contribution := harnesses.PortableRuntimeContribution{
		ClosureClass: harnesses.PortableRuntimeClosureStatic,
		Launch:       harnesses.PortableRuntimeLaunch{EntrypointTarget: "bin/gemini-state-fixture"},
		Assets: []harnesses.PortableRuntimeAsset{{
			Kind: harnesses.PortableRuntimeAssetExecutable, PathKind: harnesses.PortableRuntimePathFile,
			Source: executable, Target: "bin/gemini-state-fixture", ContentSHA256: digest, Executable: true,
		}},
		StateProjections: state.stateProjections,
	}
	contribution.Assets = append(contribution.Assets, state.assets...)
	normalized, err := harnesses.NormalizePortableRuntimeContribution(
		harnesses.PortableRuntimeTarget{GOOS: "linux", GOARCH: runtime.GOARCH}, contribution)
	if err != nil {
		t.Fatalf("NormalizePortableRuntimeContribution() error = %v", err)
	}
	return normalized
}

func writeGeminiPortableStateTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func geminiPortableStateTestAsset(t *testing.T, source, target string, kind harnesses.PortableRuntimeAssetKind) harnesses.PortableRuntimeAsset {
	t.Helper()
	digest, err := harnesses.PortableRuntimeFileDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	return harnesses.PortableRuntimeAsset{Kind: kind, PathKind: harnesses.PortableRuntimePathFile, Source: source, Target: target, ContentSHA256: digest}
}

func geminiPortableStateHasTarget(state geminiPortableState, target string) bool {
	for _, asset := range state.assets {
		if asset.Target == target {
			return true
		}
	}
	return false
}

type geminiPortableHostState struct {
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

func snapshotGeminiPortableStateSources(t *testing.T, sources geminiPortableStateSources) map[string]geminiPortableHostState {
	t.Helper()
	result := make(map[string]geminiPortableHostState)
	for _, path := range []string{sources.settings, sources.oauth, sources.accounts, sources.quota} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		result[path] = geminiPortableHostState{data: data, mode: info.Mode(), modTime: info.ModTime()}
	}
	return result
}

func materializeGeminiPortableStateForTest(t *testing.T, state geminiPortableState) string {
	t.Helper()
	root := t.TempDir()
	projectionTargets := make(map[string]string)
	for _, projection := range state.stateProjections {
		for _, entry := range projection.Entries {
			projectionTargets[entry.AssetTarget] = filepath.Join(string(projection.Directory.Scope), filepath.FromSlash(projection.Directory.Target), filepath.FromSlash(entry.Target))
		}
	}
	for _, asset := range state.assets {
		target := filepath.FromSlash(asset.Target)
		if projected, ok := projectionTargets[asset.Target]; ok {
			target = projected
		}
		destination := filepath.Join(root, target)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(asset.Source)
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if asset.Kind == harnesses.PortableRuntimeAssetConfig {
			mode = 0o400
		}
		if err := os.WriteFile(destination, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func geminiPortableEffectiveGuestPath(t *testing.T, root string, state geminiPortableState, assetTarget string) string {
	t.Helper()
	for _, projection := range state.stateProjections {
		for _, entry := range projection.Entries {
			if entry.AssetTarget == assetTarget {
				return filepath.Join(root, string(projection.Directory.Scope), filepath.FromSlash(projection.Directory.Target), filepath.FromSlash(entry.Target))
			}
		}
	}
	if geminiPortableStateHasTarget(state, assetTarget) {
		return filepath.Join(root, filepath.FromSlash(assetTarget))
	}
	t.Fatalf("unknown asset target %q", assetTarget)
	return ""
}

func assertGeminiPortableStateRedacted(t *testing.T, output string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(output, value) {
			t.Fatalf("state diagnostic/output exposed sensitive value %q: %q", value, output)
		}
	}
}

func geminiPortableStateDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
