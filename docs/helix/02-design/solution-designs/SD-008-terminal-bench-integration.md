---
ddx:
  id: SD-008
  bead: agent-a8bf4d0b
  created: 2026-04-08
  review:
    self_hash: 38357271f3c5c7c3da364497f94615698ec68c53a43e407c71ddf14c01b90b1a
    deps: {}
    reviewed_at: "2026-07-16T07:15:29Z"
---
# Solution Design: SD-008 — Terminal-Bench / Harbor Integration Path Audit

**Bead**: agent-a8bf4d0b (Audit Terminal-Bench and Harbor integration path for fiz)
**Type**: Spike / design note
**Status**: Complete — findings checked in, downstream beads should reference this document

---

## Summary

This document records findings from an audit of Terminal-Bench v2.0 and the Harbor
evaluation framework as the target benchmark harness for fiz. It answers
the five acceptance-criteria questions and recommends a concrete integration path.

---

## 1. Confirmed Supported Integration Path

**Terminal-Bench v2.0 uses Harbor as its official evaluation framework.**
The previous ad-hoc harness from v1.x is deprecated.

Harbor supports two agent types:

| Type | Description |
|------|-------------|
| `BaseInstalledAgent` | CLI agent installed inside the task container |
| `BaseDockerAgent` | Agent running in a sidecar container alongside the task env |

**Recommended path for fiz: `BaseInstalledAgent`.**

The `BaseInstalledAgent` adapter is a small Python class that Harbor uses to:
1. Install the agent binary into the task container (`install()` hook)
2. Run the agent with the task instruction (`run()` hook)
3. Collect the trajectory artifact after execution (`populate_context_post_run()` hook)

For fiz, the `run()` hook would invoke something like:
```bash
/usr/local/bin/fiz --json --preset cheap -p "<task_instruction>"
```

The Python adapter file lives in a Harbor-compatible agents repo and is referenced
by name in job configs:
```yaml
agents:
  - name: fiz
    version: "1.0"
```

**No modification to fiz's current CLI interface is required** to support
the installed-agent path. The `--json` + `--preset cheap` + `-p` invocation already
matches Harbor's expected agent invocation model.

A thin Python wrapper (~80 lines) implementing `BaseInstalledAgent` is the only
new artifact needed for integration. This is tracked in bead `agent-a3ce467a`.

---

## 2. Container / Runtime Constraints

- **Base OS**: Debian Linux (most Terminal-Bench task images derive from `debian:bookworm` or a toolchain image built on it)
- **Architecture**: amd64 — Harbor cloud runtimes (Daytona, Modal, E2B, Runloop) are x86_64. Local Docker evaluation is architecture-native.
- **Agent binary**: The Go binary must be compiled for `linux/amd64`. The static Go binary produced by `GOARCH=amd64 GOOOS=linux go build` drops directly into the container.
- **No internet access from task container**: Task environments are isolated. The agent's LLM calls must route to a provider reachable from inside the container (cloud API via HTTPS, or a forwarded local endpoint).
- **Filesystem**: The task container has a pre-populated workspace directory. File operations are scoped there. fiz's `--work-dir` flag controls the root.
- **Timeout**: Per-task time limits are enforced by Harbor (typically 10–30 minutes). The agent loop's `max_iterations` provides a secondary guard.
- **No persistent state between tasks**: Each trial is an isolated container. fiz session logs written to `/logs/agent/` are collected by Harbor.
- **User context**: Harbor runs agents as an `agent` user (configurable per task.toml). fiz does not require root.

---

## 3. Credential Injection

Harbor injects credentials as environment variables into the task container trial.
The Harbor job config specifies which env vars to pass through:

```python
# In the Python adapter
def get_env(self) -> dict:
    return {
        "ANTHROPIC_API_KEY": os.environ["ANTHROPIC_API_KEY"],
        "OPENROUTER_API_KEY": os.environ.get("OPENROUTER_API_KEY", ""),
    }
```

**fiz config approach for benchmark runs**: fiz currently reads
its provider configuration from a YAML config file (`~/.config/fizeau/config.yaml`
or `.fizeau/config.yaml` in the working dir). For benchmark use, the recommended
approach is to ship a minimal config file in the adapter's `install()` hook
that references an env-var-expanded API key:

```yaml
# Installed at ~/.config/fizeau/config.yaml inside the container
providers:
  benchmark:
    type: anthropic
    api_key: "${ANTHROPIC_API_KEY}"
    model: claude-haiku-4-5-20251001
default_provider: benchmark
```

fiz's config loader already supports `${ENV_VAR}` expansion (confirmed
in `config/config.go`). No changes needed for credential injection.

**Approved approach**: env-var injection via Harbor's `get_env()` adapter method,
with a bootstrapped config file written to the container during `install()`.

---

## 4. Result Artifact Location and Schema

Harbor expects result artifacts in specific container paths:

