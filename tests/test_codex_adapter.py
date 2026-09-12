import json
import unittest

from turnecho.hosts import codex


class CodexAdapterTests(unittest.TestCase):
    def stop_payload(self) -> dict[str, object]:
        return {
            "hook_event_name": "Stop",
            "session_id": "session-1",
            "turn_id": "turn-1",
            "last_assistant_message": "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->",
            "stop_hook_active": False,
        }

    def test_parse_stop_payload_normalizes_event(self) -> None:
        event = codex.parse_stop_payload(self.stop_payload())

        self.assertIsNotNone(event)
        assert event is not None
        self.assertEqual(event.host, "codex")
        self.assertEqual(event.session_id, "session-1")
        self.assertEqual(event.turn_id, "turn-1")
        self.assertIn("turnecho-summary:v1", event.message)

    def test_parse_stop_payload_rejects_unusable_payloads(self) -> None:
        invalid_payloads: list[object] = [
            [],
            {"hook_event_name": "UserPromptSubmit"},
            {**self.stop_payload(), "session_id": "  "},
            {**self.stop_payload(), "turn_id": ""},
            {**self.stop_payload(), "last_assistant_message": "  "},
            {**self.stop_payload(), "stop_hook_active": True},
        ]

        for payload in invalid_payloads:
            with self.subTest(payload=payload):
                self.assertIsNone(codex.parse_stop_payload(payload))

    def test_user_prompt_submit_detection(self) -> None:
        self.assertTrue(
            codex.is_user_prompt_submit_payload({"hook_event_name": "UserPromptSubmit"})
        )
        self.assertFalse(
            codex.is_user_prompt_submit_payload({"hook_event_name": "Stop"})
        )
        self.assertFalse(codex.is_user_prompt_submit_payload([]))

    def test_render_prompt_submit_output_envelope(self) -> None:
        output = json.loads(codex.render_prompt_submit_output("instruction"))

        self.assertEqual(
            output["hookSpecificOutput"]["hookEventName"], "UserPromptSubmit"
        )
        self.assertEqual(
            output["hookSpecificOutput"]["additionalContext"], "instruction"
        )


if __name__ == "__main__":
    unittest.main()
