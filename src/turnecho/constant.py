from enum import Enum

CODEX_HOOK_STOP_EVENT_NAME = "Stop"
CODEX_HOOK_USER_PROMPT_SUBMIT_NAME = "UserPromptSubmit"
CODEX_DEFAULT_OUTPUT_MESSAGE = "{}"

TURNECHO_USER_PROMPT_SUBMIT_HOOK_SUMMARY_INSTRUCTION_PROMPT = """
At the end of your final response, append exactly one TurnEcho summary using this format:
\n\n<!-- turnecho-summary:v1\nSUMMARY\n-->\n\n

Write 1 to 3 short spoken sentences, maximum 60 words.
Use plain conversational language.
Include the outcome, important blocker, or next action.
Do not use Markdown, URLs, file paths, code, IDs, or lists inside the summary.
"""

TURNECHO_SUMMARY_OPEN_MARKER = "<!-- turnecho-summary:v1\n"
TURNECHO_SUMMARY_CLOSE_MARKER = "\n-->"
TURNECHO_SUMMARY_MAX_CHARS = 500
TURNECHO_MODEL_NAME = "KittenML/kitten-tts-nano-0.8"
TURNECHO_AUDIO_SAMPLE_RATE = 24000

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
