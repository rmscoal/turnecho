"""Shared per-host event shapes.

This module holds the host identity enum and the normalized hook event that
every host adapter produces. It contains no host-specific logic and imports
nothing from the core, so both sides can depend on it freely.
"""

from dataclasses import dataclass
from enum import Enum


class TurnEchoHostSource(Enum):
    CODEX = "codex"
    CLAUDE_CODE = "claude_code"
    OPENCODE = "opencode"


@dataclass(frozen=True)
class TurnEchoEvent:
    """One normalized hook event ready for core validation and queueing."""

    host: str
    session_id: str
    turn_id: str = ""
    message: str = ""
    stop_hook_active: bool = False
