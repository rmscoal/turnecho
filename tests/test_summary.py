import unittest

from turnecho.constant import TURNECHO_SUMMARY_MAX_CHARS
from turnecho.summary import extract_turnecho_summary_from_agent_message


class SummaryTests(unittest.TestCase):
    def test_extracts_valid_trailing_summary(self) -> None:
        message = (
            "Finished the requested change.\n\n"
            "<!-- turnecho-summary:v1\n"
            "The requested change is complete and all tests passed.\n"
            "-->"
        )

        self.assertEqual(
            extract_turnecho_summary_from_agent_message(message),
            "The requested change is complete and all tests passed.",
        )

    def test_rejects_message_without_marker(self) -> None:
        self.assertIsNone(
            extract_turnecho_summary_from_agent_message("Finished the change.")
        )

    def test_rejects_oversized_summary(self) -> None:
        message = (
            "<!-- turnecho-summary:v1\n"
            + "a" * (TURNECHO_SUMMARY_MAX_CHARS + 1)
            + "\n-->"
        )

        self.assertIsNone(extract_turnecho_summary_from_agent_message(message))


if __name__ == "__main__":
    unittest.main()
