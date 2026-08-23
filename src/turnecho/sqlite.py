import hashlib
import math
import os
import re
import sqlite3
import threading
import time
from contextlib import closing
from dataclasses import dataclass
from importlib import resources
from pathlib import Path
from uuid import uuid4

from .constant import (
    TURNECHO_AVAILABLE_VOICES,
    TURNECHO_MAX_SPEED,
    TURNECHO_MIN_SPEED,
    TURNECHO_SQLITE3_DB_FILE_PATH,
    TurnEchoJobProcessingStatus,
)
from .schema import TurnEchoJob

MIGRATION_NAME_PATTERN = re.compile(r"^(?P<version>\d{3})_(?P<name>.+)\.sql$")
MIGRATION_PACKAGE = "turnecho.migrations"


class MigrationError(RuntimeError):
    """Raised when stored migration state is inconsistent with the code."""


@dataclass(frozen=True)
class Migration:
    version: int
    name: str
    sql: str
    checksum: str


_database_initialization_lock = threading.Lock()
_initialized_databases: set[tuple[int, Path]] = set()


def _database_path() -> Path:
    return Path(TURNECHO_SQLITE3_DB_FILE_PATH).expanduser().resolve()


def _connect() -> sqlite3.Connection:
    database_path = _database_path()
    database_path.parent.mkdir(parents=True, exist_ok=True)

    connection = sqlite3.connect(database_path, timeout=5)
    connection.execute("PRAGMA busy_timeout = 5000")
    connection.row_factory = sqlite3.Row
    return connection


def discover_migrations() -> list[Migration]:
    """Load and validate ordered SQL migrations bundled with TurnEcho."""
    migrations: list[Migration] = []
    migration_root = resources.files(MIGRATION_PACKAGE)

    for resource in migration_root.iterdir():
        match = MIGRATION_NAME_PATTERN.fullmatch(resource.name)
        if match is None:
            continue

        sql = resource.read_text(encoding="utf-8").strip()
        if not sql or not sqlite3.complete_statement(sql):
            raise MigrationError(f"Migration is empty or incomplete: {resource.name}")

        migrations.append(
            Migration(
                version=int(match.group("version")),
                name=match.group("name"),
                sql=sql,
                checksum=hashlib.sha256(sql.encode()).hexdigest(),
            )
        )

    migrations.sort(key=lambda migration: migration.version)
    if not migrations:
        raise MigrationError("TurnEcho contains no packaged database migrations.")
    versions = [migration.version for migration in migrations]
    if len(versions) != len(set(versions)):
        raise MigrationError("TurnEcho contains duplicate migration versions.")
    return migrations


def _migration_statements(migration: Migration) -> list[str]:
    """Split a migration without giving up the caller's transaction."""
    statements: list[str] = []
    pending: list[str] = []

    for character in migration.sql:
        pending.append(character)
        if character != ";":
            continue

        candidate = "".join(pending).strip()
        if sqlite3.complete_statement(candidate):
            statements.append(candidate)
            pending.clear()

    remainder = "".join(pending).strip()
    if remainder:
        raise MigrationError(f"Migration is incomplete: {migration.name}")
    return statements


def run_migrations(connection: sqlite3.Connection) -> None:
    """Apply all pending migrations in one immediate transaction."""
    migrations = discover_migrations()
    connection.execute("BEGIN IMMEDIATE")
    try:
        connection.execute(
            """
            CREATE TABLE IF NOT EXISTS turnecho_schema_migrations (
                version INTEGER PRIMARY KEY,
                name TEXT NOT NULL,
                checksum TEXT NOT NULL,
                applied_at INTEGER NOT NULL
            )
            """
        )
        applied = {
            row["version"]: row
            for row in connection.execute(
                """
                SELECT version, name, checksum
                FROM turnecho_schema_migrations
                """
            )
        }
        packaged_versions = {migration.version for migration in migrations}
        unknown_versions = set(applied) - packaged_versions
        if unknown_versions:
            versions = ", ".join(
                f"{version:03d}" for version in sorted(unknown_versions)
            )
            raise MigrationError(
                f"Database contains migrations unknown to this TurnEcho version: "
                f"{versions}"
            )

        for migration in migrations:
            existing = applied.get(migration.version)
            if existing is not None:
                if (
                    existing["name"] != migration.name
                    or existing["checksum"] != migration.checksum
                ):
                    raise MigrationError(
                        f"Applied migration {migration.version:03d} no longer "
                        "matches the packaged migration."
                    )
                continue

            for statement in _migration_statements(migration):
                connection.execute(statement)
            connection.execute(
                """
                INSERT INTO turnecho_schema_migrations (
                    version, name, checksum, applied_at
                )
                VALUES (?, ?, ?, ?)
                """,
                (
                    migration.version,
                    migration.name,
                    migration.checksum,
                    int(time.time()),
                ),
            )

        connection.commit()
    except Exception:
        connection.rollback()
        raise


def _init_db() -> None:
    """Initialize one database once in the current process."""
    initialization_key = (os.getpid(), _database_path())
    with _database_initialization_lock:
        if initialization_key in _initialized_databases:
            return

        with closing(_connect()) as connection:
            connection.execute("PRAGMA journal_mode = WAL")
            run_migrations(connection)

        _initialized_databases.add(initialization_key)


def insert_job_db(
    *,
    host: str,
    session_id: str,
    turn_id: str,
    message: str,
    voice: str,
    speed: float,
) -> bool:
    """Insert a pending job, returning False when the turn already exists."""
    if voice not in TURNECHO_AVAILABLE_VOICES:
        raise ValueError(f"Unsupported TurnEcho voice: {voice}")
    if (
        isinstance(speed, bool)
        or not isinstance(speed, (int, float))
        or not math.isfinite(speed)
        or not TURNECHO_MIN_SPEED <= speed <= TURNECHO_MAX_SPEED
    ):
        raise ValueError(f"Unsupported TurnEcho speed: {speed}")

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
                voice,
                speed,
                processing_status,
                created_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(host, session_id, turn_id) DO NOTHING
            """,
            (
                str(uuid4()),
                host,
                session_id,
                turn_id,
                message,
                voice,
                speed,
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
        voice=row["voice"],
        speed=row["speed"],
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
                RETURNING id, host, session_id, turn_id, message, voice, speed,
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
