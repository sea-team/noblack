from __future__ import annotations

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]


class ModelSpecContractTests(unittest.TestCase):
    def test_torch_distributed_is_not_excluded(self) -> None:
        source = (ROOT / "packaging" / "noblack-model.spec").read_text(
            encoding="utf-8"
        )

        self.assertNotIn('"torch.distributed"', source)

    def test_torch_testing_is_not_excluded(self) -> None:
        source = (ROOT / "packaging" / "noblack-model.spec").read_text(
            encoding="utf-8"
        )

        self.assertNotIn('"torch.testing"', source)

    def test_windows_backports_namespace_is_collected(self) -> None:
        source = (ROOT / "packaging" / "noblack-model.spec").read_text(
            encoding="utf-8"
        )

        self.assertIn('collect_submodules("backports")', source)

    def test_python_311_build_installs_backports_tarfile(self) -> None:
        requirements = (
            ROOT / "model_service" / "requirements-build.txt"
        ).read_text(encoding="utf-8")

        self.assertIn(
            'backports.tarfile==1.2.0; python_version < "3.12"',
            requirements,
        )


if __name__ == "__main__":
    unittest.main()
