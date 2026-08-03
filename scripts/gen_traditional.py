#!/usr/bin/env python3
"""从 OpenCC 词典生成繁简映射表 (Go 与 Python 两份)。

用法:
    python3 scripts/gen_traditional.py

依赖:
    pip install opencc-python-reimplemented

产物:
    internal/normalize/traditional.go
    model_service/src/noblack_data/traditional.py

两份表由同一数据源生成, 保证 Go 侧 (词库匹配) 与 Python 侧 (模型推理)
使用完全一致的转换规则 —— 规则漂移会导致同一文本在两条链路上被判成不同内容。
测试 model_service/tests/test_normalize_for_model.py 会校验两表一致。
"""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GO_OUTPUT = ROOT / "internal" / "normalize" / "traditional.go"
PYTHON_OUTPUT = ROOT / "model_service" / "src" / "noblack_data" / "traditional.py"


def load_opencc_table() -> dict[str, str]:
    """读取 OpenCC 的 TSCharacters.txt 并提取 1:1 单字映射。"""
    try:
        import opencc
    except ImportError:
        sys.exit(
            "需要 opencc-python-reimplemented。\n"
            "  pip install opencc-python-reimplemented"
        )

    dictionary = Path(opencc.__file__).parent / "dictionary" / "TSCharacters.txt"
    if not dictionary.is_file():
        sys.exit(f"未找到 OpenCC 词典: {dictionary}")

    pairs: dict[str, str] = {}
    for line in dictionary.read_text(encoding="utf-8").strip().splitlines():
        parts = line.split("\t")
        if len(parts) < 2:
            continue
        traditional = parts[0]
        # 第二列可能是空格分隔的多个候选, 取首选项 (OpenCC 约定)。
        simplified = parts[1].split(" ")[0]
        if len(traditional) != 1 or len(simplified) != 1:
            continue
        # 只保留 BMP 内字符, 剔除极罕见生僻字以控制表体积。
        if ord(traditional) > 0xFFFF or ord(simplified) > 0xFFFF:
            continue
        # 繁简同形的字无需转换。
        if traditional == simplified:
            continue
        pairs[traditional] = simplified
    return pairs


def compact(pairs: dict[str, str]) -> str:
    """把映射压成单个字符串: 偶数位繁体, 奇数位简体。"""
    return "".join(traditional + simplified for traditional, simplified in sorted(pairs.items()))


GO_TEMPLATE = '''package normalize

// 繁体 -> 简体 单字映射。
//
// 数据来源: OpenCC (https://github.com/BYVoid/OpenCC) 的 TSCharacters.txt,
// 这是繁简转换领域的权威开源词典。本表由该词典生成, 保留其中的 1:1 单字映射。
//
// 取舍说明:
//   - 只收录单字对单字的映射 —— 一对多的候选取 OpenCC 的首选项。
//   - 只保留基本多文种平面 (BMP) 内的字符, 剔除极罕见的生僻字, 控制表体积。
//   - 剔除繁简同形的字 (如 "毒"), 它们无需转换。
//
// 方向说明: 只做繁 -> 简单向转换。输入端可能是繁体, 而词库是纯简体,
// 转换后两侧才能在同一空间比较。反向 (简 -> 繁) 存在一简对多繁的歧义, 不做。
//
// 本文件由 scripts/gen_traditional.py 生成, 请勿手工编辑。
// 重新生成时会同步更新 model_service/src/noblack_data/traditional.py。

// tsPairs 紧凑存储映射表: 偶数位是繁体字, 奇数位是对应的简体字。
// 用单个字符串而非 map 字面量, 可显著减小编译产物与初始化开销。
const tsPairs = "{pairs}"

// traditionalToSimplified 由 tsPairs 在初始化时展开。
var traditionalToSimplified = func() map[rune]rune {{
	runes := []rune(tsPairs)
	table := make(map[rune]rune, len(runes)/2)
	for index := 0; index+1 < len(runes); index += 2 {{
		table[runes[index]] = runes[index+1]
	}}
	return table
}}()

// toSimplified 把单个繁体字转换为简体字; 非繁体字原样返回。
func toSimplified(r rune) rune {{
	if simplified, ok := traditionalToSimplified[r]; ok {{
		return simplified
	}}
	return r
}}
'''

PYTHON_TEMPLATE = '''"""繁体 -> 简体 单字映射。

数据来源: OpenCC 的 TSCharacters.txt。与 Go 侧 internal/normalize/traditional.go
由同一数据源生成, 保证词库匹配与模型推理使用完全一致的转换规则。

本文件由 scripts/gen_traditional.py 生成, 请勿手工编辑。
"""

from __future__ import annotations

# 紧凑存储: 偶数位是繁体字, 奇数位是对应简体字。
_PAIRS = (
    "{pairs}"
)

TRADITIONAL_TO_SIMPLIFIED = {{
    _PAIRS[index]: _PAIRS[index + 1] for index in range(0, len(_PAIRS), 2)
}}


def to_simplified(text: str) -> str:
    """把文本中的繁体字逐字转换为简体字; 非繁体字原样保留。"""
    if not text:
        return text
    return "".join(TRADITIONAL_TO_SIMPLIFIED.get(character, character) for character in text)
'''


def main() -> int:
    pairs = load_opencc_table()
    packed = compact(pairs)
    # 汉字里不会出现反斜杠或引号, 但生成代码时仍做转义以防意外。
    escaped = packed.replace("\\", "\\\\").replace('"', '\\"')

    GO_OUTPUT.write_text(GO_TEMPLATE.format(pairs=escaped), encoding="utf-8")
    PYTHON_OUTPUT.write_text(PYTHON_TEMPLATE.format(pairs=escaped), encoding="utf-8")

    print(f"映射数: {len(pairs)}")
    print(f"已生成: {GO_OUTPUT.relative_to(ROOT)}")
    print(f"已生成: {PYTHON_OUTPUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
