<bead-review>
  <bead id="agent-15a78af8" iter=1>
    <title>FZ-025 Rename harness record env vars to FIZEAU</title>
    <description>
Rename AGENT_HARNESS_* test/record knobs to FIZEAU_HARNESS_* in harness tests and docs. No runtime behavior changes unless these are user-facing.
    </description>
    <acceptance>
Harness tests compile/pass in non-record mode; rg finds no unallowlisted AGENT_HARNESS_* names.
    </acceptance>
    <notes>
Prework review before execution: complete and testable. Scope is narrow: rename AGENT_HARNESS_* test/record knobs to FIZEAU_HARNESS_* in harness tests/docs only, with no runtime behavior change unless already user-facing. Verification should run harness tests in non-record mode and grep for unallowlisted AGENT_HARNESS_* literals. Local bragi/qwen3.6-27b attempt 20260430T194717 stalled after an overbroad repository grep and produced no commit/result; escalate rather than retrying the same local route.
    </notes>
    <labels>area:rename, kind:test, area:harness</labels>
  </bead>

  <changed-files>
    <file>docs/helix/02-design/harness-golden-integration.md</file>
    <file>docs/research/scripts-fixtures-assets-cleanup-inventory-2026-04-30.md</file>
    <file>harness_golden_integration_test.go</file>
    <file>internal/harnesses/claude/quota_pty_integration_test.go</file>
    <file>internal/harnesses/codex/quota_pty_integration_test.go</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="e9c587465b9b47fad11a151a0e2b1b2379883764">
diff --git a/docs/helix/02-design/harness-golden-integration.md b/docs/helix/02-design/harness-golden-integration.md
index 0bf61ed..e3c56fd 100644
--- a/docs/helix/02-design/harness-golden-integration.md
+++ b/docs/helix/02-design/harness-golden-integration.md
@@ -43,22 +43,22 @@ longer provides the behavior DDX needs.
 Run the preflight:
 
 ```sh
-AGENT_HARNESS_RECORD=1 go test -tags=integration -run TestHarnessGoldenRecordModePreflight .
+FIZEAU_HARNESS_RECORD=1 go test -tags=integration -run TestHarnessGoldenRecordModePreflight .
 ```
 
 Run live record mode and write sanitized version-1 cassette directories:
 
 ```sh
-AGENT_HARNESS_RECORD=1 \
-AGENT_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live \
+FIZEAU_HARNESS_RECORD=1 \
+FIZEAU_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live \
 go test -tags=integration -run TestHarnessGoldenRecordModeLive .
 ```
 
 Run authenticated usage-specific record mode:
 
 ```sh
-AGENT_HARNESS_RECORD=1 \
-AGENT_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live \
+FIZEAU_HARNESS_RECORD=1 \
+FIZEAU_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live \
 go test -tags=integration -run usage .
 ```
 
diff --git a/docs/research/scripts-fixtures-assets-cleanup-inventory-2026-04-30.md b/docs/research/scripts-fixtures-assets-cleanup-inventory-2026-04-30.md
index 562f27a..b8f9650 100644
--- a/docs/research/scripts-fixtures-assets-cleanup-inventory-2026-04-30.md
+++ b/docs/research/scripts-fixtures-assets-cleanup-inventory-2026-04-30.md
@@ -58,7 +58,7 @@ The rows below cite these repository checks, all run from the repository root on
 | `CL-004.16` | `demos/record.sh`, `demos/scripts/demo-*.sh`, `website/static/demos/*.cast` | demo-scripts-assets | keep | Demo generator scripts and website-rendered cast assets are active. | `website/content/demos/_index.md:12,29,45` embeds all three `.cast` files; `website/content/demos/_index.md:62` links `demos/scripts/` and `demos/record.sh`; `README.md:52` links `website/static/demos/file-read.cast`; `bash -n demos/record.sh` PASS. | None. | Rename bead updates website path/product strings in place. |
 | `CL-004.17` | `demos/sessions/demo-read.jsonl`, `demos/sessions/demo-edit.jsonl`, `demos/sessions/demo-bash.jsonl` | demo-session-jsonl | archive | Raw session captures have no active consumer; published casts are the website assets. | `rg -n 'demo-read.jsonl|demo-edit.jsonl|demo-bash.jsonl' . --glob '!demos/sessions/*'` -> no matches; broader `rg -n 'demos/sessions' README.md CONTRIBUTING.md docs website demos scripts Makefile cmd internal agentcli tests bench` -> only `demos/record.sh` comments and copy target; no test or CI replay loader exists. | Website assets in `website/static/demos/*.cast` are the active published demos. | CL-007 may move these raw captures to an archive or delete them if no replay workflow is added. |
 | `CL-004.18` | `testdata/harness-cassettes/{claude,codex}/**` | pty-cassettes | keep | Active harness golden replay fixtures. | `harness_golden_integration_test.go:23` sets `harnessCassetteRoot = "testdata/harness-cassettes"` and `:226` opens the `claude`/`codex` cassette dirs; `docs/helix/02-design/harness-golden-integration.md:8` defines the suite; targeted non-integration Go run excludes build-tagged test, but the fixture-loader references are direct. | None. | None. |
