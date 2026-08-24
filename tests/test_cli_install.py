import os
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from turnecho.cli import (
    CommandInstallError,
    install_cli_command,
    restore_cli_command,
)


class CliInstallTests(unittest.TestCase):
    def create_plugin(self, root: Path, name: str) -> tuple[Path, Path]:
        plugin_root = root / name
        plugin_root.mkdir()
        (plugin_root / "pyproject.toml").write_text(
            "[project]\nname = 'turnecho'\nversion = '0.2.0'\n",
            encoding="utf-8",
        )
        command = plugin_root / ".venv" / "bin" / "turnecho"
        command.parent.mkdir(parents=True)
        command.write_text("#!/bin/sh\n", encoding="utf-8")
        return plugin_root, command

    def test_install_and_update_managed_command(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            first_root, first_command = self.create_plugin(root, "first")
            second_root, second_command = self.create_plugin(root, "second")
            command_path = root / "bin" / "turnecho"

            first_state = install_cli_command(first_root, command_path)
            second_state = install_cli_command(second_root, command_path)

            self.assertEqual(command_path.resolve(), second_command.resolve())
            restore_cli_command(second_state)
            self.assertEqual(command_path.resolve(), first_command.resolve())
            restore_cli_command(first_state)
            self.assertFalse(command_path.exists())

    def test_unrelated_command_is_not_replaced(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root, _ = self.create_plugin(root, "plugin")
            command_path = root / "bin" / "turnecho"
            command_path.parent.mkdir()
            command_path.write_text("unrelated", encoding="utf-8")

            with self.assertRaises(CommandInstallError):
                install_cli_command(plugin_root, command_path)

            self.assertEqual(command_path.read_text(encoding="utf-8"), "unrelated")

    def test_unmanaged_symlink_is_not_replaced(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root, _ = self.create_plugin(root, "plugin")
            unrelated = root / "unrelated"
            unrelated.write_text("command", encoding="utf-8")
            command_path = root / "bin" / "turnecho"
            command_path.parent.mkdir()
            command_path.symlink_to(unrelated)

            with self.assertRaises(CommandInstallError):
                install_cli_command(plugin_root, command_path)

            self.assertEqual(os.readlink(command_path), str(unrelated))

    def test_restore_does_not_remove_a_command_changed_after_install(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root, _ = self.create_plugin(root, "plugin")
            command_path = root / "bin" / "turnecho"
            state = install_cli_command(plugin_root, command_path)
            command_path.unlink()
            command_path.write_text("replacement", encoding="utf-8")

            with self.assertRaises(CommandInstallError):
                restore_cli_command(state)

            self.assertEqual(command_path.read_text(encoding="utf-8"), "replacement")


if __name__ == "__main__":
    unittest.main()
