from enum import Enum

CODEX_HOOK_STOP_EVENT_NAME = "Stop"
CODEX_DEFAULT_OUTPUT_MESSAGE = "{}"

TURNECHO_SQLITE3_DB_FILE_PATH = "~/.config/turnecho/turnecho.db"

TURNECHO_WORKER_LOCK_FILE_PATH_MACOS_LINUX = "~/.config/turnecho/worker.lock"
TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX = "~/.config/turnecho/worker.log"
TURNECHO_WORKER_IDLE_TIMEOUT_WITHOUT_JOB_SECONDS = 600
TURNECHO_WORKER_LOCK_RETRY_SECONDS = 1.0
TURNECHO_WORKER_POLL_INTERVAL_SECONDS = 0.25


class TurnEchoJobProcessingStatus(Enum):
    PENDING = "pending"
    PROCESSING = "processing"
    FAILED = "failed"
    SUCCESS = "success"


class TurnEchoHostSource(Enum):
    CODEX = "codex"
    CLAUDE_CODE = "claude_code"
    OPENCODE = "opencode"
