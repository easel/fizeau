<bead-review>
  <bead id="fizeau-39b9669a" iter=1>
    <title>Extend benchmark evidence schema for versioned FHI axes</title>
    <description>
SD-012 requires evidence records to support claims over Fizeau version x harness version x model version x provider version x benchmark version, with local runtime details and session provenance. The current schema has basic source/benchmark/subject/scope/score fields but does not require enough structured metadata for FHI or local-vs-frontier claims.

In-scope files:
- scripts/benchmark/benchmark-evidence.schema.json
- schema fixture/test files under scripts/benchmark or cmd/bench testdata
- focused schema validation tests

Out-of-scope:
- implementing importers or writers
- changing benchmark runners
- defining the FHI formula

Implementation notes:
- Add structured provenance/runtime fields for Fizeau version/git commit, harness wrapper/version, wrapped harness CLI/runtime version, provider endpoint/API surface/version, model snapshot/version, deployment class, quantization, local runtime/server version, hardware/accelerator metadata, session_log_path/session_log_sha256, trajectory_path/trajectory_sha256, invalid_class, denominator handling, and formula-ready coverage metadata.
- Preserve backwards compatibility by allowing existing fields where practical, but add tests proving FHI-required sample records validate.
    </description>
    <acceptance>
1. Schema fixtures for a managed frontier row and a local oMLX Qwen row validate against benchmark-evidence/v1.
2. The local fixture includes Fizeau version/git commit, oMLX version, quantization, hardware, provider endpoint, model snapshot/raw name, session log path/hash, and benchmark subset/version.
3. The managed frontier fixture includes provider surface/version or capture timestamp, model snapshot/raw name, harness wrapper/version, and benchmark subset/version.
4. Invalid-run fixture includes invalid_class and denominator exclusion metadata.
5. The schema validation test command for scripts/benchmark or cmd/bench passes.
    </acceptance>
    <labels>area:benchmark, area:evidence, kind:task, phase:build</labels>
  </bead>

  <changed-files>
    <file>cmd/bench/benchmark_evidence_schema_test.go</file>
    <file>cmd/bench/testdata/benchmark-evidence/invalid-run.json</file>
    <file>cmd/bench/testdata/benchmark-evidence/local-omlx-qwen.json</file>
    <file>cmd/bench/testdata/benchmark-evidence/managed-frontier-opus.json</file>
    <file>go.mod</file>
    <file>go.sum</file>
    <file>scripts/benchmark/benchmark-evidence.schema.json</file>
  </changed-files>

  <governing>
    <note>No governing documents found. Evaluate the diff against the acceptance criteria alone.</note>
  </governing>

  <diff rev="f23412564d8588321c513d4fe3c36c1c3dedf662">
