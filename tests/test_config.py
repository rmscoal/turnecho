import json
import os
import stat
import unittest
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from tempfile import TemporaryDirectory

from turnecho.config import (
    ConfigError,
    TurnEchoConfig,
    load_config,
    reset_config,
    update_config,
    write_config,
)


class ConfigTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = TemporaryDirectory()
        self.config_path = Path(self.temporary_directory.name) / "config.json"

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_missing_file_uses_defaults_without_writing(self) -> None:
        config = load_config(self.config_path)

        self.assertEqual(config, TurnEchoConfig())
        self.assertFalse(self.config_path.exists())

    def test_write_and_load_configuration(self) -> None:
        expected = TurnEchoConfig(
            enabled=False,
            model="micro",
            voice="Luna",
            speed=1.2,
        )

        write_config(expected, self.config_path)

        self.assertEqual(load_config(self.config_path), expected)
        permissions = stat.S_IMODE(self.config_path.stat().st_mode)
        self.assertEqual(permissions, 0o600)

    def test_invalid_configuration_is_rejected(self) -> None:
        invalid_payloads = (
            {},
            {
                "schema_version": 2,
                "enabled": True,
                "model": "mini",
                "voice": "Hugo",
                "speed": 1.0,
            },
            {
                "schema_version": 1,
                "enabled": True,
                "voice": "Hugo",
                "speed": 1.0,
            },
            {
                "schema_version": 1,
                "enabled": True,
                "model": "unknown",
                "voice": "Hugo",
                "speed": 1.0,
            },
            {
                "schema_version": 1,
                "enabled": True,
                "model": "mini",
                "voice": "Unknown",
                "speed": 1.0,
            },
            {
                "schema_version": 1,
                "enabled": True,
                "model": "mini",
                "voice": "Hugo",
                "speed": 3.0,
            },
            {
                "schema_version": 1,
                "enabled": True,
                "model": "mini",
                "voice": "Hugo",
                "speed": 1.0,
                "unexpected": True,
            },
        )

        for payload in invalid_payloads:
            with self.subTest(payload=payload):
                self.config_path.write_text(json.dumps(payload), encoding="utf-8")
                with self.assertRaises(ConfigError):
                    load_config(self.config_path)

    def test_reset_all_recovers_from_malformed_configuration(self) -> None:
        self.config_path.write_text("not json", encoding="utf-8")

        config = reset_config(path=self.config_path)

        self.assertEqual(config, TurnEchoConfig())
        self.assertEqual(load_config(self.config_path), TurnEchoConfig())

    def test_concurrent_updates_do_not_leave_partial_json(self) -> None:
        write_config(TurnEchoConfig(), self.config_path)

        def update(index: int) -> None:
            if index % 3 == 0:
                update_config(model="micro", path=self.config_path)
            elif index % 3 == 1:
                update_config(voice="Luna", path=self.config_path)
            else:
                update_config(speed=1.1, path=self.config_path)

        with ThreadPoolExecutor(max_workers=4) as executor:
            list(executor.map(update, range(20)))

        config = load_config(self.config_path)
        self.assertEqual(config.model, "micro")
        self.assertEqual(config.voice, "Luna")
        self.assertEqual(config.speed, 1.1)
        with self.config_path.open(encoding="utf-8") as config_file:
            self.assertIsInstance(json.load(config_file), dict)

    def test_lock_file_is_private(self) -> None:
        write_config(TurnEchoConfig(), self.config_path)
        lock_path = self.config_path.with_name("config.lock")

        self.assertTrue(lock_path.is_file())
        self.assertEqual(stat.S_IMODE(os.stat(lock_path).st_mode), 0o600)


if __name__ == "__main__":
    unittest.main()
