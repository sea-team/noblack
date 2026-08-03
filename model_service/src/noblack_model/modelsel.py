"""模型启用选择。

抽成独立模块而非放在 app.py 里, 是因为 app.py 在 import 阶段就会加载
模型权重 (RUNTIME = ModelRuntime()), 测试无法直接导入。这里只做纯粹的
名称解析, 不触碰 torch, 可独立测试。
"""
from __future__ import annotations

from collections.abc import Mapping
from typing import TypeVar

V = TypeVar("V")


def resolve_enabled_models(all_models: Mapping[str, V], raw: str | None) -> dict[str, V]:
    """按 raw (通常来自 NB_MODELS) 选择启用的模型。

    - raw 为空/None: 全部启用, 与历史行为一致。
    - 大小写与空白不敏感, 自动去重。
    - 输出顺序始终跟随 all_models 的顺序, 与书写顺序无关,
      保证响应里的模型顺序稳定。

    仅启用 lite 可显著降低推理耗时: lite 约 6-8ms, macbert 约 35-55ms;
    双模型并行时整体耗时由较慢的 macbert 决定。
    """
    text = (raw or "").strip()
    if not text:
        return dict(all_models)

    requested = {name.strip().lower() for name in text.split(",") if name.strip()}
    if not requested:
        raise ValueError("NB_MODELS must enable at least one model")

    unknown = sorted(name for name in requested if name not in all_models)
    if unknown:
        allowed = ", ".join(all_models)
        raise ValueError(
            f"NB_MODELS contains unknown model(s): {', '.join(unknown)}; allowed: {allowed}"
        )

    return {name: value for name, value in all_models.items() if name in requested}
