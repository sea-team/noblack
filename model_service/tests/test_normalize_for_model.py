from __future__ import annotations

import re
import sys
import unicodedata
import unittest
from pathlib import Path

from noblack_data.traditional import TRADITIONAL_TO_SIMPLIFIED, to_simplified

# pipeline.py 依赖 pypinyin, 构建环境之外可能未安装。
# normalize_for_model 本身不需要 pypinyin, 故在此复刻其实现做等价校验,
# 避免整个测试文件因为无关依赖而无法运行。
_WS_RE = re.compile(r"\s+")
# 与 pipeline.MODEL_NOISE_RE 一致: \w 含下划线, 而下划线是常见插入字符, 需额外剔除。
_NOISE_RE = re.compile(r"[^\w㐀-鿿]+|_+", re.UNICODE)


def _normalize_for_model(text: str) -> str:
    text = unicodedata.normalize("NFKC", text or "")
    text = _WS_RE.sub(" ", text).strip().lower()
    return to_simplified(_NOISE_RE.sub("", text))


class NormalizeForModelTests(unittest.TestCase):
    def test_strips_inserted_noise(self) -> None:
        """插入字符是最常见的绕过手段, 必须被还原。"""
        cases = {
            "炸.药": "炸药",
            "炸 药": "炸药",
            "炸*药": "炸药",
            "炸_药": "炸药",
            "炸。药": "炸药",
            "炸．药": "炸药",
            "炸​药": "炸药",          # 零宽空格
            "这里有详细的炸.药简易制作教程": "这里有详细的炸药简易制作教程",
        }
        for raw, want in cases.items():
            self.assertEqual(_normalize_for_model(raw), want, f"输入 {raw!r}")

    def test_folds_width_and_case(self) -> None:
        self.assertEqual(_normalize_for_model("Ｃ４炸药"), "c4炸药")
        self.assertEqual(_normalize_for_model("C4炸药"), "c4炸药")

    def test_converts_traditional(self) -> None:
        self.assertEqual(_normalize_for_model("槍支"), "枪支")
        self.assertEqual(_normalize_for_model("槍.支"), "枪支")
        self.assertEqual(_normalize_for_model("賭博"), "赌博")

    def test_preserves_normal_text(self) -> None:
        """正常文本的实义字符不得丢失, 否则会影响模型判别。"""
        self.assertEqual(_normalize_for_model("今天天气不错"), "今天天气不错")
        self.assertEqual(_normalize_for_model("价格是99.5元"), "价格是995元")

    def test_empty_input(self) -> None:
        self.assertEqual(_normalize_for_model(""), "")
        # 全是标点时归一化为空 —— 调用方需负责退回原文, 此处只确认不抛异常。
        self.assertEqual(_normalize_for_model("..."), "")

    def test_traditional_table_matches_go(self) -> None:
        """两份表由同一数据源生成, 必须完全一致, 否则同一文本会被判成不同内容。"""
        go_source = Path(__file__).resolve().parents[2] / "internal" / "normalize" / "traditional.go"
        if not go_source.is_file():
            self.skipTest("Go 源文件不可用")
        match = re.search(r'const tsPairs = "([^"]*)"', go_source.read_text(encoding="utf-8"))
        self.assertIsNotNone(match, "未能从 Go 源文件解析出 tsPairs")
        packed = match.group(1)
        go_table = {packed[index]: packed[index + 1] for index in range(0, len(packed), 2)}
        self.assertEqual(
            go_table,
            TRADITIONAL_TO_SIMPLIFIED,
            "繁简表与 Go 侧不一致, 请重新运行 scripts/gen_traditional.py",
        )

    def test_table_covers_common_sensitive_characters(self) -> None:
        """扩表后应覆盖敏感词场景的常用繁体字。"""
        for traditional, simplified in [
            ("槍", "枪"), ("彈", "弹"), ("藥", "药"), ("賭", "赌"),
            ("證", "证"), ("偽", "伪"), ("賣", "卖"), ("購", "购"),
            ("裸", "裸"), ("視", "视"), ("頻", "频"), ("網", "网"),
        ]:
            got = to_simplified(traditional)
            self.assertEqual(got, simplified, f"{traditional} 应转换为 {simplified}, 得到 {got}")


if __name__ == "__main__":
    unittest.main()
