#!/usr/bin/env bash
# Noblack 构建环境初始化 (Linux/WSL)
#
# 用法:
#   ./scripts/bootstrap.sh [选项]
#
# 选项:
#   --skip-lfs      跳过 git lfs pull (模型权重已就位时用)
#   --skip-venv     跳过 Python 虚拟环境创建与依赖安装
#   --recreate      删除并重建虚拟环境 (依赖版本变更后用)
#   -h, --help      显示帮助
#
# 新环境克隆仓库后执行本脚本, 即可具备构建发布包的条件。
# 它做三件在仓库里看不到、但构建必需的事:
#
#   1. git lfs pull  —— 模型权重在仓库里是 132 字节的 LFS 指针,
#                       不拉取的话 release.py 会拒绝构建
#   2. 建虚拟环境    —— .build/ 在 .gitignore 中, 新环境没有
#   3. 装依赖        —— torch(CPU) + requirements + PyInstaller
#
# 完成后即可:
#   .build/linux-venv/bin/python scripts/release.py build --target linux-amd64

set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
VENV="$ROOT/.build/linux-venv"

# torch 单独装: 它不在 requirements.txt 里, 因为要走 PyTorch 官方 CPU 源
# (PyPI 上的 torch 会带上数 GB 的 CUDA 依赖)。版本与 scripts/install-cpu-runtime.ps1
# 保持一致 —— 运行时与打包时用不同版本会导致难以排查的行为差异。
TORCH_VERSION="2.6.0+cpu"
TORCH_INDEX="https://download.pytorch.org/whl/cpu"

SKIP_LFS=0
SKIP_VENV=0
RECREATE=0

log()  { echo "[bootstrap] $*"; }
warn() { echo "[bootstrap] $*" >&2; }
die()  { echo "[bootstrap] 错误: $*" >&2; exit 1; }

usage() { sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-lfs)  SKIP_LFS=1; shift ;;
    --skip-venv) SKIP_VENV=1; shift ;;
    --recreate)  RECREATE=1; shift ;;
    -h|--help)   usage ;;
    *)           die "未知选项: $1 (用 --help 查看用法)" ;;
  esac
done

cd "$ROOT"

# ---------- 1. 基础工具 ----------

log "检查基础工具..."
command -v go >/dev/null 2>&1 || die "未找到 go, 请先安装 Go (go.mod 要求 $(grep '^go ' go.mod | awk '{print $2}'))"
log "  go       $(go version | awk '{print $3}')"

PYTHON="${PYTHON:-python3}"
command -v "$PYTHON" >/dev/null 2>&1 || die "未找到 $PYTHON, 请先安装 Python 3.11+"
log "  python   $("$PYTHON" -V 2>&1 | awk '{print $2}')"

# ---------- 2. 模型权重 (Git LFS) ----------

# 判断依据是文件内容而非 git lfs 命令是否存在: 有些环境已经用别的方式
# 把权重放好了, 此时不必强求安装 git-lfs。
lfs_pointer() {
  [ -f "$1" ] && head -c 42 "$1" 2>/dev/null | grep -q "^version https://git-lfs"
}

MODEL_FILE="models/lite-production-v1/model.safetensors"
if [ "$SKIP_LFS" = "1" ]; then
  log "跳过 LFS 拉取 (--skip-lfs)"
elif [ ! -f "$MODEL_FILE" ]; then
  warn "未找到 $MODEL_FILE, 跳过 LFS 检查"
elif lfs_pointer "$MODEL_FILE"; then
  log "模型权重是 LFS 指针, 需要拉取真实文件..."
  if ! command -v git-lfs >/dev/null 2>&1 && ! git lfs version >/dev/null 2>&1; then
    die "需要 git-lfs 但未安装。安装后重新运行:
    Debian/Ubuntu:  sudo apt install git-lfs
    macOS:          brew install git-lfs
    其他:           https://git-lfs.com"
  fi
  git lfs install --local
  log "拉取模型权重 (约 800MB, 请稍候)..."
  git lfs pull
  if lfs_pointer "$MODEL_FILE"; then
    die "git lfs pull 后仍是指针, 请检查 LFS 服务是否可达"
  fi
  log "模型权重已就位"
else
  log "模型权重已是真实文件, 无需拉取"
fi

# ---------- 3. Python 虚拟环境 ----------

if [ "$SKIP_VENV" = "1" ]; then
  log "跳过虚拟环境 (--skip-venv)"
else
  if [ "$RECREATE" = "1" ] && [ -d "$VENV" ]; then
    log "删除现有虚拟环境 (--recreate)"
    rm -rf "$VENV"
  fi

  if [ -d "$VENV" ]; then
    log "复用已有虚拟环境: $VENV"
  else
    log "创建虚拟环境: $VENV"
    mkdir -p "$(dirname "$VENV")"
    "$PYTHON" -m venv "$VENV" || die "创建虚拟环境失败 (Debian/Ubuntu 可能需要: sudo apt install python3-venv)"
  fi

  PIP="$VENV/bin/pip"
  [ -x "$PIP" ] || die "虚拟环境损坏: 找不到 $PIP"

  # 已装齐就跳过, 让重复执行本脚本是廉价的。
  if "$VENV/bin/python" -c "import torch, transformers, PyInstaller" >/dev/null 2>&1; then
    log "依赖已齐备, 跳过安装 (需要重装请加 --recreate)"
  else
    log "安装 torch $TORCH_VERSION (CPU 版, 约 200MB)..."
    "$PIP" install --quiet --disable-pip-version-check \
      "torch==$TORCH_VERSION" --index-url "$TORCH_INDEX" ||
      die "安装 torch 失败, 请检查网络或代理设置"

    log "安装模型服务依赖..."
    "$PIP" install --quiet --disable-pip-version-check -r model_service/requirements.txt ||
      die "安装 requirements.txt 失败"

    log "安装打包依赖..."
    "$PIP" install --quiet --disable-pip-version-check -r model_service/requirements-build.txt ||
      die "安装 requirements-build.txt 失败"
  fi

  log "已安装版本:"
  "$VENV/bin/python" - <<'PY' 2>/dev/null | sed 's/^/    /'
import importlib.metadata as meta
for name in ("torch", "transformers", "safetensors", "pypinyin", "pyinstaller"):
    try:
        print(f"{name:14s} {meta.version(name)}")
    except Exception:
        print(f"{name:14s} 未安装")
PY
fi

# ---------- 4. 自检 ----------

log "运行 Go 测试..."
go build ./... || die "Go 构建失败"
if go test ./... >/dev/null 2>&1; then
  log "  Go 测试全部通过"
else
  warn "  Go 测试未全部通过, 请运行 go test ./... 查看详情"
fi

if [ "$SKIP_VENV" != "1" ]; then
  log "校验发布输入 (模型权重完整性)..."
  if "$VENV/bin/python" scripts/release.py validate >/dev/null 2>&1; then
    log "  校验通过"
  else
    warn "  校验未通过, 详情:"
    "$VENV/bin/python" scripts/release.py validate 2>&1 | tail -5 | sed 's/^/    /'
    die "发布输入校验失败, 无法构建发布包"
  fi
fi

log ""
log "环境就绪。构建发布包:"
log "  .build/linux-venv/bin/python scripts/release.py build --target linux-amd64"
log ""
log "Windows 包需在 Windows 上构建 (PyInstaller 不支持交叉编译),"
log "且必须先清空 PYTHONPATH, 详见 README 的\"Windows/Linux 离线运行包\"章节。"
