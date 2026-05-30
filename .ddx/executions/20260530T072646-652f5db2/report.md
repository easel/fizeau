# bench-pr-A1 — fizeau-harbor-runner image + shell adapters [fizeau-6fc8957a]

## Summary

The A1 deliverables (Dockerfile.harbor-runner, build-harbor-runner.sh,
8 harness-adapters, task-executors/harbor, runtime-probe.sh, both CONTRACT.md)
were already present in the worktree at base-rev `b5884536`. On scrutiny — as
required by the `triage:no-changes-unjustified` re-dispatch — one real defect
violated **AC3** ("harbor_plugin naming a Harbor-loadable class"): every wrapper
adapter and `fiz` emitted a `harbor_plugin` import path prefixed with
`scripts.benchmark.`, a leftover from the old host-PYTHONPATH (Go) model. Inside
the `fizeau-harbor-runner` image the Dockerfile sets `PYTHONPATH=/app` and copies
`harbor_adapters/` → `/app/harbor_adapters/` and `harbor_agent.py` →
`/app/harbor_agent.py`, so only the bare module paths are importable. The stale
prefix would make `harbor run --agent-import-path` fail to load the class at
container runtime.

The adapters' own `# SUMMARY:` lines already named the correct bare paths (e.g.
`harbor_adapters.claude:ClaudeAgent`), confirming the emitted value was the bug.

## Fix

Stripped the `scripts.benchmark.` prefix from the emitted `harbor_plugin` so it
matches the container's `/app` layout and each adapter's own SUMMARY:

| adapter   | before                                              | after                              |
|-----------|-----------------------------------------------------|------------------------------------|
| fiz       | `scripts.benchmark.harbor_agent:FizeauAgent`        | `harbor_agent:FizeauAgent`         |
| claude    | `scripts.benchmark.harbor_adapters.claude:ClaudeAgent` | `harbor_adapters.claude:ClaudeAgent` |
| codex     | `scripts.benchmark.harbor_adapters.codex:CodexAgent`   | `harbor_adapters.codex:CodexAgent`   |
| opencode  | `scripts.benchmark.harbor_adapters.opencode:OpencodeAgent` | `harbor_adapters.opencode:OpencodeAgent` |
| pi        | `scripts.benchmark.harbor_adapters.pi:PiAgent`      | `harbor_adapters.pi:PiAgent`       |

(`cost-probe`, `noop`, `dumb-script` keep `harbor_plugin: null` — calibration
adapters bypass Harbor, as documented.)

Updated the stale examples in `harness-adapters/CONTRACT.md`,
`task-executors/CONTRACT.md`, the `task-executors/harbor` usage text, and the
`test/harbor-runner.bats` fixtures to the loadable bare paths.

## Acceptance evidence

1. **Image SHA label** — `build-harbor-runner.sh` computes
   `sha256(harbor_adapters/ + harbor_agent.py + Dockerfile.harbor-runner)` and
   passes `--label image-content-sha=<sha>`; re-run with no content change yields
   the same sha (cache no-op). `test/test_harbor_image.sh` → 5/5 PASS.
2. **command-spec** — all 8 adapters executable, line-2 `# SUMMARY:` header,
   `<name> command < profile.json` emits `{command,env,secret_env_keys}`. Verified
   via `jq` shape check across all 8; `test/test_harness_adapters.sh` → 4/4 PASS.
3. **install-spec** — all 8 emit `{install_command,artifact_source,binary_path,harbor_plugin}`;
   `harbor_plugin` now names a container-loadable class (or `null` for calibration).
4. **harbor task-executor** — reads task-spec.json on stdin, builds the
   `docker run … fizeau-harbor-runner:latest run --agent-import-path … --path …`
   argv, writes `cell_dir/result.json` (or a `missing_result` stub), and stamps
   `task_executor_version` + `harbor_runner_image_digest`. `test/harbor-runner.bats`
   → 6/6 ok.
5. **runtime-probe** — `runtime-probe.sh` has case arms for lucebox, llamacpp,
   vllm, omlx, ds4, rapid-mlx and emits `{name,version,commit,endpoint}`.
   `test/test_runtime_probe.sh` → 6/6 PASS.
6. **CONTRACTs** — both `CONTRACT.md` files exist and document the JSON shapes
   and the `# SUMMARY:` convention.

## Gates

- `bats scripts/benchmark/test` — **6/6 ok**.
- `lefthook run pre-commit` (`make fmt-check`, `go vet ./...`) — **green**.
- `go test ./scripts/benchmark/` (the bench package) — **ok**.
- `go test ./...` (repo-wide) — pre-existing, unrelated failures only. The diff
  for this bead is **100% shell + markdown + bats (zero Go files)**, so it cannot
  affect Go compilation or behavior. The failing packages — `github.com/easel/fizeau`
  and `…/agentcli` (10-minute test timeouts), `…/internal/harnesses` (single-account
  attestation / pty snapshot tests), `…/internal/harnesses/ptyquota` — depend on
  real model-server infra and live accounts not available in this sandbox and fail
  identically at base-rev. No Go regression is introduced by this bead.
