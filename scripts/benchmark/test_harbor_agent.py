from __future__ import annotations

import asyncio
import json
import os
import subprocess
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import AsyncMock, patch


def _install_harbor_stubs() -> None:
    if "harbor.agents.installed.base" in sys.modules:
        return

    harbor = types.ModuleType("harbor")
    harbor.__path__ = []  # type: ignore[attr-defined]

    agents = types.ModuleType("harbor.agents")
    agents.__path__ = []  # type: ignore[attr-defined]
    installed = types.ModuleType("harbor.agents.installed")
    installed.__path__ = []  # type: ignore[attr-defined]
    base = types.ModuleType("harbor.agents.installed.base")

    environments = types.ModuleType("harbor.environments")
    environments.__path__ = []  # type: ignore[attr-defined]
    env_base = types.ModuleType("harbor.environments.base")

    models = types.ModuleType("harbor.models")
    models.__path__ = []  # type: ignore[attr-defined]
    agent_mod = types.ModuleType("harbor.models.agent")
    agent_mod.__path__ = []  # type: ignore[attr-defined]
    context_mod = types.ModuleType("harbor.models.agent.context")

    class BaseInstalledAgent:  # pragma: no cover - exercised through FizeauAgent
        def __init__(self, *args: object, **kwargs: object) -> None:
            del args
            self._version = str(kwargs.get("version") or "")
            self.logs_dir = Path(tempfile.mkdtemp(prefix="fizeau-agent-logs-"))
            self.model_name = ""

        def version(self) -> str:
            return self._version

        async def exec_as_root(self, *args: object, **kwargs: object) -> None:
            del args, kwargs

        async def exec_as_agent(self, *args: object, **kwargs: object) -> None:
            del args, kwargs

    def with_prompt_template(fn):
        return fn

    class BaseEnvironment:  # pragma: no cover - marker stub
        pass

    class AgentContext:  # pragma: no cover - simple mutable container
        def __init__(self) -> None:
            self.n_input_tokens = None
            self.n_output_tokens = None
            self.cost_usd = None

    base.BaseInstalledAgent = BaseInstalledAgent
    base.with_prompt_template = with_prompt_template
    env_base.BaseEnvironment = BaseEnvironment
    context_mod.AgentContext = AgentContext

    harbor.agents = agents
    agents.installed = installed
    installed.base = base
    harbor.environments = environments
    environments.base = env_base
    harbor.models = models
    models.agent = agent_mod
    agent_mod.context = context_mod

    sys.modules["harbor"] = harbor
    sys.modules["harbor.agents"] = agents
    sys.modules["harbor.agents.installed"] = installed
    sys.modules["harbor.agents.installed.base"] = base
    sys.modules["harbor.environments"] = environments
    sys.modules["harbor.environments.base"] = env_base
    sys.modules["harbor.models"] = models
    sys.modules["harbor.models.agent"] = agent_mod
    sys.modules["harbor.models.agent.context"] = context_mod


_install_harbor_stubs()

from scripts.benchmark import harbor_agent

AgentContext = sys.modules["harbor.models.agent.context"].AgentContext


class FizeauAgentTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory(prefix="fizeau-agent-test-")
        self.addCleanup(self.tempdir.cleanup)
        self.agent = harbor_agent.FizeauAgent()
        self.agent.logs_dir = Path(self.tempdir.name)

    def test_run_maps_all_official_harness_pins_to_cli_flag(self) -> None:
        for harness in ("claude", "codex", "pi", "opencode"):
            with self.subTest(harness=harness), patch.dict(
                os.environ,
                {
                    "FIZEAU_HARNESS": harness,
                    "FIZEAU_PROVIDER": "openrouter",
                    "FIZEAU_MODEL": "qwen/qwen3.6-plus",
                    "FIZEAU_MODEL_REF": "qwen/qwen3.6-plus@latest",
                    "FIZEAU_REASONING": "high",
                },
                clear=False,
            ):
                agent = harbor_agent.FizeauAgent()
                agent.logs_dir = Path(self.tempdir.name)
                agent.exec_as_agent = AsyncMock(return_value=None)

                asyncio.run(agent.run("solve task", object(), AgentContext()))

                command = agent.exec_as_agent.await_args.kwargs["command"]
                self.assertIn('append_arg --harness "${FIZEAU_HARNESS:-}"', command)
                self.assertIn("--preset default", command)
                self.assertIn('append_arg --provider "${FIZEAU_PROVIDER:-}"', command)
                self.assertIn('append_arg --model "${FIZEAU_MODEL:-}"', command)
                self.assertIn('append_arg --model-ref "${FIZEAU_MODEL_REF:-}"', command)
                self.assertIn('append_arg --reasoning "${FIZEAU_REASONING:-}"', command)
                self.assertIn("target.env", command)
                self.assertNotIn("harbor_adapters/claude.py", command)
                self.assertNotIn("harbor_adapters/codex.py", command)
                self.assertNotIn("harbor_adapters/pi.py", command)
                self.assertNotIn("harbor_adapters/opencode.py", command)

    def test_build_command_symlinks_jsonl_into_bind_mount(self) -> None:
        """Session JSONL must land in /logs/agent/sessions via symlink, not via post-run cp.

        The old cp loop raced with Harbor's container teardown and silently dropped
        JSONL files when the agent ran as root (causing turns:0 / invalid_setup
        misclassification). The symlink eliminates the race.
        """
        with patch.dict(
            os.environ,
            {"FIZEAU_PROVIDER": "vllm", "FIZEAU_BASE_URL": "http://sindri:8020/v1"},
            clear=False,
        ):
            self.agent.exec_as_agent = AsyncMock(return_value=None)
            asyncio.run(self.agent.run("solve task", object(), AgentContext()))

        command = self.agent.exec_as_agent.await_args.kwargs["command"]
        # Pre-fiz: symlink both potential session dirs into the bind mount.
        self.assertIn('ln -s /logs/agent/sessions "$work_dir/.fizeau/sessions"', command)
        self.assertIn('ln -s /logs/agent/sessions "$HOME/.fizeau/sessions"', command)
        # No more racy post-run cp loop.
        self.assertNotIn("for session_root in", command)
        self.assertNotIn("cp -f", command)
        # Diagnostic warning still fires if no JSONL appears.
        self.assertIn("warning: no fiz session JSONL found after run", command)

    def test_build_command_traps_term_and_kills_fiz_process_tree(self) -> None:
        """Harbor cancellation must not leave fiz / wrapped harness orphaned.

        The generated bash must trap TERM/INT/EXIT, retain the fiz child PID
        (so a bare-pipeline shell kill does not lose it), preserve tee
        logging, wait on the child via PID (not just pipeline status), and
        SIGKILL the process tree if SIGTERM grace expires. This guards
        against the OrbStack-OOM-2026-05-16 incident where ~610 orphaned
        gemini processes consumed ~70 GiB after Harbor AgentTimeout.
        """
        with patch.dict(
            os.environ,
            {
                "FIZEAU_HARNESS": "gemini",
                "FIZEAU_PROVIDER": "openrouter",
                "FIZEAU_MODEL": "qwen/qwen3.6-plus",
            },
            clear=False,
        ):
            self.agent.exec_as_agent = AsyncMock(return_value=None)
            asyncio.run(self.agent.run("solve task", object(), AgentContext()))

        command = self.agent.exec_as_agent.await_args.kwargs["command"]

        # Trap registered for TERM/INT/EXIT — the three signals Harbor or a
        # parent runner can deliver on cancellation.
        self.assertIn("trap fiz_cleanup TERM INT EXIT", command)

        # fiz runs in the background and its PID is retained. A bare
        # foreground pipeline (`fiz ... | tee`) would let the shell die on
        # SIGTERM with no way to address the surviving fiz child.
        self.assertIn("fiz_pid=$!", command)
        self.assertNotRegex(
            command,
            r'"\$\{cmd\[@\]\}"\s*2>&1\s*\|\s*stdbuf',
            "fiz must not run as a foreground pipeline that loses the child PID on shell kill",
        )

        # Tee logging is preserved via process substitution so the fiz output
        # still lands in /logs/agent/fiz.txt while the wrapper keeps fiz_pid.
        self.assertIn("stdbuf -oL tee /logs/agent/fiz.txt", command)

        # Wait by PID so we capture fiz's true exit status, not a pipeline's.
        self.assertIn('wait "$fiz_pid"', command)
        self.assertIn("fiz_rc=$?", command)

        # Cleanup signals the whole process group (kill -TERM -$fiz_pid) so
        # the wrapped harness CLI (gemini/codex/claude/pi) forked by fiz is
        # also terminated, then escalates to SIGKILL after a grace interval.
        self.assertIn('kill -TERM -"$fiz_pid"', command)
        self.assertIn('kill -KILL -"$fiz_pid"', command)

        # Job control is enabled so the background fiz lands in its own
        # process group (PGID == fiz_pid) — required for the group-kill above.
        self.assertIn("set -m", command)

    def test_run_preserves_provider_model_model_ref_and_reasoning_pins(self) -> None:
        with patch.dict(
            os.environ,
            {
                "FIZEAU_HARNESS": "codex",
                "FIZEAU_PROVIDER": "openrouter",
                "FIZEAU_MODEL": "qwen/qwen3.6-plus",
                "FIZEAU_MODEL_REF": "qwen/qwen3.6-plus@2026-05-06",
                "FIZEAU_REASONING": "medium",
            },
            clear=False,
        ):
            self.agent.exec_as_agent = AsyncMock(return_value=None)
            asyncio.run(self.agent.run("solve task", object(), AgentContext()))

        command = self.agent.exec_as_agent.await_args.kwargs["command"]
        self.assertIn('append_arg --provider "${FIZEAU_PROVIDER:-}"', command)
        self.assertIn('append_arg --model "${FIZEAU_MODEL:-}"', command)
        self.assertIn('append_arg --model-ref "${FIZEAU_MODEL_REF:-}"', command)
        self.assertIn('append_arg --reasoning "${FIZEAU_REASONING:-}"', command)

    def test_populate_context_post_run_records_target_metadata(self) -> None:
        sessions_dir = self.agent.logs_dir / "sessions"
        sessions_dir.mkdir(parents=True, exist_ok=True)
        (self.agent.logs_dir / "target.env").write_text(
            "\n".join(
                [
                    "FIZEAU_HARNESS=pi",
                    "FIZEAU_PROVIDER=openai-compat",
                    "FIZEAU_MODEL=qwen/qwen3.6-plus",
                    "FIZEAU_MODEL_REF=qwen/qwen3.6-plus@2026-05-06",
                    "FIZEAU_REASONING=low",
                    "FIZEAU_BASE_URL=https://openrouter.ai/api/v1",
                ]
            ),
            encoding="utf-8",
        )
        session_log = sessions_dir / "svc-123.jsonl"
        session_log.write_text(
            "\n".join(
                [
                    json.dumps(
                        {
                            "type": "session.start",
                            "timestamp": "2026-05-06T21:30:00Z",
                            "data": {
                                "model": "qwen/qwen3.6-plus",
                                "prompt": "solve task",
                            },
                        }
                    ),
                    json.dumps(
                        {
                            "type": "llm.response",
                            "timestamp": "2026-05-06T21:30:01Z",
                            "data": {
                                "model": "qwen/qwen3.6-plus",
                                "content": "done",
                                "usage": {"input": 12, "output": 5},
                                "cost_usd": 0.25,
                            },
                        }
                    ),
                ]
            ),
            encoding="utf-8",
        )

        with patch.dict(
            os.environ,
            {
                "FIZEAU_HARNESS": "pi",
                "FIZEAU_PROVIDER": "openai-compat",
                "FIZEAU_MODEL": "qwen/qwen3.6-plus",
                "FIZEAU_MODEL_REF": "qwen/qwen3.6-plus@2026-05-06",
                "FIZEAU_REASONING": "low",
                "FIZEAU_BASE_URL": "https://openrouter.ai/api/v1",
            },
            clear=False,
        ):
            context = AgentContext()
            self.agent.populate_context_post_run(context)

        trajectory = json.loads((self.agent.logs_dir / "trajectory.json").read_text(encoding="utf-8"))
        self.assertEqual(trajectory["target"]["requested"]["harness"], "pi")
        self.assertEqual(trajectory["target"]["requested"]["provider"], "openai-compat")
        self.assertEqual(trajectory["target"]["requested"]["model"], "qwen/qwen3.6-plus")
        self.assertEqual(trajectory["target"]["requested"]["model_ref"], "qwen/qwen3.6-plus@2026-05-06")
        self.assertEqual(trajectory["target"]["requested"]["reasoning"], "low")
        self.assertEqual(trajectory["target"]["resolved"]["provider"], "openrouter")
        self.assertEqual(trajectory["target"]["resolved"]["harness"], "pi")
        self.assertEqual(trajectory["target"]["resolved"]["model"], "qwen/qwen3.6-plus")
        self.assertEqual(trajectory["target"]["resolved"]["model_ref"], "qwen/qwen3.6-plus@2026-05-06")
        self.assertEqual(trajectory["target"]["resolved"]["reasoning"], "low")
        self.assertEqual(trajectory["agent"]["model_name"], "qwen/qwen3.6-plus")
        self.assertEqual(trajectory["final_metrics"]["total_prompt_tokens"], 12)
        self.assertEqual(trajectory["final_metrics"]["total_completion_tokens"], 5)
        self.assertEqual(trajectory["final_metrics"]["total_cost_usd"], 0.25)


