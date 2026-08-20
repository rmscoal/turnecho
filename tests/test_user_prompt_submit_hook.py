import json
import os
import subprocess
import sys
import unittest
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


class UserPromptSubmitHookTests(unittest.TestCase):
    def run_hook(self, payload: object) -> subprocess.CompletedProcess[str]:
        source_path = str(PROJECT_ROOT / "src")
        python_path = os.environ.get("PYTHONPATH")
        if python_path:
            source_path = os.pathsep.join((source_path, python_path))

        environment = os.environ.copy()
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

    def test_registered_prompt_hook_does_not_use_uv(self) -> None:
        hooks = json.loads(
            (PROJECT_ROOT / "hooks" / "hooks.json").read_text(encoding="utf-8")
        )
        command = hooks["hooks"]["UserPromptSubmit"][0]["hooks"][0]["command"]

        self.assertEqual(
            command,
            'PYTHONPATH="$PLUGIN_ROOT/src" python3 -m turnecho.prompt_hook',
        )
        self.assertNotIn("uv", command)


if __name__ == "__main__":
    unittest.main()
