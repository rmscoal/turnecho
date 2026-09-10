"""Validate TurnEcho summary markers in host agent messages.

This module is host-independent: every host speaks summaries through the same
trailing marker contract.
"""

from .constant import (
    TURNECHO_SUMMARY_CLOSE_MARKER,
    TURNECHO_SUMMARY_MAX_CHARS,
    TURNECHO_SUMMARY_OPEN_MARKER,
)


def extract_turnecho_summary_from_agent_message(agent_message: str) -> str | None:
    normalized_message = agent_message.replace("\r\n", "\n").rstrip()
    if not normalized_message.endswith(TURNECHO_SUMMARY_CLOSE_MARKER):
        return None

    marker_start = normalized_message.rfind(TURNECHO_SUMMARY_OPEN_MARKER)
    if marker_start == -1:
        return None

    summary_start = marker_start + len(TURNECHO_SUMMARY_OPEN_MARKER)
    summary_end = len(normalized_message) - len(TURNECHO_SUMMARY_CLOSE_MARKER)
    raw_summary = normalized_message[summary_start:summary_end]

    if "<!--" in raw_summary or "-->" in raw_summary:
        return None

    summary = " ".join(raw_summary.split())
    if not summary:
        return None

    if len(summary) > TURNECHO_SUMMARY_MAX_CHARS:
        return None

    return summary
