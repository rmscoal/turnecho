import io
import json
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from turnecho import hook


class StopHookTests(unittest.TestCase):
    def test_worker_process_is_detached_from_hook_streams(self) -> None:
        with TemporaryDirectory() as directory:
            log_path = str(Path(directory) / "worker.log")
            with (
                patch.object(
                    hook, "TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX", log_path
                ),
                patch.object(hook.subprocess, "Popen") as popen,
            ):
                hook.spawn_background_worker()

        command = popen.call_args.args[0]
        options = popen.call_args.kwargs
        self.assertEqual(command, [sys.executable, "-m", "turnecho.worker"])
        self.assertNotIn("env", options)
        self.assertIs(options["stdin"], hook.subprocess.DEVNULL)
        self.assertIs(options["stderr"], hook.subprocess.STDOUT)
        self.assertTrue(options["start_new_session"])
        self.assertTrue(options["close_fds"])

    def run_hook(self) -> tuple[str, str]:
        input_message = {
            "hook_event_name": "Stop",
            "session_id": "session-1",
            "turn_id": "turn-1",
            "last_assistant_message": "Finished the requested change.",
            "stop_hook_active": False,
        }
        stdout = io.StringIO()
        stderr = io.StringIO()

        with (
            patch.object(sys, "stdin", io.StringIO(json.dumps(input_message))),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            hook.main()

        return stdout.getvalue(), stderr.getvalue()

    def test_new_job_spawns_worker_and_returns_empty_output(self) -> None:
        with (
            patch.object(hook, "insert_job_db", return_value=True),
            patch.object(hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook()

        spawn_worker.assert_called_once_with()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")

    def test_duplicate_job_still_spawns_recovery_worker(self) -> None:
        with (
            patch.object(hook, "insert_job_db", return_value=False),
            patch.object(hook, "spawn_background_worker") as spawn_worker,
        ):
            stdout, stderr = self.run_hook()

        spawn_worker.assert_called_once_with()
        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")


if __name__ == "__main__":
    unittest.main()
