import json
import tomllib
import unittest
from pathlib import Path

from turnecho import install_plugin

PROJECT_ROOT = Path(__file__).resolve().parents[1]


class PluginManifestTests(unittest.TestCase):
    def test_manifest_exposes_the_configuration_skill(self) -> None:
        manifest_path = PROJECT_ROOT / ".codex-plugin" / "plugin.json"
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

        self.assertEqual(manifest["skills"], "./skills/")

        skills_root = PROJECT_ROOT / manifest["skills"]
        skill_directories = sorted(
            path.name for path in skills_root.iterdir() if path.is_dir()
        )
        self.assertEqual(skill_directories, ["turnecho-config"])
        self.assertTrue((skills_root / "turnecho-config" / "SKILL.md").is_file())

    def test_release_versions_are_aligned(self) -> None:
        manifest = json.loads(
            (PROJECT_ROOT / ".codex-plugin" / "plugin.json").read_text(encoding="utf-8")
        )
        project = tomllib.loads(
            (PROJECT_ROOT / "pyproject.toml").read_text(encoding="utf-8")
        )
        marketplace = json.loads(
            (PROJECT_ROOT / ".agents" / "plugins" / "marketplace.json").read_text(
                encoding="utf-8"
            )
        )

        version = project["project"]["version"]
        marketplace_ref = marketplace["plugins"][0]["source"]["ref"]
        self.assertEqual(manifest["version"], version)
        self.assertEqual(marketplace_ref, f"v{version}")
        self.assertEqual(install_plugin.MARKETPLACE_REF, marketplace_ref)


if __name__ == "__main__":
    unittest.main()
