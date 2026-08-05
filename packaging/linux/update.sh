#!/usr/bin/env bash
# Noblack 原地升级脚本 (systemd)
#
# 用法:
#   sudo ./update.sh [选项]
#
# 选项:
#   --dir <路径>       安装目录 (默认 /opt/noblack)
#   --force-models     强制重新同步模型文件 (默认: 校验和相同则跳过)
#   --rollback         回滚到上一次升级前的版本
#   --no-start         只替换文件不重启服务
#   -h, --help         显示帮助
#
# 与 deploy.sh 的区别: deploy.sh 面向首次安装 (创建用户、写 systemd unit、
# 全量同步模型); update.sh 面向已装好的实例, 只做必要的增量替换:
#
#   - 二进制: 升级前备份, 校验和相同则跳过
#   - 模型:   校验和相同则跳过 (约 400MB, 这是升级耗时的大头)
#   - 词库:   永不覆盖 (线上维护的数据)
#   - 配置:   保留现有值, 只把新版本新增的配置项追加到末尾
#   - 失败:   健康检查不通过时自动回滚到升级前的二进制
#
# 脚本从自身所在的发布包目录读取文件, 因此需要在解压后的包内运行。

set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

INSTALL_DIR="/opt/noblack"
FORCE_MODELS=0
DO_ROLLBACK=0
AUTO_START=1

GO_SERVICE="noblack"
MODEL_SERVICE="noblack-model"

log()  { echo "[update] $*"; }
warn() { echo "[update] $*" >&2; }
die()  { echo "[update] 错误: $*" >&2; exit 1; }

usage() { sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dir)          INSTALL_DIR="${2:?--dir 需要参数}"; shift 2 ;;
    --force-models) FORCE_MODELS=1; shift ;;
    --rollback)     DO_ROLLBACK=1; shift ;;
    --no-start)     AUTO_START=0; shift ;;
    -h|--help)      usage ;;
    *)              die "未知选项: $1 (用 --help 查看用法)" ;;
  esac
done

[ "$(id -u)" = "0" ] || die "需要 root 权限, 请用 sudo 运行"
command -v systemctl >/dev/null 2>&1 || die "未检测到 systemd, 本脚本依赖 systemctl"

