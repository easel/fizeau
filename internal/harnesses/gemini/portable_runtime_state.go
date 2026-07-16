package gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/safefs"
)

const (
	geminiPortableSettingsTarget = "config/gemini/settings.json"
	geminiPortableOAuthTarget    = "state/gemini/oauth_creds.json"
	geminiPortableAccountsTarget = "state/gemini/google_accounts.json"
	geminiPortableQuotaTarget    = "state/fizeau/gemini-quota.json"
	geminiPortableStateDirectory = ".gemini"
	geminiPortableSettingsName   = "settings.json"
	geminiPortableOAuthName      = "oauth_creds.json"
	geminiPortableAccountsName   = "google_accounts.json"
	geminiPortableMaxStateBytes  = 8 << 20
)

type geminiPortableStateSources struct {
	settings string
	oauth    string
	accounts string
	quota    string
}

type geminiPortableState struct {
	assets           []harnesses.PortableRuntimeAsset
	stateProjections []harnesses.PortableRuntimeStateProjection
}

type geminiPortableStateDescriptor struct {
	name       string
	source     string
	target     string
	targetName string
	kind       harnesses.PortableRuntimeAssetKind
	required   bool
	validate   func([]byte) error
}

// discoverGeminiPortableState is a narrow source-discovery seam. Production
// uses the exact user and Fizeau cache paths below; retained-layout tests swap
// in isolated state without copying or changing the operator's credentials.
var discoverGeminiPortableState = inspectGeminiPortableStateForHome

func inspectGeminiPortableStateForHome(ctx context.Context, home string) (geminiPortableState, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return geminiPortableState{}, geminiPortableStateError("state root is unavailable")
	}
	quota, err := geminiQuotaCachePath()
	if err != nil {
		return geminiPortableState{}, geminiPortableStateError("quota state is unavailable")
	}
	root := filepath.Join(home, geminiPortableStateDirectory)
	return inspectGeminiPortableState(ctx, geminiPortableStateSources{
		settings: filepath.Join(root, geminiPortableSettingsName),
		oauth:    filepath.Join(root, geminiPortableOAuthName),
		accounts: filepath.Join(root, geminiPortableAccountsName),
		quota:    quota,
	})
}

func inspectGeminiPortableState(ctx context.Context, sources geminiPortableStateSources) (geminiPortableState, error) {
	if ctx == nil {
		return geminiPortableState{}, geminiPortableStateError("state discovery context is unavailable")
	}
	descriptors := []geminiPortableStateDescriptor{
		{name: "settings", source: sources.settings, target: geminiPortableSettingsTarget, targetName: geminiPortableSettingsName, kind: harnesses.PortableRuntimeAssetConfig, required: true, validate: validateGeminiPortableProjectedSettings},
		{name: "OAuth credential", source: sources.oauth, target: geminiPortableOAuthTarget, targetName: geminiPortableOAuthName, kind: harnesses.PortableRuntimeAssetCredential, required: true, validate: validateGeminiPortableOAuth},
		{name: "account selection", source: sources.accounts, target: geminiPortableAccountsTarget, targetName: geminiPortableAccountsName, kind: harnesses.PortableRuntimeAssetCache, validate: validateGeminiPortableAccounts},
		{name: "quota", source: sources.quota, target: geminiPortableQuotaTarget, kind: harnesses.PortableRuntimeAssetQuota, validate: validateGeminiPortableQuota},
	}

	result := geminiPortableState{}
	projection := harnesses.PortableRuntimeStateProjection{
		Directory: harnesses.PortableRuntimeGuestPath{Scope: harnesses.PortableRuntimeGuestPathHome, Target: geminiPortableStateDirectory},
	}
	for _, descriptor := range descriptors {
		if err := ctx.Err(); err != nil {
			return geminiPortableState{}, geminiPortableStateError("state discovery was canceled")
		}
		data, digest, present, err := readGeminiPortableStateFile(descriptor.source, descriptor.required)
		if err != nil {
			return geminiPortableState{}, geminiPortableStateError(descriptor.name + " state is unavailable")
		}
		if !present {
			continue
		}
		if err := descriptor.validate(data); err != nil {
			return geminiPortableState{}, err
		}
		result.assets = append(result.assets, harnesses.PortableRuntimeAsset{
			Kind: descriptor.kind, PathKind: harnesses.PortableRuntimePathFile,
			Source: descriptor.source, Target: descriptor.target, ContentSHA256: digest,
		})
		if descriptor.targetName != "" {
			projection.Entries = append(projection.Entries, harnesses.PortableRuntimeStateProjectionEntry{
				AssetTarget: descriptor.target,
				Target:      descriptor.targetName,
			})
		}
	}
	// settings.json and oauth_creds.json are required, so a successful result
	// always has both the immutable and writable sides required by the neutral
	// mixed-state projection contract.
	result.stateProjections = []harnesses.PortableRuntimeStateProjection{projection}
	return result, nil
}

