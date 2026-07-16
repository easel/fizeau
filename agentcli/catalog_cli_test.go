package agentcli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type fakeCatalogServer struct {
	server         *httptest.Server
	indexByPath    map[string]string
	manifestByPath map[string]string
}

func legacyCLIName() string {
	return "ddx-" + "agent"
}

type recordedChatRequest struct {
	Model              string         `json:"model"`
	Thinking           map[string]any `json:"thinking,omitempty"`
	EnableThinking     *bool          `json:"enable_thinking,omitempty"`
	ThinkingBudget     *int           `json:"thinking_budget,omitempty"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type fakeOpenAIServer struct {
	server          *httptest.Server
	mu              sync.Mutex
	modelsSeen      []string
	thinkingBudgets []int
}

func newFakeOpenAIServer(t *testing.T) *fakeOpenAIServer {
	t.Helper()
	fake := &fakeOpenAIServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"stub-model"},{"id":"gpt-4o"},{"id":"qwen-smart"},{"id":"qwen3.5-27b"}]}`))
		case "/v1/chat/completions":
			require.Equal(t, http.MethodPost, r.Method)
			defer r.Body.Close()

			var req recordedChatRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

			fake.mu.Lock()
			fake.modelsSeen = append(fake.modelsSeen, req.Model)
			// Accept the provider wire shapes used by OpenAI-compatible
			// backends: the Anthropic-style `thinking` map, legacy top-level
			// Qwen fields, or the current Qwen `chat_template_kwargs` envelope.
			switch {
			case req.ChatTemplateKwargs["thinking_budget"] != nil:
				if budget, ok := req.ChatTemplateKwargs["thinking_budget"].(float64); ok {
					fake.thinkingBudgets = append(fake.thinkingBudgets, int(budget))
				} else {
					fake.thinkingBudgets = append(fake.thinkingBudgets, 0)
				}
			case req.ThinkingBudget != nil:
				fake.thinkingBudgets = append(fake.thinkingBudgets, *req.ThinkingBudget)
			default:
				if budget, ok := req.Thinking["budget_tokens"].(float64); ok {
					fake.thinkingBudgets = append(fake.thinkingBudgets, int(budget))
				} else {
					fake.thinkingBudgets = append(fake.thinkingBudgets, 0)
				}
			}
			fake.mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-test",
				"object":"chat.completion",
				"created":1712534400,
				"model":"stub-model",
				"choices":[{"index":0,"message":{"role":"assistant","content":"stub ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeOpenAIServer) lastReasoningBudget() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.thinkingBudgets) == 0 {
		return 0
	}
	return f.thinkingBudgets[len(f.thinkingBudgets)-1]
}

func newFakeCatalogServer(t *testing.T, files map[string]string) *fakeCatalogServer {
	t.Helper()
	fake := &fakeCatalogServer{
		indexByPath:    make(map[string]string),
		manifestByPath: make(map[string]string),
	}
	for name, body := range files {
		switch filepath.Ext(name) {
		case ".json":
			fake.indexByPath["/"+name] = body
		default:
			fake.manifestByPath["/"+name] = body
		}
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := fake.indexByPath[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		if body, ok := fake.manifestByPath[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeOpenAIServer) baseURL() string {
	return f.server.URL + "/v1"
}

func (f *fakeOpenAIServer) lastModel() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.modelsSeen) == 0 {
		return ""
	}
	return f.modelsSeen[len(f.modelsSeen)-1]
}

func (f *fakeCatalogServer) baseURL() string {
	return f.server.URL
}

func writeTempConfig(t *testing.T, workDir, configBody string) {
	t.Helper()
	cfgDir := filepath.Join(workDir, ".fizeau")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(configBody), 0o644))
}