| Path | Content | Schema |
|------|---------|--------|
| `/logs/verifier/reward.txt` | Task reward: `1` (passed) or `0` (failed) | Single integer |
| `/logs/verifier/ctrf.json` | Test results (pytest CTRF format) | CTRF v1 JSON |
| `/logs/agent/trajectory.json` | Agent trajectory (ATIF v1.4) | ATIF JSON |
| `/logs/verifier/test_output.log` | Raw pytest output | Plain text |

**fiz's session log** (JSONL format in `.fizeau/sessions/`) is NOT
the trajectory format Harbor expects. The Python adapter's
`populate_context_post_run()` hook must convert fiz's JSONL session log
to ATIF v1.4 format and write it to `/logs/agent/trajectory.json`.

**ATIF v1.4 minimal schema** (sufficient for Terminal-Bench scoring):
```json
{
  "schema_version": "1.4",
  "session_id": "<uuid>",
  "agent": { "name": "fiz", "version": "1.0", "model_name": "<model>" },
  "steps": [
    {
      "step_id": 1,
      "timestamp": "<RFC3339>",
      "source": "user|agent|system",
      "message": "<content>",
      "tool_calls": [],
      "metrics": { "input_tokens": 0, "output_tokens": 0, "cost": 0 }
    }
  ],
  "final_metrics": { "total_input_tokens": 0, "total_output_tokens": 0, "total_cost": 0 }
}
```

fiz's `--json` output already includes `session_id`, model name, token
counts, and a full account of tool calls. The adapter conversion is
straightforward mapping.

**Reward determination**: Terminal-Bench tasks ship their own test scripts.
The Harbor verifier runs `pytest --ctrf /logs/verifier/ctrf.json tests/test_outputs.py`
against the modified workspace after the agent exits. fiz does not need
to produce the reward — it just needs to complete the task and exit cleanly.
A non-zero exit code from fiz is treated as a trial failure.

---

## 5. Recommended Smoke-Run Command Path

**Local Docker smoke run** (before cloud evaluation):

```bash
# Step 1: Build fiz for Linux amd64
GOOS=linux GOARCH=amd64 go build -o dist/fiz-linux-amd64 ./cmd/fiz

# Step 2: Install Harbor and Terminal-Bench dataset
pip install harbor-framework
harbor dataset pull terminal-bench/terminal-bench-2

# Step 3: Register the fiz adapter
# (adapter lives at scripts/benchmark/harbor_agent.py — tracked in agent-a3ce467a)

# Step 4: Smoke run against a single task
harbor run \
  --dataset terminal-bench/terminal-bench-2 \
  --agent fiz \
  --task-filter "hello-world" \
  --runtime docker

# Step 5: Check results
cat ~/.harbor/jobs/<job-id>/trials/*/verifier/reward.txt
```

**Verification that a run is valid**: A completed run produces:
- `reward.txt` containing `1` or `0`
- `trajectory.json` with at least one step
- `trial_result.json` with `status: passed|failed|timeout`

---

## Key Findings and Decisions

| Question | Finding | Decision |
|----------|---------|----------|
| Integration type | `BaseInstalledAgent` is the right path | Use installed-agent adapter |
| fiz interface changes needed | None — existing CLI flags sufficient | No changes for basic integration |
| Credential injection | Harbor env-var passthrough + bootstrapped config file | Env-var expansion in config (already supported) |
| Trajectory format | fiz JSONL != ATIF v1.4; conversion needed | Adapter handles conversion in `populate_context_post_run()` |
| Binary portability | Static Go binary for `linux/amd64` | `GOOS=linux GOARCH=amd64` in build step |
| Smoke run | `harbor run --dataset terminal-bench/terminal-bench-2 --agent fiz -n 1` | Defined in scripts/benchmark/ |

---

## 6. Multi-Harness Extension

Sections 1–5 cover the fiz integration via Harbor's `BaseInstalledAgent`.
This section extends the integration model to **non-fiz harnesses**
(third-party CLI agents we want to benchmark side-by-side, e.g. `pi`, Claude
Code, Codex, etc.) so that comparisons run on the same Terminal-Bench task set
under the same scoring rules.

The governing constraint, derived from §2: **the agent runs inside the task
container with no host-side fallback**. Harbor's isolation model is what makes
trial results comparable; an agent that runs on the host has different filesystem
access, different network egress, and different timeout semantics, and its
results are not comparable to in-container runs. See SD-010 for the broader
multi-harness benchmarking plan that this section feeds into.

### 6.1 In-container installability checklist

Before a third-party harness is added to the benchmark matrix, it must clear
every item on this checklist. Items (a)–(f) are hard requirements; an agent
that cannot satisfy any one of them is dropped per §6.4.

- [ ] **(a) Linux/amd64 binary or installable package.** A static binary, a
  `pip install`-able wheel, an `npm install -g`-able package, or a `curl | sh`
  installer that produces a working CLI on Debian bookworm amd64.
- [ ] **(b) Non-interactive invocation.** The agent accepts a task instruction
  via argv, stdin, or a file path and runs to completion without a TTY,
  prompts, or human-in-the-loop confirmations.
- [ ] **(c) Deterministic exit.** Exits 0 on task completion (regardless of
  task pass/fail, since Harbor's verifier scores the workspace, not the agent
  exit code) and non-zero only on agent-internal failure.
