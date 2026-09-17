import argparse
import io
import json
import subprocess
import sys
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import call, patch

from turnecho import install_plugin as github_installer
from turnecho.constant import TURNECHO_PLUGIN_VERSION

PROJECT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PROJECT_ROOT / "scripts"))

from install_local_plugin import (  # noqa: E402
    InstallError,
    install_plugin,
    main,
)
from update_plugin_cachebuster import update_plugin_cachebuster  # noqa: E402


class LocalPluginInstallerTests(unittest.TestCase):
    def prepare_runtime(
        self,
        plugin_root: Path,
        version: str,
        runtime_base: Path,
    ) -> github_installer.RuntimeInstallState:
        runtime_root = runtime_base / version
        runtime_root.mkdir(parents=True)
        (runtime_root / "pyproject.toml").write_bytes(
            (plugin_root / "pyproject.toml").read_bytes()
        )
        (runtime_root / github_installer.TURNECHO_RUNTIME_MARKER_FILE).write_text(
            json.dumps({"name": "turnecho", "version": version}),
            encoding="utf-8",
        )
        command = runtime_root / ".venv" / "bin" / "turnecho"
        command.parent.mkdir(parents=True)
        command.write_text("#!/bin/sh\n", encoding="utf-8")
        return github_installer.RuntimeInstallState(runtime_root, None)

    def create_plugin_root(self, directory: Path) -> Path:
        plugin_root = directory / "turnecho"
        manifest_directory = plugin_root / ".codex-plugin"
        manifest_directory.mkdir(parents=True)
        (manifest_directory / "plugin.json").write_text(
            json.dumps(
                {
                    "name": "turnecho",
                    "version": "0.2.0",
                    "interface": {"category": "Productivity"},
                }
            ),
            encoding="utf-8",
        )
        (plugin_root / "pyproject.toml").write_text(
            "[project]\nname = 'turnecho'\nversion = '0.2.0'\n",
            encoding="utf-8",
        )
        command = plugin_root / ".venv" / "bin" / "turnecho"
        command.parent.mkdir(parents=True)
        command.write_text("#!/bin/sh\n", encoding="utf-8")
        return plugin_root

    def test_install_creates_link_marketplace_entry_and_calls_codex(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / ".agents" / "plugins" / "marketplace.json"
            runtime_base = root / "runtimes"

            with (
                patch(
                    "install_local_plugin.shutil.which", return_value="/usr/bin/codex"
                ),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ) as prepare_runtime,
                patch("install_local_plugin.subprocess.run") as run,
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    command_path=root / "bin" / "turnecho",
                    runtime_base=runtime_base,
                )

            self.assertTrue(plugin_link.is_symlink())
            self.assertEqual(plugin_link.resolve(), plugin_root.resolve())
            command_path = root / "bin" / "turnecho"
            self.assertTrue(command_path.is_symlink())
            self.assertEqual(
                command_path.resolve(),
                (
                    runtime_base
                    / TURNECHO_PLUGIN_VERSION
                    / ".venv"
                    / "bin"
                    / "turnecho"
                ).resolve(),
            )
            marketplace = json.loads(marketplace_path.read_text(encoding="utf-8"))
            self.assertEqual(marketplace["name"], "personal")
            self.assertEqual(marketplace["plugins"][0]["name"], "turnecho")
            self.assertEqual(
                marketplace["plugins"][0]["source"]["path"],
                "./plugins/turnecho",
            )
            prepare_runtime.assert_called_once_with(
                plugin_root.resolve(),
                TURNECHO_PLUGIN_VERSION,
                runtime_base.resolve(),
            )
            run.assert_called_once_with(
                ["codex", "plugin", "add", "turnecho@personal"],
                check=True,
            )

    def test_existing_directory_is_never_replaced(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            plugin_link.mkdir(parents=True)
            marketplace_path = root / "marketplace.json"

            with self.assertRaises(InstallError):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    force=True,
                    run_codex=False,
                    sync_dependencies=False,
                )

            self.assertTrue(plugin_link.is_dir())
            self.assertFalse(plugin_link.is_symlink())

    def test_conflicting_marketplace_entry_requires_force(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"
            marketplace_path.write_text(
                json.dumps(
                    {
                        "name": "personal",
                        "plugins": [
                            {
                                "name": "turnecho",
                                "source": {"source": "git", "path": "other"},
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )

            with self.assertRaises(InstallError):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    run_codex=False,
                    sync_dependencies=False,
                )

            self.assertFalse(plugin_link.exists())

    def test_install_accepts_a_custom_checkout_directory_name(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = root / "my-turnecho-checkout"
            manifest_directory = plugin_root / ".codex-plugin"
            manifest_directory.mkdir(parents=True)
            (manifest_directory / "plugin.json").write_text(
                json.dumps({"name": "turnecho"}),
                encoding="utf-8",
            )
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"

            install_plugin(
                plugin_root,
                plugin_link=plugin_link,
                marketplace_path=marketplace_path,
                run_codex=False,
                sync_dependencies=False,
            )

            self.assertEqual(plugin_link.resolve(), plugin_root.resolve())

    def test_dependency_sync_failure_does_not_modify_installation(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"
            runtime_base = root / "runtimes"

            with (
                patch("install_local_plugin.shutil.which", return_value="/usr/bin/uv"),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=subprocess.CalledProcessError(1, ["uv", "sync"]),
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    command_path=root / "bin" / "turnecho",
                    runtime_base=runtime_base,
                )

            self.assertFalse(plugin_link.exists())
            self.assertFalse(marketplace_path.exists())

    def test_codex_failure_rolls_back_link_and_marketplace(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"
            runtime_base = root / "runtimes"
            original_marketplace = json.dumps(
                {
                    "name": "personal",
                    "interface": {"displayName": "Personal"},
                    "plugins": [],
                },
                indent=2,
            ).encode()
            marketplace_path.write_bytes(original_marketplace)

            with (
                patch(
                    "install_local_plugin.shutil.which", return_value="/usr/bin/tool"
                ),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ),
                patch(
                    "install_local_plugin.subprocess.run",
                    side_effect=subprocess.CalledProcessError(
                        1, ["codex", "plugin", "add"]
                    ),
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    command_path=root / "bin" / "turnecho",
                    runtime_base=runtime_base,
                )

            self.assertFalse(plugin_link.exists())
            self.assertFalse((root / "bin" / "turnecho").exists())
            self.assertFalse((runtime_base / TURNECHO_PLUGIN_VERSION).exists())
            self.assertEqual(marketplace_path.read_bytes(), original_marketplace)

    def test_update_uses_cachebuster_and_preserves_marketplace(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"

            install_plugin(
                plugin_root,
                plugin_link=plugin_link,
                marketplace_path=marketplace_path,
                run_codex=False,
                sync_dependencies=False,
            )
            original_marketplace = marketplace_path.read_bytes()

            with (
                patch(
                    "install_local_plugin.shutil.which", return_value="/usr/bin/codex"
                ),
                patch("install_local_plugin.subprocess.run") as run,
                patch(
                    "install_local_plugin.update_plugin_cachebuster",
                    side_effect=lambda path: update_plugin_cachebuster(
                        path, cachebuster="local-test"
                    ),
                ),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    sync_dependencies=False,
                    update=True,
                )

            manifest = json.loads(
                (plugin_root / ".codex-plugin" / "plugin.json").read_text(
                    encoding="utf-8"
                )
            )
            self.assertEqual(manifest["version"], "0.2.0+codex.local-test")
            self.assertEqual(marketplace_path.read_bytes(), original_marketplace)
            run.assert_called_once_with(
                ["codex", "plugin", "add", "turnecho@personal"],
                check=True,
            )

    def test_install_runs_claude_flow_when_enabled(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ),
                patch("install_local_plugin.run_json_list_command") as run_json_list,
                patch("install_local_plugin.run_checked_command") as run,
            ):
                run_json_list.side_effect = [[], []]
                install_plugin(
                    plugin_root,
                    plugin_link=root / "plugins" / "turnecho",
                    marketplace_path=root / "marketplace.json",
                    command_path=root / "bin" / "turnecho",
                    runtime_base=root / "runtimes",
                    run_codex=False,
                    run_claude=True,
                )

            self.assertEqual(
                run.call_args_list,
                [
                    call(
                        [
                            "claude",
                            "plugin",
                            "marketplace",
                            "add",
                            str(plugin_root.resolve()),
                            "--scope",
                            "user",
                        ]
                    ),
                    call(
                        [
                            "claude",
                            "plugin",
                            "install",
                            "turnecho@turnecho",
                            "--scope",
                            "user",
                        ]
                    ),
                ],
            )

    def test_install_skips_existing_claude_marketplace_and_plugin(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ),
                patch(
                    "install_local_plugin.run_json_list_command",
                    side_effect=[
                        [
                            {
                                "name": "turnecho",
                                "source": "directory",
                                "path": str(plugin_root.resolve()),
                            }
                        ],
                        [{"id": "turnecho@turnecho"}],
                    ],
                ),
                patch("install_local_plugin.run_checked_command") as run,
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=root / "plugins" / "turnecho",
                    marketplace_path=root / "marketplace.json",
                    command_path=root / "bin" / "turnecho",
                    runtime_base=root / "runtimes",
                    run_codex=False,
                    run_claude=True,
                )

            run.assert_not_called()

    def test_claude_marketplace_from_other_source_is_rejected(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ),
                patch(
                    "install_local_plugin.run_json_list_command",
                    side_effect=[
                        [
                            {
                                "name": "turnecho",
                                "source": "github",
                                "repo": "rmscoal/turnecho",
                            }
                        ],
                    ],
                ),
                patch("install_local_plugin.run_checked_command") as run,
                self.assertRaisesRegex(InstallError, "different source"),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=root / "plugins" / "turnecho",
                    marketplace_path=root / "marketplace.json",
                    command_path=root / "bin" / "turnecho",
                    runtime_base=root / "runtimes",
                    run_codex=False,
                    run_claude=True,
                )

            run.assert_not_called()

    def test_update_refreshes_installed_claude_plugin(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"

            install_plugin(
                plugin_root,
                plugin_link=plugin_link,
                marketplace_path=marketplace_path,
                run_codex=False,
                sync_dependencies=False,
            )

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.run_json_list_command",
                    side_effect=[
                        [
                            {
                                "name": "turnecho",
                                "source": "directory",
                                "path": str(plugin_root.resolve()),
                            }
                        ],
                        [{"id": "turnecho@turnecho"}],
                    ],
                ),
                patch("install_local_plugin.run_checked_command") as run,
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    run_codex=False,
                    run_claude=True,
                    sync_dependencies=False,
                    update=True,
                )

            self.assertEqual(
                run.call_args_list,
                [
                    call(
                        [
                            "claude",
                            "plugin",
                            "uninstall",
                            "turnecho@turnecho",
                            "--scope",
                            "user",
                        ]
                    ),
                    call(
                        [
                            "claude",
                            "plugin",
                            "install",
                            "turnecho@turnecho",
                            "--scope",
                            "user",
                        ]
                    ),
                ],
            )

    def test_claude_only_install_skips_codex_files(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.prepare_installed_runtime",
                    side_effect=self.prepare_runtime,
                ),
                patch("install_local_plugin.run_json_list_command") as run_json_list,
                patch("install_local_plugin.run_checked_command") as run,
            ):
                run_json_list.side_effect = [[], []]
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                    command_path=root / "bin" / "turnecho",
                    runtime_base=root / "runtimes",
                    run_codex=False,
                    run_claude=True,
                    prepare_codex=False,
                )

            self.assertFalse(plugin_link.exists())
            self.assertFalse(marketplace_path.exists())
            self.assertTrue((root / "bin" / "turnecho").is_symlink())
            self.assertEqual(run.call_count, 2)

    def test_claude_only_update_leaves_codex_manifest_untouched(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            manifest_path = plugin_root / ".codex-plugin" / "plugin.json"
            original_manifest = manifest_path.read_bytes()

            with (
                patch("install_local_plugin.shutil.which", return_value="/bin/tool"),
                patch(
                    "install_local_plugin.run_json_list_command",
                    side_effect=[
                        [
                            {
                                "name": "turnecho",
                                "source": "directory",
                                "path": str(plugin_root.resolve()),
                            }
                        ],
                        [{"id": "turnecho@turnecho"}],
                    ],
                ),
                patch("install_local_plugin.run_checked_command") as run,
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=root / "plugins" / "turnecho",
                    marketplace_path=root / "marketplace.json",
                    run_codex=False,
                    run_claude=True,
                    prepare_codex=False,
                    sync_dependencies=False,
                    update=True,
                )

            self.assertEqual(manifest_path.read_bytes(), original_manifest)
            self.assertFalse((root / "plugins" / "turnecho").exists())
            self.assertFalse((root / "marketplace.json").exists())
            self.assertEqual(run.call_count, 2)

    def test_dry_run_without_codex_omits_codex_lines(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            output = io.StringIO()

            with redirect_stdout(output):
                install_plugin(
                    plugin_root,
                    plugin_link=root / "plugins" / "turnecho",
                    marketplace_path=root / "marketplace.json",
                    dry_run=True,
                    run_codex=False,
                    run_claude=True,
                    prepare_codex=False,
                    update=True,
                )

            report = output.getvalue()
            self.assertNotIn("Would link", report)
            self.assertNotIn("Would update marketplace", report)
            self.assertNotIn("update_plugin_cachebuster", report)
            self.assertIn("claude plugin install turnecho@turnecho", report)

    def run_main_with_hosts(
        self,
        detected_hosts: list[str],
        **overrides: bool,
    ) -> tuple[int, dict[str, bool]]:
        """Run main() with mocked detection and capture its host flags."""
        options: dict[str, bool] = {
            "force": False,
            "dry_run": False,
            "skip_codex": False,
            "skip_claude": False,
            "skip_dependency_sync": True,
            "update": False,
        }
        options.update(overrides)
        args = argparse.Namespace(**options)
        with (
            patch("install_local_plugin.parse_args", return_value=args),
            patch("install_local_plugin.detect_hosts", return_value=detected_hosts),
            patch("install_local_plugin.install_plugin") as install,
        ):
            exit_code = main()
        if install.call_count:
            flags = install.call_args.kwargs
            return exit_code, {
                "run_codex": flags["run_codex"],
                "run_claude": flags["run_claude"],
                "prepare_codex": flags["prepare_codex"],
            }
        return exit_code, {}

    def test_main_installs_into_each_detected_host(self) -> None:
        exit_code, flags = self.run_main_with_hosts(["codex", "claude_code"])
        self.assertEqual(exit_code, 0)
        self.assertEqual(
            flags, {"run_codex": True, "run_claude": True, "prepare_codex": True}
        )

    def test_main_skips_undetected_codex_files(self) -> None:
        exit_code, flags = self.run_main_with_hosts(["claude_code"])
        self.assertEqual(exit_code, 0)
        self.assertEqual(
            flags, {"run_codex": False, "run_claude": True, "prepare_codex": False}
        )

    def test_main_skip_codex_still_prepares_files_for_manual_add(self) -> None:
        exit_code, flags = self.run_main_with_hosts(
            ["codex", "claude_code"], skip_codex=True
        )
        self.assertEqual(exit_code, 0)
        self.assertEqual(
            flags, {"run_codex": False, "run_claude": True, "prepare_codex": True}
        )

    def test_main_errors_when_no_host_detected(self) -> None:
        exit_code, flags = self.run_main_with_hosts([])
        self.assertEqual(exit_code, 1)
        self.assertEqual(flags, {})


if __name__ == "__main__":
    unittest.main()
