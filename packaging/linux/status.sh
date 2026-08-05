#!/usr/bin/env bash
# Noblack 服务状态查看脚本
#
# 用法:
#   ./status.sh
#
# 显示服务运行状态、监听端口、当前检测模式和健康检查结果。
# 无需 root 权限。

set -uo pipefail

INSTALL_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
GO_SERVICE="noblack"
MODEL_SERVICE="noblack-model"
SYSTEMD_DIR="/etc/systemd/system"

read_config() {
  local key="$1" default="$2" value
  [ -r "$INSTALL_DIR/config.env" ] || { echo "$default"; return; }
  value="$(sed -n "s/^${key}=//p" "$INSTALL_DIR/config.env" 2>/dev/null | head -n 1)"
  echo "${value:-$default}"
}

unit_state() {
  local unit="$1"
  if [ ! -f "$SYSTEMD_DIR/$unit.service" ]; then
    echo "未安装"
  elif systemctl is-active --quiet "$unit" 2>/dev/null; then
    local since
    since="$(systemctl show "$unit" --property=ActiveEnterTimestamp --value 2>/dev/null)"
    echo "运行中 (自 ${since:-未知})"
  else
    echo "已停止"
  fi
}

ADDR="$(read_config NB_ADDR ":8080")"
GO_PORT="${ADDR##*:}"
MODEL_PORT="$(read_config NB_MODEL_PORT 8091)"

echo "Noblack 服务状态"
echo "================"
echo "安装目录:  $INSTALL_DIR"
echo "配置文件:  $INSTALL_DIR/config.env"
echo ""
echo "主服务:    $(unit_state "$GO_SERVICE")"
echo "模型服务:  $(unit_state "$MODEL_SERVICE")"
echo ""
echo "检测模式:  $(read_config NB_DETECT_MODE both)"
echo "未命中召回: $(read_config NB_RECALL_ON_MISS false)"
echo "监听端口:  $GO_PORT"
[ -f "$SYSTEMD_DIR/$MODEL_SERVICE.service" ] && echo "模型端口:  $MODEL_PORT"
echo ""

echo "健康检查"
echo "--------"
if curl -sf --noproxy '*' "http://127.0.0.1:$GO_PORT/health" >/dev/null 2>&1; then
  echo "主服务:    正常 (http://127.0.0.1:$GO_PORT)"
else
  echo "主服务:    无响应"
fi
if [ -f "$SYSTEMD_DIR/$MODEL_SERVICE.service" ]; then
  if curl -sf --noproxy '*' "http://127.0.0.1:$MODEL_PORT/health" >/dev/null 2>&1; then
    echo "模型服务:  正常 (http://127.0.0.1:$MODEL_PORT)"
  else
    echo "模型服务:  无响应"
  fi
fi

# update.sh 留下的备份: 有备份才能回滚, 状态里明示避免临事才发现没有。
if [ -f "$INSTALL_DIR/.backup/noblack" ]; then
  echo ""
  echo "可回滚版本"
  echo "----------"
  if [ -f "$INSTALL_DIR/.backup/VERSION" ]; then
    echo "备份时间:  $(cat "$INSTALL_DIR/.backup/VERSION")"
  fi
  echo "回滚命令:  sudo $INSTALL_DIR/update.sh --rollback"
fi

echo ""
echo "查看日志:  journalctl -u $GO_SERVICE -f"