func readGeminiPortableStateFile(source string, required bool) ([]byte, string, bool, error) {
	return readGeminiPortableStateFileWithHook(source, required, nil)
}

func readGeminiPortableStateFileWithHook(source string, required bool, afterOpen func() error) ([]byte, string, bool, error) {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return nil, "", false, errors.New("invalid source")
	}
	pathBefore, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		if required {
			return nil, "", false, errors.New("required state is missing")
		}
		return nil, "", false, nil
	}
	if err != nil || !geminiPortableStateFileMode(pathBefore.Mode()) {
		return nil, "", false, errors.New("unsupported state source")
	}
	file, err := safefs.OpenRead(source)
	if err != nil {
		return nil, "", false, errors.New("state cannot be read")
	}
	descriptorBefore, err := file.Stat()
	if err != nil || !geminiPortableSameStateFile(pathBefore, descriptorBefore) {
		_ = file.Close()
		return nil, "", false, errors.New("state identity changed during discovery")
	}
	if afterOpen != nil {
		if err := afterOpen(); err != nil {
			_ = file.Close()
			return nil, "", false, errors.New("state identity changed during discovery")
		}
	}
	data, readErr := io.ReadAll(io.LimitReader(file, geminiPortableMaxStateBytes+1))
	if readErr == nil {
		_, readErr = file.Seek(0, io.SeekStart)
	}
	var confirmation []byte
	if readErr == nil {
		confirmation, readErr = io.ReadAll(io.LimitReader(file, geminiPortableMaxStateBytes+1))
	}
	descriptorAfter, statErr := file.Stat()
	closeErr := file.Close()
	pathAfter, pathErr := os.Lstat(source)
	if readErr != nil || statErr != nil || closeErr != nil || pathErr != nil ||
		len(data) > geminiPortableMaxStateBytes || len(confirmation) > geminiPortableMaxStateBytes {
		return nil, "", false, errors.New("state cannot be read")
	}
	if !bytes.Equal(data, confirmation) || int64(len(data)) != descriptorBefore.Size() ||
		!geminiPortableSameStateFile(descriptorBefore, descriptorAfter) ||
		!geminiPortableSameStateFile(descriptorAfter, pathAfter) {
		return nil, "", false, errors.New("state identity changed during discovery")
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), true, nil
}

func geminiPortableStateFileMode(mode os.FileMode) bool {
	return mode.IsRegular() && mode&os.ModeSymlink == 0 && mode.Perm()&0o111 == 0
}

func geminiPortableSameStateFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Size() == right.Size() && left.Mode() == right.Mode() &&
		left.ModTime().Equal(right.ModTime())
}

