import sqlite3
import sys
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from pathlib import Path
from tempfile import TemporaryDirectory

PROJECT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PROJECT_ROOT / "src"))

from turnecho import sqlite  # noqa: E402
from turnecho.constant import TurnEchoJobProcessingStatus  # noqa: E402


class SQLiteJobStoreTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = TemporaryDirectory()
        self.database_path = Path(self.temporary_directory.name) / "turnecho.db"
        self.original_database_path = sqlite.TURNECHO_SQLITE3_DB_FILE_PATH
        sqlite.TURNECHO_SQLITE3_DB_FILE_PATH = self.database_path

    def tearDown(self) -> None:
        sqlite.TURNECHO_SQLITE3_DB_FILE_PATH = self.original_database_path
        self.temporary_directory.cleanup()

    def test_insert_and_deduplicate_job(self) -> None:
        inserted = sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Finished the requested change.",
        )
        duplicate_inserted = sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Duplicate event.",
        )
        has_pending_job = sqlite.has_pending_jobs_in_db()
        job = sqlite.claim_next_job_from_db()

        self.assertTrue(inserted)
        self.assertFalse(duplicate_inserted)
        self.assertTrue(has_pending_job)
        self.assertIsNotNone(job)
        assert job is not None
        self.assertEqual(job.message, "Finished the requested change.")
        self.assertIsNone(sqlite.claim_next_job_from_db())
        self.assertFalse(sqlite.has_pending_jobs_in_db())

    def test_claim_job_is_atomic(self) -> None:
        sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Finished the requested change.",
        )

        claimed_job = sqlite.claim_next_job_from_db()
        duplicate_claim = sqlite.claim_next_job_from_db()

        self.assertIsNotNone(claimed_job)
        assert claimed_job is not None
        self.assertEqual(
            claimed_job.processing_status,
            TurnEchoJobProcessingStatus.PROCESSING.value,
        )
        self.assertIsNotNone(claimed_job.started_at)
        self.assertIsNone(duplicate_claim)

    def test_concurrent_workers_only_claim_job_once(self) -> None:
        sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Finished the requested change.",
        )

        with ThreadPoolExecutor(max_workers=2) as executor:
            claims = list(
                executor.map(lambda _: sqlite.claim_next_job_from_db(), range(2))
            )

        self.assertEqual(sum(claim is not None for claim in claims), 1)

    def test_update_completed_job(self) -> None:
        sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Finished the requested change.",
        )
        claimed_job = sqlite.claim_next_job_from_db()
        assert claimed_job is not None
        claimed_job.processing_status = TurnEchoJobProcessingStatus.SUCCESS.value
        claimed_job.completed_at = int(time.time())

        updated = sqlite.update_job_db(claimed_job)

        with closing(sqlite3.connect(self.database_path)) as connection:
            stored_status, stored_completed_at = connection.execute(
                """
                SELECT processing_status, completed_at
                FROM turnecho_jobs
                WHERE id = ?
                """,
                (claimed_job.id,),
            ).fetchone()

        self.assertTrue(updated)
        self.assertEqual(stored_status, TurnEchoJobProcessingStatus.SUCCESS.value)
        self.assertEqual(stored_completed_at, claimed_job.completed_at)

    def test_requeue_processing_job(self) -> None:
        sqlite.insert_job_db(
            host="codex",
            session_id="session-1",
            turn_id="turn-1",
            message="Finished the requested change.",
        )
        first_claim = sqlite.claim_next_job_from_db()
        assert first_claim is not None

        requeued_count = sqlite.requeue_processing_jobs_from_db()
        second_claim = sqlite.claim_next_job_from_db()

        self.assertEqual(requeued_count, 1)
        self.assertIsNotNone(second_claim)
        assert second_claim is not None
        self.assertEqual(second_claim.id, first_claim.id)


if __name__ == "__main__":
    unittest.main()