-| `CL-004.19` | `testdata/harness-cassettes/live/{claude,codex}/**` | live-pty-cassettes | keep | Manual/live replay fixtures are documented and env-selectable. | `harness_golden_integration_test.go:281,347` reads `AGENT_HARNESS_CASSETTE_DIR`; `internal/harnesses/codex/quota_pty_integration_test.go:51` and `internal/harnesses/claude/quota_pty_integration_test.go:54` also honor the env var; `docs/helix/02-design/harness-golden-integration.md:53,61` documents `AGENT_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live`. | None. | None. |
+| `CL-004.19` | `testdata/harness-cassettes/live/{claude,codex}/**` | live-pty-cassettes | keep | Manual/live replay fixtures are documented and env-selectable. | `harness_golden_integration_test.go:281,347` reads `FIZEAU_HARNESS_CASSETTE_DIR`; `internal/harnesses/codex/quota_pty_integration_test.go:51` and `internal/harnesses/claude/quota_pty_integration_test.go:54` also honor the env var; `docs/helix/02-design/harness-golden-integration.md:53,61` documents `FIZEAU_HARNESS_CASSETTE_DIR=./testdata/harness-cassettes/live`. | None. | None. |
 | `CL-004.20` | `internal/harnesses/{claude,codex,gemini}/testdata/**` | harness-testdata | keep | Active harness parser and quota usage fixtures. | `internal/harnesses/codex/runner_test.go:304,415` loads `tool_events.jsonl` and `usage_cassettes/*.jsonl`; `internal/harnesses/claude/stream_test.go:269` loads `usage_cassettes/*.jsonl`; `internal/harnesses/gemini/runner_test.go:324,381` loads `live-ok-20260421.jsonl` and `gemini-cli-0.38.2-models.txt`; targeted harness Go tests PASS. | None. | None. |
 | `CL-004.21` | `internal/ptytest/testdata/docker-conformance/*.{sh,yaml}` | ptytest-fixtures | keep | Active PTY conformance scenario fixtures. | `internal/ptytest/docker_conformance_integration_test.go:589` locates `testdata/docker-conformance`; `internal/ptytest/scenario.go:111` reads scenario YAML; `docs/helix/02-design/harness-golden-integration.md:190` cites the fixtures; targeted `go test ./internal/ptytest` PASS. | None. | None. |
 | `CL-004.22` | `internal/comparison/testdata/benchmark-feat019.json` | comparison-fixture | keep | Active comparison benchmark fixture. | `internal/comparison/benchmark_test.go:12` loads `testdata/benchmark-feat019.json`; targeted `go test ./internal/comparison` PASS. | None. | None. |
diff --git a/harness_golden_integration_test.go b/harness_golden_integration_test.go
index c031a8f..18d27c5 100644
--- a/harness_golden_integration_test.go
+++ b/harness_golden_integration_test.go
@@ -266,19 +266,19 @@ func TestHarnessGoldenCassetteReplay_ServiceEvents(t *testing.T) {
 }
 
 func TestHarnessGoldenRecordModePreflight(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to run live harness record-mode preflight")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to run live harness record-mode preflight")
 	}
 	preflightLiveHarnessRecordMode(t)
 }
 
 func TestHarnessGoldenRecordModeLive(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to run live harness record mode")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to run live harness record mode")
 	}
 	preflightLiveHarnessRecordMode(t)
 
-	cassetteDir := os.Getenv("AGENT_HARNESS_CASSETTE_DIR")
+	cassetteDir := os.Getenv("FIZEAU_HARNESS_CASSETTE_DIR")
 	if cassetteDir == "" {
 		cassetteDir = t.TempDir()
 	}
@@ -339,12 +339,12 @@ func TestHarnessGoldenRecordModeLive(t *testing.T) {
 }
 
 func Test_usageRecordMode(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to run authenticated usage cassette record mode")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to run authenticated usage cassette record mode")
 	}
 	preflightLiveHarnessRecordMode(t)
 
-	cassetteDir := os.Getenv("AGENT_HARNESS_CASSETTE_DIR")
+	cassetteDir := os.Getenv("FIZEAU_HARNESS_CASSETTE_DIR")
 	if cassetteDir == "" {
 		cassetteDir = t.TempDir()
 	}
