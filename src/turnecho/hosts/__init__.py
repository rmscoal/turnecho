"""Per-host hook adapters over the shared TurnEcho core."""

from collections.abc import Sequence

from . import claude, codex
from .types import TurnEchoHostSource

__all__ = ["claude", "codex", "default_output", "detect_host", "forced_host"]


def detect_host(raw_input: object, forced_host: str | None = None) -> str:
    """Return the hook source for a payload.

    An explicit override always wins, even when it names no known host (the
    caller then fails safe to empty output). Otherwise Claude Code always
    sends transcript_path while Codex sends turn_id instead, so the payload
    keys decide. Anything unrecognized falls through to Codex parsing, which
    rejects it safely.
    """
    if forced_host is not None:
        return forced_host
    if isinstance(raw_input, dict) and "transcript_path" in raw_input:
        return TurnEchoHostSource.CLAUDE_CODE.value
    return TurnEchoHostSource.CODEX.value


def forced_host(argv: Sequence[str]) -> str | None:
    """Return an explicit --host=... override from hook argv, if present."""
    for argument in argv:
        if argument.startswith("--host="):
            return argument.removeprefix("--host=")
    return None


def default_output(host: str) -> str:
    """Return the host's fail-safe empty hook output."""
    if host == TurnEchoHostSource.CLAUDE_CODE.value:
        return claude.CLAUDE_DEFAULT_OUTPUT_MESSAGE
    return codex.CODEX_DEFAULT_OUTPUT_MESSAGE
