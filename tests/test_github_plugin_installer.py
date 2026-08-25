import subprocess
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import call, patch

from turnecho import install_plugin
from turnecho.cli import CommandInstallError
from turnecho.constant import TURNECHO_MARKETPLACE_MANIFEST_PATH

CURRENT_VERSION = "0.2.1"
CURRENT_REF = f"v{CURRENT_VERSION}"


class GitHubPluginInstallerTests(unittest.TestCase):
    def create_installed_plugin(
        self,
        directory: Path,
        *,
        name: str = "installed-turnecho",
        version: str = CURRENT_VERSION,
    ) -> Path:
        plugin_root = directory / name
        plugin_root.mkdir()
        (plugin_root / "pyproject.toml").write_text(
            f"[project]\nname = 'turnecho'\nversion = '{version}'\n",
            encoding="utf-8",
        )
        command = plugin_root / ".venv" / "bin" / "turnecho"
        command.parent.mkdir(parents=True)
        command.write_text("#!/bin/sh\n", encoding="utf-8")
        return plugin_root

    def installed_payload(
        self,
        plugin_root: Path,
        *,
        version: str = CURRENT_VERSION,
        ref: str = CURRENT_REF,
    ) -> dict[str, object]:
        return {
            "installed": [
                {
                    "pluginId": "turnecho@turnecho",
                    "version": version,
                    "source": {
                        "source": "git",
                        "url": "https://github.com/rmscoal/turnecho.git",
                        "ref": ref,
                    },
                }
            ]
        }

    def installed_result(
        self,
        plugin_root: Path,
        *,
        version: str = CURRENT_VERSION,
    ) -> dict[str, object]:
        return {
            "pluginId": "turnecho@turnecho",
            "name": "turnecho",
            "marketplaceName": "turnecho",
            "version": version,
            "installedPath": str(plugin_root),
            "authPolicy": "ON_INSTALL",
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
                        self.installed_result(plugin_root),
                        self.installed_payload(plugin_root),
                    ],
                ) as run_json,
                patch.object(install_plugin, "run_checked_command") as run,
            ):
                result = install_plugin.install_plugin(
                    command_path=Path(directory) / "bin" / "turnecho"
                )

        self.assertEqual(result, plugin_root.resolve())
        preflight.assert_called_once_with()
        self.assertEqual(
            run_json.call_args_list[2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
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
                        CURRENT_REF,
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

    def test_fresh_install_repairs_a_dangling_managed_command(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            cache_root = root / "cache" / "turnecho"
            cache_root.mkdir(parents=True)
            plugin_root = self.create_installed_plugin(
                cache_root,
                name=CURRENT_VERSION,
            )
            command_path = root / "bin" / "turnecho"
            command_path.parent.mkdir()
            command_path.symlink_to(cache_root / "0.2.0" / ".venv" / "bin" / "turnecho")

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(install_plugin, "validate_runtime_dependencies"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        {"marketplaces": []},
                        {"installed": []},
                        self.installed_result(plugin_root),
                        self.installed_payload(plugin_root),
                    ],
                ),
                patch.object(install_plugin, "run_checked_command"),
            ):
                install_plugin.install_plugin(command_path=command_path)

            self.assertEqual(
                command_path.resolve(),
                (plugin_root / ".venv" / "bin" / "turnecho").resolve(),
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

    def test_runtime_restore_rejects_an_unexpected_previous_ref(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_installed_plugin(root)

            with (
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        self.installed_result(plugin_root),
                        self.installed_payload(plugin_root),
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
                self.assertRaisesRegex(
                    install_plugin.InstallError,
                    "unexpected TurnEcho release",
                ),
            ):
                install_plugin.restore_plugin_runtime(
                    "v0.1.0",
                    root / "bin" / "turnecho",
                )

        run.assert_not_called()

    def test_missing_install_path_rolls_back_fresh_codex_state(self) -> None:
        with (
            patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
            patch.object(install_plugin, "validate_runtime_dependencies"),
            patch.object(
                install_plugin,
                "run_json_command",
                side_effect=[
                    {"marketplaces": []},
                    {"installed": []},
                    {"pluginId": "turnecho@turnecho"},
                ],
            ),
            patch.object(install_plugin, "run_checked_command") as run,
            self.assertRaises(install_plugin.InstallError),
        ):
            install_plugin.install_plugin()

        self.assertEqual(
            run.call_args_list[-2:],
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
                        self.installed_result(plugin_root),
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

    def test_command_conflict_rolls_back_fresh_codex_state(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_installed_plugin(root)
            command_path = root / "bin" / "turnecho"
            command_path.parent.mkdir()
            command_path.write_text("unrelated", encoding="utf-8")

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(install_plugin, "validate_runtime_dependencies"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        {"marketplaces": []},
                        {"installed": []},
                        self.installed_result(plugin_root),
                        self.installed_payload(plugin_root),
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
                self.assertRaises(CommandInstallError),
            ):
                install_plugin.install_plugin(command_path=command_path)

        self.assertEqual(
            run.call_args_list[-2:],
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

    def test_update_replaces_pinned_marketplace_before_reinstalling(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))
            previous_ref = "v0.1.0"
            previous_payload = self.installed_payload(
                plugin_root,
                version="0.1.0",
                ref=previous_ref,
            )
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
                        previous_payload,
                        self.installed_result(plugin_root),
                        installed_payload,
                    ],
                ) as run_json,
                patch.object(install_plugin, "run_checked_command") as run,
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=Path(directory) / "bin" / "turnecho",
                )

        self.assertEqual(
            run.call_args_list[:2],
            [
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
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "rmscoal/turnecho",
                        "--ref",
                        CURRENT_REF,
                        "--json",
                    ]
                ),
            ],
        )
        self.assertEqual(
            run_json.call_args_list[2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )

    def test_update_rejects_an_unexpected_installed_release(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))
            previous_ref = "v0.1.0"
            previous_payload = self.installed_payload(
                plugin_root,
                version="0.1.0",
                ref=previous_ref,
            )
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
                        previous_payload,
                        self.installed_result(plugin_root),
                        previous_payload,
                        self.installed_result(plugin_root, version="0.1.0"),
                        previous_payload,
                    ],
                ) as run_json,
                patch.object(install_plugin, "run_checked_command") as run,
                self.assertRaisesRegex(
                    install_plugin.InstallError,
                    "unexpected TurnEcho release",
                ),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=Path(directory) / "bin" / "turnecho",
                )

        self.assertEqual(
            run_json.call_args_list[-2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
        self.assertEqual(
            run.call_args_list[-4:],
            [
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
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "rmscoal/turnecho",
                        "--ref",
                        previous_ref,
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

    def test_failed_update_restores_previous_marketplace_and_plugin(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_installed_plugin(root, name="current")
            previous_root = self.create_installed_plugin(
                root,
                name="previous",
                version="0.1.0",
            )
            command_path = root / "bin" / "turnecho"
            previous_ref = "v0.1.0"
            previous_payload = self.installed_payload(
                previous_root,
                version="0.1.0",
                ref=previous_ref,
            )
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
                        marketplace_payload,
                        previous_payload,
                        self.installed_result(plugin_root),
                        installed_payload,
                        self.installed_result(previous_root, version="0.1.0"),
                        previous_payload,
                    ],
                ) as run_json,
                patch.object(
                    install_plugin,
                    "run_checked_command",
                    side_effect=run,
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=command_path,
                )

            self.assertEqual(
                command_path.resolve(),
                (previous_root / ".venv" / "bin" / "turnecho").resolve(),
            )

        self.assertEqual(
            run_json.call_args_list[-2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
        self.assertEqual(
            run_command.call_args_list[-4:],
            [
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
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "rmscoal/turnecho",
                        "--ref",
                        previous_ref,
                        "--json",
                    ]
                ),
                call(
                    [
                        "uv",
                        "sync",
                        "--project",
                        str(previous_root.resolve()),
                        "--no-dev",
                    ]
                ),
                call(
                    [
                        "uv",
                        "run",
                        "--project",
                        str(previous_root.resolve()),
                        "--no-dev",
                        "python",
                        "-m",
                        "turnecho.runtime_preflight",
                    ]
                ),
            ],
        )

    def test_failed_marketplace_replacement_restores_previous_ref(self) -> None:
        with TemporaryDirectory() as directory:
            plugin_root = self.create_installed_plugin(Path(directory))
            previous_ref = "v0.1.0"
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
            add_current_marketplace = [
                "codex",
                "plugin",
                "marketplace",
                "add",
                "rmscoal/turnecho",
                "--ref",
                CURRENT_REF,
                "--json",
            ]

            def run(command: list[str]) -> None:
                if command == add_current_marketplace:
                    raise subprocess.CalledProcessError(1, command)

            with (
                patch.object(install_plugin.shutil, "which", return_value="/bin/tool"),
                patch.object(install_plugin, "validate_runtime_dependencies"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        marketplace_payload,
                        self.installed_payload(
                            plugin_root,
                            version="0.1.0",
                            ref=previous_ref,
                        ),
                    ],
                ),
                patch.object(
                    install_plugin,
                    "run_checked_command",
                    side_effect=run,
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=Path(directory) / "bin" / "turnecho",
                )

        self.assertEqual(
            run_command.call_args_list[-1],
            call(
                [
                    "codex",
                    "plugin",
                    "marketplace",
                    "add",
                    "rmscoal/turnecho",
                    "--ref",
                    previous_ref,
                    "--json",
                ]
            ),
        )

    def test_failed_update_restores_plugin_when_marketplace_was_missing(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_installed_plugin(root, name="current")
            previous_root = self.create_installed_plugin(
                root,
                name="previous",
                version="0.1.0",
            )
            command_path = root / "bin" / "turnecho"
            previous_ref = "v0.1.0"
            previous_payload = self.installed_payload(
                previous_root,
                version="0.1.0",
                ref=previous_ref,
            )
            installed_payload = self.installed_payload(plugin_root)
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
                        previous_payload,
                        self.installed_result(plugin_root),
                        installed_payload,
                        self.installed_result(previous_root, version="0.1.0"),
                        previous_payload,
                    ],
                ) as run_json,
                patch.object(
                    install_plugin,
                    "run_checked_command",
                    side_effect=run,
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=command_path,
                )

            self.assertEqual(
                command_path.resolve(),
                (previous_root / ".venv" / "bin" / "turnecho").resolve(),
            )

        self.assertEqual(
            run_json.call_args_list[-2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
        self.assertEqual(
            run_command.call_args_list[-5:],
            [
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
                call(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "add",
                        "rmscoal/turnecho",
                        "--ref",
                        previous_ref,
                        "--json",
                    ]
                ),
                call(
                    [
                        "uv",
                        "sync",
                        "--project",
                        str(previous_root.resolve()),
                        "--no-dev",
                    ]
                ),
                call(
                    [
                        "uv",
                        "run",
                        "--project",
                        str(previous_root.resolve()),
                        "--no-dev",
                        "python",
                        "-m",
                        "turnecho.runtime_preflight",
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

    def test_marketplace_snapshot_ref_is_used_when_plugin_is_not_installed(
        self,
    ) -> None:
        with TemporaryDirectory() as directory:
            marketplace_root = Path(directory)
            manifest_path = marketplace_root / TURNECHO_MARKETPLACE_MANIFEST_PATH
            manifest_path.parent.mkdir(parents=True)
            manifest_path.write_text(
                """{
  "name": "turnecho",
  "plugins": [
    {
      "name": "turnecho",
      "source": {"source": "url", "ref": "v0.1.0"}
    }
  ]
}
""",
                encoding="utf-8",
            )

            ref = install_plugin.resolve_previous_marketplace_ref(
                {"root": str(marketplace_root)},
                None,
            )

        self.assertEqual(ref, "v0.1.0")

    def test_existing_git_plugin_uses_its_codex_cache_path(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            codex_home = root / "codex-home"
            plugin_root = (
                codex_home / "plugins" / "cache" / "turnecho" / "turnecho" / "0.1.0"
            )
            plugin_root.mkdir(parents=True)
            (plugin_root / "pyproject.toml").write_text(
                "[project]\nname = 'turnecho'\nversion = '0.1.0'\n",
                encoding="utf-8",
            )
            command = plugin_root / ".venv" / "bin" / "turnecho"
            command.parent.mkdir(parents=True)
            command.write_text("#!/bin/sh\n", encoding="utf-8")
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
                        self.installed_payload(
                            plugin_root,
                            version="0.1.0",
                            ref="v0.1.0",
                        ),
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
                patch.dict(
                    install_plugin.os.environ,
                    {"CODEX_HOME": str(codex_home)},
                ),
            ):
                result = install_plugin.install_plugin(
                    command_path=Path(directory) / "bin" / "turnecho"
                )

        self.assertEqual(result, plugin_root.resolve())
        self.assertEqual(
            run.call_args_list,
            [
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


if __name__ == "__main__":
    unittest.main()
