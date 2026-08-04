#!/usr/bin/env python3
"""生成汉字 -> 拼音映射表 (Go 侧)。

用法:
    .build/linux-venv/bin/python scripts/gen_pinyin.py

依赖:
    pypinyin (已在 model_service/requirements.txt 中)

产物:
    internal/normalize/pinyin_table.go

用途: 词库匹配阶段把词条转成拼音建立第二张自动机, 拦截
"怎么做zha yào" 这类用拼音替换汉字的绕过写法。

只取每个字的首选读音 (pypinyin 的 NORMAL 风格, 无声调)。多音字不做
全展开: 一个字最多 3~5 个读音, 全展开会让词条拼音组合爆炸, 且大幅
提高误报。首选读音已覆盖绝大多数实际写法。
"""

from __future__ import annotations

import sys
from pathlib import Path

try:
    from pypinyin import Style, pinyin
except ImportError:  # pragma: no cover
    sys.exit("需要 pypinyin: pip install pypinyin")

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "internal" / "normalize" / "pinyin_table.go"

# 覆盖 CJK 统一汉字基本区 (U+4E00..U+9FFF)。扩展区为生僻字,
# 极少出现在敏感词里, 收录只会徒增表体积。
START, END = 0x4E00, 0x9FFF


def build_table() -> dict[str, str]:
    table: dict[str, str] = {}
    for code in range(START, END + 1):
        ch = chr(code)
        result = pinyin(ch, style=Style.NORMAL, errors="ignore")
        if not result or not result[0]:
            continue
        py = result[0][0].strip().lower()
        # 只保留纯 ASCII 字母的读音: 转换失败时 pypinyin 会原样返回汉字。
        if not py or not py.isascii() or not py.isalpha():
            continue
        table[ch] = py
    return table


def main() -> int:
    table = build_table()
    if len(table) < 10000:
        sys.exit(f"拼音表条目过少 ({len(table)}), 疑似生成失败")

    lines = [
        "package normalize",
        "",
        "// 汉字 -> 拼音 (无声调) 映射表。",
        "//",
        "// 数据来源: pypinyin (https://github.com/mozillazg/python-pinyin),",
        "// 由 scripts/gen_pinyin.py 生成, 请勿手工编辑。",
        "//",
        "// 取舍说明:",
        "//   - 只取每个字的首选读音。多音字不做全展开 —— 全展开会让词条的",
        "//     拼音组合爆炸, 并显著提高误报, 而首选读音已覆盖绝大多数写法。",
        "//   - 只收录 CJK 基本区 (U+4E00..U+9FFF), 扩展区生僻字不参与拼音匹配。",
        "",
        "var hanziPinyin = map[rune]string{",
    ]
    for ch in sorted(table):
        lines.append(f"\t{ord(ch):#x}: {table[ch]!r},".replace("'", '"'))
    lines.append("}")
    lines.append("")

    OUTPUT.write_text("\n".join(lines), encoding="utf-8")
    print(f"已生成 {OUTPUT} ({len(table)} 个汉字)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
