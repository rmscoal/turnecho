import hashlib
import sqlite3
import unittest
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from turnecho import sqlite as turnecho_sqlite


class MigrationTests(unittest.TestCase):
    def connect(self, path: Path) -> sqlite3.Connection:
        connection = sqlite3.connect(path, timeout=5)
        connection.execute("PRAGMA busy_timeout = 5000")
        connection.row_factory = sqlite3.Row
        return connection

    def test_fresh_database_applies_all_migrations_once(self) -> None:
        with TemporaryDirectory() as directory:
            path = Path(directory) / "turnecho.db"
            with closing(self.connect(path)) as connection:
                turnecho_sqlite.run_migrations(connection)
                turnecho_sqlite.run_migrations(connection)
                columns = {
                    row["name"]
                    for row in connection.execute("PRAGMA table_info(turnecho_jobs)")
                }
                applied_count = connection.execute(
                    "SELECT COUNT(*) FROM turnecho_schema_migrations"
                ).fetchone()[0]
                indexes = {
                    row["name"]
                    for row in connection.execute("PRAGMA index_list(turnecho_jobs)")
                }

        self.assertIn("voice", columns)
        self.assertIn("speed", columns)
        self.assertIn("idx_turnecho_jobs_status_queue", indexes)
        self.assertEqual(applied_count, 1)

    def test_failed_migration_rolls_back_schema_and_ledger(self) -> None:
        sql = "CREATE TABLE partial_change (id INTEGER); THIS IS NOT SQL;"
        invalid = turnecho_sqlite.Migration(
            version=999,
            name="invalid",
            sql=sql,
            checksum=hashlib.sha256(sql.encode()).hexdigest(),
        )
        with TemporaryDirectory() as directory:
            path = Path(directory) / "turnecho.db"
            with closing(self.connect(path)) as connection:
                with (
                    patch.object(
                        turnecho_sqlite,
                        "discover_migrations",
                        return_value=[invalid],
                    ),
                    self.assertRaises(sqlite3.Error),
                ):
                    turnecho_sqlite.run_migrations(connection)
                tables = {
                    row["name"]
                    for row in connection.execute(
                        "SELECT name FROM sqlite_schema WHERE type = 'table'"
                    )
                }

        self.assertNotIn("partial_change", tables)
        self.assertNotIn("turnecho_schema_migrations", tables)

    def test_concurrent_initialization_serializes_migrations(self) -> None:
        with TemporaryDirectory() as directory:
            path = Path(directory) / "turnecho.db"

            def migrate(_: int) -> None:
                with closing(self.connect(path)) as connection:
                    turnecho_sqlite.run_migrations(connection)

            with ThreadPoolExecutor(max_workers=2) as executor:
                list(executor.map(migrate, range(2)))

            with closing(self.connect(path)) as connection:
                count = connection.execute(
                    "SELECT COUNT(*) FROM turnecho_schema_migrations"
                ).fetchone()[0]

        self.assertEqual(count, 1)

    def test_changed_applied_migration_is_rejected(self) -> None:
        with TemporaryDirectory() as directory:
            path = Path(directory) / "turnecho.db"
            with closing(self.connect(path)) as connection:
                turnecho_sqlite.run_migrations(connection)
                connection.execute(
                    """
                    UPDATE turnecho_schema_migrations
                    SET checksum = 'changed'
                    WHERE version = 1
                    """
                )
                connection.commit()

                with self.assertRaises(turnecho_sqlite.MigrationError):
                    turnecho_sqlite.run_migrations(connection)

    def test_newer_database_migration_is_rejected(self) -> None:
        with TemporaryDirectory() as directory:
            path = Path(directory) / "turnecho.db"
            with closing(self.connect(path)) as connection:
                turnecho_sqlite.run_migrations(connection)
                connection.execute(
                    """
                    INSERT INTO turnecho_schema_migrations (
                        version, name, checksum, applied_at
                    ) VALUES (999, 'future', 'checksum', 1)
                    """
                )
                connection.commit()

                with self.assertRaises(turnecho_sqlite.MigrationError):
                    turnecho_sqlite.run_migrations(connection)


if __name__ == "__main__":
    unittest.main()
