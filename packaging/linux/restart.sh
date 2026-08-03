#!/usr/bin/env bash
# Noblack 服务重启脚本
#
# 用法:
#   sudo ./restart.sh [选项]
#
# 选项:
#   --model-only    只重启模型服务
#   --go-only       只重启主服务 (改词库/检测模式后用这个, 不用等模型重新加载)
#   -h, --help      显示帮助
#
# 不带参数时重启全部已安装的服务。

set -euo pipefail

INSTALL_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
GO_SERVICE="noblack"
MODEL_SERVICE="noblack-model"
SYSTEMD_DIR="/etc/systemd/system"

RESTART_GO=1
RESTART_MODEL=1

log()  { echo "[restart] $*"; }
warn() { echo "[restart] $*" >&2; }
die()  { echo "[restart] 错误: $*" >&2; exit 1; }

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --model-only) RESTART_GO=0; shift ;;
    --go-only)    RESTART_MODEL=0; shift ;;
    -h|--help)    usage ;;
    *)            die "未知选项: $1 (用 --help 查看用法)" ;;
  esac
done

[ "$(id -u)" = "0" ] || die "需要 root 权限, 请用 sudo 运行"
command -v systemctl >/dev/null 2>&1 || die "未检测到 systemd"

# 模型服务可能未安装 (仅词库模式), 这不是错误。
MODEL_INSTALLED=0
[ -f "$SYSTEMD_DIR/$MODEL_SERVICE.service" ] && MODEL_INSTALLED=1
[ -f "$SYSTEMD_DIR/$GO_SERVICE.service" ] || die "主服务未安装, 请先运行 deploy.sh"

read_config() {
  local key="$1" default="$2" value
  [ -f "$INSTALL_DIR/config.env" ] || { echo "$default"; return; }
  value="$(sed -n "s/^${key}=//p" "$INSTALL_DIR/config.env" | head -n 1)"
  echo "${value:-$default}"
}

if [ "$RESTART_MODEL" = "1" ] && [ "$MODEL_INSTALLED" = "1" ]; then
  MODEL_PORT="$(read_config NB_MODEL_PORT 8091)"
  log "重启模型服务 (重新加载模型需要时间)..."
  systemctl restart "$MODEL_SERVICE"
  ready=0
  for _ in $(seq 1 180); do
    if ! systemctl is-active --quiet "$MODEL_SERVICE"; then
      warn "模型服务启动失败, 日志: journalctl -u $MODEL_SERVICE -n 50"
      exit 1
    fi
    if curl -sf --noproxy '*' "http://127.0.0.1:$MODEL_PORT/health" >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 1
  done
  [ "$ready" = "1" ] || { warn "模型服务健康检查超时 (180s)"; exit 1; }
  log "模型服务就绪"
elif [ "$RESTART_MODEL" = "1" ]; then
  log "跳过模型服务 (未安装, 当前为仅词库模式)"
fi

if [ "$RESTART_GO" = "1" ]; then
  log "重启主服务..."
  systemctl restart "$GO_SERVICE"
  ADDR="$(read_config NB_ADDR ":8080")"
  PORT="${ADDR##*:}"
  ready=0
  for _ in $(seq 1 30); do
    if ! systemctl is-active --quiet "$GO_SERVICE"; then
      warn "主服务启动失败, 日志: journalctl -u $GO_SERVICE -n 50"
      exit 1
    fi
    if curl -sf --noproxy '*' "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 1
  done
  [ "$ready" = "1" ] || { warn "主服务健康检查超时, 日志: journalctl -u $GO_SERVICE -n 50"; exit 1; }
  log "主服务就绪: http://127.0.0.1:$PORT (检测模式: $(read_config NB_DETECT_MODE both))"
fi

log "重启完成"
