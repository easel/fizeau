<bead-review>
  <bead id="agent-db083b89" iter=1>
    <title>FZ-023 Rename runtime provider env vars to FIZEAU</title>
    <description>
Hard-cut provider env overrides from AGENT_PROVIDER/BASE_URL/API_KEY/MODEL to FIZEAU_PROVIDER/BASE_URL/API_KEY/MODEL. No aliases.
    </description>
    <acceptance>
Tests prove FIZEAU_PROVIDER, FIZEAU_BASE_URL, FIZEAU_API_KEY, and FIZEAU_MODEL work; old runtime env names are absent outside allowlist.
    </acceptance>
    <labels>area:rename, kind:config, area:env</labels>
  </bead>

  <changed-files>
    <file>README.md</file>
    <file>agentcli/cli_process_test.go</file>
    <file>agentcli/run.go</file>
    <file>demos/record.sh</file>
    <file>docs/helix/01-frame/features/FEAT-006-standalone-cli.md</file>
    <file>docs/helix/02-design/plan-2026-04-08-rename-agent.md</file>
    <file>docs/helix/02-design/solution-designs/SD-002-standalone-cli.md</file>
    <file>docs/helix/02-design/solution-designs/SD-007-provider-import.md</file>
    <file>internal/config/config.go</file>
    <file>internal/config/config_test.go</file>
    <file>picompat/translate.go</file>
    <file>service_new_test.go</file>
    <file>website/content/docs/getting-started.md</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="8e7404c798622ab74cac7985133c9c15a52e322c">
diff --git a/README.md b/README.md
index 7829a0f..5cef725 100644
--- a/README.md
+++ b/README.md
@@ -39,9 +39,9 @@ DDX Agent connects to `localhost:1234` by default.
 ### With Anthropic
 
 ```bash
-export AGENT_PROVIDER=anthropic
-export AGENT_API_KEY=sk-ant-...
-export AGENT_MODEL=claude-sonnet-4-20250514
+export FIZEAU_PROVIDER=anthropic
+export FIZEAU_API_KEY=sk-ant-...
+export FIZEAU_MODEL=claude-sonnet-4-20250514
 ddx-agent -p "Read main.go and tell me the package name"
 ```
 
@@ -144,7 +144,7 @@ tools:
       max_bytes: 51200
 ```
 
-Environment overrides: `AGENT_PROVIDER`, `AGENT_BASE_URL`, `AGENT_API_KEY`, `AGENT_MODEL`
+Environment overrides: `FIZEAU_PROVIDER`, `FIZEAU_BASE_URL`, `FIZEAU_API_KEY`, `FIZEAU_MODEL`
 
 `tools.bash.output_filter.mode: rtk` proxies allowlisted noisy commands such as
 `git status` and `go test` through an installed `rtk` binary. If `rtk` is not
diff --git a/agentcli/cli_process_test.go b/agentcli/cli_process_test.go
index 330b05c..0226f39 100644
--- a/agentcli/cli_process_test.go
+++ b/agentcli/cli_process_test.go
@@ -316,8 +316,8 @@ default: local
 `)
 
 	env := testEnvWithHome(home, map[string]string{
-		"AGENT_BASE_URL": envFake.baseURL(),
-		"AGENT_MODEL":    "env-model",
+		"FIZEAU_BASE_URL": envFake.baseURL(),
+		"FIZEAU_MODEL":    "env-model",
 	})
 
 	first := runBuiltCLI(t, exe, workDir, env, "--work-dir", workDir, "-p", "first")
