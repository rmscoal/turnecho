import json
import shutil
import subprocess
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import call, patch

from turnecho import install_plugin
from turnecho.cli import CommandInstallError
from turnecho.constant import TURNECHO_MARKETPLACE_MANIFEST_PATH

CURRENT_VERSION = "0.2.4"
CURRENT_REF = f"v{CURRENT_VERSION}"


class GitHubPluginInstallerTests(unittest.TestCase):
    def setUp(self) -> None:
        self.runtime_patcher = patch.object(
            install_plugin,
            "prepare_installed_runtime",
            side_effect=self.prepare_runtime,
        )
        self.runtime_mock = self.runtime_patcher.start()
        self.addCleanup(self.runtime_patcher.stop)

    def prepare_runtime(
        self,
        plugin_root: Path,
        version: str,
        runtime_base: Path,
    ) -> install_plugin.RuntimeInstallState:
        del runtime_base
        runtime_root = plugin_root.parent / "managed-runtimes" / version
        runtime_root.mkdir(parents=True, exist_ok=True)
        (runtime_root / "pyproject.toml").write_text(
            f"[project]\nname = 'turnecho'\nversion = '{version}'\n",
            encoding="utf-8",
        )
        (runtime_root / install_plugin.TURNECHO_RUNTIME_MARKER_FILE).write_text(
            json.dumps({"name": "turnecho", "version": version}),
            encoding="utf-8",
        )
        command = runtime_root / ".venv" / "bin" / "turnecho"
        command.parent.mkdir(parents=True, exist_ok=True)
        command.write_text("#!/bin/sh\n", encoding="utf-8")
        return install_plugin.RuntimeInstallState(runtime_root, None)

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

        self.assertEqual(
            result,
            (plugin_root.parent / "managed-runtimes" / CURRENT_VERSION).resolve(),
        )
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
                install_plugin.install_plugin(
                    command_path=command_path,
                    runtime_base=cache_root,
                )

            self.assertEqual(
                command_path.resolve(),
                (
                    plugin_root.parent
                    / "managed-runtimes"
                    / CURRENT_VERSION
                    / ".venv"
                    / "bin"
                    / "turnecho"
                ).resolve(),
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
                    root / "runtimes",
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
            runtime_error = subprocess.CalledProcessError(1, ["uv", "sync"])
            self.runtime_mock.side_effect = runtime_error

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
            run.call_args_list[-2:],
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

            def prepare_runtime(
                candidate_root: Path,
                version: str,
                runtime_base: Path,
            ) -> install_plugin.RuntimeInstallState:
                if candidate_root == plugin_root.resolve():
                    raise subprocess.CalledProcessError(1, ["uv", "sync"])
                return self.prepare_runtime(candidate_root, version, runtime_base)

            self.runtime_mock.side_effect = prepare_runtime

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
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=command_path,
                )

            self.assertEqual(
                command_path.resolve(),
                (
                    previous_root.parent
                    / "managed-runtimes"
                    / "0.1.0"
                    / ".venv"
                    / "bin"
                    / "turnecho"
                ).resolve(),
            )

        self.assertEqual(
            run_json.call_args_list[-2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
        self.assertEqual(
            run_command.call_args_list[-2:],
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

            def prepare_runtime(
                candidate_root: Path,
                version: str,
                runtime_base: Path,
            ) -> install_plugin.RuntimeInstallState:
                if candidate_root == plugin_root.resolve():
                    raise subprocess.CalledProcessError(1, ["uv", "sync"])
                return self.prepare_runtime(candidate_root, version, runtime_base)

            self.runtime_mock.side_effect = prepare_runtime

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
                ) as run_command,
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.install_plugin(
                    update=True,
                    command_path=command_path,
                )

            self.assertEqual(
                command_path.resolve(),
                (
                    previous_root.parent
                    / "managed-runtimes"
                    / "0.1.0"
                    / ".venv"
                    / "bin"
                    / "turnecho"
                ).resolve(),
            )

        self.assertEqual(
            run_json.call_args_list[-2],
            call(["codex", "plugin", "add", "turnecho@turnecho", "--json"]),
        )
        self.assertEqual(
            run_command.call_args_list[-3:],
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

        self.assertEqual(
            result,
            (plugin_root.parent / "managed-runtimes" / "0.1.0").resolve(),
        )
        run.assert_not_called()


class RuntimeLifecycleTests(unittest.TestCase):
    def create_plugin_source(self, root: Path, version: str = CURRENT_VERSION) -> Path:
        plugin_root = root / "plugin-cache" / version
        plugin_root.mkdir(parents=True)
        (plugin_root / "pyproject.toml").write_text(
            f"[project]\nname = 'turnecho'\nversion = '{version}'\n",
            encoding="utf-8",
        )
        return plugin_root

    def fake_runtime_command(
        self,
        command: list[str],
        *,
        environment: dict[str, str] | None = None,
        suppress_stdout: bool = False,
    ) -> None:
        del suppress_stdout
        if command[:2] != ["uv", "sync"]:
            return
        self.assertIsNotNone(environment)
        environment_root = Path(environment["UV_PROJECT_ENVIRONMENT"])
        bin_directory = environment_root / "bin"
        bin_directory.mkdir(parents=True)
        runtime_python = bin_directory / "python"
        runtime_python.write_text("python", encoding="utf-8")
        (bin_directory / "turnecho").write_text(
            f"#!{runtime_python}\n",
            encoding="utf-8",
        )

    def test_runtime_is_built_at_its_permanent_path(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_source(root)
            runtime_base = root / "user-data" / "runtimes"
            with patch.object(
                install_plugin,
                "run_checked_command",
                side_effect=self.fake_runtime_command,
            ) as run:
                state = install_plugin.prepare_installed_runtime(
                    plugin_root,
                    CURRENT_VERSION,
                    runtime_base,
                )
                install_plugin.commit_runtime_install(state)

            self.assertIn("--no-editable", run.call_args_list[0].args[0])
            sync_environment = run.call_args_list[0].kwargs["environment"]
            self.assertNotIn("VIRTUAL_ENV", sync_environment)
            sync_environment_path = Path(sync_environment["UV_PROJECT_ENVIRONMENT"])
            self.assertEqual(
                sync_environment_path,
                runtime_base.resolve() / CURRENT_VERSION / ".venv",
            )
            runtime_command = state.runtime_root / ".venv" / "bin" / "turnecho"
            self.assertEqual(
                runtime_command.read_text(encoding="utf-8").splitlines()[0],
                f"#!{state.runtime_root / '.venv' / 'bin' / 'python'}",
            )
            self.assertEqual(
                run.call_args_list[-2:],
                [
                    call(
                        [
                            str(state.runtime_root / ".venv" / "bin" / "python"),
                            "-m",
                            "turnecho.runtime_preflight",
                        ]
                    ),
                    call(
                        [str(runtime_command), "--version"],
                        suppress_stdout=True,
                    ),
                ],
            )
            shutil.rmtree(plugin_root)
            self.assertTrue(runtime_command.is_file())

    def test_same_version_repair_can_be_rolled_back(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_source(root)
            runtime_base = root / "runtimes"
            with patch.object(
                install_plugin,
                "run_checked_command",
                side_effect=self.fake_runtime_command,
            ):
                first = install_plugin.prepare_installed_runtime(
                    plugin_root,
                    CURRENT_VERSION,
                    runtime_base,
                )
                install_plugin.commit_runtime_install(first)
                command = first.runtime_root / ".venv" / "bin" / "turnecho"
                command.write_text("previous", encoding="utf-8")

                replacement = install_plugin.prepare_installed_runtime(
                    plugin_root,
                    CURRENT_VERSION,
                    runtime_base,
                )
                self.assertIsNotNone(replacement.backup_root)
                install_plugin.rollback_runtime_install(replacement)

            self.assertEqual(command.read_text(encoding="utf-8"), "previous")

    def test_failed_runtime_command_validation_restores_previous_runtime(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_source(root)
            runtime_base = root / "runtimes"
            with patch.object(
                install_plugin,
                "run_checked_command",
                side_effect=self.fake_runtime_command,
            ):
                first = install_plugin.prepare_installed_runtime(
                    plugin_root,
                    CURRENT_VERSION,
                    runtime_base,
                )
                install_plugin.commit_runtime_install(first)

            command = first.runtime_root / ".venv" / "bin" / "turnecho"
            command.write_text("previous", encoding="utf-8")

            def fail_runtime_command(
                candidate: list[str],
                *,
                environment: dict[str, str] | None = None,
                suppress_stdout: bool = False,
            ) -> None:
                self.fake_runtime_command(
                    candidate,
                    environment=environment,
                    suppress_stdout=suppress_stdout,
                )
                if candidate[-1:] == ["--version"]:
                    raise subprocess.CalledProcessError(1, candidate)

            with (
                patch.object(
                    install_plugin,
                    "run_checked_command",
                    side_effect=fail_runtime_command,
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin.prepare_installed_runtime(
                    plugin_root,
                    CURRENT_VERSION,
                    runtime_base,
                )

            self.assertEqual(command.read_text(encoding="utf-8"), "previous")
            self.assertEqual(
                [path.name for path in runtime_base.iterdir()],
                [CURRENT_VERSION],
            )

    def test_uninstall_removes_managed_runtime_and_command(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            runtime_base = root / "runtimes"
            runtime_root = runtime_base / CURRENT_VERSION
            command = runtime_root / ".venv" / "bin" / "turnecho"
            command.parent.mkdir(parents=True)
            command.write_text("turnecho", encoding="utf-8")
            (runtime_root / "pyproject.toml").write_text(
                "[project]\nname = 'turnecho'\n",
                encoding="utf-8",
            )
            (runtime_root / install_plugin.TURNECHO_RUNTIME_MARKER_FILE).write_text(
                json.dumps({"name": "turnecho", "version": CURRENT_VERSION}),
                encoding="utf-8",
            )
            command_path = root / "bin" / "turnecho"
            command_path.parent.mkdir()
            command_path.symlink_to(command)
            marketplace = {
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
                patch.object(install_plugin.shutil, "which", return_value="/bin/codex"),
                patch.object(
                    install_plugin,
                    "run_json_command",
                    side_effect=[
                        {"installed": [{"pluginId": "turnecho@turnecho"}]},
                        marketplace,
                    ],
                ),
                patch.object(install_plugin, "run_checked_command") as run,
            ):
                command_removed, runtime_count = install_plugin.uninstall_plugin(
                    command_path=command_path,
                    runtime_base=runtime_base,
                )

            self.assertTrue(command_removed)
            self.assertEqual(runtime_count, 1)
            self.assertFalse(command_path.is_symlink())
            self.assertFalse(runtime_root.exists())
            self.assertEqual(
                run.call_args_list,
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

    def test_runtime_cleanup_leaves_unmarked_directories_unchanged(self) -> None:
        with TemporaryDirectory() as directory:
            runtime_base = Path(directory) / "runtimes"
            unrelated = runtime_base / "custom"
            unrelated.mkdir(parents=True)
            probe = unrelated / "keep"
            probe.write_text("unrelated", encoding="utf-8")

            removed = install_plugin.remove_managed_runtimes(runtime_base)

            self.assertEqual(removed, 0)
            self.assertEqual(probe.read_text(encoding="utf-8"), "unrelated")


if __name__ == "__main__":
    unittest.main()
