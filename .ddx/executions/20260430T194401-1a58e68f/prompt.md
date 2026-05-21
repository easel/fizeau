<bead-review>
  <bead id="agent-06080e67" iter=1>
    <title>FZ-024 Rename wire debug env vars to FIZEAU</title>
    <description>
Hard-cut OpenAI-compatible wire debug env vars to FIZEAU_DEBUG_WIRE, FIZEAU_DEBUG_WIRE_FILE, and FIZEAU_DEBUG_WIRE_STREAM_FULL.
    </description>
    <acceptance>
Targeted debug tests pass and old AGENT_DEBUG_WIRE names are absent outside allowlist.
    </acceptance>
    <labels>area:rename, kind:config, area:env</labels>
  </bead>

  <changed-files>
    <file>docs/helix/01-frame/features/FEAT-003-providers.md</file>
    <file>internal/sdk/openaicompat/debug.go</file>
    <file>internal/sdk/openaicompat/debug_test.go</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="2505b876b7c4a2ab316854de62c88fb6b84a10a9">
diff --git a/docs/helix/01-frame/features/FEAT-003-providers.md b/docs/helix/01-frame/features/FEAT-003-providers.md
index d0f7b39..2005dcb 100644
--- a/docs/helix/01-frame/features/FEAT-003-providers.md
+++ b/docs/helix/01-frame/features/FEAT-003-providers.md
@@ -253,9 +253,9 @@ type AttemptMetadata struct {
 
 #### Debug and Observability
 
-32. A process-wide opt-in debug mode (`AGENT_DEBUG_WIRE=1`) dumps every HTTP
+32. A process-wide opt-in debug mode (`FIZEAU_DEBUG_WIRE=1`) dumps every HTTP
     request and response at the openai-go transport boundary to stderr (or a
-    file via `AGENT_DEBUG_WIRE_FILE=<path>`). Default off, zero cost when
+    file via `FIZEAU_DEBUG_WIRE_FILE=<path>`). Default off, zero cost when
     disabled. Authorization Bearer tokens are redacted before any event is
     written. Complements session events (`EventLLMRequest`/`EventLLMResponse`)
     which capture the logical view; wire dump captures the HTTP view.
diff --git a/internal/sdk/openaicompat/debug.go b/internal/sdk/openaicompat/debug.go
index a4b3c52..dc0d80c 100644
--- a/internal/sdk/openaicompat/debug.go
+++ b/internal/sdk/openaicompat/debug.go
@@ -22,15 +22,15 @@ import (
 // raw bodies, and SSE chunk boundaries. Do not duplicate the logical fields.
 //
 // Env vars:
-//   AGENT_DEBUG_WIRE      — "1" or "true" enables the dump; default off
-//   AGENT_DEBUG_WIRE_FILE — optional path; when set, JSONL is written to this
-//                           file, otherwise events go to stderr as JSONL
+//   FIZEAU_DEBUG_WIRE      — "1" or "true" enables the dump; default off
+//   FIZEAU_DEBUG_WIRE_FILE — optional path; when set, JSONL is written to this
+//                            file, otherwise events go to stderr as JSONL
 //
 // When disabled, no middleware is installed and overhead is zero.
 
 const (
-	envDebugWire     = "AGENT_DEBUG_WIRE"
-	envDebugWireFile = "AGENT_DEBUG_WIRE_FILE"
+	envDebugWire     = "FIZEAU_DEBUG_WIRE"
+	envDebugWireFile = "FIZEAU_DEBUG_WIRE_FILE"
 )
 
 // bearerTokenPattern matches "Authorization: Bearer <token>" so we can redact
@@ -49,7 +49,7 @@ type DebugSink struct {
 
 var (
 	sinkOnce sync.Once
-	sink     *DebugSink // nil when AGENT_DEBUG_WIRE is unset
+	sink     *DebugSink // nil when FIZEAU_DEBUG_WIRE is unset
 )
 
 // envBoolTrue reports whether the named env var is set to a truthy value
@@ -73,7 +73,7 @@ func ResolveDebugSink() *DebugSink {
 			// #nosec G304 G703 -- path comes from operator-set env var; intentional.
 			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
 			if err != nil {
-				fmt.Fprintf(os.Stderr, "agent: AGENT_DEBUG_WIRE_FILE open failed: %v — falling back to stderr\n", err)
+				fmt.Fprintf(os.Stderr, "agent: FIZEAU_DEBUG_WIRE_FILE open failed: %v — falling back to stderr\n", err)
 				sink = &DebugSink{w: os.Stderr}
 				return
 			}
@@ -111,7 +111,7 @@ const maxBodyBytes = 64 * 1024
 // avoid flooding stderr during long reasoning-model generations; this knob
 // disables the cap so the full stream is available for post-mortem analysis
 // of client-side truncation defects (see bead agent-f237e07b).
-const envDebugWireStreamFull = "AGENT_DEBUG_WIRE_STREAM_FULL"
+const envDebugWireStreamFull = "FIZEAU_DEBUG_WIRE_STREAM_FULL"
 
 func (s *DebugSink) emit(ev wireEvent) {
 	s.mu.Lock()
@@ -166,7 +166,7 @@ func captureBody(body io.ReadCloser) ([]byte, bool, io.ReadCloser, error) {
 //
 // When streamFull is false (default), cumulative captured bytes are capped at
 // maxBodyBytes to avoid flooding stderr on long reasoning-model generations.
-// Setting AGENT_DEBUG_WIRE_STREAM_FULL=1 disables the cap so the entire
+// Setting FIZEAU_DEBUG_WIRE_STREAM_FULL=1 disables the cap so the entire
 // stream is captured — necessary when diagnosing client-side truncation
 // defects (bead agent-f237e07b wire capture stopped at 186 bytes because the
 // cap + the teeBody emitting only up to the first cap-sized prefix made
diff --git a/internal/sdk/openaicompat/debug_test.go b/internal/sdk/openaicompat/debug_test.go
index 03cb8f0..806e9bb 100644
--- a/internal/sdk/openaicompat/debug_test.go
+++ b/internal/sdk/openaicompat/debug_test.go
@@ -160,7 +160,7 @@ func TestResolveDebugSink_DisabledByDefault(t *testing.T) {
 	assert.False(t, envBoolTrue(envDebugWire))
 }
 
-// Sanity: ResolveDebugSink returns a non-nil sink when AGENT_DEBUG_WIRE is set
+// Sanity: ResolveDebugSink returns a non-nil sink when FIZEAU_DEBUG_WIRE is set
 // the first time the package observes the env. Because sinkOnce is global we
 // can't reset it; this test runs in isolation only when the env is set at
 // process start. Documented here so future readers understand the coverage gap.
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
