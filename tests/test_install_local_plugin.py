import json
import subprocess
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import call, patch

PROJECT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(PROJECT_ROOT / "scripts"))

from install_local_plugin import (  # noqa: E402
    InstallError,
    install_plugin,
)
from update_plugin_cachebuster import update_plugin_cachebuster  # noqa: E402


class LocalPluginInstallerTests(unittest.TestCase):
    def create_plugin_root(self, directory: Path) -> Path:
        plugin_root = directory / "turnecho"
        manifest_directory = plugin_root / ".codex-plugin"
        manifest_directory.mkdir(parents=True)
        (manifest_directory / "plugin.json").write_text(
            json.dumps(
                {
                    "name": "turnecho",
                    "version": "0.1.0",
                    "interface": {"category": "Productivity"},
                }
            ),
            encoding="utf-8",
        )
        return plugin_root

    def test_install_creates_link_marketplace_entry_and_calls_codex(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / ".agents" / "plugins" / "marketplace.json"

            with (
                patch(
                    "install_local_plugin.shutil.which", return_value="/usr/bin/codex"
                ),
                patch("install_local_plugin.subprocess.run") as run,
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                )

            self.assertTrue(plugin_link.is_symlink())
            self.assertEqual(plugin_link.resolve(), plugin_root.resolve())
            marketplace = json.loads(marketplace_path.read_text(encoding="utf-8"))
            self.assertEqual(marketplace["name"], "personal")
            self.assertEqual(marketplace["plugins"][0]["name"], "turnecho")
            self.assertEqual(
                marketplace["plugins"][0]["source"]["path"],
                "./plugins/turnecho",
            )
            run.assert_has_calls(
                [
                    call(
                        [
                            "uv",
                            "sync",
                            "--project",
                            str(plugin_root.resolve()),
                            "--no-dev",
                        ],
                        check=True,
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
                        ],
                        check=True,
                    ),
                    call(
                        ["codex", "plugin", "add", "turnecho@personal"],
                        check=True,
                    ),
                ]
            )
            self.assertEqual(run.call_count, 3)

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

            with (
                patch("install_local_plugin.shutil.which", return_value="/usr/bin/uv"),
                patch(
                    "install_local_plugin.subprocess.run",
                    side_effect=subprocess.CalledProcessError(1, ["uv", "sync"]),
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                )

            self.assertFalse(plugin_link.exists())
            self.assertFalse(marketplace_path.exists())

    def test_codex_failure_rolls_back_link_and_marketplace(self) -> None:
        with TemporaryDirectory() as directory:
            root = Path(directory)
            plugin_root = self.create_plugin_root(root)
            plugin_link = root / "plugins" / "turnecho"
            marketplace_path = root / "marketplace.json"
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
                    "install_local_plugin.subprocess.run",
                    side_effect=[
                        None,
                        None,
                        subprocess.CalledProcessError(1, ["codex", "plugin", "add"]),
                    ],
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                install_plugin(
                    plugin_root,
                    plugin_link=plugin_link,
                    marketplace_path=marketplace_path,
                )

            self.assertFalse(plugin_link.exists())
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
            self.assertEqual(manifest["version"], "0.1.0+codex.local-test")
            self.assertEqual(marketplace_path.read_bytes(), original_marketplace)
            run.assert_called_once_with(
                ["codex", "plugin", "add", "turnecho@personal"],
                check=True,
            )


if __name__ == "__main__":
    unittest.main()
