import sqlite3
import time
from contextlib import closing
from pathlib import Path
from uuid import uuid4

from .constant import TURNECHO_SQLITE3_DB_FILE_PATH, TurnEchoJobProcessingStatus
from .schema import TurnEchoJob


def _connect() -> sqlite3.Connection:
    database_path = Path(TURNECHO_SQLITE3_DB_FILE_PATH).expanduser()
    database_path.parent.mkdir(parents=True, exist_ok=True)

    connection = sqlite3.connect(database_path, timeout=5)
    connection.execute("PRAGMA busy_timeout = 5000")
    connection.row_factory = sqlite3.Row
    return connection


def _init_db() -> None:
    """Create the job table and configure the database for concurrent hooks."""
    with closing(_connect()) as connection:
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute(
            """
            CREATE TABLE IF NOT EXISTS turnecho_jobs (
                id TEXT PRIMARY KEY,
                host TEXT NOT NULL CHECK (host <> ''),
                session_id TEXT NOT NULL CHECK (session_id <> ''),
                turn_id TEXT NOT NULL CHECK (turn_id <> ''),
                message TEXT NOT NULL CHECK (message <> ''),
                processing_status TEXT NOT NULL CHECK (processing_status <> ''),
                created_at INTEGER NOT NULL CHECK (created_at > 0),
                started_at INTEGER,
                completed_at INTEGER,
                error_message TEXT,
                UNIQUE(host, session_id, turn_id)
            )
            """
        )
        connection.execute(
            """
            CREATE INDEX IF NOT EXISTS idx_turnecho_jobs_status_queue
            ON turnecho_jobs(processing_status, created_at)
            """
        )
        connection.commit()


def insert_job_db(
    *,
    host: str,
    session_id: str,
    turn_id: str,
    message: str,
) -> bool:
    """Insert a pending job, returning False when the turn already exists."""
    _init_db()

    with closing(_connect()) as connection:
        cursor = connection.execute(
            """
            INSERT INTO turnecho_jobs (
                id,
                host,
                session_id,
                turn_id,
                message,
                processing_status,
                created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(host, session_id, turn_id) DO NOTHING
            """,
            (
                str(uuid4()),
                host,
                session_id,
                turn_id,
                message,
                TurnEchoJobProcessingStatus.PENDING.value,
                int(time.time()),
            ),
        )
        connection.commit()
        return cursor.rowcount == 1


def _row_to_job(row: sqlite3.Row) -> TurnEchoJob:
    return TurnEchoJob(
        id=row["id"],
        host=row["host"],
        session_id=row["session_id"],
        turn_id=row["turn_id"],
        message=row["message"],
        processing_status=row["processing_status"],
        created_at=row["created_at"],
        started_at=row["started_at"],
        completed_at=row["completed_at"],
        error_message=row["error_message"],
    )


def claim_next_job_from_db() -> TurnEchoJob | None:
    """Atomically claim the oldest pending job."""
    _init_db()
    started_at = int(time.time())

    with closing(_connect()) as connection:
        try:
            connection.execute("BEGIN IMMEDIATE")
            record = connection.execute(
                """
                UPDATE turnecho_jobs
                SET processing_status = ?, started_at = ?
                WHERE rowid = (
                    SELECT rowid
                    FROM turnecho_jobs
                    WHERE processing_status = ?
                    ORDER BY created_at, rowid
                    LIMIT 1
                )
                AND processing_status = ?
                RETURNING id, host, session_id, turn_id, message,
                          processing_status, created_at, started_at,
                          completed_at, error_message
                """,
                (
                    TurnEchoJobProcessingStatus.PROCESSING.value,
                    started_at,
                    TurnEchoJobProcessingStatus.PENDING.value,
                    TurnEchoJobProcessingStatus.PENDING.value,
                ),
            ).fetchone()
            connection.commit()
        except Exception:
            connection.rollback()
            raise

    return None if record is None else _row_to_job(record)


def has_pending_jobs_in_db() -> bool:
    """Return whether the queue contains work for a worker."""
    _init_db()

    with closing(_connect()) as connection:
        record = connection.execute(
            """
            SELECT 1
            FROM turnecho_jobs
            WHERE processing_status = ?
            LIMIT 1
            """,
            (TurnEchoJobProcessingStatus.PENDING.value,),
        ).fetchone()

    return record is not None


def requeue_processing_jobs_from_db() -> int:
    """Requeue jobs abandoned by a previous lock-owning worker."""
    _init_db()

    with closing(_connect()) as connection:
        cursor = connection.execute(
            """
            UPDATE turnecho_jobs
            SET processing_status = ?,
                started_at = NULL,
                completed_at = NULL,
                error_message = NULL
            WHERE processing_status = ?
            """,
            (
                TurnEchoJobProcessingStatus.PENDING.value,
                TurnEchoJobProcessingStatus.PROCESSING.value,
            ),
        )
        connection.commit()
        return cursor.rowcount


def update_job_db(job: TurnEchoJob) -> bool:
    """Persist worker-owned status and completion details."""
    _init_db()

    with closing(_connect()) as connection:
        cursor = connection.execute(
            """
            UPDATE turnecho_jobs
            SET processing_status = ?,
                completed_at = ?,
                error_message = ?
            WHERE id = ?
            """,
            (
                job.processing_status,
                job.completed_at,
                job.error_message,
                job.id,
            ),
        )
        connection.commit()
        return cursor.rowcount == 1
