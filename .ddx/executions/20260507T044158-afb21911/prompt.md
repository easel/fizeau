<bead-review>
  <bead id="fizeau-169b19c4" iter=1>
    <title>Add TerminalBench 2.1 local Qwen provider profiles</title>
    <description>
Add or update benchmark profiles for every local qwen3.6-27b provider intended for the TerminalBench 2.1 local sweep. The intended local provider set is now explicit:

- vidar-qwen3-6-27b: native oMLX at http://vidar:1235/v1 serving Qwen3.6-27B-MLX-8bit
- grendel-rapid-mlx: Rapid-MLX at http://grendel:8000/v1 serving mlx-community/Qwen3.6-27B-8bit
- bragi-club-3090: vLLM-compatible club-3090 at http://bragi:8020/v1 serving qwen3.6-27b-autoround
- sindri-club-3090: vLLM-compatible club-3090 at http://sindri:8020/v1 serving qwen3.6-27b-autoround

Do not include Vidar through an OpenAI-compatible alias as a separate sweep lane; Vidar should be represented by the native oMLX provider type only. Bragi LM Studio may remain as historical/existing profile data, but it is not part of the verified local Qwen sweep unless it passes a fresh preflight.

In scope:
- scripts/benchmark/profiles/*.yaml for the intended local qwen3.6-27b providers
- benchmark profile schema support for provider types used by those profiles
- any lane/resource config consumed by the 2.1 sweep runner
- lightweight reachability/model-list checks documented as preflight, not as committed secrets

Out of scope:
- running the full sweep
- changing managed OpenRouter profiles
    </description>
    <acceptance>
1. Profiles exist for each intended local qwen3.6-27b provider: Vidar native oMLX, Grendel Rapid-MLX, Bragi club-3090 vLLM on http://bragi:8020/v1, and Sindri club-3090 vLLM on http://sindri:8020/v1.
2. Vidar is not represented as a separate OpenAI-compatible sweep lane; the active Vidar local profile uses provider type omlx.
3. Each profile records provider type, model id, base_url, api_key_env, pricing=0, context/output limits, sampling/reasoning defaults, and versioning snapshot/resolved_at.
4. The club-3090 profiles use model qwen3.6-27b-autoround unless preflight discovers a different exact served id.
5. The Grendel Rapid-MLX profile uses model mlx-community/Qwen3.6-27B-8bit unless preflight discovers a different exact served id.
6. Each local provider lane is assigned or can be mapped to a resource_group keyed by endpoint/server so runner concurrency can be capped.
7. `go run ./cmd/bench profiles list --json` loads the profiles successfully.
8. Any live model-list smoke checks are recorded in notes/docs without committing secrets or machine-local config.
    </acceptance>
    <notes>
2026-05-07 preflight: Vidar native oMLX /v1/models lists Qwen3.6-27B-MLX-8bit; Grendel Rapid-MLX http://grendel:8000/v1/models lists mlx-community/Qwen3.6-27B-8bit and /health reports ready; Bragi club-3090 http://bragi:8020/v1/models lists qwen3.6-27b-autoround; Sindri club-3090 http://sindri:8020/v1/models lists qwen3.6-27b-autoround. Vidar OpenAI-compatible profile should not be used for the sweep.
    </notes>
    <labels>area:benchmark, area:provider, area:terminalbench, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>.ddx/executions/20260507T043200-aa499006/preflight-notes-2026-05-07.md</file>
    <file>scripts/benchmark/profiles/bragi-club-3090.yaml</file>
    <file>scripts/benchmark/profiles/grendel-rapid-mlx.yaml</file>
    <file>scripts/benchmark/profiles/sindri-club-3090.yaml</file>
    <file>scripts/benchmark/terminalbench-2-1-sweep.yaml</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="184220785b311a4a81262dff6ae5112a4276490d">
<untrusted-data>
diff --git a/.ddx/executions/20260507T043200-aa499006/preflight-notes-2026-05-07.md b/.ddx/executions/20260507T043200-aa499006/preflight-notes-2026-05-07.md
new file mode 100644
index 00000000..b389866f
--- /dev/null
+++ b/.ddx/executions/20260507T043200-aa499006/preflight-notes-2026-05-07.md
@@ -0,0 +1,44 @@
+# TerminalBench 2.1 Local Qwen Provider Preflight — 2026-05-07
+
+Recorded per bead fizeau-169b19c4 AC 8: live model-list smoke checks documented
+without committing secrets or machine-local config.
+
+## Verified endpoints and model IDs
+
+| Provider profile    | Endpoint                    | Model ID verified via /v1/models       | /health |
+|---------------------|-----------------------------|----------------------------------------|---------|
+| vidar-qwen3-6-27b   | http://vidar:1235/v1        | Qwen3.6-27B-MLX-8bit                   | n/a     |
+| grendel-rapid-mlx   | http://grendel:8000/v1      | mlx-community/Qwen3.6-27B-8bit         | ready   |
+| bragi-club-3090     | http://bragi:8020/v1        | qwen3.6-27b-autoround                  | n/a     |
+| sindri-club-3090    | http://sindri:8020/v1       | qwen3.6-27b-autoround                  | n/a     |
+
+Source: bead notes recorded at execution time; no API keys or machine-local
+config are stored here.
+
+## Smoke check commands (reference only — do not commit outputs)
+
+```sh
+# vidar native oMLX
+curl -sf http://vidar:1235/v1/models | python3 -m json.tool
+
+# grendel Rapid-MLX
+curl -sf http://grendel:8000/v1/models | python3 -m json.tool
+curl -sf http://grendel:8000/health
+
+# bragi club-3090 vLLM
+curl -sf http://bragi:8020/v1/models | python3 -m json.tool
+
+# sindri club-3090 vLLM
+curl -sf http://sindri:8020/v1/models | python3 -m json.tool
+```
+
+## Notes
+
+- No API key required for oMLX (vidar); `OMLX_API_KEY` may be any non-empty string.
+- `RAPID_MLX_API_KEY` and `VLLM_API_KEY` are placeholders; set to any non-empty
+  string if the server does not enforce auth, or to the actual token if configured.
+- The bragi LM Studio profile (`bragi-qwen3-6-27b`, port 1234) is retained as
+  historical data but is not part of the verified 2026-05-07 local sweep. It
+  should be re-preflighted before inclusion in a future run.
+- Vidar is represented only via the native oMLX profile (`provider.type: omlx`).
+  No OpenAI-compatible alias sweep lane exists for vidar.
diff --git a/scripts/benchmark/profiles/bragi-club-3090.yaml b/scripts/benchmark/profiles/bragi-club-3090.yaml
index 8c8c8d21..a22a7d3a 100644
--- a/scripts/benchmark/profiles/bragi-club-3090.yaml
+++ b/scripts/benchmark/profiles/bragi-club-3090.yaml
@@ -17,6 +17,7 @@ limits:
   rate_limit_tpm: 100000
 sampling:
   temperature: 0.6
+  reasoning: low
   top_p: 0.95
   top_k: 20
 versioning:
diff --git a/scripts/benchmark/profiles/grendel-rapid-mlx.yaml b/scripts/benchmark/profiles/grendel-rapid-mlx.yaml
index 02b6fe08..b1f4a21c 100644
--- a/scripts/benchmark/profiles/grendel-rapid-mlx.yaml
+++ b/scripts/benchmark/profiles/grendel-rapid-mlx.yaml
@@ -17,6 +17,7 @@ limits:
   rate_limit_tpm: 100000
 sampling:
   temperature: 0.6
+  reasoning: low
   top_p: 0.95
   top_k: 20
 versioning:
diff --git a/scripts/benchmark/profiles/sindri-club-3090.yaml b/scripts/benchmark/profiles/sindri-club-3090.yaml
index f4b205a7..fd7bf82b 100644
--- a/scripts/benchmark/profiles/sindri-club-3090.yaml
+++ b/scripts/benchmark/profiles/sindri-club-3090.yaml
@@ -17,6 +17,7 @@ limits:
   rate_limit_tpm: 100000
 sampling:
   temperature: 0.6
+  reasoning: low
   top_p: 0.95
   top_k: 20
 versioning:
diff --git a/scripts/benchmark/terminalbench-2-1-sweep.yaml b/scripts/benchmark/terminalbench-2-1-sweep.yaml
index a61fcce1..e56e92dc 100644
--- a/scripts/benchmark/terminalbench-2-1-sweep.yaml
+++ b/scripts/benchmark/terminalbench-2-1-sweep.yaml
@@ -45,11 +45,14 @@ phases:
       - fiz-openrouter-claude-sonnet-4-6
       - fiz-openrouter-gpt-5-4-mini
       - fiz-vidar-omlx-qwen3-6-27b
+      - fiz-grendel-rapid-mlx-qwen3-6-27b
+      - fiz-bragi-club-3090-qwen3-6-27b
+      - fiz-sindri-club-3090-qwen3-6-27b
       - fiz-bragi-lmstudio-qwen3-6-27b
-      - fiz-bragi-club-vllm-qwen3-6-27b
     preflight:
       - "Verify all local endpoints are reachable before starting canary"
-      - "bragi-club-3090 may be excluded if preflight fails; mark cells invalid_provider"
+      - "Verified 2026-05-07: vidar:1235, grendel:8000, bragi:8020, sindri:8020 all reachable"
+      - "bragi-lmstudio may be excluded if preflight fails; mark cells invalid_provider"
     parallel_policy: >-
       Local resource groups serialize (one cell at a time per endpoint).
       OpenRouter lanes (fiz-harness-*, fiz-openrouter-*) may run in parallel
@@ -65,12 +68,15 @@ phases:
     subset: terminalbench-2-1-full    # full 2.1 task catalog or a 2.1 expanded subset; see profile_inventory
     lanes:
       - fiz-vidar-omlx-qwen3-6-27b
+      - fiz-grendel-rapid-mlx-qwen3-6-27b
+      - fiz-bragi-club-3090-qwen3-6-27b
+      - fiz-sindri-club-3090-qwen3-6-27b
       - fiz-bragi-lmstudio-qwen3-6-27b
-      - fiz-bragi-club-vllm-qwen3-6-27b
     parallel_policy: >-
-      Each lane belongs to a distinct resource group; all three may run in
-      parallel since they use different servers/endpoints. Within each resource
-      group max_concurrency=1.
+      Each lane belongs to a distinct resource group (rg-vidar-omlx,
+      rg-grendel-rapid-mlx, rg-bragi-club-3090, rg-sindri-club-3090,
+      rg-bragi-lmstudio); all five may run in parallel since they use
+      different servers/endpoints. Within each resource group max_concurrency=1.
 
   - id: sonnet-comparison
     description: >-
@@ -115,8 +121,10 @@ comparison_groups:
       inference providers, runtimes, and quantization configurations?
     lanes:
       - fiz-vidar-omlx-qwen3-6-27b
+      - fiz-grendel-rapid-mlx-qwen3-6-27b
+      - fiz-bragi-club-3090-qwen3-6-27b
+      - fiz-sindri-club-3090-qwen3-6-27b
       - fiz-bragi-lmstudio-qwen3-6-27b
-      - fiz-bragi-club-vllm-qwen3-6-27b
     equivalence: approximate_same_family
     equivalence_note: >-
       NOT a true same-model comparison. Rows share the same weights lineage
@@ -198,20 +206,35 @@ resource_groups:
       LM Studio (llama.cpp) single-model server. Serialize all cells.
       Loaded context length: 101770 tokens (verified 2026-05-01).
 
-  - id: rg-bragi-club-vllm
-    server: bragi-club
-    base_url: "http://bragi-club:8000/v1"   # provisional; default vLLM port
-    provider_type: openai-compat
-    hardware: "RTX 3090 (bragi-club host)"
+  - id: rg-bragi-club-3090
+    server: bragi
+    base_url: "http://bragi:8020/v1"
+    provider_type: vllm
+    hardware: "RTX 3090 (bragi-club, port 8020)"
+    max_concurrency: 1
+    notes: >-
+      vLLM with autoround quantization. Verified 2026-05-07: /v1/models
+      lists qwen3.6-27b-autoround at http://bragi:8020/v1.
+
+  - id: rg-grendel-rapid-mlx
+    server: grendel
+    base_url: "http://grendel:8000/v1"
+    provider_type: rapid-mlx
+    hardware: "Apple Silicon (grendel), Rapid-MLX"
+    max_concurrency: 1
+    notes: >-
+      Rapid-MLX server. Verified 2026-05-07: /v1/models lists
+      mlx-community/Qwen3.6-27B-8bit; /health reports ready.
+
+  - id: rg-sindri-club-3090
+    server: sindri
+    base_url: "http://sindri:8020/v1"
+    provider_type: vllm
+    hardware: "RTX 3090 (sindri-club, port 8020)"
     max_concurrency: 1
-    status: requires_preflight
-    preflight_command: >-
-      curl -sf http://bragi-club:8000/v1/models | python3 -m json.tool
     notes: >-
-      vLLM with autoround quantization. Endpoint and model ID are provisional;
-      run preflight_command before any cell. If host is unreachable, mark all
-      cells in this group invalid_provider and proceed with remaining lanes.
-      Update bragi-club-3090-vllm-qwen3-6-27b.yaml after verification.
+      vLLM with autoround quantization. Verified 2026-05-07: /v1/models
+      lists qwen3.6-27b-autoround at http://sindri:8020/v1.
 
   - id: rg-openrouter
     base_url: "https://openrouter.ai/api/v1"
@@ -419,34 +442,89 @@ lanes:
       RTX 5090-mobile, llama.cpp Q4_K_M GGUF. Local free tier. Different
       quant and runtime from vidar-omlx; same weights lineage.
 
-  - id: fiz-bragi-club-vllm-qwen3-6-27b
-    profile_id: bragi-club-3090-vllm-qwen3-6-27b
+  - id: fiz-bragi-club-3090-qwen3-6-27b
+    profile_id: bragi-club-3090
     lane_type: fiz_provider_native
     phases: [canary, local-qwen]
     comparison_groups: [cg-local-qwen-provider-quant]
-    resource_group: rg-bragi-club-vllm
-    status: requires_preflight
+    resource_group: rg-bragi-club-3090
     fizeau_env:
-      FIZEAU_PROVIDER: openai-compat
-      FIZEAU_MODEL: "<qwen3.6-27b-autoround-model-id>"   # fill in after preflight
-      FIZEAU_BASE_URL: "http://bragi-club:8000/v1"       # confirm before use
+      FIZEAU_PROVIDER: vllm
+      FIZEAU_MODEL: "qwen3.6-27b-autoround"
+      FIZEAU_BASE_URL: "http://bragi:8020/v1"
       FIZEAU_API_KEY_ENV: VLLM_API_KEY
     model_family: qwen3-6-27b
-    model_id: "<qwen3.6-27b-autoround-model-id>"
+    model_id: "qwen3.6-27b-autoround"
     quant_label: vllm-autoround
     provider_surface: bragi-club-vllm
     runtime: vllm
     hardware_label: bragi-club-3090
-    endpoint: "http://bragi-club:8000/v1"   # provisional
+    endpoint: "http://bragi:8020/v1"
+    sampling:
+      temperature: 0.6
+      reasoning: low
+      top_p: 0.95
+      top_k: 20
+    equivalence_note: >-
+      RTX 3090, vLLM with autoround quantization. Verified 2026-05-07.
+      Same weights lineage as vidar-omlx, grendel-rapid-mlx, and sindri-club-3090;
+      different quant and runtime — approximate_same_family only.
+
+  - id: fiz-grendel-rapid-mlx-qwen3-6-27b
+    profile_id: grendel-rapid-mlx
+    lane_type: fiz_provider_native
+    phases: [canary, local-qwen]
+    comparison_groups: [cg-local-qwen-provider-quant]
+    resource_group: rg-grendel-rapid-mlx
+    fizeau_env:
+      FIZEAU_PROVIDER: rapid-mlx
+      FIZEAU_MODEL: "mlx-community/Qwen3.6-27B-8bit"
+      FIZEAU_BASE_URL: "http://grendel:8000/v1"
+      FIZEAU_API_KEY_ENV: RAPID_MLX_API_KEY
+    model_family: qwen3-6-27b
+    model_id: "mlx-community/Qwen3.6-27B-8bit"
+    quant_label: mlx-8bit
+    provider_surface: grendel-rapid-mlx
+    runtime: rapid-mlx
+    hardware_label: grendel-apple-m
+    endpoint: "http://grendel:8000/v1"
     sampling:
       temperature: 0.6
       reasoning: low
       top_p: 0.95
       top_k: 20
     equivalence_note: >-
-      RTX 3090, vLLM with autoround quantization. Endpoint and model ID are
-      provisional. If preflight fails, exclude from sweep and mark cells
-      invalid_provider; do not block remaining local-qwen lanes.
+      Apple Silicon (grendel), Rapid-MLX 8-bit quantization. Verified
+      2026-05-07. Same weights lineage as vidar-omlx and club-3090 lanes;
+      different runtime — approximate_same_family only.
+
+  - id: fiz-sindri-club-3090-qwen3-6-27b
+    profile_id: sindri-club-3090
+    lane_type: fiz_provider_native
+    phases: [canary, local-qwen]
+    comparison_groups: [cg-local-qwen-provider-quant]
+    resource_group: rg-sindri-club-3090
+    fizeau_env:
+      FIZEAU_PROVIDER: vllm
+      FIZEAU_MODEL: "qwen3.6-27b-autoround"
+      FIZEAU_BASE_URL: "http://sindri:8020/v1"
+      FIZEAU_API_KEY_ENV: VLLM_API_KEY
+    model_family: qwen3-6-27b
+    model_id: "qwen3.6-27b-autoround"
+    quant_label: vllm-autoround
+    provider_surface: sindri-club-vllm
+    runtime: vllm
+    hardware_label: sindri-club-3090
+    endpoint: "http://sindri:8020/v1"
+    sampling:
+      temperature: 0.6
+      reasoning: low
+      top_p: 0.95
+      top_k: 20
+    equivalence_note: >-
+      RTX 3090, vLLM with autoround quantization. Verified 2026-05-07.
+      Same weights lineage and quant method as bragi-club-3090; different
+      server — approximate_same_family only.
 
 # ---------------------------------------------------------------------------
 # Resume Policy
@@ -567,13 +645,27 @@ profile_inventory:
       used_in_phases: [canary, local-qwen]
 
   new_required:
-    - id: bragi-club-3090-vllm-qwen3-6-27b
-      path: scripts/benchmark/profiles/bragi-club-3090-vllm-qwen3-6-27b.yaml
-      status: created_provisional   # file exists; model_id and endpoint must be updated after preflight
+    - id: bragi-club-3090
+      path: scripts/benchmark/profiles/bragi-club-3090.yaml
+      status: verified
       used_in_phases: [canary, local-qwen]
-      action_needed: >-
-        Run rg-bragi-club-vllm preflight_command; update model field and
-        base_url if they differ from placeholders; update versioning.snapshot.
+      notes: >-
+        Verified 2026-05-07: /v1/models lists qwen3.6-27b-autoround at
+        http://bragi:8020/v1. Profile and lane updated from provisional.
+    - id: grendel-rapid-mlx
+      path: scripts/benchmark/profiles/grendel-rapid-mlx.yaml
+      status: verified
+      used_in_phases: [canary, local-qwen]
+      notes: >-
+        Verified 2026-05-07: /v1/models lists mlx-community/Qwen3.6-27B-8bit;
+        /health reports ready at http://grendel:8000/v1.
+    - id: sindri-club-3090
+      path: scripts/benchmark/profiles/sindri-club-3090.yaml
+      status: verified
+      used_in_phases: [canary, local-qwen]
+      notes: >-
+        Verified 2026-05-07: /v1/models lists qwen3.6-27b-autoround at
+        http://sindri:8020/v1.
     - id: terminalbench-2-1-canary
       path: scripts/benchmark/task-subset-tb21-canary.yaml   # to be created
       status: missing
</untrusted-data>
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
