import json
import unittest
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[1]


class RepositoryMarketplaceTests(unittest.TestCase):
    def test_marketplace_installs_the_root_plugin_from_github(self) -> None:
        marketplace_path = PROJECT_ROOT / ".agents" / "plugins" / "marketplace.json"
        marketplace = json.loads(marketplace_path.read_text(encoding="utf-8"))

        self.assertEqual(marketplace["name"], "turnecho")
        self.assertEqual(marketplace["interface"]["displayName"], "TurnEcho")
        self.assertEqual(len(marketplace["plugins"]), 1)

        plugin = marketplace["plugins"][0]
        self.assertEqual(plugin["name"], "turnecho")
        self.assertEqual(
            plugin["source"],
            {
                "source": "url",
                "url": "https://github.com/rmscoal/turnecho.git",
                "ref": "main",
            },
        )
        self.assertEqual(
            plugin["policy"],
            {
                "installation": "AVAILABLE",
                "authentication": "ON_INSTALL",
            },
        )
        self.assertEqual(plugin["category"], "Productivity")


if __name__ == "__main__":
    unittest.main()
