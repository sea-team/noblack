"""NB_MODELS 模型选择的单元测试。

直接测试 noblack_model.modelsel 中的真实实现, 不加载模型权重,
因此无需 torch 即可运行。
"""
from __future__ import annotations

import sys
from pathlib import Path

import pytest

SRC = Path(__file__).resolve().parent.parent / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

from noblack_model.modelsel import resolve_enabled_models  # noqa: E402
from noblack_model.policy import combine_model_actions  # noqa: E402

ALL = {"lite": Path("/models/lite"), "macbert": Path("/models/macbert")}


@pytest.mark.parametrize("raw", [None, "", "   "])
def test_blank_enables_all_models(raw):
    """未设置或留空时保持历史行为: 全部模型启用。"""
    assert list(resolve_enabled_models(ALL, raw)) == ["lite", "macbert"]


def test_lite_only():
    got = resolve_enabled_models(ALL, "lite")
    assert list(got) == ["lite"]
    assert got["lite"] == ALL["lite"]


def test_macbert_only():
    assert list(resolve_enabled_models(ALL, "macbert")) == ["macbert"]


def test_order_follows_all_models_not_input():
    """书写顺序不影响输出顺序, 保证响应中模型顺序稳定。"""
    assert list(resolve_enabled_models(ALL, "macbert,lite")) == ["lite", "macbert"]


def test_case_and_whitespace_tolerated():
    assert list(resolve_enabled_models(ALL, " LITE , MacBert ")) == ["lite", "macbert"]


def test_duplicates_deduplicated():
    assert list(resolve_enabled_models(ALL, "lite,lite,lite")) == ["lite"]


def test_trailing_separator_ignored():
    assert list(resolve_enabled_models(ALL, "lite,")) == ["lite"]


def test_unknown_model_rejected():
    with pytest.raises(ValueError, match="unknown model"):
        resolve_enabled_models(ALL, "lite,gpt5")


def test_unknown_model_message_lists_allowed():
    with pytest.raises(ValueError, match="allowed: lite, macbert"):
        resolve_enabled_models(ALL, "bogus")


def test_only_separators_rejected():
    with pytest.raises(ValueError, match="at least one model"):
        resolve_enabled_models(ALL, ",,,")


def test_result_is_a_copy():
    """返回值应是新字典, 修改它不能影响调用方传入的表。"""
    got = resolve_enabled_models(ALL, None)
    got["lite"] = Path("/tmp/hacked")
    assert ALL["lite"] == Path("/models/lite")


# ---- 单模型下的合并策略退化行为 ----


@pytest.mark.parametrize("policy", ["max", "consensus"])
@pytest.mark.parametrize("action", ["pass", "review", "block"])
def test_single_model_combine_returns_own_action(policy, action):
    """单模型时两种策略都应退化为该模型自身的判定, 不改变语义。"""
    assert combine_model_actions([{"action": action}], policy=policy) == action


def test_dual_model_max_takes_strictest():
    assert combine_model_actions([{"action": "pass"}, {"action": "block"}], policy="max") == "block"


def test_dual_model_consensus_needs_agreement():
    assert (
        combine_model_actions([{"action": "pass"}, {"action": "block"}], policy="consensus")
        == "pass"
    )
