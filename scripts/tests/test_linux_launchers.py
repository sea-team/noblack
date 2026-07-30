from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
LINUX = ROOT / "packaging" / "linux"


class LinuxLauncherContractTests(unittest.TestCase):
    def read(self, name: str) -> str:
        return (LINUX / name).read_text(encoding="utf-8")

    def test_full_launcher_uses_package_paths_and_health_checks(self) -> None:
        source = self.read("start.sh")

        self.assertIn('ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"', source)
        self.assertIn('data/noblack.pid', source)
        self.assertIn('data/noblack-model.pid', source)
        self.assertIn('logs/noblack.log', source)
        self.assertIn('logs/noblack-model.log', source)
        self.assertIn("/health", source)
        self.assertIn("noblack-model", source)
        self.assertIn("-model-service-url", source)

    def test_keywords_launcher_disables_models(self) -> None:
        source = self.read("start-keywords-only.sh")

        self.assertIn("NB_KEYWORDS_ONLY=1", source)
        self.assertIn("start.sh", source)

    def test_stop_launcher_only_uses_recorded_verified_pids(self) -> None:
        source = self.read("stop.sh")

        self.assertIn("/proc/$pid/exe", source)
        self.assertIn('data/noblack.pid', source)
        self.assertIn('data/noblack-model.pid', source)
        self.assertNotIn("pkill", source)
        self.assertNotIn("killall", source)


if __name__ == "__main__":
    unittest.main()
