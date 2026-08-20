from dataclasses import dataclass


@dataclass
class TurnEchoJob:
    """One queued summary and its processing state."""

    id: str
    host: str
    session_id: str
    turn_id: str
    message: str
    processing_status: str
    created_at: int
    started_at: int | None = None
    completed_at: int | None = None
    error_message: str | None = None
