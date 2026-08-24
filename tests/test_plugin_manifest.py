import json
import unittest
from pathlib import Path

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


if __name__ == "__main__":
    unittest.main()
