import hashlib
import io
import json
import os
import sys
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from turnecho import prompt_hook, stop_hook
from turnecho.hosts import claude, detect_host


class ClaudeAdapterTests(unittest.TestCase):
    def write_transcript(self, directory: str, assistant_turns: int = 2) -> str:
        transcript = Path(directory) / "transcript.jsonl"
        with transcript.open("w", encoding="utf-8") as handle:
            handle.write(json.dumps({"type": "user", "message": "hi"}) + "\n")
            handle.write("not json\n")
            for index in range(assistant_turns):
                handle.write(
                    json.dumps({"type": "assistant", "message": f"reply {index}"})
                    + "\n"
                )
        return str(transcript)

    def stop_payload(
        self, message: str = "Done.", transcript: str | None = None
    ) -> dict[str, object]:
        return {
            "hook_event_name": "Stop",
            "session_id": "abc123",
            "transcript_path": "/missing.jsonl" if transcript is None else transcript,
            "cwd": "/tmp",
            "permission_mode": "default",
            "stop_hook_active": False,
            "last_assistant_message": (
                f"{message}\n\n<!-- turnecho-summary:v1\nAll good.\n-->"
            ),
        }

    def test_derive_turn_id_counts_assistant_turns(self) -> None:
        with TemporaryDirectory() as directory:
            transcript = self.write_transcript(directory)
            message = "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->"
            digest = hashlib.sha1(message.encode()).hexdigest()[:12]

            self.assertEqual(
                claude.derive_turn_id(transcript, message), f"turn-2-{digest}"
            )

    def test_derive_turn_id_falls_back_without_readable_transcript(self) -> None:
        message = "Done."
        digest = hashlib.sha1(message.encode()).hexdigest()[:12]

        first = claude.derive_turn_id(None, message)
        second = claude.derive_turn_id("/missing.jsonl", message)

        self.assertTrue(first.endswith(f"-{digest}"))
        self.assertTrue(second.endswith(f"-{digest}"))
        self.assertNotEqual(first, second)

    def test_parse_stop_payload_normalizes_event(self) -> None:
        with TemporaryDirectory() as directory:
            event = claude.parse_stop_payload(
                self.stop_payload(transcript=self.write_transcript(directory))
            )

        self.assertIsNotNone(event)
        assert event is not None
        self.assertEqual(event.host, "claude_code")
        self.assertEqual(event.session_id, "abc123")
        self.assertTrue(event.turn_id.startswith("turn-2-"))
        self.assertIn("turnecho-summary:v1", event.message)

    def test_parse_stop_payload_rejects_unusable_payloads(self) -> None:
        invalid_payloads: list[object] = [
            [],
            {"hook_event_name": "UserPromptSubmit"},
            {**self.stop_payload(), "session_id": "  "},
            {**self.stop_payload(), "last_assistant_message": "  "},
            {**self.stop_payload(), "stop_hook_active": True},
        ]

        for payload in invalid_payloads:
            with self.subTest(payload=payload):
                self.assertIsNone(claude.parse_stop_payload(payload))

    def test_prompt_submit_detection_and_envelope(self) -> None:
        self.assertTrue(
            claude.is_user_prompt_submit_payload(
                {"hook_event_name": "UserPromptSubmit"}
            )
        )
        self.assertFalse(
            claude.is_user_prompt_submit_payload({"hook_event_name": "Stop"})
        )

        output = json.loads(claude.render_prompt_submit_output("instruction"))
        self.assertEqual(
            output["hookSpecificOutput"]["hookEventName"], "UserPromptSubmit"
        )
        self.assertEqual(
            output["hookSpecificOutput"]["additionalContext"], "instruction"
        )

    def run_stop_hook(self, payload: object) -> tuple[str, str]:
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            TemporaryDirectory() as home,
            patch.dict(os.environ, {"HOME": home}),
            patch.object(sys, "argv", ["stop_hook"]),
            patch.object(sys, "stdin", io.StringIO(json.dumps(payload))),
            patch.object(stop_hook, "insert_job_db", return_value=True) as insert,
            patch.object(stop_hook, "spawn_background_worker"),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            stop_hook.main()
        return stdout.getvalue(), stderr.getvalue(), insert

    def test_stop_dispatch_queues_claude_job(self) -> None:
        with TemporaryDirectory() as directory:
            stdout, stderr, insert = self.run_stop_hook(
                self.stop_payload(transcript=self.write_transcript(directory))
            )

        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")
        insert.assert_called_once()
        self.assertEqual(insert.call_args.kwargs["host"], "claude_code")
        self.assertEqual(insert.call_args.kwargs["session_id"], "abc123")
        self.assertTrue(insert.call_args.kwargs["turn_id"].startswith("turn-2-"))
        self.assertEqual(insert.call_args.kwargs["message"], "All good.")

    def test_stop_dispatch_still_queues_codex_job(self) -> None:
        stdout, stderr, insert = self.run_stop_hook(
            {
                "hook_event_name": "Stop",
                "session_id": "session-1",
                "turn_id": "turn-1",
                "last_assistant_message": (
                    "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->"
                ),
                "stop_hook_active": False,
            }
        )

        self.assertEqual(stdout, "{}\n")
        self.assertEqual(stderr, "")
        insert.assert_called_once()
        self.assertEqual(insert.call_args.kwargs["host"], "codex")
        self.assertEqual(insert.call_args.kwargs["turn_id"], "turn-1")

    def test_stop_forced_host_overrides_detection(self) -> None:
        payload = {
            "hook_event_name": "Stop",
            "session_id": "session-1",
            "turn_id": "turn-1",
            "last_assistant_message": (
                "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->"
            ),
        }
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            TemporaryDirectory() as home,
            patch.dict(os.environ, {"HOME": home}),
            patch.object(sys, "argv", ["stop_hook", "--host=claude_code"]),
            patch.object(sys, "stdin", io.StringIO(json.dumps(payload))),
            patch.object(stop_hook, "insert_job_db", return_value=True) as insert,
            patch.object(stop_hook, "spawn_background_worker"),
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            stop_hook.main()

        self.assertEqual(stdout.getvalue(), "{}\n")
        self.assertEqual(insert.call_args.kwargs["host"], "claude_code")

    def test_stop_unknown_host_fails_safe(self) -> None:
        stdout, stderr = io.StringIO(), io.StringIO()
        with (
            TemporaryDirectory() as home,
            patch.dict(os.environ, {"HOME": home}),
            patch.object(sys, "argv", ["stop_hook", "--host=bogus"]),
            patch.object(
                sys, "stdin", io.StringIO(json.dumps({"hook_event_name": "Stop"}))
            ),
            patch.object(stop_hook, "insert_job_db") as insert,
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            stop_hook.main()

        self.assertEqual(stdout.getvalue(), "{}\n")
        insert.assert_not_called()

    def test_prompt_dispatch_returns_claude_context(self) -> None:
        stdout, stderr = io.StringIO(), io.StringIO()
        payload = {
            "hook_event_name": "UserPromptSubmit",
            "session_id": "abc123",
            "transcript_path": "/missing.jsonl",
            "prompt": "Implement the change.",
        }
        with (
            TemporaryDirectory() as home,
            patch.dict(os.environ, {"HOME": home}),
            patch.object(sys, "argv", ["prompt_hook"]),
            patch.object(sys, "stdin", io.StringIO(json.dumps(payload))),
            patch.object(prompt_hook, "spawn_background_worker") as spawn,
            redirect_stdout(stdout),
            redirect_stderr(stderr),
        ):
            prompt_hook.main()

        output = json.loads(stdout.getvalue())
        self.assertEqual(
            output["hookSpecificOutput"]["hookEventName"], "UserPromptSubmit"
        )
        self.assertIn(
            "turnecho-summary:v1", output["hookSpecificOutput"]["additionalContext"]
        )
        spawn.assert_called_once_with()

    def test_detect_host_prefers_transcript_path(self) -> None:
        self.assertEqual(detect_host({"transcript_path": "/t.jsonl"}), "claude_code")
        self.assertEqual(detect_host({"turn_id": "turn-1"}), "codex")
        self.assertEqual(detect_host([]), "codex")
        self.assertEqual(
            detect_host({"turn_id": "turn-1"}, forced_host="claude_code"),
            "claude_code",
        )


if __name__ == "__main__":
    unittest.main()
