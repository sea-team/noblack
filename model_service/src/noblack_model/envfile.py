"""加载发布包中的 config.env。

存在意义: 启动脚本 (start.cmd / start.sh) 会解析 config.env 并导出为环境变量,
但用户直接运行 noblack-model 可执行文件时不经过脚本, 配置会静默失效
(表现为端口等设置不生效、回落到默认值)。让服务自身也能读取该文件,
两种启动方式行为一致。

与 Go 侧 internal/envfile 保持相同规则: 只接受 NB_ 前缀的大写键,
已存在的同名环境变量优先。
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path

DEFAULT_NAME = "config.env"

# 与启动脚本及 Go 侧保持一致: 只接受 NB_ 前缀的大写键,
# 避免 config.env 里的无关行污染进程环境 (例如 PATH)。
_KEY_PATTERN = re.compile(r"^NB_[A-Z0-9_]+$")


def load(path: Path) -> int:
    """读取 config.env 并把其中的键写入进程环境。

    已存在的同名环境变量优先, 文件不会覆盖它, 因此优先级为:
    config.env < 环境变量 < 命令行参数。

    返回实际写入的键数量; 文件不存在时返回 0 (配置文件是可选的)。
    """
    try:
        # 用 utf-8-sig 兼容 Windows 记事本可能写入的 BOM。
        content = path.read_text(encoding="utf-8-sig")
    except FileNotFoundError:
        return 0
    except OSError:
        return 0

    applied = 0
    for raw_line in content.splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        separator = line.find("=")
        if separator < 1:
            continue
        key = line[:separator].strip()
        if not _KEY_PATTERN.match(key):
            continue
        # 值仅裁剪两侧空白, 不剥离引号 —— 与启动脚本保持一致,
        # 避免同一份文件在两条路径下解析结果不同。
        value = line[separator + 1:].strip()
        # 已存在且非空的环境变量优先。
        if os.environ.get(key):
            continue
        os.environ[key] = value
        applied += 1
    return applied


def resolve() -> Path | None:
    """推断 config.env 的位置。

    依次尝试: 可执行文件所在目录 (PyInstaller 冻结后即发布包根目录)、
    当前工作目录。都不存在时返回 None。
    """
    candidates: list[Path] = []
    configured = os.getenv("NB_PACKAGE_ROOT", "").strip()
    if configured:
        candidates.append(Path(configured).expanduser() / DEFAULT_NAME)
    if getattr(sys, "frozen", False):
        candidates.append(Path(sys.executable).resolve().parent / DEFAULT_NAME)
    try:
        candidates.append(Path.cwd() / DEFAULT_NAME)
    except OSError:
        pass
    for candidate in candidates:
        if candidate.is_file():
            return candidate
    return None


def load_default() -> tuple[Path | None, int]:
    """查找并加载 config.env, 返回 (实际路径, 生效键数)。"""
    path = resolve()
    if path is None:
        return None, 0
    return path, load(path)