case "$INSTALL_DIR" in
  /*) ;;
  *) die "--dir 必须是绝对路径: $INSTALL_DIR" ;;
esac
[ -d "$INSTALL_DIR" ] || die "安装目录不存在: $INSTALL_DIR (首次安装请用 deploy.sh)"

BACKUP_DIR="$INSTALL_DIR/.backup"
CONFIG_FILE="$INSTALL_DIR/config.env"

# 服务是否已注册: 决定升级后是否需要重启, 以及是否有模型服务要处理。
has_unit() { [ -f "/etc/systemd/system/$1.service" ]; }

# ---------- 回滚 ----------

if [ "$DO_ROLLBACK" = "1" ]; then
  [ -d "$BACKUP_DIR" ] || die "没有可回滚的备份 (从未通过 update.sh 升级过)"
  [ -f "$BACKUP_DIR/noblack" ] || die "备份不完整: 缺少 $BACKUP_DIR/noblack"

  log "回滚到升级前的版本..."
  if [ -f "$BACKUP_DIR/VERSION" ]; then
    log "备份时间: $(cat "$BACKUP_DIR/VERSION")"
  fi

  for unit in "$GO_SERVICE" "$MODEL_SERVICE"; do
    if has_unit "$unit" && systemctl is-active --quiet "$unit" 2>/dev/null; then
      systemctl stop "$unit"
    fi
  done

  install -m 0755 "$BACKUP_DIR/noblack" "$INSTALL_DIR/noblack"
  log "已恢复 noblack"
  if [ -f "$BACKUP_DIR/noblack-model" ]; then
    install -m 0755 "$BACKUP_DIR/noblack-model" "$INSTALL_DIR/noblack-model"
    log "已恢复 noblack-model"
  fi

  # 配置由 update.sh 只追加不修改, 回滚时保留当前配置 —— 多出来的
  # 新配置项对旧二进制是未知环境变量, 会被忽略, 不影响启动。
  owner="$(stat -c '%U' "$INSTALL_DIR" 2>/dev/null || echo root)"
  chown -R "$owner":"$owner" "$INSTALL_DIR" 2>/dev/null || true

  if [ "$AUTO_START" = "1" ]; then
    has_unit "$MODEL_SERVICE" && systemctl start "$MODEL_SERVICE" || true
    systemctl start "$GO_SERVICE"
    log "服务已重启"
  fi
  log "回滚完成"
  exit 0
fi

# ---------- 校验发布包 ----------

[ -f "$SOURCE_DIR/noblack" ] || die "未找到 noblack 可执行文件, 请在解压后的发布包目录内运行"
[ -f "$INSTALL_DIR/noblack" ] || die "$INSTALL_DIR 下没有 noblack, 这不像是已安装的实例 (首次安装请用 deploy.sh)"

# 是否需要处理模型: 以安装目录里有没有 noblack-model 为准, 而不是看
# systemd unit —— unit 可能被手工删过, 但只要程序还在就该保持更新。
HAS_MODEL_SERVICE=0
if [ -f "$INSTALL_DIR/noblack-model" ] || has_unit "$MODEL_SERVICE"; then
  HAS_MODEL_SERVICE=1
  [ -f "$SOURCE_DIR/noblack-model" ] ||
    die "当前实例含模型服务, 但发布包里没有 noblack-model (是否拿错了仅词库版的包?)"
fi

# sha 计算单个文件的 SHA-256, 文件不存在时返回空串。
sha() {
  [ -f "$1" ] || { echo ""; return; }
  sha256sum "$1" 2>/dev/null | cut -d' ' -f1
}

# same 比较两个文件内容是否一致。
same() {
  local a b
  a="$(sha "$1")"
  b="$(sha "$2")"
  [ -n "$a" ] && [ "$a" = "$b" ]
}

# ---------- 展示将要发生的变化 ----------

log "安装目录: $INSTALL_DIR"

GO_CHANGED=0
MODEL_BIN_CHANGED=0
MODELS_CHANGED=0

same "$SOURCE_DIR/noblack" "$INSTALL_DIR/noblack" || GO_CHANGED=1
if [ "$HAS_MODEL_SERVICE" = "1" ]; then
  same "$SOURCE_DIR/noblack-model" "$INSTALL_DIR/noblack-model" || MODEL_BIN_CHANGED=1
fi

# 模型权重逐个比对。文件多但只比校验和, 比复制 400MB 快得多。
if [ "$HAS_MODEL_SERVICE" = "1" ] && [ -d "$SOURCE_DIR/models" ]; then
  if [ "$FORCE_MODELS" = "1" ]; then
    MODELS_CHANGED=1
  else
    while IFS= read -r relative; do
      if ! same "$SOURCE_DIR/models/$relative" "$INSTALL_DIR/models/$relative"; then
        MODELS_CHANGED=1
        break
      fi
    done < <(cd "$SOURCE_DIR/models" && find . -type f | sed 's|^\./||')
  fi
fi

log "变更检测:"
log "  主服务二进制 : $([ "$GO_CHANGED" = 1 ] && echo '需更新' || echo '无变化')"
if [ "$HAS_MODEL_SERVICE" = "1" ]; then
  log "  模型服务程序 : $([ "$MODEL_BIN_CHANGED" = 1 ] && echo '需更新' || echo '无变化')"
  log "  模型权重     : $([ "$MODELS_CHANGED" = 1 ] && echo '需同步 (约 400MB)' || echo '无变化, 跳过')"
fi

if [ "$GO_CHANGED" = "0" ] && [ "$MODEL_BIN_CHANGED" = "0" ] && [ "$MODELS_CHANGED" = "0" ]; then
  log "所有文件均无变化, 无需升级"
  # 配置项仍需检查: 可能上次升级后又手工改过配置。
fi

# ---------- 备份 ----------

if [ "$GO_CHANGED" = "1" ] || [ "$MODEL_BIN_CHANGED" = "1" ]; then
  mkdir -p "$BACKUP_DIR"
  install -m 0755 "$INSTALL_DIR/noblack" "$BACKUP_DIR/noblack"
  if [ "$HAS_MODEL_SERVICE" = "1" ] && [ -f "$INSTALL_DIR/noblack-model" ]; then
    install -m 0755 "$INSTALL_DIR/noblack-model" "$BACKUP_DIR/noblack-model"
  fi
  date '+%Y-%m-%d %H:%M:%S' > "$BACKUP_DIR/VERSION"
  log "已备份当前二进制到 $BACKUP_DIR (回滚: ./update.sh --rollback)"
fi

# ---------- 停止服务 ----------

STOPPED=""
if [ "$GO_CHANGED" = "1" ] || [ "$MODEL_BIN_CHANGED" = "1" ] || [ "$MODELS_CHANGED" = "1" ]; then
  for unit in "$GO_SERVICE" "$MODEL_SERVICE"; do
    if has_unit "$unit" && systemctl is-active --quiet "$unit" 2>/dev/null; then
      log "停止服务: $unit"
      systemctl stop "$unit"
      STOPPED="$STOPPED $unit"
    fi
  done
fi

# ---------- 替换文件 ----------

if [ "$GO_CHANGED" = "1" ]; then
  install -m 0755 "$SOURCE_DIR/noblack" "$INSTALL_DIR/noblack"
  log "已更新 noblack"
fi
if [ "$MODEL_BIN_CHANGED" = "1" ]; then
  install -m 0755 "$SOURCE_DIR/noblack-model" "$INSTALL_DIR/noblack-model"
  log "已更新 noblack-model"
fi
if [ "$MODELS_CHANGED" = "1" ]; then
  log "同步模型权重 (约 400MB, 请稍候)..."
  if command -v rsync >/dev/null 2>&1; then
    rsync -a --delete "$SOURCE_DIR/models/" "$INSTALL_DIR/models/"
  else
    rm -rf "$INSTALL_DIR/models"
    cp -r "$SOURCE_DIR/models" "$INSTALL_DIR/models"
  fi
  log "模型权重已同步"
fi

# 管理脚本与文档随包更新 (它们不含状态, 直接覆盖)。
for helper in deploy.sh update.sh restart.sh status.sh stop.sh uninstall.sh start.sh start-keywords-only.sh; do
  [ -f "$SOURCE_DIR/$helper" ] && install -m 0755 "$SOURCE_DIR/$helper" "$INSTALL_DIR/$helper"
done
[ -f "$SOURCE_DIR/README.txt" ] && install -m 0644 "$SOURCE_DIR/README.txt" "$INSTALL_DIR/README.txt"
[ -f "$SOURCE_DIR/config.env.example" ] &&
  install -m 0644 "$SOURCE_DIR/config.env.example" "$INSTALL_DIR/config.env.example"

# 词库只在缺失时补齐, 已有的一律不动 —— 线上词库是运营维护的数据。
if [ ! -f "$INSTALL_DIR/data/words.json" ] && [ -f "$SOURCE_DIR/data/words.json" ]; then
  mkdir -p "$INSTALL_DIR/data"
  install -m 0644 "$SOURCE_DIR/data/words.json" "$INSTALL_DIR/data/words.json"
  log "安装初始词库 (原先缺失)"
else
  log "保留已有词库"
fi

# ---------- 合并配置新增项 ----------
#
# 只追加不修改: 新版本引入的配置项 (如 NB_PINYIN) 若不补进去, 对应功能
# 会静默失效; 而已有项一律保持线上值, 避免把运维改过的配置覆盖回默认。

TEMPLATE="$SOURCE_DIR/config.env.example"
if [ -f "$CONFIG_FILE" ] && [ -f "$TEMPLATE" ]; then
  added=0
  pending=""
  while IFS= read -r key; do
    [ -n "$key" ] || continue
    if ! grep -q "^[[:space:]]*${key}=" "$CONFIG_FILE"; then
      pending="$pending $key"
      added=$((added + 1))
    fi
  done < <(grep -oE '^[A-Z_][A-Z0-9_]*=' "$TEMPLATE" | sed 's/=$//' | sort -u)

  if [ "$added" -gt 0 ]; then
    {
      echo ""
      echo "# ---- 以下为 $(date '+%Y-%m-%d') 升级时新增的配置项 ----"
      echo "# 取自 config.env.example 的默认值, 如需调整请直接修改。"
    } >> "$CONFIG_FILE"

    for key in $pending; do
      # 连同该项在模板里的注释一起搬过来, 保留说明文字。
      awk -v k="$key" '
        $0 ~ "^"k"=" { for (i = 1; i <= n; i++) print buf[i]; print; n = 0; next }
        /^#/ { buf[++n] = $0; next }
        { n = 0 }
      ' "$TEMPLATE" >> "$CONFIG_FILE"
      log "  新增配置项: $key"
    done
    log "配置已追加 $added 项新增配置 (已有项未改动)"
  else
    log "配置无新增项"
  fi
fi

# 令牌可能写在配置里, 保持仅服务用户可读。
SERVICE_USER="$(stat -c '%U' "$INSTALL_DIR" 2>/dev/null || echo root)"
chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR" 2>/dev/null || true
[ -f "$CONFIG_FILE" ] && chmod 0640 "$CONFIG_FILE"

# ---------- 重启并健康检查 ----------

if [ "$AUTO_START" != "1" ]; then
  log "已更新但未重启 (--no-start); 手动重启: $INSTALL_DIR/restart.sh"
  exit 0
fi

if [ -z "$STOPPED" ]; then
  # 文件没变但配置可能变了, 仍需重启才能生效。
  if [ "${added:-0}" -gt 0 ]; then
    log "配置有变更, 重启服务使其生效"
    for unit in "$MODEL_SERVICE" "$GO_SERVICE"; do
      has_unit "$unit" && systemctl restart "$unit" || true
    done
  else
    log "无变更, 服务保持运行"
    exit 0
  fi
else
  for unit in "$MODEL_SERVICE" "$GO_SERVICE"; do
    case " $STOPPED " in
      *" $unit "*) log "启动服务: $unit"; systemctl start "$unit" ;;
    esac
  done
fi

# 从 config.env 读取实际端口做健康检查。
read_config() {
  local key="$1" default="$2" value
  value="$(sed -n "s/^${key}=//p" "$CONFIG_FILE" 2>/dev/null | head -n 1)"
  echo "${value:-$default}"
}
EFFECTIVE_ADDR="$(read_config NB_ADDR ':8080')"
HEALTH_PORT="${EFFECTIVE_ADDR##*:}"

# 升级失败时自动回滚: 这是本脚本相对 "先卸载再安装" 的核心价值 ——
# 出问题不必手忙脚乱找旧包。
rollback_and_exit() {
  warn "$1"
  if [ -f "$BACKUP_DIR/noblack" ]; then
    warn "正在自动回滚到升级前的版本..."
    systemctl stop "$GO_SERVICE" 2>/dev/null || true
    has_unit "$MODEL_SERVICE" && systemctl stop "$MODEL_SERVICE" 2>/dev/null || true
    install -m 0755 "$BACKUP_DIR/noblack" "$INSTALL_DIR/noblack"
    [ -f "$BACKUP_DIR/noblack-model" ] &&
      install -m 0755 "$BACKUP_DIR/noblack-model" "$INSTALL_DIR/noblack-model"
    chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR" 2>/dev/null || true
    has_unit "$MODEL_SERVICE" && systemctl start "$MODEL_SERVICE" 2>/dev/null || true
    systemctl start "$GO_SERVICE" 2>/dev/null || true
    warn "已回滚。请检查日志: journalctl -u $GO_SERVICE -n 50"
  else
    warn "没有可用备份, 无法自动回滚。日志: journalctl -u $GO_SERVICE -n 50"
  fi
  exit 1
}

log "等待服务就绪..."
ready=0
for _ in $(seq 1 60); do
  if ! systemctl is-active --quiet "$GO_SERVICE"; then
    rollback_and_exit "主服务启动失败"
  fi
  if curl -sf --noproxy '*' "http://127.0.0.1:$HEALTH_PORT/health" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[ "$ready" = "1" ] || rollback_and_exit "健康检查超时 (60s)"

log ""
log "升级完成"
log "  服务地址: http://127.0.0.1:$HEALTH_PORT"
log "  版本信息: $(curl -sf --noproxy '*' "http://127.0.0.1:$HEALTH_PORT/health" 2>/dev/null | head -c 200)"
log ""
log "如发现异常可回滚: sudo $INSTALL_DIR/update.sh --rollback"
