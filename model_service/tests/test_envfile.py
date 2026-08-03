from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from noblack_model import envfile


class EnvFileTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name).resolve()

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def write(self, content: str) -> Path:
        path = self.root / envfile.DEFAULT_NAME
        path.write_text(content, encoding="utf-8")
        return path

    def test_load_applies_keys(self) -> None:
        path = self.write(
            "# 注释\n"
            "NB_MODEL_PORT=18091\n"
            "NB_ADDR=:18080\n"
            "\n"
            "NB_MODEL_THREADS=4\n"
        )
        with mock.patch.dict(os.environ, {}, clear=True):
            applied = envfile.load(path)
            self.assertEqual(applied, 3)
            self.assertEqual(os.environ["NB_MODEL_PORT"], "18091")
            self.assertEqual(os.environ["NB_ADDR"], ":18080")
            self.assertEqual(os.environ["NB_MODEL_THREADS"], "4")

    def test_existing_environment_wins(self) -> None:
        """已存在的环境变量优先, 与启动脚本行为一致。"""
        path = self.write("NB_MODEL_PORT=18091\n")
        with mock.patch.dict(os.environ, {"NB_MODEL_PORT": "9999"}, clear=True):
            envfile.load(path)
            self.assertEqual(os.environ["NB_MODEL_PORT"], "9999")

    def test_foreign_keys_ignored(self) -> None:
        """非 NB_ 前缀的键不得写入环境, 避免配置文件污染进程环境。"""
        path = self.write("PATH=/evil\nHOME=/evil\nnb_lower=x\nNB_OK=1\n")
        with mock.patch.dict(os.environ, {"PATH": "/original"}, clear=True):
            applied = envfile.load(path)
            self.assertEqual(applied, 1)
            self.assertEqual(os.environ["PATH"], "/original")
            self.assertEqual(os.environ["NB_OK"], "1")

    def test_value_edge_cases(self) -> None:
        path = self.write(
            "NB_EMPTY=\n"
            "NB_SPACES=  值两侧有空白  \n"
            "NB_EQUALS=a=b=c\n"
            "# NB_COMMENTED=x\n"
            "NB_NOEQUALS\n"
        )
        with mock.patch.dict(os.environ, {}, clear=True):
            envfile.load(path)
            self.assertEqual(os.environ["NB_SPACES"], "值两侧有空白")
            # 值内等号必须保留 (令牌等场景可能含 =)。
            self.assertEqual(os.environ["NB_EQUALS"], "a=b=c")
            self.assertEqual(os.environ["NB_EMPTY"], "")
            self.assertNotIn("NB_COMMENTED", os.environ)

    def test_bom_is_tolerated(self) -> None:
        """Windows 记事本另存可能带 BOM, 不应导致首个键失效。"""
        path = self.root / envfile.DEFAULT_NAME
        path.write_text("NB_MODEL_PORT=18091\n", encoding="utf-8-sig")
        with mock.patch.dict(os.environ, {}, clear=True):
            envfile.load(path)
            self.assertEqual(os.environ["NB_MODEL_PORT"], "18091")

    def test_crlf_is_tolerated(self) -> None:
        """Windows 编辑器保存的 CRLF 行尾不应残留在值里。"""
        path = self.root / envfile.DEFAULT_NAME
        path.write_bytes(b"NB_MODEL_PORT=18091\r\nNB_ADDR=:18080\r\n")
        with mock.patch.dict(os.environ, {}, clear=True):
            envfile.load(path)
            self.assertEqual(os.environ["NB_MODEL_PORT"], "18091")
            self.assertEqual(os.environ["NB_ADDR"], ":18080")

    def test_missing_file_is_not_an_error(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(envfile.load(self.root / "nonexistent.env"), 0)

    def test_resolve_prefers_package_root(self) -> None:
        path = self.write("NB_MODEL_PORT=18091\n")
        with mock.patch.dict(os.environ, {"NB_PACKAGE_ROOT": str(self.root)}, clear=True):
            self.assertEqual(envfile.resolve(), path)

    def test_resolve_returns_none_when_absent(self) -> None:
        with mock.patch.dict(os.environ, {"NB_PACKAGE_ROOT": str(self.root)}, clear=True):
            self.assertIsNone(envfile.resolve())


if __name__ == "__main__":
    unittest.main()
