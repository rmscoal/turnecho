import subprocess
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import call, patch

from turnecho import install_plugin


class GitHubPluginInstallerTests(unittest.TestCase):
    def create_installed_plugin(self, directory: Path) -> Path:
        plugin_root = directory / "installed-turnecho"
        plugin_root.mkdir()
        (plugin_root / "pyproject.toml").write_text(
            "[project]\nname = 'turnecho'\nversion = '0.1.0'\n",
            encoding="utf-8",
        )
        return plugin_root

    def installed_payload(self, plugin_root: Path) -> dict[str, object]:
        return {
            "installed": [
                {
                    "pluginId": "turnecho@turnecho",
                    "source": {"source": "local", "path": str(plugin_root)},
                }
            ]
        }

    def test_fresh_install_preflights_and_syncs_installed_plugin(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(
                    install_plugin, "validate_runtime_dependencies"
                ) as preflight,
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        {"marketplaces": []},
                        {"installed": []},
                        self.installed_payload(plugin_root),
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
            ):
                result = install_plugin.install_plugin()

        self.assertEqual(result, plugin_root.resolve())
        preflight.assert_called_once_with()
        self.assertEqual(
            run.call_args_list,
            [
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "rmscoal/turnecho",
                        "--ref",
                        "main",
                        "--json",
                    ]
                ),
                call(
                    [
                        "codex",
                        "plugin",
                        "add",
                        "turnecho@turnecho",
                        "--json",
                    ]
                ),
                call(
                    [
                        "uv",
                        "sync",
                        "--project",
                        str(plugin_root.resolve()),
                        "--no-dev",
                    ]
                ),
                call(
                    [
                        "uv",
                        "run",
                        "--project",
                        str(plugin_root.resolve()),
                        "--no-dev",
                        "python",
                        "-m",
                        "turnecho.runtime_preflight",
                    ]
                ),
            ],
        )

    def test_runtime_preflight_failure_does_not_change_codex(self) -> None:
        with (
            patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
            patch.object(
                install_plugin,
                "validate_runtime_dependencies",
                side_effect=ImportError("sounddevice unavailable"),
            ),
            patch.object(install_plugin, "run_json_command") as run_json,
            self.assertRaises(install_plugin.InstallError),
        ):
            install_plugin.install_plugin()

        run_json.assert_not_called()

    def test_runtime_sync_failure_rolls_back_fresh_codex_state(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))
            sync_command = [
                "uv",
                "sync",
                "--project",
                str(plugin_root.resolve()),
                "--no-dev",
            ]

            def run(command: list[str]) -> None:
                if command == sync_command:
                    raise subprocess.CalledProcessError(1, command)

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(install_plugin, "validate_runtime_dependencies"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        {"marketplaces": []},
                        {"installed": []},
                        self.installed_payload(plugin_root),
                    ],
                ),
                patch.object(
                    install_plugin,
                    "run_checked_command",
                    side_effect=run,
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin()

        self.assertEqual(
            run_command.call_args_list[-2:],
            [
                call(
                    [
                        "codex",
                        "plugin",
                        "remove",
                        "turnecho@turnecho",
                        "--json",
                    ]
                ),
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "remove",
                        "turnecho",
                        "--json",
                    ]
                ),
            ],
        )

    def test_existing_marketplace_must_match_github_source(self) -> None:
        marketplace = {
            "marketplaces": [
                {
                    "name": "turnecho",
                    "marketplaceSource": {
                        "sourceType": "git",
                        "source": "someone-else/turnecho",
                    },
                }
            ]
        }

        with (
            patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
            patch.object(install_plugin, "validate_runtime_dependencies"),
            patch.object(
                install_plugin,
                "run_json_command",
                side_effect=[marketplace, {"installed": []}],
            ),
            patch.object(install_plugin, "run_checked_command") as run,
            self.assertRaises(install_plugin.InstallError),
        ):
            install_plugin.install_plugin()

        run.assert_not_called()

    def test_update_refreshes_marketplace_before_reinstalling(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))
            installed_payload = self.installed_payload(plugin_root)
            marketplace_payload = {
                "marketplaces": [
                    {
                        "name": "turnecho",
                        "marketplaceSource": {
                            "sourceType": "git",
                            "source": "rmscoal/turnecho",
                        },
                    }
                ]
            }

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(install_plugin, "validate_runtime_dependencies"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        marketplace_payload,
                        installed_payload,
                        installed_payload,
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
            ):
                install_plugin.install_plugin(update=True)

        self.assertEqual(
            run.call_args_list[:2],
            [
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "upgrade",
                        "turnecho",
                        "--json",
                    ]
                ),
                call(
                    [
                        "codex",
                        "plugin",
                        "add",
                        "turnecho@turnecho",
                        "--json",
                    ]
                ),
            ],
        )


if __name__ == "__main__":
    unittest.main()