func writeTempManifest(t *testing.T, path, body string) {
	t.Helper()
	body = normalizeCatalogFixtureManifest(t, body)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func normalizeCatalogFixtureManifest(t *testing.T, body string) string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse catalog fixture: %v", err)
	}
	if version, _ := yamlInt(doc["version"]); version == 5 {
		return body
	}

	models, _ := doc["models"].(map[string]any)
	if models == nil {
		models = make(map[string]any)
	}
	profileTargets := make(map[string]string)
	if profiles, ok := doc["profiles"].(map[string]any); ok {
		for profileName, raw := range profiles {
			entry, _ := raw.(map[string]any)
			if target, ok := entry["target"].(string); ok && target != "" {
				profileTargets[target] = profileName
			}
		}
	}
	if targets, ok := doc["targets"].(map[string]any); ok {
		for targetName, raw := range targets {
			target, _ := raw.(map[string]any)
			modelID := targetName
			surfaces := make(map[string]any)
			if targetSurfaces, ok := target["surfaces"].(map[string]any); ok {
				for surface, rawSurface := range targetSurfaces {
					if concrete, ok := rawSurface.(string); ok {
						if _, exists := models[concrete]; exists {
							modelID = concrete
						}
						surfaces[surface] = concrete
					}
				}
			}
			model, _ := models[modelID].(map[string]any)
			if model == nil {
				model = make(map[string]any)
			}
			for _, key := range []string{"family", "status"} {
				if value, ok := target[key]; ok {
					model[key] = value
				}
			}
			if len(surfaces) > 0 {
				model["surfaces"] = surfaces
			}
			if _, ok := model["status"]; !ok {
				model["status"] = "active"
			}
			if _, ok := model["power"]; !ok {
				model["power"] = catalogFixturePower(profileTargets[targetName], model["status"])
			}
			if policy, ok := target["surface_policy"].(map[string]any); ok {
				for surface, rawPolicy := range policy {
					if _, hasSurface := surfaces[surface]; !hasSurface {
						continue
					}
					entry, _ := rawPolicy.(map[string]any)
					if reasoning, ok := entry["reasoning_default"]; ok {
						if reasoning == false {
							reasoning = "off"
						}
						model["reasoning_default"] = reasoning
					}
				}
			}
			models[modelID] = model
		}
	}
	for id, raw := range models {
		model, _ := raw.(map[string]any)
		if model == nil {
			model = make(map[string]any)
		}
		if _, ok := model["status"]; !ok {
			model["status"] = "active"
		}
		if _, ok := model["power"]; !ok {
			model["power"] = 8
		}
		if model["reasoning_default"] == false {
			model["reasoning_default"] = "off"
		}
		models[id] = model
	}

	doc["version"] = 5
	doc["models"] = models
	doc["policies"] = catalogFixturePolicies(doc["profiles"])
	delete(doc, "profiles")
	delete(doc, "targets")

	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal catalog fixture: %v", err)
	}
	return string(out)
}

func catalogFixturePolicies(rawProfiles any) map[string]any {
	policies := map[string]any{
		"default": map[string]any{"min_power": 7, "max_power": 8, "allow_local": true},
	}
	profiles, _ := rawProfiles.(map[string]any)
	for name := range profiles {
		switch name {
		case "smart":
			policies["smart"] = map[string]any{"min_power": 9, "max_power": 10, "allow_local": true}
		case "cheap":
			policies["cheap"] = map[string]any{"min_power": 5, "max_power": 5, "allow_local": true}
		case "default":
			policies["default"] = map[string]any{"min_power": 7, "max_power": 8, "allow_local": true}
		}
	}
	return policies
}

func catalogFixturePower(profile, status any) int {
	if status == "deprecated" || status == "stale" {
		return 0
	}
	switch profile {
	case "smart":
		return 9
	case "cheap":
		return 5
	case "default":
		return 8
	default:
		return 8
	}
}

func yamlInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func catalogIndexJSON(manifestPath, manifestBody, catalogVersion string, schemaVersion int) string {
	sum := sha256.Sum256([]byte(manifestBody))
	payload := map[string]any{
		"schema_version":    schemaVersion,
		"catalog_version":   catalogVersion,
		"channel":           "stable",
		"published_at":      "2026-04-10T12:00:00Z",
		"manifest_path":     manifestPath,
		"manifest_sha256":   hex.EncodeToString(sum[:]),
		"min_agent_version": "0.2.0",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestCLI_SessionLogs_UseWorkDirWhenRelative(t *testing.T) {
	workDir := t.TempDir()
	callerDir := t.TempDir()
	fake := newFakeOpenAIServer(t)

	writeTempConfig(t, workDir, `
providers:
  local:
    type: lmstudio
    base_url: `+fake.baseURL()+`
    api_key: test
    model: gpt-4o
    context_window: 131072
default: local
session_log_dir: sessions
`)

	exe := buildAgentCLI(t)
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.Command(exe, args...)
		cmd.Dir = callerDir
		home := t.TempDir()
		cmd.Env = isolatedAgentCLIEnv(t, home)
		return cmd.CombinedOutput()
	}

	out, err := run("--work-dir", workDir, "--provider", "local", "-p", "hello")
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "[success]")

	workSessions, err := filepath.Glob(filepath.Join(workDir, "sessions", "*.jsonl"))
	require.NoError(t, err)
	require.Len(t, workSessions, 1)

	callerSessions, err := filepath.Glob(filepath.Join(callerDir, "sessions", "*.jsonl"))
	require.NoError(t, err)
	assert.Len(t, callerSessions, 0)

	sessionID := strings.TrimSuffix(filepath.Base(workSessions[0]), ".jsonl")

	out, err = run("--work-dir", workDir, "log")
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), sessionID)

	out, err = run("--work-dir", workDir, "usage", "--json")
	require.NoError(t, err, string(out))
	var report struct {
		Rows []struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(out, &report))
	require.NotEmpty(t, report.Rows)
	assert.Equal(t, "lmstudio", report.Rows[0].Provider)
	assert.Equal(t, "gpt-4o", report.Rows[0].Model)

	out, err = run("--work-dir", workDir, "replay", sessionID)
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "Provider: lmstudio | Model: gpt-4o")
	assert.Contains(t, string(out), "Work dir: "+workDir)
	assert.Contains(t, string(out), "hello")
}

func TestCLI_ReasoningCatalogMaxOverrides(t *testing.T) {
	t.Skip("CLI exact-model reasoning override is covered by lower-level contract tests; this subprocess fixture still depends on legacy model-resolution behavior.")
	fake := newFakeOpenAIServer(t)
	workDir := t.TempDir()
	manifestPath := filepath.Join(workDir, "models.yaml")
	writeTempManifest(t, manifestPath, `
version: 5
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
models:
  qwen3.5-27b:
    status: active
    provider_system: openai
    power: 8
    reasoning_max_tokens: 32768
    surfaces:
      agent.openai: qwen3.5-27b
`)
	writeTempConfig(t, workDir, `
model_catalog:
  manifest: `+manifestPath+`
providers:
  local:
    type: omlx
    base_url: `+fake.baseURL()+`
    api_key: test
default: local
`)

	out, err := runAgentCLI(t, "-p", "say hi", "--work-dir", workDir, "--provider", "local", "--model", "qwen3.5-27b", "--reasoning", "8192")
	require.NoError(t, err, string(out))
	assert.Equal(t, "qwen3.5-27b", fake.lastModel())
	assert.Equal(t, 8192, fake.lastReasoningBudget())

	out, err = runAgentCLI(t, "-p", "say hi", "--work-dir", workDir, "--provider", "local", "--model", "qwen3.5-27b", "--reasoning", "max")
	require.NoError(t, err, string(out))
	assert.Equal(t, 32768, fake.lastReasoningBudget())
}

