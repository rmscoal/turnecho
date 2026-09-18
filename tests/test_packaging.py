import os
import subprocess
import sys
import tomllib
import unittest
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]

# Modules behind the console scripts and hook entries. Every one must import
# with only the standard library so `uvx --from <tag|path>` never resolves
# the TTS graph.
STDLIB_ONLY_MODULES = (
    "turnecho.cli",
    "turnecho.install_plugin",
    "turnecho.prompt_hook",
    "turnecho.runtime_preflight",
    "turnecho.stop_hook",
    "turnecho.worker",
)


class PackagingTests(unittest.TestCase):
    def test_audio_dependencies_are_optional(self) -> None:
        project = tomllib.loads(
            (PROJECT_ROOT / "pyproject.toml").read_text(encoding="utf-8")
        )["project"]

        self.assertEqual(project.get("dependencies", []), [])
        audio = project["optional-dependencies"]["audio"]
        self.assertTrue(any(entry == "kittentts" for entry in audio))
        self.assertTrue(any(entry.startswith("sounddevice") for entry in audio))
        self.assertTrue(any(entry.startswith("curated-tokenizers") for entry in audio))
        self.assertTrue(any(entry.startswith("spacy") for entry in audio))

    def test_console_scripts_cover_cli_hook_and_installer(self) -> None:
        project = tomllib.loads(
            (PROJECT_ROOT / "pyproject.toml").read_text(encoding="utf-8")
        )["project"]

        self.assertEqual(
            project["scripts"],
            {
                "turnecho": "turnecho.cli:main",
                "turnecho-hook": "turnecho.stop_hook:main",
                "turnecho-install": "turnecho.install_plugin:main",
            },
        )

    def test_entry_modules_import_without_audio_dependencies(self) -> None:
        script = (
            "import sys; "
            "[sys.modules.__setitem__(name, None) "
            "for name in ('kittentts', 'sounddevice')]; "
            f"import {', '.join(STDLIB_ONLY_MODULES)}"
        )
        environment = os.environ.copy()
        environment["PYTHONPATH"] = str(PROJECT_ROOT / "src")

        completed = subprocess.run(
            [sys.executable, "-c", script],
            capture_output=True,
            cwd=PROJECT_ROOT,
            env=environment,
            text=True,
        )

        self.assertEqual(completed.returncode, 0, completed.stderr)


if __name__ == "__main__":
    unittest.main()
