#!/usr/bin/env python3
"""Inject the TurnEcho summary instruction without resolving project dependencies."""

from __future__ import annotations

import json
import sys

from .config import ConfigError, load_config
from .constant import (
    TURNECHO_USER_PROMPT_SUBMIT_HOOK_SUMMARY_INSTRUCTION_PROMPT,
)
from .hosts import codex
from .worker import spawn_background_worker


def main() -> int:
    """Print the UserPromptSubmit hook response using only the standard library."""
    try:
        raw_input: object = json.load(sys.stdin)
    except Exception as error:
        print(error, file=sys.stderr)
        print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    if not codex.is_user_prompt_submit_payload(raw_input):
        print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    try:
        config = load_config()
    except ConfigError as error:
        print(error, file=sys.stderr)
        print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    if not config.enabled:
        print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)
        return 0

    print(
        codex.render_prompt_submit_output(
            TURNECHO_USER_PROMPT_SUBMIT_HOOK_SUMMARY_INSTRUCTION_PROMPT
        )
    )

    # Spawn background worker here to reduce process startup delay.
    try:
        spawn_background_worker()
    except Exception as e:
        print(e, file=sys.stderr)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