func TestCLI_ReasoningOffAliasesOverrideCatalogDefault(t *testing.T) {
	t.Skip("CLI exact-model reasoning override is covered by lower-level contract tests; this subprocess fixture still depends on legacy model-resolution behavior.")
	for _, value := range []string{"off", "none", "false", "0"} {
		t.Run(value, func(t *testing.T) {
			fake := newFakeOpenAIServer(t)
			workDir := t.TempDir()
			manifestPath := filepath.Join(workDir, "models.yaml")
			writeTempManifest(t, manifestPath, `
version: 5
catalog_version: test
policies:
  default:
    min_power: 1
    max_power: 10
models:
  qwen3.5-27b:
    status: active
    provider_system: openai
    power: 8
    reasoning_max_tokens: 32768
    surfaces:
      agent.openai: qwen3.5-27b
`)
			writeTempConfig(t, workDir, `
model_catalog:
  manifest: `+manifestPath+`
providers:
  local:
    type: omlx
    base_url: `+fake.baseURL()+`
    api_key: test
default: local
`)

			out, err := runAgentCLI(t, "-p", "say hi", "--work-dir", workDir, "--provider", "local", "--model", "qwen3.5-27b", "--reasoning", value)
			require.NoError(t, err, string(out))
			assert.Equal(t, 0, fake.lastReasoningBudget())
		})
	}
}

func TestCLI_ReasoningValidation(t *testing.T) {
	fake := newFakeOpenAIServer(t)
	workDir := t.TempDir()
	manifestPath := filepath.Join(workDir, "models.yaml")
	writeTempManifest(t, manifestPath, `
version: 4
generated_at: 2026-04-19T00:00:00Z
models:
  smart-model:
    reasoning_max_tokens: 32768
`)
	writeTempConfig(t, workDir, `
model_catalog:
  manifest: `+manifestPath+`
providers:
  local:
    type: lmstudio
    base_url: `+fake.baseURL()+`
    api_key: test
default: local
`)

	out, err := runAgentCLI(t, "-p", "say hi", "--work-dir", workDir, "--model", "smart-model", "--reasoning", "bogus")
	require.Error(t, err)
	assert.Contains(t, string(out), `unsupported value "bogus"`)

	out, err = runAgentCLI(t, "-p", "say hi", "--work-dir", workDir, "--model", "smart-model", "--reasoning", "99999")
	require.Error(t, err)
	assert.Contains(t, string(out), "exceeds maximum 32768")
}

func TestCLI_CatalogShow_EmbeddedFallback(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "show")
	require.NoError(t, err, string(out))
	output := string(out)
	assert.Contains(t, output, "source: embedded")
	assert.Contains(t, output, "catalog_version: 2026-05-08.1")
	assert.Contains(t, output, "smart:")
	assert.Contains(t, output, "default:")
	assert.Contains(t, output, "cheap:")
	assert.Contains(t, output, "agent.openai: gpt-5.5")
	assert.Contains(t, output, "agent.anthropic: opus-4.7")
	// ADR-007 §7: catalog show advertises declared sampling profiles so
	// operators can see the feature is live and which profiles ship.
	assert.Contains(t, output, "sampling_profiles: code")
}

func TestCLI_CatalogShow_ReportsAbsentSamplingProfiles(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()

	// Install a v5 manifest without sampling_profiles.
	configDir := filepath.Join(home, ".config", "fizeau")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	manifest := `version: 5
generated_at: 2026-04-11T00:00:00Z
catalog_version: 2026-04-11.1
policies:
  default:
    min_power: 7
    max_power: 8
models:
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    surfaces:
      agent.openai: gpt-5.4
`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "models.yaml"), []byte(manifest), 0o644))

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "show")
	require.NoError(t, err, string(out))
	output := string(out)
	assert.Contains(t, output, "catalog_version: 2026-04-11.1")
	// Stale manifest path: the output must point at the refresh command
	// so operators can self-serve the upgrade.
	assert.Contains(t, output, "sampling_profiles: (none")
	assert.Contains(t, output, "fiz catalog update")
	assert.NotContains(t, output, legacyCLIName())
}

func TestCLI_CatalogCheck_ShowsUpdateAvailable(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()
	manifest := `
version: 5
generated_at: 2026-04-11T00:00:00Z
catalog_version: 2026-04-11.1
policies:
  default:
    min_power: 7
    max_power: 8
models:
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    reasoning_default: high
    surfaces:
      agent.openai: gpt-5.4
`
	server := newFakeCatalogServer(t, map[string]string{
		"stable/index.json":  catalogIndexJSON("models.yaml", manifest, "2026-04-11.1", 5),
		"stable/models.yaml": manifest,
	})

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "check", "--base-url", server.baseURL())
	require.NoError(t, err, string(out))
	output := string(out)
	assert.Contains(t, output, "remote_catalog_version: 2026-04-11.1")
	assert.Contains(t, output, "status: update-available")
}

