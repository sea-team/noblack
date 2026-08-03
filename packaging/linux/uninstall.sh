#!/usr/bin/env bash
# Noblack 服务卸载脚本
#
# 用法:
#   sudo ./uninstall.sh [选项]
#
# 选项:
#   --purge         同时删除词库、统计数据和配置文件 (不可恢复)
#   --remove-user   同时删除 noblack 系统用户
#   --yes           跳过确认提示 (用于自动化)
#   -h, --help      显示帮助
#
# 默认只停止服务、注销 systemd 注册并删除程序文件,
# 保留 data/ (词库、统计) 和 config.env, 方便重装后继续使用。

set -euo pipefail

INSTALL_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
GO_SERVICE="noblack"
MODEL_SERVICE="noblack-model"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_USER="noblack"

PURGE=0
REMOVE_USER=0
ASSUME_YES=0

log()  { echo "[uninstall] $*"; }
warn() { echo "[uninstall] $*" >&2; }
die()  { echo "[uninstall] 错误: $*" >&2; exit 1; }

usage() { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --purge)       PURGE=1; shift ;;
    --remove-user) REMOVE_USER=1; shift ;;
    --yes|-y)      ASSUME_YES=1; shift ;;
    -h|--help)     usage ;;
    *)             die "未知选项: $1 (用 --help 查看用法)" ;;
  esac
done

[ "$(id -u)" = "0" ] || die "需要 root 权限, 请用 sudo 运行"

# 安全检查: 拒绝在系统关键目录上执行删除。
case "$INSTALL_DIR" in
  /|/usr|/etc|/var|/bin|/sbin|/lib|/boot|/home|/root|/opt)
    die "拒绝卸载系统关键目录: $INSTALL_DIR" ;;
esac
# 必须看起来像 noblack 安装目录, 避免脚本被误拷到别处执行。
if [ ! -f "$INSTALL_DIR/noblack" ] && [ ! -f "$INSTALL_DIR/config.env" ]; then
  die "$INSTALL_DIR 不像 noblack 安装目录 (缺少 noblack 和 config.env), 已中止"
fi

log "安装目录: $INSTALL_DIR"
if [ "$PURGE" = "1" ]; then
  warn ""
  warn "--purge 将永久删除以下内容:"
  warn "  - 整个目录 $INSTALL_DIR"
  warn "  - 词库 data/words.json 及所有统计数据"
  warn "  - 配置文件 config.env"
  warn ""
else
  log "保留数据: data/ 与 config.env (如需一并删除请加 --purge)"
fi

if [ "$ASSUME_YES" != "1" ]; then
  if [ "$PURGE" = "1" ]; then
    printf "确认删除? 输入 yes 继续: "
  else
    printf "确认卸载服务? 输入 yes 继续: "
  fi
  read -r answer
  [ "$answer" = "yes" ] || { log "已取消"; exit 0; }
fi

# ---------- 停止并注销服务 ----------

for unit in "$GO_SERVICE" "$MODEL_SERVICE"; do
  if systemctl list-unit-files 2>/dev/null | grep -q "^$unit.service"; then
    if systemctl is-active --quiet "$unit" 2>/dev/null; then
      log "停止服务: $unit"
      systemctl stop "$unit" || warn "停止 $unit 失败, 继续"
    fi
    log "注销开机自启: $unit"
    systemctl disable "$unit" >/dev/null 2>&1 || true
  fi
  if [ -f "$SYSTEMD_DIR/$unit.service" ]; then
    rm -f "$SYSTEMD_DIR/$unit.service"
    log "删除 unit 文件: $unit.service"
  fi
done

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload
  systemctl reset-failed "$GO_SERVICE" "$MODEL_SERVICE" >/dev/null 2>&1 || true
fi

# ---------- 删除文件 ----------

if [ "$PURGE" = "1" ]; then
  log "删除整个安装目录..."
  rm -rf "$INSTALL_DIR"
  log "已删除: $INSTALL_DIR"
else
  # 只删程序文件和模型, 保留 data/ 与 config.env。
  for item in noblack noblack-model models README.txt config.env.example \
              deploy.sh restart.sh status.sh start.sh stop.sh start-keywords-only.sh SHA256SUMS; do
    if [ -e "$INSTALL_DIR/$item" ]; then
      rm -rf "$INSTALL_DIR/${item:?}"
    fi
  done
  log "已删除程序文件, 保留:"
  [ -d "$INSTALL_DIR/data" ] && log "  $INSTALL_DIR/data (词库与统计)"
  [ -f "$INSTALL_DIR/config.env" ] && log "  $INSTALL_DIR/config.env (配置)"
  [ -d "$INSTALL_DIR/logs" ] && log "  $INSTALL_DIR/logs (日志)"
fi

# ---------- 删除用户 ----------

if [ "$REMOVE_USER" = "1" ]; then
  if id -u "$SERVICE_USER" >/dev/null 2>&1; then
    log "删除系统用户: $SERVICE_USER"
    userdel "$SERVICE_USER" 2>/dev/null || warn "删除用户失败, 可能仍有进程占用"
  fi
else
  id -u "$SERVICE_USER" >/dev/null 2>&1 &&
    log "保留系统用户 $SERVICE_USER (如需删除请加 --remove-user)"
fi

log "卸载完成"
# uninstall.sh 自身在非 purge 模式下会被保留, 提示用户可手动清理。
if [ "$PURGE" != "1" ] && [ -f "$INSTALL_DIR/uninstall.sh" ]; then
  log "如需彻底清除残留, 执行: rm -rf $INSTALL_DIR"
fi