diff --git a/agentcli/run.go b/agentcli/run.go
index 6c941f0..f6289c4 100644
--- a/agentcli/run.go
+++ b/agentcli/run.go
@@ -206,7 +206,7 @@ func runWithOptions(opts Options) int {
 	// `len(args) == 0` with no prompt flag is ambiguous — skip validation there to
 	// preserve the existing "no prompt" error precedence.
 	if len(cfg.Providers) == 0 && (*promptFlag != "" || runSubcommand) {
-		fmt.Fprintf(os.Stderr, "error: no providers configured — run '%s import pi' or '%s import opencode', set AGENT_PROVIDER/AGENT_BASE_URL, or create .fizeau/config.yaml\n", productinfo.BinaryName, productinfo.BinaryName)
+		fmt.Fprintf(os.Stderr, "error: no providers configured — run '%s import pi' or '%s import opencode', set FIZEAU_PROVIDER/FIZEAU_BASE_URL, or create .fizeau/config.yaml\n", productinfo.BinaryName, productinfo.BinaryName)
 		return 2
 	}
 
diff --git a/demos/record.sh b/demos/record.sh
index 80290fa..299571e 100755
--- a/demos/record.sh
+++ b/demos/record.sh
@@ -2,7 +2,7 @@
 # Record all demo reels. Requires ddx-agent binary and asciinema.
 # Usage: ./demos/record.sh [--lmstudio URL]
 #
-# By default uses AGENT_BASE_URL from env or http://localhost:1234/v1.
+# By default uses FIZEAU_BASE_URL from env or http://localhost:1234/v1.
 # Session logs are saved to demos/sessions/ for CI replay.
 set -euo pipefail
 
@@ -11,11 +11,11 @@ cd "$(dirname "$0")/.."
 # Ensure ddx-agent is built
 make build
 
-LMSTUDIO_URL="${AGENT_BASE_URL:-http://localhost:1234/v1}"
-MODEL="${AGENT_MODEL:-qwen/qwen3-coder-next}"
+LMSTUDIO_URL="${FIZEAU_BASE_URL:-http://localhost:1234/v1}"
+MODEL="${FIZEAU_MODEL:-qwen/qwen3-coder-next}"
 
-export AGENT_BASE_URL="$LMSTUDIO_URL"
-export AGENT_MODEL="$MODEL"
+export FIZEAU_BASE_URL="$LMSTUDIO_URL"
+export FIZEAU_MODEL="$MODEL"
 
 echo "Recording demos against $LMSTUDIO_URL with model $MODEL"
 
diff --git a/docs/helix/01-frame/features/FEAT-006-standalone-cli.md b/docs/helix/01-frame/features/FEAT-006-standalone-cli.md
index 5491467..1f6db3c 100644
--- a/docs/helix/01-frame/features/FEAT-006-standalone-cli.md
+++ b/docs/helix/01-frame/features/FEAT-006-standalone-cli.md
@@ -61,8 +61,8 @@ Patterned on pi's CLI interface (`pi -p "prompt"`) and DDx's config conventions
    max_iterations: 20
    session_log_dir: .agent/sessions
    ```
-10. Environment variable overrides: `AGENT_BASE_URL`, `AGENT_API_KEY`,
-   `AGENT_MODEL`, `AGENT_PROVIDER`
+10. Environment variable overrides: `FIZEAU_BASE_URL`, `FIZEAU_API_KEY`,
+   `FIZEAU_MODEL`, `FIZEAU_PROVIDER`
 11. CLI flags override config file and env vars
 12. Model-first routing flags (`--model`, `--model-ref`) are the preferred
     selection surface; `--provider` remains the explicit override path.
diff --git a/docs/helix/02-design/plan-2026-04-08-rename-agent.md b/docs/helix/02-design/plan-2026-04-08-rename-agent.md
index 8cd41cd..8bd35a4 100644
--- a/docs/helix/02-design/plan-2026-04-08-rename-agent.md
+++ b/docs/helix/02-design/plan-2026-04-08-rename-agent.md
@@ -22,7 +22,7 @@ five different naming surfaces:
   `github.com/DocumentDrivenDX/agent`)
 - CLI identity (`ddx-agent`, `cmd/ddx-agent`, installer, updater, release asset names)
 - config and filesystem identity (`.agent`, `~/.config/agent`, `~/.cache/agent`)
-- environment variable identity (`AGENT_PROVIDER`, `AGENT_MODEL`, etc.)
+- environment variable identity (`FIZEAU_PROVIDER`, `FIZEAU_MODEL`, etc.)
 - consumer-specific model-catalog surfaces (`agent.openai`, `agent.anthropic`)
 
 The rename must be staged so runtime breakage is easy to attribute and public
@@ -66,7 +66,7 @@ surface decisions are made explicitly rather than via accidental search/replace.
    - `~/.config/agent/config.yaml`
    - `.agent/sessions`
    - `~/.cache/agent/latest-version.json`
-   - `AGENT_PROVIDER`, `AGENT_BASE_URL`, `AGENT_API_KEY`, `AGENT_MODEL`
+   - `FIZEAU_PROVIDER`, `FIZEAU_BASE_URL`, `FIZEAU_API_KEY`, `FIZEAU_MODEL`
 
 4. Distribution and update flow
    - `install.sh`
diff --git a/docs/helix/02-design/solution-designs/SD-002-standalone-cli.md b/docs/helix/02-design/solution-designs/SD-002-standalone-cli.md
index d7d3f55..6c53c49 100644
--- a/docs/helix/02-design/solution-designs/SD-002-standalone-cli.md
+++ b/docs/helix/02-design/solution-designs/SD-002-standalone-cli.md
@@ -71,8 +71,8 @@ ddx-agent usage --since=7d         # with time window
 1. Built-in defaults (localhost:1234, openai-compat, 20 iterations)
 2. Global config: `~/.config/agent/config.yaml`
 3. Project config: `.agent/config.yaml`
-4. Environment variables: `AGENT_PROVIDER`, `AGENT_BASE_URL`, `AGENT_API_KEY`,
-   `AGENT_MODEL`
+4. Environment variables: `FIZEAU_PROVIDER`, `FIZEAU_BASE_URL`, `FIZEAU_API_KEY`,
+   `FIZEAU_MODEL`
 5. CLI flags: `--provider`, `--model`, `--model-ref`, `--max-iter`,
    `--work-dir`
 
diff --git a/docs/helix/02-design/solution-designs/SD-007-provider-import.md b/docs/helix/02-design/solution-designs/SD-007-provider-import.md
index 0798bb0..face56c 100644
--- a/docs/helix/02-design/solution-designs/SD-007-provider-import.md
+++ b/docs/helix/02-design/solution-designs/SD-007-provider-import.md
@@ -100,7 +100,7 @@ default: anthropic
 
 **What gets skipped with warnings:**
 - `!command` API key values → warning: "provider X uses shell-resolved key,
-  set AGENT_API_KEY or add api_key manually"
+  set FIZEAU_API_KEY or add api_key manually"
 - Providers with `api` field that doesn't map to `openai-compat` or `anthropic`
 - `headers` values that use `!command` resolution
 
@@ -205,7 +205,7 @@ type is configured):
 
 These implicit providers have lower precedence than any explicit config.
 They don't create a `default:` — the user must specify `--provider` or set
-`AGENT_PROVIDER` to use them.
+`FIZEAU_PROVIDER` to use them.
 
 ### Config Schema Additions to SD-005
 
diff --git a/internal/config/config.go b/internal/config/config.go
index f0df7ae..ebcc2e3 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -422,19 +422,19 @@ func (c *Config) applyEnvOverrides() {
 	p := c.Providers[defName]
 	changed := false
 
-	if v := os.Getenv("AGENT_PROVIDER"); v != "" {
+	if v := os.Getenv("FIZEAU_PROVIDER"); v != "" {
 		p.Type = v
 		changed = true
 	}
-	if v := os.Getenv("AGENT_BASE_URL"); v != "" {
+	if v := os.Getenv("FIZEAU_BASE_URL"); v != "" {
 		p.BaseURL = v
 		changed = true
 	}
-	if v := os.Getenv("AGENT_API_KEY"); v != "" {
+	if v := os.Getenv("FIZEAU_API_KEY"); v != "" {
 		p.APIKey = v
 		changed = true
 	}
-	if v := os.Getenv("AGENT_MODEL"); v != "" {
+	if v := os.Getenv("FIZEAU_MODEL"); v != "" {
 		p.Model = v
 		changed = true
 	}
diff --git a/internal/config/config_test.go b/internal/config/config_test.go
index 27bf175..71422af 100644
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -256,9 +256,9 @@ targets:
 
 func TestLoad_EnvOverrides(t *testing.T) {
 	isolateHome(t)
-	t.Setenv("AGENT_PROVIDER", "anthropic")
-	t.Setenv("AGENT_API_KEY", "env-key")
-	t.Setenv("AGENT_MODEL", "env-model")
+	t.Setenv("FIZEAU_PROVIDER", "anthropic")
+	t.Setenv("FIZEAU_API_KEY", "env-key")
+	t.Setenv("FIZEAU_MODEL", "env-model")
 
 	cfg, err := Load(t.TempDir())
 	require.NoError(t, err)
@@ -570,7 +570,7 @@ model: qwen3.5-7b
 
 func TestLoad_EnvOverridesCreateDeterministicDefaultProvider(t *testing.T) {
 	isolateHome(t)
-	t.Setenv("AGENT_MODEL", "env-model")
+	t.Setenv("FIZEAU_MODEL", "env-model")
 
 	cfg := Defaults()
 	cfg.Providers = map[string]ProviderConfig{
diff --git a/picompat/translate.go b/picompat/translate.go
index 823c328..4aab7fb 100644
--- a/picompat/translate.go
+++ b/picompat/translate.go
@@ -102,7 +102,7 @@ func Translate(piDir string) (*TranslationResult, error) {
 		// Check for !command API key
 		resolvedKey := cred.ResolvedKey()
 		if len(resolvedKey) > 0 && resolvedKey[0] == '!' {
-			result.Warnings.Add("provider %q uses shell-resolved key, set AGENT_API_KEY or add api_key manually", name)
+			result.Warnings.Add("provider %q uses shell-resolved key, set FIZEAU_API_KEY or add api_key manually", name)
 			continue
 		}
 
diff --git a/service_new_test.go b/service_new_test.go
index 553319b..6778260 100644
--- a/service_new_test.go
+++ b/service_new_test.go
@@ -99,10 +99,10 @@ func TestNew_FallsBackToDefaultPath(t *testing.T) {
 	// Unset any env vars that could inject a real provider so we get a
 	// predictable empty config from the global path (which likely doesn't
 	// exist in CI).
-	t.Setenv("AGENT_PROVIDER", "")
-	t.Setenv("AGENT_BASE_URL", "")
-	t.Setenv("AGENT_API_KEY", "")
-	t.Setenv("AGENT_MODEL", "")
+	t.Setenv("FIZEAU_PROVIDER", "")
+	t.Setenv("FIZEAU_BASE_URL", "")
+	t.Setenv("FIZEAU_API_KEY", "")
+	t.Setenv("FIZEAU_MODEL", "")
 
 	// New should not fail even when config is missing or empty.
 	svc, err := fizeau.New(fizeau.ServiceOptions{})
diff --git a/website/content/docs/getting-started.md b/website/content/docs/getting-started.md
index 1358d02..d8da141 100644
--- a/website/content/docs/getting-started.md
+++ b/website/content/docs/getting-started.md
@@ -24,9 +24,9 @@ DDX Agent connects to LM Studio at `localhost:1234` by default.
 ## Quick Start with Anthropic
 
 ```bash
-export AGENT_PROVIDER=anthropic
-export AGENT_API_KEY=sk-ant-...
-export AGENT_MODEL=claude-sonnet-4-20250514
+export FIZEAU_PROVIDER=anthropic
+export FIZEAU_API_KEY=sk-ant-...
+export FIZEAU_MODEL=claude-sonnet-4-20250514
 
 ddx-agent -p "Read main.go and tell me the package name"
 ```
@@ -44,10 +44,10 @@ session_log_dir: .agent/sessions
 ```
 
 Environment variables override the config file:
-- `AGENT_PROVIDER` — `openai-compat` or `anthropic`
-- `AGENT_BASE_URL` — provider base URL
-- `AGENT_API_KEY` — API key
-- `AGENT_MODEL` — model name
+- `FIZEAU_PROVIDER` — `openai-compat` or `anthropic`
+- `FIZEAU_BASE_URL` — provider base URL
+- `FIZEAU_API_KEY` — API key
+- `FIZEAU_MODEL` — model name
 
 ## As a Library
 
  </diff>

  <instructions>
You are reviewing a bead implementation against its acceptance criteria.

For each acceptance-criteria (AC) item, decide whether it is implemented correctly, then assign one overall verdict:

- APPROVE — every AC item is fully and correctly implemented.
- REQUEST_CHANGES — some AC items are partial or have fixable minor issues.
- BLOCK — at least one AC item is not implemented or incorrectly implemented; or the diff is insufficient to evaluate.

## Required output format (schema_version: 1)

Respond with EXACTLY one JSON object as your final response, fenced as a single ```json … ``` code block. Do not include any prose outside the fenced block. The JSON must match this schema:

```json
{
  "schema_version": 1,
  "verdict": "APPROVE",
  "summary": "≤300 char human-readable verdict justification",
  "findings": [
    { "severity": "info", "summary": "what is wrong or notable", "location": "path/to/file.go:42" }
  ]
}
```

Rules:
- "verdict" must be exactly one of "APPROVE", "REQUEST_CHANGES", "BLOCK".
- "severity" must be exactly one of "info", "warn", "block".
- Output the JSON object inside ONE fenced ```json … ``` block. No additional prose, no extra fences, no markdown headings.
- Do not echo this template back. Do not write the words APPROVE, REQUEST_CHANGES, or BLOCK anywhere except as the JSON value of the verdict field.
  </instructions>
</bead-review>
