#!/usr/bin/env python3
"""Inject the TurnEcho summary instruction without resolving project dependencies."""

from __future__ import annotations

import json
import sys

from .constant import (
    CODEX_DEFAULT_OUTPUT_MESSAGE,
    CODEX_HOOK_USER_PROMPT_SUBMIT_NAME,
    TURNECHO_USER_PROMPT_SUBMIT_HOOK_SUMMARY_INSTRUCTION_PROMPT,
)


def main() -> int:
    """Print the UserPromptSubmit hook response using only the standard library."""
    try:
        raw_input: object = json.load(sys.stdin)
    except Exception as error:
        print(error, file=sys.stderr)
        print(CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    if (
        not isinstance(raw_input, dict)
        or raw_input.get("hook_event_name") != CODEX_HOOK_USER_PROMPT_SUBMIT_NAME
    ):
        print(CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    print(
        json.dumps(
            {
                "hookSpecificOutput": {
                    "hookEventName": CODEX_HOOK_USER_PROMPT_SUBMIT_NAME,
                    "additionalContext": (
                        TURNECHO_USER_PROMPT_SUBMIT_HOOK_SUMMARY_INSTRUCTION_PROMPT
                    ),
                }
            }
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
