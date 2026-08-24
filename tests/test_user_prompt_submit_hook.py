import json
import os
import subprocess
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

PROJECT_ROOT = Path(__file__).resolve().parents[1]


class UserPromptSubmitHookTests(unittest.TestCase):
    def run_hook(self, payload: object) -> subprocess.CompletedProcess[str]:
        source_path = str(PROJECT_ROOT / "src")
        python_path = os.environ.get("PYTHONPATH")
        if python_path:
            source_path = os.pathsep.join((source_path, python_path))

        with TemporaryDirectory() as home:
            environment = os.environ.copy()
            environment["HOME"] = home
            environment["PYTHONPATH"] = source_path
            return subprocess.run(
                [sys.executable, "-m", "turnecho.prompt_hook"],
                input=json.dumps(payload),
                capture_output=True,
                check=True,
                env=environment,
                text=True,
            )

    def run_hook_with_config(
        self,
        payload: object,
        config_content: str,
    ) -> subprocess.CompletedProcess[str]:
        source_path = str(PROJECT_ROOT / "src")
        with TemporaryDirectory() as home:
            config_path = Path(home) / ".config" / "turnecho" / "config.json"
            config_path.parent.mkdir(parents=True)
            config_path.write_text(config_content, encoding="utf-8")
            environment = os.environ.copy()
            environment["HOME"] = home
            environment["PYTHONPATH"] = source_path
            return subprocess.run(
                [sys.executable, "-m", "turnecho.prompt_hook"],
                input=json.dumps(payload),
                capture_output=True,
                check=True,
                env=environment,
                text=True,
            )

    def test_prompt_hook_returns_context_without_project_runtime(self) -> None:
        result = self.run_hook(
            {
                "hook_event_name": "UserPromptSubmit",
                "session_id": "session-1",
                "turn_id": "turn-1",
                "prompt": "Implement the requested change.",
            }
        )

        output = json.loads(result.stdout)
        self.assertEqual(
            output["hookSpecificOutput"]["hookEventName"], "UserPromptSubmit"
        )
        self.assertIn(
            "turnecho-summary:v1",
            output["hookSpecificOutput"]["additionalContext"],
        )
        self.assertEqual(result.stderr, "")

    def test_prompt_hook_ignores_other_events(self) -> None:
        result = self.run_hook({"hook_event_name": "Stop"})

        self.assertEqual(result.stdout, "{}\n")
        self.assertEqual(result.stderr, "")

    def test_disabled_configuration_returns_no_context(self) -> None:
        result = self.run_hook_with_config(
            {"hook_event_name": "UserPromptSubmit"},
            json.dumps(
                {
                    "schema_version": 1,
                    "enabled": False,
                    "model": "mini",
                    "voice": "Hugo",
                    "speed": 1.0,
                }
            ),
        )

        self.assertEqual(result.stdout, "{}\n")
        self.assertEqual(result.stderr, "")

    def test_invalid_configuration_returns_no_context_and_reports_error(self) -> None:
        result = self.run_hook_with_config(
            {"hook_event_name": "UserPromptSubmit"},
            "not json",
        )

        self.assertEqual(result.stdout, "{}\n")
        self.assertIn("Cannot read TurnEcho configuration", result.stderr)

    def test_registered_prompt_hook_uses_uv_without_syncing_dependencies(self) -> None:
        hooks = json.loads(
            (PROJECT_ROOT / "hooks" / "hooks.json").read_text(encoding="utf-8")
        )
        command = hooks["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"]

        self.assertEqual(
            command,
            'PYTHONPATH="$PLUGIN_ROOT/src" uv run --project "$PLUGIN_ROOT" '
            "--no-dev --no-sync python -m turnecho.prompt_hook",
        )
        self.assertIn("--no-sync", command)


if __name__ == "__main__":
    unittest.main()
