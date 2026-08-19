from pydantic import BaseModel, Field


class CodexHookInputMessage(BaseModel):
    session_id: str = Field(default="")
    cwd: str = Field(default="")
    hook_event_name: str = Field(default="")
    model: str = Field(default="")


class CodexHookStopInputMessage(CodexHookInputMessage):
    turn_id: str = Field(default="")
    stop_hook_active: bool = Field(default=False)
    last_assistant_message: str | None = Field(default="")


class TurnEchoJob(BaseModel):
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