- [ ] **(d) Credential injection via environment variables.** Reads provider
  API keys from env vars set by Harbor's `get_env()` (see §3); does not
  require interactive login or browser-based OAuth.
- [ ] **(e) Bounded runtime.** Honors a wall-clock or step budget passed via
  flag/env, so the per-task timeout in §2 is enforceable from the agent side
  as well as from Harbor's container kill.
- [ ] **(f) Trajectory output we can map to ATIF v1.4.** Either emits a
  structured log (JSON/JSONL) with at minimum step source, message, tool
  calls, and token counts, or writes a transcript the adapter can parse. See
  §4 for the ATIF target schema.

If a harness fails (a)–(c), it cannot be benchmarked and is dropped (§6.4).
If it fails (d)–(f), the adapter must work around the gap (e.g. write a
config file in `install()`, wrap the agent in a timeout, synthesize a minimal
trajectory from logs) — but the gap is recorded in the adapter's docstring.

### 6.2 Per-harness adapter file location

Each harness gets its own Python adapter file under:

```
scripts/benchmark/harness_adapters/
  fiz.py        # the BaseInstalledAgent adapter from §1 / agent-a3ce467a
  pi.py               # adapter for the `pi` CLI
  claude_code.py      # adapter for Claude Code CLI
  codex.py            # adapter for Codex CLI
  __init__.py
```

Each adapter file:

1. Defines a single `BaseInstalledAgent` subclass named after the harness.
2. Implements `install()`, `run()`, `get_env()`, and
   `populate_context_post_run()` per Harbor's adapter contract.
3. Carries a module-level docstring covering: which harness version was
   tested, which §6.1 checklist items required workarounds, and which
   provider(s) it routes through.
4. Is registered in a single `harness_adapters/registry.yaml` that maps
   `--agent <name>` on the Harbor CLI to the adapter class.

Keeping adapters in a single directory (as opposed to one file per harness
elsewhere in the tree) makes the multi-harness sweep script trivially
discoverable and lets the registry double as the canonical "what do we
benchmark" list.

### 6.3 Egress requirements for provider API calls

Per §2, the task container has no general internet access. Harbors enforces
this with a default-deny network policy and explicit egress allowlists per
trial. Any harness that calls a provider API from inside the container must
have its provider endpoint(s) added to the trial's egress allowlist.

For each adapter, the following must be specified — typically as an
`EGRESS_ALLOWLIST: list[str]` class attribute the sweep script reads when
constructing the Harbor job config:

- **Provider hostnames** that must be reachable (e.g. `api.anthropic.com`,
  `api.openai.com`, `openrouter.ai`).
- **Auxiliary hostnames** the harness reaches at startup (telemetry,
  package registries hit at install time, model-list endpoints).
- **Port and protocol** — Harbor's default policy permits `:443` HTTPS;
  anything else (e.g. WebSocket on a non-standard port) must be explicitly
  declared and reviewed.

If a harness reaches out to a hostname not in its declared allowlist, the
trial fails with a network error rather than silently routing through an
unintended path. This is the desired behavior — undeclared egress is a
benchmark integrity issue.

For harnesses that call back to a developer-controlled endpoint (e.g. a
forwarded local LLM, a custom router), the endpoint must be exposed via
Harbor's per-trial port forwarding and added to the allowlist as a
loopback-style entry. Routing benchmark traffic through host-side network
namespaces that bypass the container's policy is **not** permitted, since it
defeats the isolation guarantee that makes results comparable.

### 6.4 Drop rule

If a harness cannot be installed and run inside the task container per §6.1,
it is **documented and excluded** from the benchmark matrix. It is **not**
run host-side as a fallback.

The mechanics:

1. The harness's intended adapter file under `harness_adapters/` is replaced
   (or never created) with a short stub or `EXCLUDED.md` entry that records:
   the harness name and version evaluated, which §6.1 checklist item(s) it
   failed, the date of the evaluation, and a one-line rationale.
2. The harness is omitted from `registry.yaml` so the sweep script cannot
   accidentally invoke it.
3. SD-010's harness comparison table notes the exclusion and the reason, so
   readers understand which CLIs were considered and why some are absent.

The rationale: a host-side run produces a different number on a different
substrate. Mixing host-side and in-container numbers in the same comparison
is worse than excluding a harness entirely, because it gives the appearance
of a fair comparison while violating the constraint that makes the
comparison meaningful (§2 isolation). When a harness becomes
in-container-installable later (vendor ships a Linux binary, exposes a
non-interactive mode, etc.), its `EXCLUDED.md` is converted into a real
adapter and it rejoins the matrix.

---

## Downstream References

Beads that depend on this audit:

- `agent-a3ce467a` — Implement the installed-agent Python adapter and smoke-run workflow
- `agent-82042311` — Specify benchmark mode and evaluation plan (consumes §1, §3, §4)
- `agent-1192db7b` — Capture baseline (consumes §5 for run methodology)
- `agent-5f35fdeb` — Benchmark-mode preset (no new CLI changes needed per §1)