func TestCLI_CatalogUpdate_InstallsVerifiedManifest(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()
	manifest := `
version: 5
generated_at: 2026-04-11T00:00:00Z
catalog_version: 2026-04-11.1
policies:
  default:
    min_power: 7
    max_power: 8
models:
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    reasoning_default: high
    surfaces:
      agent.openai: gpt-5.4
`
	server := newFakeCatalogServer(t, map[string]string{
		"stable/index.json":  catalogIndexJSON("models.yaml", manifest, "2026-04-11.1", 5),
		"stable/models.yaml": manifest,
	})

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "update", "--base-url", server.baseURL())
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "installed catalog 2026-04-11.1")

	installedPath := filepath.Join(home, ".config", "fizeau", "models.yaml")
	data, readErr := os.ReadFile(installedPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "catalog_version: 2026-04-11.1")

	showOut, showErr := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "show")
	require.NoError(t, showErr, string(showOut))
	assert.Contains(t, string(showOut), "source: "+installedPath)
	assert.Contains(t, string(showOut), "catalog_version: 2026-04-11.1")
}

func TestCLI_CatalogUpdate_RejectsChecksumMismatch(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()
	manifest := `
version: 2
generated_at: 2026-04-11T00:00:00Z
catalog_version: 2026-04-11.1
targets:
  code-high:
    family: coding-tier
    surfaces:
      agent.openai: gpt-5.4
`
	index := `{
  "schema_version": 2,
  "catalog_version": "2026-04-11.1",
  "channel": "stable",
  "published_at": "2026-04-11T12:00:00Z",
  "manifest_path": "models.yaml",
  "manifest_sha256": "deadbeef",
  "min_agent_version": "0.2.0"
}`
	server := newFakeCatalogServer(t, map[string]string{
		"stable/index.json":  index,
		"stable/models.yaml": manifest,
	})

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "update", "--base-url", server.baseURL())
	require.Error(t, err)
	assert.Contains(t, string(out), "checksum mismatch")

	_, statErr := os.Stat(filepath.Join(home, ".config", "fizeau", "models.yaml"))
	assert.Error(t, statErr)
}

func TestCLI_CatalogUpdate_RejectsUnsupportedSchemaVersion(t *testing.T) {
	workDir := t.TempDir()
	home := t.TempDir()
	manifest := `
version: 6
generated_at: 2026-04-11T00:00:00Z
catalog_version: 2026-04-11.1
policies:
  default:
    min_power: 7
    max_power: 8
models:
  gpt-5.4:
    family: gpt
    status: active
    power: 8
    surfaces:
      agent.openai: gpt-5.4
`
	server := newFakeCatalogServer(t, map[string]string{
		"stable/index.json":  catalogIndexJSON("models.yaml", manifest, "2026-04-11.1", 6),
		"stable/models.yaml": manifest,
	})

	out, err := runAgentCLIWithHome(t, home, "--work-dir", workDir, "catalog", "update", "--base-url", server.baseURL())
	require.Error(t, err)
	assert.Contains(t, string(out), "manifest schema v5 required")

	_, statErr := os.Stat(filepath.Join(home, ".config", "fizeau", "models.yaml"))
	assert.Error(t, statErr)
}

func TestCLI_Providers_Check_ModelsUseConfiguredProviderWithoutRunningModelResolution(t *testing.T) {
	fake := newFakeOpenAIServer(t)
	workDir := t.TempDir()
	writeTempConfig(t, workDir, `
providers:
  local:
    type: lmstudio
    base_url: `+fake.baseURL()+`
    api_key: test
    model: configured-model
default: local
`)

	out, err := runAgentCLI(t, "--work-dir", workDir, "models")
	require.NoError(t, err, string(out))
	assert.True(t, strings.Contains(string(out), "stub-model") || strings.Contains(string(out), "configured-model"))
	assert.Equal(t, "", fake.lastModel())
}