type geminiPortableOAuth struct {
	AccessToken  *string `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	Scope        *string `json:"scope"`
	TokenType    *string `json:"token_type"`
	ExpiryDate   *int64  `json:"expiry_date"`
	IDToken      *string `json:"id_token"`
}

type geminiPortableProjectedSettings struct {
	Security *geminiPortableProjectedSecuritySettings `json:"security"`
}

type geminiPortableProjectedSecuritySettings struct {
	Auth *geminiPortableProjectedAuthSettings `json:"auth"`
}

type geminiPortableProjectedAuthSettings struct {
	SelectedType string `json:"selectedType"`
}

// validateGeminiPortableProjectedSettings is deliberately allowlist-only.
// Ordinary dispatch retains its separate executable-source rejection above;
// only the state copied into a closed portable runtime uses this exact schema.
func validateGeminiPortableProjectedSettings(data []byte) error {
	root, ok := decodeGeminiPortableObject(data, "security")
	if !ok || len(root) != 1 {
		return geminiPortableStateError("settings document is outside the portable-safe schema")
	}
	security, ok := decodeGeminiPortableObject(root["security"], "auth")
	if !ok || len(security) != 1 {
		return geminiPortableStateError("settings document is outside the portable-safe schema")
	}
	auth, ok := decodeGeminiPortableObject(security["auth"], "selectedType")
	if !ok || len(auth) != 1 {
		return geminiPortableStateError("settings document is outside the portable-safe schema")
	}
	var settings geminiPortableProjectedSettings
	if !decodeGeminiPortableExactJSON(data, &settings) || settings.Security == nil ||
		settings.Security.Auth == nil || settings.Security.Auth.SelectedType != authTypeOAuth {
		return geminiPortableStateError("settings document is outside the portable-safe schema")
	}
	return nil
}

func validateGeminiPortableOAuth(data []byte) error {
	return validateGeminiPortableOAuthAt(data, time.Now())
}

func validateGeminiPortableOAuthAt(data []byte, now time.Time) error {
	if _, ok := decodeGeminiPortableObject(data,
		"access_token", "refresh_token", "scope", "token_type", "expiry_date", "id_token"); !ok {
		return geminiPortableStateError("OAuth credential state is invalid")
	}
	var credential geminiPortableOAuth
	if !decodeGeminiPortableExactJSON(data, &credential) || credential.ExpiryDate != nil && *credential.ExpiryDate <= 0 {
		return geminiPortableStateError("OAuth credential state is invalid")
	}
	expiryDate := int64(0)
	if credential.ExpiryDate != nil {
		expiryDate = *credential.ExpiryDate
	}
	accessToken := ""
	if credential.AccessToken != nil {
		accessToken = *credential.AccessToken
	}
	if !geminiOAuthCredentialsAuthenticated(accessToken, credential.RefreshToken, expiryDate, now) {
		return geminiPortableStateError("OAuth credential state is invalid")
	}
	return nil
}

type geminiPortableAccounts struct {
	Active *string  `json:"active"`
	Old    []string `json:"old"`
}

func validateGeminiPortableAccounts(data []byte) error {
	object, ok := decodeGeminiPortableObject(data, "active", "old")
	if !ok {
		return geminiPortableStateError("account selection state is invalid")
	}
	if active, present := object["active"]; present && !bytes.Equal(bytes.TrimSpace(active), []byte("null")) {
		var value string
		if !decodeGeminiPortableExactJSON(active, &value) {
			return geminiPortableStateError("account selection state is invalid")
		}
	}
	if old, present := object["old"]; present {
		var values []string
		if bytes.Equal(bytes.TrimSpace(old), []byte("null")) || !decodeGeminiPortableExactJSON(old, &values) {
			return geminiPortableStateError("account selection state is invalid")
		}
	}
	return nil
}

func validateGeminiPortableQuota(data []byte) error {
	object, ok := decodeGeminiPortableObject(data, "captured_at", "windows", "source", "account", "detail")
	if !ok || !geminiPortableRequiredString(object, "captured_at") ||
		!geminiPortableRequiredString(object, "source") ||
		!geminiPortableOptionalString(object, "detail") {
		return geminiPortableStateError("quota state is invalid")
	}
	var rawWindows []json.RawMessage
	if !decodeGeminiPortableExactJSON(object["windows"], &rawWindows) || rawWindows == nil {
		return geminiPortableStateError("quota state is invalid")
	}
	for _, rawWindow := range rawWindows {
		window, ok := decodeGeminiPortableObject(rawWindow,
			"name", "limit_id", "limit_name", "window_minutes", "used_percent", "resets_at", "resets_at_unix", "state")
		if !ok || !geminiPortableRequiredString(window, "name") ||
			!geminiPortableRequiredString(window, "limit_id") ||
			!geminiPortableOptionalString(window, "limit_name") ||
			!geminiPortableRequiredInteger(window, "window_minutes") ||
			!geminiPortableRequiredNumber(window, "used_percent") ||
			!geminiPortableOptionalString(window, "resets_at") ||
			!geminiPortableOptionalInteger(window, "resets_at_unix") ||
			!geminiPortableRequiredString(window, "state") {
			return geminiPortableStateError("quota state is invalid")
		}
	}
	if rawAccount, present := object["account"]; present {
		account, ok := decodeGeminiPortableObject(rawAccount, "email", "plan_type", "org_name")
		if !ok || !geminiPortableOptionalString(account, "email") ||
			!geminiPortableOptionalString(account, "plan_type") ||
			!geminiPortableOptionalString(account, "org_name") {
			return geminiPortableStateError("quota state is invalid")
		}
	}
	var quota geminiQuotaSnapshot
	if !decodeGeminiPortableExactJSON(data, &quota) || quota.CapturedAt.IsZero() || !geminiPortableKnownQuotaSource(quota.Source) || len(quota.Windows) == 0 {
		return geminiPortableStateError("quota state is invalid")
	}
	seen := make(map[string]struct{}, len(quota.Windows))
	for _, window := range quota.Windows {
		if !geminiPortableKnownTier(window.Name, window.LimitID) ||
			math.IsNaN(window.UsedPercent) || math.IsInf(window.UsedPercent, 0) ||
			window.UsedPercent < 0 || window.UsedPercent > 100 || window.State != geminiQuotaState(window.UsedPercent) {
			return geminiPortableStateError("quota state is invalid")
		}
		if _, exists := seen[window.LimitID]; exists {
			return geminiPortableStateError("quota state is invalid")
		}
		seen[window.LimitID] = struct{}{}
	}
	return nil
}

func geminiPortableKnownQuotaSource(source string) bool {
	return source == "pty" || source == "cassette"
}

func geminiPortableKnownTier(name, limitID string) bool {
	for _, tier := range geminiTiers {
		if tier.Label == name && tier.LimitID == limitID {
			return true
		}
	}
	return false
}

func geminiPortableRequiredString(object map[string]json.RawMessage, key string) bool {
	raw, present := object[key]
	return present && geminiPortableJSONString(raw)
}

func geminiPortableOptionalString(object map[string]json.RawMessage, key string) bool {
	raw, present := object[key]
	return !present || geminiPortableJSONString(raw)
}

func geminiPortableRequiredNumber(object map[string]json.RawMessage, key string) bool {
	raw, present := object[key]
	return present && geminiPortableJSONNumber(raw)
}

func geminiPortableRequiredInteger(object map[string]json.RawMessage, key string) bool {
	raw, present := object[key]
	return present && geminiPortableJSONInteger(raw)
}

func geminiPortableOptionalInteger(object map[string]json.RawMessage, key string) bool {
	raw, present := object[key]
	return !present || geminiPortableJSONInteger(raw)
}

func geminiPortableJSONString(raw json.RawMessage) bool {
	var value any
	if !decodeGeminiPortableExactJSON(raw, &value) {
		return false
	}
	_, ok := value.(string)
	return ok
}

func geminiPortableJSONNumber(raw json.RawMessage) bool {
	var value any
	if !decodeGeminiPortableExactJSON(raw, &value) {
		return false
	}
	_, ok := value.(json.Number)
	return ok
}

func geminiPortableJSONInteger(raw json.RawMessage) bool {
	var value int64
	return geminiPortableJSONNumber(raw) && decodeGeminiPortableExactJSON(raw, &value)
}

func decodeGeminiPortableObject(data []byte, allowedKeys ...string) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if !decodeGeminiPortableExactJSON(data, &object) || object == nil {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return nil, false
		}
	}
	return object, true
}

func decodeGeminiPortableExactJSON(data []byte, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if decoder.Decode(destination) != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func geminiPortableStateError(message string) error {
	return geminiPortableRuntimeError(message)
}
