import io
import json
import os
import subprocess
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from turnecho import stop_hook
from turnecho.config import ConfigError, TurnEchoConfig
from turnecho.constant import TURNECHO_SUMMARY_MAX_CHARS


class HookTests(unittest.TestCase):
    def test_worker_process_is_detached_from_hook_streams(self) -> None:
        with TemporaryDirectory() as directory:
            log_path = str(Path(directory) / "worker.log")
            with (
                patch.object(
                    stop_hook, "TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX", log_path
                ),
                patch.object(stop_hook.subprocess, "Popen") as popen,
            ):
                stop_hook.spawn_background_worker()

        command = popen.call_args.args[0]
        options = popen.call_args.kwargs
        self.assertEqual(command, [sys.executable, "-m", "turnecho.worker"])
        self.assertNotIn("env", options)
        self.assertIs(options["stdin"], stop_hook.subprocess.DEVNULL)
        self.assertIs(options["stderr"], stop_hook.subprocess.STDOUT)
        self.assertTrue(options["start_new_session"])
        self.assertTrue(options["close_fds"])

    def test_plugin_worker_uses_project_runtime_without_dev_dependencies(self) -> None:
        with TemporaryDirectory() as directory:
            log_path = str(Path(directory) / "worker.log")
            with (
                patch.object(
                    stop_hook, "TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX", log_path
                ),
                patch.dict(os.environ, {"PLUGIN_ROOT": "/cached/turnecho"}),
                patch.object(stop_hook.subprocess, "Popen") as popen,
            ):
                stop_hook.spawn_background_worker()

        command = popen.call_args.args[0]
        self.assertEqual(
            command,
            [
                "uv",
                "run",
                "--project",
                "/cached/turnecho",
                "--no-dev",
                "python",
                "-m",
                "turnecho.worker",
            ],
        )

    def run_hook(
        self,
        input_message: object,
        *,
        config: TurnEchoConfig = TurnEchoConfig(),
        config_error: ConfigError | None = None,
    ) -> tuple[str, str]:
        stdout = io.StringIO()
        stderr = io.StringIO()

        with (
            patch.object(sys, "stdin", io.StringIO(json.dumps(input_message))),
            patch.object(
                stop_hook,
                "load_config",
                return_value=config,
                side_effect=config_error,
            ),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            stop_hook.main()

        return stdout.getvalue(), stderr.getvalue()

    def stop_input_message(self, assistant_message: str) -> dict[str, object]:
        return {
            "hook_event_name": "Stop",
            "session_id": "session-1",
            "turn_id": "turn-1",
            "last_assistant_message": assistant_message,
            "stop_hook_active": False,
        }

    def test_new_job_queues_summary_and_spawns_worker(self) -> None:
        input_message = self.stop_input_message(
            "Finished the requested change.\n\n"
            "<!-- turnecho-summary:v1\n"
            "The requested change is complete and all tests passed.\n"
            "-->"
        )

        with (
            patch.object(stop_hook, "insert_job_db", return_value=True) as insert_job,
            patch.object(stop_hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook(input_message)

        insert_job.assert_called_once_with(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="The requested change is complete and all tests passed.",
            voice="Hugo",
            speed=1.0,
        )
        spawn_worker.assert_called_once_with()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_duplicate_job_still_spawns_recovery_worker(self) -> None:
        input_message = self.stop_input_message(
            "Finished the requested change.\n\n"
            "<!-- turnecho-summary:v1\n"
            "The requested change is complete.\n"
            "-->"
        )

        with (
            patch.object(stop_hook, "insert_job_db", return_value=False),
            patch.object(stop_hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook(input_message)

        spawn_worker.assert_called_once_with()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_stop_without_summary_does_not_queue_or_spawn_worker(self) -> None:
        input_message = self.stop_input_message("Finished the requested change.")

        with (
            patch.object(stop_hook, "insert_job_db") as insert_job,
            patch.object(stop_hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook(input_message)

        insert_job.assert_not_called()
        spawn_worker.assert_not_called()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_disabled_configuration_does_not_queue_or_spawn_worker(self) -> None:
        input_message = self.stop_input_message(
            "Visible response.\n\n<!-- turnecho-summary:v1\nSummary.\n-->"
        )
        with (
            patch.object(stop_hook, "insert_job_db") as insert_job,
            patch.object(stop_hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook(
                input_message,
                config=TurnEchoConfig(enabled=False),
            )

        insert_job.assert_not_called()
        spawn_worker.assert_not_called()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_invalid_configuration_fails_silent_with_stderr_diagnostic(self) -> None:
        input_message = self.stop_input_message(
            "Visible response.\n\n<!-- turnecho-summary:v1\nSummary.\n-->"
        )
        with (
            patch.object(stop_hook, "insert_job_db") as insert_job,
            patch.object(stop_hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook(
                input_message,
                config_error=ConfigError("invalid config"),
            )

        insert_job.assert_not_called()
        spawn_worker.assert_not_called()
        self.assertEqual(stdout, "{}\n")
        self.assertIn("invalid config", stderr)

    def test_non_object_input_returns_empty_output(self) -> None:
        stdout, stderr = self.run_hook([])

        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_registered_stop_hook_uses_uv_without_syncing_dependencies(self) -> None:
        hooks = json.loads(
            (Path(__file__).resolve().parents[1] / "hooks" / "hooks.json").read_text(
                encoding="utf-8"
            )
        )
        command = hooks["hooks"]["Stop"][0]["hooks"][0]["command"]

        self.assertEqual(
            command,
            'PYTHONPATH="$PLUGIN_ROOT/src" uv run --project "$PLUGIN_ROOT" '
            "--no-dev --no-sync python -m turnecho.stop_hook",
        )
        self.assertIn("--no-sync", command)

    def test_stop_hook_starts_without_site_packages(self) -> None:
        project_root = Path(__file__).resolve().parents[1]
        environment = os.environ.copy()
        environment["PYTHONPATH"] = str(project_root / "src")

        result = subprocess.run(
            [sys.executable, "-S", "-m", "turnecho.stop_hook"],
            input=json.dumps({"hook_event_name": "Stop"}),
            capture_output=True,
            check=True,
            env=environment,
            text=True,
        )

        self.assertEqual(result.stdout, "{}\n")
        self.assertEqual(result.stderr, "")

    def test_extract_summary_normalizes_multiline_text(self) -> None:
        message = (
            "Visible response.\r\n\r\n"
            "<!-- turnecho-summary:v1\r\n"
            "The change is complete.\r\nAll tests passed.\r\n"
            "-->\r\n"
        )

        summary = stop_hook.extract_turnecho_summary_from_agent_message(message)

        self.assertEqual(summary, "The change is complete. All tests passed.")

    def test_extract_summary_rejects_invalid_payloads(self) -> None:
        invalid_messages = {
            "missing marker": "Visible response.",
            "marker not at end": (
                "<!-- turnecho-summary:v1\nSummary.\n-->\nVisible response."
            ),
            "empty summary": "<!-- turnecho-summary:v1\n\n-->",
            "nested comment": ("<!-- turnecho-summary:v1\n<!-- nested -->\n-->"),
            "summary too long": (
                "<!-- turnecho-summary:v1\n"
                + "a" * (TURNECHO_SUMMARY_MAX_CHARS + 1)
                + "\n-->"
            ),
        }

        for name, message in invalid_messages.items():
            with self.subTest(name=name):
                self.assertIsNone(
                    stop_hook.extract_turnecho_summary_from_agent_message(message)
                )


if __name__ == "__main__":
    unittest.main()