<untrusted-data>
diff --git a/cmd/bench/benchmark_evidence_schema_test.go b/cmd/bench/benchmark_evidence_schema_test.go
new file mode 100644
index 0000000..be66727
--- /dev/null
+++ b/cmd/bench/benchmark_evidence_schema_test.go
@@ -0,0 +1,145 @@
+package main
+
+import (
+	"bytes"
+	"encoding/json"
+	"os"
+	"path/filepath"
+	"testing"
+
+	"github.com/santhosh-tekuri/jsonschema/v5"
+)
+
+func TestBenchmarkEvidenceFixturesValidate(t *testing.T) {
+	schema := compileBenchmarkEvidenceSchema(t)
+
+	validate := func(name string) map[string]any {
+		t.Helper()
+		doc := loadBenchmarkEvidenceFixture(t, name)
+		if err := schema.Validate(doc); err != nil {
+			t.Fatalf("%s failed schema validation: %v", name, err)
+		}
+		return doc
+	}
+
+	frontier := validate("managed-frontier-opus.json")
+	assertString(t, frontier, "subject.model_raw", "Opus 4.7")
+	assertString(t, frontier, "subject.harness", "claude-code")
+	assertString(t, frontier, "subject.provider", "anthropic")
+	assertString(t, frontier, "benchmark.subset_id", "tb2-wide")
+	assertString(t, frontier, "benchmark.subset_version", "v2")
+	assertString(t, frontier, "provenance.harness_wrapper_name", "claude-code")
+	assertString(t, frontier, "provenance.harness_wrapper_version", "1.0.0")
+	assertString(t, frontier, "provenance.provider_capture_at", "2026-05-06T20:00:00Z")
+	assertString(t, frontier, "provenance.model_snapshot", "opus-4.7-20260505")
+	assertString(t, frontier, "coverage.formula_version", "fhi/v1")
+
+	local := validate("local-omlx-qwen.json")
+	assertString(t, local, "subject.model_raw", "Qwen3.6-27B-MLX-8bit")
+	assertString(t, local, "subject.provider", "omlx")
+	assertString(t, local, "subject.endpoint", "http://vidar:1235/v1")
+	assertString(t, local, "runtime.deployment_class", "local")
+	assertString(t, local, "runtime.quantization", "8-bit")
+	assertString(t, local, "runtime.local_runtime_name", "omlx")
+	assertString(t, local, "runtime.local_runtime_version", "0.8.10")
+	assertString(t, local, "runtime.hardware_class", "Mac Studio")
+	assertString(t, local, "provenance.fizeau_version", "0.1.0")
+	assertString(t, local, "provenance.fizeau_git_commit", "fa48595c7262b1522ab41897c3f60e128014f598")
+	assertString(t, local, "provenance.provider_version", "0.8.10")
+	assertString(t, local, "provenance.session_log_path", "benchmark-results/evidence/local-omlx-qwen/session.log.jsonl")
+	assertString(t, local, "provenance.session_log_sha256", "3333333333333333333333333333333333333333333333333333333333333333")
+	assertString(t, local, "benchmark.subset_id", "tb2-wide")
+	assertString(t, local, "benchmark.subset_version", "v2")
+
+	invalid := validate("invalid-run.json")
+	assertString(t, invalid, "invalid_class", "invalid_setup")
+	assertBool(t, invalid, "denominator.included", false)
+	assertString(t, invalid, "denominator.policy", "exclude_invalid_runs")
+	assertString(t, invalid, "denominator.reason", "setup failure before first benchmark task")
+	assertString(t, invalid, "scope.denominator_rule", "exclude_invalid_runs")
+}
+
+func compileBenchmarkEvidenceSchema(t *testing.T) *jsonschema.Schema {
+	t.Helper()
+
+	repoRoot := benchRepoRoot(t)
+	rawSchema, err := os.ReadFile(filepath.Join(repoRoot, "scripts", "benchmark", "benchmark-evidence.schema.json"))
+	if err != nil {
+		t.Fatalf("read benchmark evidence schema: %v", err)
+	}
+
+	compiler := jsonschema.NewCompiler()
+	compiler.AssertFormat = true
+	if err := compiler.AddResource("benchmark-evidence.schema.json", bytes.NewReader(rawSchema)); err != nil {
+		t.Fatalf("add benchmark evidence schema: %v", err)
+	}
+	schema, err := compiler.Compile("benchmark-evidence.schema.json")
+	if err != nil {
+		t.Fatalf("compile benchmark evidence schema: %v", err)
+	}
+	return schema
+}
+
+func loadBenchmarkEvidenceFixture(t *testing.T, name string) map[string]any {
+	t.Helper()
+
+	repoRoot := benchRepoRoot(t)
+	raw, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "bench", "testdata", "benchmark-evidence", name))
+	if err != nil {
+		t.Fatalf("read fixture %s: %v", name, err)
+	}
+
+	dec := json.NewDecoder(bytes.NewReader(raw))
+	dec.UseNumber()
+	var doc map[string]any
+	if err := dec.Decode(&doc); err != nil {
+		t.Fatalf("parse fixture %s: %v", name, err)
+	}
+	return doc
+}
+
+func assertString(t *testing.T, doc map[string]any, path, want string) {
+	t.Helper()
+	got, ok := lookupPath(doc, path)
+	if !ok {
+		t.Fatalf("missing %s", path)
+	}
+	str, ok := got.(string)
+	if !ok {
+		t.Fatalf("%s = %T, want string", path, got)
+	}
+	if str != want {
+		t.Fatalf("%s = %q, want %q", path, str, want)
+	}
+}
+
+func assertBool(t *testing.T, doc map[string]any, path string, want bool) {
+	t.Helper()
+	got, ok := lookupPath(doc, path)
+	if !ok {
+		t.Fatalf("missing %s", path)
+	}
+	b, ok := got.(bool)
+	if !ok {
+		t.Fatalf("%s = %T, want bool", path, got)
+	}
+	if b != want {
+		t.Fatalf("%s = %t, want %t", path, b, want)
+	}
+}
+
+func lookupPath(doc map[string]any, path string) (any, bool) {
+	var cur any = doc
+	for _, segment := range bytes.Split([]byte(path), []byte(".")) {
+		m, ok := cur.(map[string]any)
+		if !ok {
+			return nil, false
+		}
+		next, ok := m[string(segment)]
+		if !ok {
+			return nil, false
+		}
+		cur = next
+	}
+	return cur, true
+}
diff --git a/cmd/bench/testdata/benchmark-evidence/invalid-run.json b/cmd/bench/testdata/benchmark-evidence/invalid-run.json
new file mode 100644
index 0000000..61ddc52
--- /dev/null
+++ b/cmd/bench/testdata/benchmark-evidence/invalid-run.json
@@ -0,0 +1,104 @@
+{
+  "schema_version": "benchmark-evidence/v1",
+  "record_id": "invalid-setup-local-omlx-qwen3-6-27b-terminalbench-tb2-wide",
+  "captured_at": "2026-05-06T20:12:00Z",
+  "source": {
+    "type": "fizeau_runner",
+    "name": "Fizeau matrix run",
+    "url": "https://example.com/fizeau/matrix/20260506",
+    "artifact_path": "cmd/bench/testdata/benchmark-evidence/invalid-run.json"
+  },
+  "benchmark": {
+    "name": "terminal-bench",
+    "version": "2026.05.06",
+    "dataset_commit": "903487e82ad1998f0c20b721a7df66ec815ea673",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "scorer": "verifier",
+    "scorer_version": "1.0.0",
+    "higher_is_better": true
+  },
+  "subject": {
+    "model": "qwen3.6-27b",
+    "model_raw": "Qwen3.6-27B-MLX-8bit",
+    "harness": "fiz",
+    "provider": "omlx",
+    "endpoint": "http://vidar:1235/v1",
+    "surface": "openai-compat",
+    "deployment_class": "local",
+    "reasoning": "high"
+  },
+  "scope": {
+    "run_id": "terminalbench-tb2-wide-20260506-invalid",
+    "task_id": "fix-git",
+    "subset": "tb2-wide",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "denominator_rule": "exclude_invalid_runs",
+    "split": "test",
+    "rep": 1,
+    "n_tasks": 15
+  },
+  "score": {
+    "metric": "pass_rate",
+    "value": 0,
+    "raw_value": 0,
+    "n": 1,
+    "passed": 0,
+    "failed": 1
+  },
+  "invalid_class": "invalid_setup",
+  "coverage": {
+    "formula_version": "fhi/v1",
+    "evidence_window": "2026-Q2",
+    "included_benchmarks": ["terminal-bench"],
+    "included_subsets": ["tb2-wide"],
+    "denominator_rule": "exclude_invalid_runs",
+    "coverage_note": "invalid benchmark run excluded from pass-rate denominator",
+    "confidence_note": "setup failure prevents a valid task-level score"
+  },
+  "denominator": {
+    "included": false,
+    "policy": "exclude_invalid_runs",
+    "reason": "setup failure before first benchmark task",
+    "excluded_classes": ["invalid_setup"],
+    "excluded_tasks": ["fix-git"],
+    "included_count": 0,
+    "excluded_count": 1
+  },
+  "runtime": {
+    "deployment_class": "local",
+    "quantization": "8-bit",
+    "local_runtime_name": "omlx",
+    "local_runtime_version": "0.8.10",
+    "server_name": "vidar",
+    "server_version": "0.8.10",
+    "hardware_class": "Mac Studio",
+    "hardware_accelerator": "Apple GPU",
+    "hardware_accelerator_backend": "mlx",
+    "hardware_memory_gb": 512,
+    "hardware_os": "macOS 15.5",
+    "hardware_arch": "arm64"
+  },
+  "provenance": {
+    "fizeau_version": "0.1.0",
+    "fizeau_git_commit": "fa48595c7262b1522ab41897c3f60e128014f598",
+    "harness_wrapper_name": "fiz-native",
+    "harness_wrapper_version": "1.0.0",
+    "harness_cli_version": "0.67.1",
+    "harness_runtime_version": "go1.26.2",
+    "provider_endpoint": "http://vidar:1235/v1",
+    "provider_surface": "openai-compat",
+    "provider_version": "0.8.10",
+    "provider_capture_at": "2026-05-06T20:12:00Z",
+    "model_snapshot": "qwen3.6-27b-mlx-8bit@0.8.10",
+    "model_version": "Qwen3.6-27B-MLX-8bit",
+    "session_log_path": "benchmark-results/evidence/invalid-run/session.log.jsonl",
+    "session_log_sha256": "5555555555555555555555555555555555555555555555555555555555555555",
+    "trajectory_path": "benchmark-results/evidence/invalid-run/trajectory.json",
+    "trajectory_sha256": "6666666666666666666666666666666666666666666666666666666666666666",
+    "benchmark_subset": "tb2-wide",
+    "benchmark_subset_version": "v2",
+    "benchmark_runner_version": "harbor-0.20.0"
+  }
+}
diff --git a/cmd/bench/testdata/benchmark-evidence/local-omlx-qwen.json b/cmd/bench/testdata/benchmark-evidence/local-omlx-qwen.json
new file mode 100644
index 0000000..ccb87b8
--- /dev/null
+++ b/cmd/bench/testdata/benchmark-evidence/local-omlx-qwen.json
@@ -0,0 +1,118 @@
+{
+  "schema_version": "benchmark-evidence/v1",
+  "record_id": "local-omlx-qwen3-6-27b-mlx-8bit-terminalbench-tb2-wide",
+  "captured_at": "2026-05-06T20:10:00Z",
+  "source": {
+    "type": "fizeau_runner",
+    "name": "Fizeau matrix run",
+    "url": "https://example.com/fizeau/matrix/20260506",
+    "artifact_path": "cmd/bench/testdata/benchmark-evidence/local-omlx-qwen.json",
+    "artifact_sha256": "1111111111111111111111111111111111111111111111111111111111111111"
+  },
+  "benchmark": {
+    "name": "terminal-bench",
+    "version": "2026.05.06",
+    "dataset_commit": "903487e82ad1998f0c20b721a7df66ec815ea673",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "scorer": "verifier",
+    "scorer_version": "1.0.0",
+    "higher_is_better": true
+  },
+  "subject": {
+    "model": "qwen3.6-27b",
+    "model_raw": "Qwen3.6-27B-MLX-8bit",
+    "harness": "fiz",
+    "provider": "omlx",
+    "endpoint": "http://vidar:1235/v1",
+    "surface": "openai-compat",
+    "deployment_class": "local",
+    "reasoning": "high"
+  },
+  "scope": {
+    "run_id": "terminalbench-tb2-wide-20260506-local-omlx",
+    "task_id": "fix-git",
+    "subset": "tb2-wide",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "denominator_rule": "count_valid_tasks",
+    "split": "test",
+    "rep": 1,
+    "n_tasks": 15
+  },
+  "score": {
+    "metric": "pass_rate",
+    "value": 0.5,
+    "raw_value": 50,
+    "n": 15,
+    "passed": 7,
+    "failed": 8,
+    "confidence": {
+      "method": "wilson",
+      "lower": 0.27,
+      "upper": 0.73
+    }
+  },
+  "coverage": {
+    "formula_version": "fhi/v1",
+    "evidence_window": "2026-Q2",
+    "included_benchmarks": ["terminal-bench"],
+    "included_subsets": ["tb2-wide"],
+    "denominator_rule": "count_valid_tasks",
+    "coverage_note": "local oMLX sample with versioned runtime metadata",
+    "confidence_note": "session and trajectory hashes present"
+  },
+  "denominator": {
+    "included": true,
+    "policy": "count_valid_runs_only",
+    "reason": "this sample represents a valid benchmark run",
+    "included_count": 15,
+    "excluded_count": 0,
+    "excluded_classes": []
+  },
+  "runtime": {
+    "deployment_class": "local",
+    "quantization": "8-bit",
+    "local_runtime_name": "omlx",
+    "local_runtime_version": "0.8.10",
+    "server_name": "vidar",
+    "server_version": "0.8.10",
+    "context_limit": 131072,
+    "hardware_class": "Mac Studio",
+    "hardware_accelerator": "Apple GPU",
+    "hardware_accelerator_backend": "mlx",
+    "hardware_memory_gb": 512,
+    "hardware_os": "macOS 15.5",
+    "hardware_arch": "arm64",
+    "sampling_temperature": 0.2,
+    "sampling_top_p": 0.9,
+    "sampling_reasoning": "high",
+    "utilization_active_requests": 1,
+    "utilization_queue_depth": 0,
+    "utilization_memory_pressure": 0.34,
+    "utilization_tokens_per_second": 18.5
+  },
+  "provenance": {
+    "fizeau_version": "0.1.0",
+    "fizeau_git_commit": "fa48595c7262b1522ab41897c3f60e128014f598",
+    "harness_wrapper_name": "fiz-native",
+    "harness_wrapper_version": "1.0.0",
+    "harness_cli_version": "0.67.1",
+    "harness_runtime_version": "go1.26.2",
+    "provider_endpoint": "http://vidar:1235/v1",
+    "provider_surface": "openai-compat",
+    "provider_version": "0.8.10",
+    "provider_capture_at": "2026-05-06T20:10:00Z",
+    "model_snapshot": "qwen3.6-27b-mlx-8bit@0.8.10",
+    "model_version": "Qwen3.6-27B-MLX-8bit",
+    "model_artifact_path": "/models/qwen3.6-27b-mlx-8bit",
+    "model_artifact_sha256": "2222222222222222222222222222222222222222222222222222222222222222",
+    "session_log_path": "benchmark-results/evidence/local-omlx-qwen/session.log.jsonl",
+    "session_log_sha256": "3333333333333333333333333333333333333333333333333333333333333333",
+    "trajectory_path": "benchmark-results/evidence/local-omlx-qwen/trajectory.json",
+    "trajectory_sha256": "4444444444444444444444444444444444444444444444444444444444444444",
+    "benchmark_subset": "tb2-wide",
+    "benchmark_subset_version": "v2",
+    "benchmark_runner_version": "harbor-0.20.0"
+  }
+}
diff --git a/cmd/bench/testdata/benchmark-evidence/managed-frontier-opus.json b/cmd/bench/testdata/benchmark-evidence/managed-frontier-opus.json
new file mode 100644
index 0000000..53b6a4c
--- /dev/null
+++ b/cmd/bench/testdata/benchmark-evidence/managed-frontier-opus.json
@@ -0,0 +1,88 @@
+{
+  "schema_version": "benchmark-evidence/v1",
+  "record_id": "managed-frontier-opus-4-7-terminalbench-tb2-wide",
+  "captured_at": "2026-05-06T20:00:00Z",
+  "source": {
+    "type": "external_leaderboard",
+    "name": "TerminalBench public leaderboard",
+    "url": "https://example.com/terminal-bench/leaderboard",
+    "artifact_path": "cmd/bench/testdata/benchmark-evidence/managed-frontier-opus.json"
+  },
+  "benchmark": {
+    "name": "terminal-bench",
+    "version": "2026.05.06",
+    "dataset_commit": "903487e82ad1998f0c20b721a7df66ec815ea673",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "scorer": "verifier",
+    "scorer_version": "1.0.0",
+    "higher_is_better": true
+  },
+  "subject": {
+    "model": "opus-4.7",
+    "model_raw": "Opus 4.7",
+    "harness": "claude-code",
+    "provider": "anthropic",
+    "surface": "messages",
+    "deployment_class": "managed_frontier",
+    "reasoning": "high"
+  },
+  "scope": {
+    "run_id": "terminalbench-tb2-wide-20260506",
+    "task_id": "fix-git",
+    "subset": "tb2-wide",
+    "subset_id": "tb2-wide",
+    "subset_version": "v2",
+    "denominator_rule": "count_valid_tasks",
+    "split": "test",
+    "rep": 1,
+    "n_tasks": 15
+  },
+  "score": {
+    "metric": "pass_rate",
+    "value": 0.8133333333,
+    "raw_value": 81.3,
+    "n": 15,
+    "passed": 12,
+    "failed": 3,
+    "confidence": {
+      "method": "wilson",
+      "lower": 0.6,
+      "upper": 0.92
+    }
+  },
+  "coverage": {
+    "formula_version": "fhi/v1",
+    "evidence_window": "2026-Q2",
+    "included_benchmarks": ["terminal-bench"],
+    "included_subsets": ["tb2-wide"],
+    "denominator_rule": "count_valid_tasks",
+    "coverage_note": "managed frontier sample imported from public leaderboard rows",
+    "confidence_note": "capture timestamp recorded in provenance"
+  },
+  "denominator": {
+    "included": true,
+    "policy": "count_valid_runs_only",
+    "reason": "leaderboard rows are already filtered to valid benchmark runs",
+    "included_count": 15,
+    "excluded_count": 0,
+    "excluded_classes": []
+  },
+  "provenance": {
+    "fizeau_version": "0.1.0",
+    "fizeau_git_commit": "fa48595c7262b1522ab41897c3f60e128014f598",
+    "harness_wrapper_name": "claude-code",
+    "harness_wrapper_version": "1.0.0",
+    "harness_cli_version": "1.0.0",
+    "harness_runtime_version": "node20.19.2",
+    "provider_endpoint": "https://api.anthropic.com/v1",
+    "provider_surface": "messages",
+    "provider_version": "2026-05-05",
+    "provider_capture_at": "2026-05-06T20:00:00Z",
+    "model_snapshot": "opus-4.7-20260505",
+    "model_version": "4.7",
+    "benchmark_subset": "tb2-wide",
+    "benchmark_subset_version": "v2",
+    "benchmark_runner_version": "terminalbench-2026.05.06"
+  }
+}
diff --git a/go.mod b/go.mod
index d99ae64..1ae3010 100644
--- a/go.mod
+++ b/go.mod
@@ -27,6 +27,7 @@ require (
 	github.com/google/uuid v1.6.0 // indirect
 	github.com/inconshreveable/mousetrap v1.1.0 // indirect
 	github.com/pmezard/go-difflib v1.0.0 // indirect
+	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 // indirect
 	github.com/spf13/cobra v1.10.2 // indirect
 	github.com/spf13/pflag v1.0.9 // indirect
 	github.com/tidwall/gjson v1.18.0 // indirect
diff --git a/go.sum b/go.sum
index f08d2e2..211b6b5 100644
--- a/go.sum
+++ b/go.sum
@@ -33,6 +33,8 @@ github.com/pmezard/go-difflib v1.0.0/go.mod h1:iKH77koFhYxTK1pcRnkKkqfTogsbg7gZN
 github.com/rogpeppe/go-internal v1.14.1 h1:UQB4HGPB6osV0SQTLymcB4TgvyWu6ZyliaW0tI/otEQ=
 github.com/rogpeppe/go-internal v1.14.1/go.mod h1:MaRKkUm5W0goXpeCfT7UZI6fk/L7L7so1lCWt35ZSgc=
 github.com/russross/blackfriday/v2 v2.1.0/go.mod h1:+Rmxgy9KzJVeS9/2gXHxylqXiyQDYRxCVz55jmeOWTM=
+github.com/santhosh-tekuri/jsonschema/v5 v5.3.1 h1:lZUw3E0/J3roVtGQ+SCrUrg3ON6NgVqpn3+iol9aGu4=
+github.com/santhosh-tekuri/jsonschema/v5 v5.3.1/go.mod h1:uToXkOrWAZ6/Oc07xWQrPOhJotwFIyu2bBVN41fcDUY=
 github.com/spf13/cobra v1.10.2 h1:DMTTonx5m65Ic0GOoRY2c16WCbHxOOw6xxezuLaBpcU=
 github.com/spf13/cobra v1.10.2/go.mod h1:7C1pvHqHw5A4vrJfjNwvOdzYu0Gml16OCs2GRiTUUS4=
 github.com/spf13/pflag v1.0.9 h1:9exaQaMOCwffKiiiYk6/BndUBv+iRViNW+4lEMi0PvY=
diff --git a/scripts/benchmark/benchmark-evidence.schema.json b/scripts/benchmark/benchmark-evidence.schema.json
index a973987..b841050 100644
--- a/scripts/benchmark/benchmark-evidence.schema.json
+++ b/scripts/benchmark/benchmark-evidence.schema.json
@@ -72,9 +72,18 @@
         "dataset_commit": {
           "type": "string"
         },
+        "subset_id": {
+          "type": "string"
+        },
+        "subset_version": {
+          "type": "string"
+        },
         "scorer": {
           "type": "string"
         },
+        "scorer_version": {
+          "type": "string"
+        },
         "higher_is_better": {
           "type": "boolean",
           "default": true
@@ -133,6 +142,15 @@
         "subset": {
           "type": "string"
         },
+        "subset_id": {
+          "type": "string"
+        },
+        "subset_version": {
+          "type": "string"
+        },
+        "denominator_rule": {
+          "type": "string"
+        },
         "split": {
           "type": "string"
         },
@@ -185,10 +203,73 @@
         }
       }
     },