@@ -357,7 +357,7 @@ func Test_usageRecordMode(t *testing.T) {
 	if err != nil {
 		t.Fatalf("New: %v", err)
 	}
-	account := os.Getenv("AGENT_HARNESS_ACCOUNT")
+	account := os.Getenv("FIZEAU_HARNESS_ACCOUNT")
 	for _, harness := range harnessGoldenRecordHarnesses() {
 		t.Run(harness, func(t *testing.T) {
 			lock, err := cassette.AcquireRecordLock(usageCassetteDir, harness+":"+account)
@@ -425,7 +425,7 @@ func Test_usageRecordMode(t *testing.T) {
 }
 
 func harnessGoldenRecordHarnesses() []string {
-	raw := strings.TrimSpace(os.Getenv("AGENT_HARNESS_RECORD_HARNESSES"))
+	raw := strings.TrimSpace(os.Getenv("FIZEAU_HARNESS_RECORD_HARNESSES"))
 	if raw == "" {
 		return []string{"claude", "codex"}
 	}
diff --git a/internal/harnesses/claude/quota_pty_integration_test.go b/internal/harnesses/claude/quota_pty_integration_test.go
index bb6a84e..225e2d0 100644
--- a/internal/harnesses/claude/quota_pty_integration_test.go
+++ b/internal/harnesses/claude/quota_pty_integration_test.go
@@ -14,8 +14,8 @@ import (
 )
 
 func Test_quotaRecordClaudePTY(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to refresh authenticated claude quota cassette")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to refresh authenticated claude quota cassette")
 	}
 	dir := filepath.Join(recordBaseDir(t), "claude", "quota")
 	windows, account, err := ReadClaudeQuotaViaPTY(45*time.Second, WithQuotaPTYCassetteDir(dir))
@@ -33,8 +33,8 @@ func Test_quotaRecordClaudePTY(t *testing.T) {
 }
 
 func Test_modelDiscoveryRecordClaudePTY(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to refresh authenticated claude model cassette")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to refresh authenticated claude model cassette")
 	}
 	dir := filepath.Join(recordBaseDir(t), "claude", "models")
 	snapshot, err := ReadClaudeModelDiscoveryViaPTY(45*time.Second, WithQuotaPTYCassetteDir(dir))
@@ -51,10 +51,10 @@ func Test_modelDiscoveryRecordClaudePTY(t *testing.T) {
 
 func recordBaseDir(t *testing.T) string {
 	t.Helper()
-	if dir := os.Getenv("AGENT_HARNESS_CASSETTE_DIR"); dir != "" {
+	if dir := os.Getenv("FIZEAU_HARNESS_CASSETTE_DIR"); dir != "" {
 		return dir
 	}
-	if dir := os.Getenv("AGENT_HARNESS_RECORD_DIR"); dir != "" {
+	if dir := os.Getenv("FIZEAU_HARNESS_RECORD_DIR"); dir != "" {
 		return dir
 	}
 	return t.TempDir()
diff --git a/internal/harnesses/codex/quota_pty_integration_test.go b/internal/harnesses/codex/quota_pty_integration_test.go
index 6016162..3541ba4 100644
--- a/internal/harnesses/codex/quota_pty_integration_test.go
+++ b/internal/harnesses/codex/quota_pty_integration_test.go
@@ -14,8 +14,8 @@ import (
 )
 
 func Test_quotaRecordCodexPTY(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to refresh authenticated codex quota cassette")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to refresh authenticated codex quota cassette")
 	}
 	dir := filepath.Join(recordBaseDir(t), "codex", "quota")
 	windows, err := ReadCodexQuotaViaPTY(45*time.Second, WithQuotaPTYCassetteDir(dir))
@@ -30,8 +30,8 @@ func Test_quotaRecordCodexPTY(t *testing.T) {
 }
 
 func Test_modelDiscoveryRecordCodexPTY(t *testing.T) {
-	if os.Getenv("AGENT_HARNESS_RECORD") != "1" {
-		t.Skip("set AGENT_HARNESS_RECORD=1 to refresh authenticated codex model cassette")
+	if os.Getenv("FIZEAU_HARNESS_RECORD") != "1" {
+		t.Skip("set FIZEAU_HARNESS_RECORD=1 to refresh authenticated codex model cassette")
 	}
 	dir := filepath.Join(recordBaseDir(t), "codex", "models")
 	snapshot, err := ReadCodexModelDiscoveryViaPTY(45*time.Second, WithQuotaPTYCassetteDir(dir))
@@ -48,10 +48,10 @@ func Test_modelDiscoveryRecordCodexPTY(t *testing.T) {
 
 func recordBaseDir(t *testing.T) string {
 	t.Helper()
-	if dir := os.Getenv("AGENT_HARNESS_CASSETTE_DIR"); dir != "" {
+	if dir := os.Getenv("FIZEAU_HARNESS_CASSETTE_DIR"); dir != "" {
 		return dir
 	}
-	if dir := os.Getenv("AGENT_HARNESS_RECORD_DIR"); dir != "" {
+	if dir := os.Getenv("FIZEAU_HARNESS_RECORD_DIR"); dir != "" {
 		return dir
 	}
 	return t.TempDir()
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
