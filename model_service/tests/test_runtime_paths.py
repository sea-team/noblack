from __future__ import annotations

import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from noblack_model import runtime_paths


class RuntimePathTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name).resolve()

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def test_package_root_prefers_environment(self) -> None:
        with mock.patch.dict(os.environ, {"NB_PACKAGE_ROOT": str(self.root)}):
            self.assertEqual(runtime_paths.resolve_package_root(), self.root)

    def test_package_root_uses_executable_directory_when_frozen(self) -> None:
        executable = self.root / "noblack-model"
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("NB_PACKAGE_ROOT", None)
            with mock.patch.object(sys, "frozen", True, create=True), mock.patch.object(
                sys,
                "executable",
                str(executable),
            ):
                self.assertEqual(runtime_paths.resolve_package_root(), self.root)

    def test_package_root_uses_repository_root_in_source_mode(self) -> None:
        expected = Path(__file__).resolve().parents[2]
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("NB_PACKAGE_ROOT", None)
            with mock.patch.object(sys, "frozen", False, create=True):
                self.assertEqual(runtime_paths.resolve_package_root(), expected)


if __name__ == "__main__":
    unittest.main()