class TestHarborAgentInstall(unittest.TestCase):
    """Verifies shell adapter install-spec harbor_plugin values match Python import paths."""

    EXPECTED_PLUGINS: dict[str, str | None] = {
        "fiz": "scripts.benchmark.harbor_agent:FizeauAgent",
        "claude": "scripts.benchmark.harbor_adapters.claude:ClaudeAgent",
        "codex": "scripts.benchmark.harbor_adapters.codex:CodexAgent",
        "opencode": "scripts.benchmark.harbor_adapters.opencode:OpencodeAgent",
        "pi": "scripts.benchmark.harbor_adapters.pi:PiAgent",
        "cost-probe": None,
        "noop": None,
        "dumb-script": None,
    }

    def _adapter_dir(self) -> Path:
        return Path(__file__).parent / "harness-adapters"

    def test_install_harbor_plugins_match_expected(self) -> None:
        for adapter_name, expected_plugin in self.EXPECTED_PLUGINS.items():
            with self.subTest(adapter=adapter_name):
                adapter_path = self._adapter_dir() / adapter_name
                result = subprocess.run(
                    [str(adapter_path), "install", "/tmp/fake-artifact"],
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(
                    result.returncode, 0, f"{adapter_name} install failed: {result.stderr}"
                )
                install_spec = json.loads(result.stdout)
                self.assertEqual(
                    install_spec.get("harbor_plugin"),
                    expected_plugin,
                    f"{adapter_name}: expected harbor_plugin={expected_plugin!r}",
                )


if __name__ == "__main__":
    unittest.main()