+    "invalid_class": {
+      "type": "string"
+    },
+    "coverage": {
+      "type": "object",
+      "additionalProperties": false,
+      "properties": {
+        "formula_version": {
+          "type": "string"
+        },
+        "evidence_window": {
+          "type": "string"
+        },
+        "included_benchmarks": {
+          "type": "array",
+          "items": { "type": "string" }
+        },
+        "included_subsets": {
+          "type": "array",
+          "items": { "type": "string" }
+        },
+        "denominator_rule": {
+          "type": "string"
+        },
+        "coverage_note": {
+          "type": "string"
+        },
+        "confidence_note": {
+          "type": "string"
+        }
+      }
+    },
+    "denominator": {
+      "type": "object",
+      "additionalProperties": false,
+      "properties": {
+        "included": {
+          "type": "boolean"
+        },
+        "policy": {
+          "type": "string"
+        },
+        "reason": {
+          "type": "string"
+        },
+        "excluded_classes": {
+          "type": "array",
+          "items": { "type": "string" }
+        },
+        "excluded_tasks": {
+          "type": "array",
+          "items": { "type": "string" }
+        },
+        "included_count": {
+          "type": "integer",
+          "minimum": 0
+        },
+        "excluded_count": {
+          "type": "integer",
+          "minimum": 0
+        }
+      }
+    },
     "components": {
       "type": "object",
       "additionalProperties": {
-        "type": ["number", "integer", "string", "boolean", "null"]
+        "type": ["number", "integer", "string", "boolean", "null", "array", "object"]
       }
     },
     "cost": {
@@ -211,19 +292,162 @@
         "tool_calls": { "type": "integer", "minimum": 0 },
         "tool_call_errors": { "type": "integer", "minimum": 0 },
         "exit_code": { "type": "integer" },
-        "outcome": { "type": "string" }
+        "outcome": { "type": "string" },
+        "deployment_class": {
+          "type": "string",
+          "enum": ["managed_frontier", "managed_non_frontier", "local", "self_hosted"]
+        },
+        "quantization": {
+          "type": "string"
+        },
+        "local_runtime_name": {
+          "type": "string"
+        },
+        "local_runtime_version": {
+          "type": "string"
+        },
+        "server_name": {
+          "type": "string"
+        },
+        "server_version": {
+          "type": "string"
+        },
+        "context_limit": {
+          "type": "integer",
+          "minimum": 0
+        },
+        "hardware_class": {
+          "type": "string"
+        },
+        "hardware_accelerator": {
+          "type": "string"
+        },
+        "hardware_accelerator_backend": {
+          "type": "string"
+        },
+        "hardware_memory_gb": {
+          "type": "number",
+          "minimum": 0
+        },
+        "hardware_os": {
+          "type": "string"
+        },
+        "hardware_arch": {
+          "type": "string"
+        },
+        "sampling_temperature": {
+          "type": "number"
+        },
+        "sampling_top_p": {
+          "type": "number"
+        },
+        "sampling_top_k": {
+          "type": "integer",
+          "minimum": 0
+        },
+        "sampling_min_p": {
+          "type": "number"
+        },
+        "sampling_reasoning": {
+          "type": "string"
+        },
+        "utilization_active_requests": {
+          "type": "integer",
+          "minimum": 0
+        },
+        "utilization_queue_depth": {
+          "type": "integer",
+          "minimum": 0
+        },
+        "utilization_memory_pressure": {
+          "type": "number",
+          "minimum": 0
+        },
+        "utilization_tokens_per_second": {
+          "type": "number",
+          "minimum": 0
+        }
       }
     },
     "environment": {
       "type": "object",
       "additionalProperties": {
-        "type": ["number", "integer", "string", "boolean", "null"]
+        "type": ["number", "integer", "string", "boolean", "null", "array", "object"]
       }
     },
     "provenance": {
       "type": "object",
       "additionalProperties": {
-        "type": ["number", "integer", "string", "boolean", "null"]
+        "type": ["number", "integer", "string", "boolean", "null", "array", "object"]
+      },
+      "properties": {
+        "fizeau_version": {
+          "type": "string"
+        },
+        "fizeau_git_commit": {
+          "type": "string"
+        },
+        "harness_wrapper_name": {
+          "type": "string"
+        },
+        "harness_wrapper_version": {
+          "type": "string"
+        },
+        "harness_cli_version": {
+          "type": "string"
+        },
+        "harness_runtime_version": {
+          "type": "string"
+        },
+        "provider_endpoint": {
+          "type": "string"
+        },
+        "provider_surface": {
+          "type": "string"
+        },
+        "provider_version": {
+          "type": "string"
+        },
+        "provider_capture_at": {
+          "type": "string",
+          "format": "date-time"
+        },
+        "model_snapshot": {
+          "type": "string"
+        },
+        "model_version": {
+          "type": "string"
+        },
+        "model_artifact_path": {
+          "type": "string"
+        },
+        "model_artifact_sha256": {
+          "type": "string",
+          "pattern": "^[a-f0-9]{64}$"
+        },
+        "session_log_path": {
+          "type": "string"
+        },
+        "session_log_sha256": {
+          "type": "string",
+          "pattern": "^[a-f0-9]{64}$"
+        },
+        "trajectory_path": {
+          "type": "string"
+        },
+        "trajectory_sha256": {
+          "type": "string",
+          "pattern": "^[a-f0-9]{64}$"
+        },
+        "benchmark_subset": {
+          "type": "string"
+        },
+        "benchmark_subset_version": {
+          "type": "string"
+        },
+        "benchmark_runner_version": {
+          "type": "string"
+        }
       }
     }
   }
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
