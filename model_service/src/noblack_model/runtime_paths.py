from __future__ import annotations

import os
import sys
from pathlib import Path


def resolve_package_root() -> Path:
    configured = os.getenv("NB_PACKAGE_ROOT", "").strip()
    if configured:
        return Path(configured).expanduser().resolve()
    if getattr(sys, "frozen", False):
        return Path(sys.executable).resolve().parent
    return Path(__file__).resolve().parents[3]
