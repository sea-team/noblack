#!/usr/bin/env bash
# Noblack 一键部署脚本 (systemd)
#
# 用法:
#   sudo ./deploy.sh [选项]
#
# 选项:
#   --keywords-only        仅词库模式, 不部署 AI 模型服务 (省内存, 不加载模型)
#   --dir <路径>           安装目录 (默认 /opt/noblack)
#   --user <用户名>        运行服务的系统用户 (默认 noblack, 不存在时自动创建)
#   --port <端口>          Go 服务监听端口 (默认 8080)
#   --model-port <端口>    模型服务端口 (默认 8091, 仅完整模式)
#   --detect-mode <模式>   检测模式 word_only|model_only|model_first|word_first|both
#   --recall-on-miss       开启未命中召回
#   --token <令牌>         词库写操作鉴权令牌
#   --detect-token <令牌>  检测接口(/check,/stats)鉴权令牌; 留空则检测不鉴权
#   --no-start             只安装不启动
#   -h, --help             显示帮助
#
# 脚本从自身所在的发布包目录读取文件, 因此需要在解压后的包内运行。

set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

INSTALL_DIR="/opt/noblack"
SERVICE_USER="noblack"
GO_PORT="8080"
MODEL_PORT="8091"
DETECT_MODE=""
RECALL_ON_MISS=""
AUTH_TOKEN=""
DETECT_TOKEN=""
KEYWORDS_ONLY=0
AUTO_START=1

GO_SERVICE="noblack"
MODEL_SERVICE="noblack-model"
SYSTEMD_DIR="/etc/systemd/system"

log()  { echo "[deploy] $*"; }
warn() { echo "[deploy] $*" >&2; }
die()  { echo "[deploy] 错误: $*" >&2; exit 1; }

usage() { sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    --keywords-only) KEYWORDS_ONLY=1; shift ;;
    --dir)           INSTALL_DIR="${2:?--dir 需要参数}"; shift 2 ;;
    --user)          SERVICE_USER="${2:?--user 需要参数}"; shift 2 ;;
    --port)          GO_PORT="${2:?--port 需要参数}"; shift 2 ;;
    --model-port)    MODEL_PORT="${2:?--model-port 需要参数}"; shift 2 ;;
    --detect-mode)   DETECT_MODE="${2:?--detect-mode 需要参数}"; shift 2 ;;
    --recall-on-miss) RECALL_ON_MISS="true"; shift ;;
    --token)         AUTH_TOKEN="${2:?--token 需要参数}"; shift 2 ;;
    --detect-token)  DETECT_TOKEN="${2:?--detect-token 需要参数}"; shift 2 ;;
    --no-start)      AUTO_START=0; shift ;;
    -h|--help)       usage ;;
    *)               die "未知选项: $1 (用 --help 查看用法)" ;;
  esac
done

# ---------- 前置检查 ----------

[ "$(id -u)" = "0" ] || die "需要 root 权限, 请用 sudo 运行"
command -v systemctl >/dev/null 2>&1 || die "未检测到 systemd, 本脚本依赖 systemctl"

case "$INSTALL_DIR" in
  /*) ;;
  *) die "--dir 必须是绝对路径: $INSTALL_DIR" ;;
esac
# 安装目录会被 rsync --delete 同步, 指向系统关键目录会造成破坏。
case "$INSTALL_DIR" in
  /|/usr|/etc|/var|/bin|/sbin|/lib|/boot|/home|/root|/opt)
    die "--dir 不能是系统关键目录: $INSTALL_DIR" ;;
esac

for port in "$GO_PORT" "$MODEL_PORT"; do
  case "$port" in
    ''|*[!0-9]*) die "端口必须是数字: $port" ;;
  esac
  [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die "端口超出范围: $port"
done
if [ "$KEYWORDS_ONLY" != "1" ] && [ "$GO_PORT" = "$MODEL_PORT" ]; then
  die "Go 端口与模型端口不能相同: $GO_PORT"
fi

if [ -n "$DETECT_MODE" ]; then
  case "$DETECT_MODE" in
    word_only|model_only|model_first|word_first|both) ;;
    *) die "--detect-mode 无效: $DETECT_MODE" ;;
  esac
  # 仅词库模式不部署模型服务, 依赖模型的检测模式无法工作。
  if [ "$KEYWORDS_ONLY" = "1" ] && \
     { [ "$DETECT_MODE" = "model_only" ] || [ "$DETECT_MODE" = "model_first" ]; }; then
    die "--keywords-only 与 --detect-mode=$DETECT_MODE 冲突 (该模式需要模型服务)"
  fi
fi

# 校验发布包完整性: 必需文件缺失时立即失败, 避免装出半个服务。
[ -f "$SOURCE_DIR/noblack" ] || die "未找到 noblack 可执行文件, 请在解压后的发布包目录内运行"
[ -f "$SOURCE_DIR/data/words.json" ] || die "未找到 data/words.json"
if [ "$KEYWORDS_ONLY" != "1" ]; then
  [ -f "$SOURCE_DIR/noblack-model" ] || die "未找到 noblack-model; 如只需词库检测请加 --keywords-only"
  [ -d "$SOURCE_DIR/models" ] || die "未找到 models 目录; 如只需词库检测请加 --keywords-only"
fi

if [ "$KEYWORDS_ONLY" = "1" ]; then
  log "部署模式: 仅词库 (不安装模型服务)"
else
  log "部署模式: 完整 (词库 + 双模型)"
fi

# ---------- 系统用户 ----------

if id -u "$SERVICE_USER" >/dev/null 2>&1; then
  log "复用已有系统用户: $SERVICE_USER"
else
  log "创建系统用户: $SERVICE_USER"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" 2>/dev/null \
    || useradd --system --no-create-home --shell /sbin/nologin "$SERVICE_USER"
fi

# ---------- 停止已有服务 ----------

for unit in "$GO_SERVICE" "$MODEL_SERVICE"; do
  if systemctl is-active --quiet "$unit" 2>/dev/null; then
    log "停止运行中的服务: $unit"
    systemctl stop "$unit"
  fi
done

# ---------- 同步文件 ----------

log "安装目录: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR" "$INSTALL_DIR/data" "$INSTALL_DIR/logs"

install -m 0755 "$SOURCE_DIR/noblack" "$INSTALL_DIR/noblack"
if [ "$KEYWORDS_ONLY" != "1" ]; then
  install -m 0755 "$SOURCE_DIR/noblack-model" "$INSTALL_DIR/noblack-model"
  log "同步模型文件 (约 400MB, 请稍候)..."
  if command -v rsync >/dev/null 2>&1; then
    rsync -a --delete "$SOURCE_DIR/models/" "$INSTALL_DIR/models/"
  else
    rm -rf "$INSTALL_DIR/models"
    cp -r "$SOURCE_DIR/models" "$INSTALL_DIR/models"
  fi
fi

# 词库只在首次安装时复制, 升级时保留线上已维护的词库。
if [ -f "$INSTALL_DIR/data/words.json" ]; then
  log "保留已有词库: $INSTALL_DIR/data/words.json"
else
  install -m 0644 "$SOURCE_DIR/data/words.json" "$INSTALL_DIR/data/words.json"
  log "安装初始词库"
fi

[ -f "$SOURCE_DIR/config.env.example" ] &&
  install -m 0644 "$SOURCE_DIR/config.env.example" "$INSTALL_DIR/config.env.example"
[ -f "$SOURCE_DIR/README.txt" ] &&
  install -m 0644 "$SOURCE_DIR/README.txt" "$INSTALL_DIR/README.txt"

# ---------- 生成 config.env ----------

CONFIG_FILE="$INSTALL_DIR/config.env"

# 把 KEY=value 就地写回配置: 已有该键则替换整行, 没有则追加。
# value 里可能含斜杠(路径)与 & (sed 替换特殊字符), 用 | 作分隔并转义 &。
set_config() {
  local key="$1" value="$2" escaped
  escaped="$(printf '%s' "$value" | sed -e 's/[\\&|]/\\&/g')"
  if grep -q "^${key}=" "$CONFIG_FILE"; then
    sed -i "s|^${key}=.*|${key}=${escaped}|" "$CONFIG_FILE"
  else
    printf '%s=%s\n' "$key" "$value" >> "$CONFIG_FILE"
  fi
}

if [ -f "$CONFIG_FILE" ]; then
  log "保留已有配置: $CONFIG_FILE (命令行参数不会覆盖已有配置)"
else
  log "生成配置文件: $CONFIG_FILE (基于 config.env.example)"
  [ -f "$SOURCE_DIR/config.env.example" ] || die "未找到 config.env.example, 无法生成配置"
  install -m 0640 "$SOURCE_DIR/config.env.example" "$CONFIG_FILE"

  # 用命令行参数覆盖模板默认值, 其余项保留模板的默认值与注释。
  set_config NB_ADDR ":$GO_PORT"
  set_config NB_MODEL_PORT "$MODEL_PORT"
  set_config NB_STATS "$INSTALL_DIR/data/stats.json"
  set_config NB_TOKEN "$AUTH_TOKEN"
  set_config NB_DETECT_TOKEN "$DETECT_TOKEN"
  set_config NB_DETECT_MODE "${DETECT_MODE:-both}"
  set_config NB_RECALL_ON_MISS "${RECALL_ON_MISS:-false}"
  chmod 0640 "$CONFIG_FILE"
fi

# 令牌可能写在配置里, 限制为服务用户可读。
chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR"
chmod 0640 "$CONFIG_FILE"

# ---------- 生成 systemd unit ----------

# 从 config.env 读取实际生效值, 保证 unit 与配置一致
# (升级时配置文件保留原值, 不能直接用命令行参数)。
read_config() {
  local key="$1" default="$2" value
  value="$(sed -n "s/^${key}=//p" "$CONFIG_FILE" | head -n 1)"
  echo "${value:-$default}"
}
EFFECTIVE_MODEL_PORT="$(read_config NB_MODEL_PORT "$MODEL_PORT")"

if [ "$KEYWORDS_ONLY" != "1" ]; then
  log "写入 systemd unit: $MODEL_SERVICE.service"
  cat > "$SYSTEMD_DIR/$MODEL_SERVICE.service" <<EOF
[Unit]
Description=Noblack dual-model inference service (CPU)
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
EnvironmentFile=$CONFIG_FILE
Environment=NB_PACKAGE_ROOT=$INSTALL_DIR
Environment=NB_MODEL_HOST=127.0.0.1
Environment=NB_LITE_MODEL=$INSTALL_DIR/models/lite-production-v1
Environment=NB_MACBERT_MODEL=$INSTALL_DIR/models/macbert-production-v1
ExecStart=$INSTALL_DIR/noblack-model
Restart=on-failure
RestartSec=5
# 模型加载需要较长时间, 放宽启动超时。
TimeoutStartSec=300
StandardOutput=append:$INSTALL_DIR/logs/noblack-model.log
StandardError=append:$INSTALL_DIR/logs/noblack-model.log

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=$INSTALL_DIR

[Install]
WantedBy=multi-user.target
EOF
else
  # 从完整模式切换到仅词库模式时, 移除残留的模型服务注册。
  if [ -f "$SYSTEMD_DIR/$MODEL_SERVICE.service" ]; then
    log "移除模型服务注册 (仅词库模式)"
    systemctl disable "$MODEL_SERVICE" >/dev/null 2>&1 || true
    rm -f "$SYSTEMD_DIR/$MODEL_SERVICE.service"
  fi
fi

log "写入 systemd unit: $GO_SERVICE.service"
{
  echo "[Unit]"
  echo "Description=Noblack sensitive word detection service"
  echo "After=network.target"
  if [ "$KEYWORDS_ONLY" != "1" ]; then
    # 模型服务先起, 但用 Wants 而非 Requires:
    # 模型挂掉时词库检测仍可用, 不应连带停掉主服务。
    echo "Wants=$MODEL_SERVICE.service"
    echo "After=$MODEL_SERVICE.service"
  fi
  echo ""
  echo "[Service]"
  echo "Type=simple"
  echo "User=$SERVICE_USER"
  echo "Group=$SERVICE_USER"
  echo "WorkingDirectory=$INSTALL_DIR"
  echo "EnvironmentFile=$CONFIG_FILE"
  printf 'ExecStart=%s/noblack \\\n' "$INSTALL_DIR"
  printf '  -addr ${NB_ADDR} \\\n'
  printf '  -words %s/data/words.json \\\n' "$INSTALL_DIR"
  printf '  -watch=${NB_WATCH} \\\n'
  printf '  -stats-file ${NB_STATS} \\\n'
  printf '  -token ${NB_TOKEN} \\\n'
  printf '  -detect-mode ${NB_DETECT_MODE} \\\n'
  printf '  -recall-on-miss=${NB_RECALL_ON_MISS} \\\n'
  if [ "$KEYWORDS_ONLY" != "1" ]; then
    printf '  -model-service-url http://127.0.0.1:%s\n' "$EFFECTIVE_MODEL_PORT"
  else
    printf '  -model-service-url ""\n'
  fi
  echo "Restart=on-failure"
  echo "RestartSec=3"
  echo "StandardOutput=append:$INSTALL_DIR/logs/noblack.log"
  echo "StandardError=append:$INSTALL_DIR/logs/noblack.log"
  echo ""
  echo "NoNewPrivileges=true"
  echo "PrivateTmp=true"
  echo "ProtectSystem=full"
  echo "ProtectHome=true"
  echo "ReadWritePaths=$INSTALL_DIR"
  echo ""
  echo "[Install]"
  echo "WantedBy=multi-user.target"
} > "$SYSTEMD_DIR/$GO_SERVICE.service"

# ---------- 安装管理脚本 ----------

for helper in restart.sh uninstall.sh status.sh; do
  if [ -f "$SOURCE_DIR/$helper" ]; then
    install -m 0755 "$SOURCE_DIR/$helper" "$INSTALL_DIR/$helper"
  fi
done

systemctl daemon-reload
log "启用开机自启"
[ "$KEYWORDS_ONLY" != "1" ] && systemctl enable "$MODEL_SERVICE" >/dev/null 2>&1
systemctl enable "$GO_SERVICE" >/dev/null 2>&1

# ---------- 启动 ----------

if [ "$AUTO_START" != "1" ]; then
  log "已安装但未启动 (--no-start); 手动启动: systemctl start $GO_SERVICE"
  exit 0
fi

if [ "$KEYWORDS_ONLY" != "1" ]; then
  log "启动模型服务 (首次加载模型需要时间)..."
  systemctl start "$MODEL_SERVICE"
  model_ready=0
  for _ in $(seq 1 180); do
    if ! systemctl is-active --quiet "$MODEL_SERVICE"; then
      warn "模型服务启动失败, 日志: journalctl -u $MODEL_SERVICE -n 50"
      warn "或查看 $INSTALL_DIR/logs/noblack-model.log"
      exit 1
    fi
    if curl -sf --noproxy '*' "http://127.0.0.1:$EFFECTIVE_MODEL_PORT/health" >/dev/null 2>&1; then
      model_ready=1
      break
    fi
    sleep 1
  done
  if [ "$model_ready" != "1" ]; then
    warn "模型服务健康检查超时 (180s), 日志: journalctl -u $MODEL_SERVICE -n 50"
    exit 1
  fi
  log "模型服务就绪"
fi

log "启动主服务..."
systemctl start "$GO_SERVICE"
EFFECTIVE_ADDR="$(read_config NB_ADDR ":$GO_PORT")"
HEALTH_PORT="${EFFECTIVE_ADDR##*:}"
go_ready=0
for _ in $(seq 1 30); do
  if ! systemctl is-active --quiet "$GO_SERVICE"; then
    warn "主服务启动失败, 日志: journalctl -u $GO_SERVICE -n 50"
    exit 1
  fi
  if curl -sf --noproxy '*' "http://127.0.0.1:$HEALTH_PORT/health" >/dev/null 2>&1; then
    go_ready=1
    break
  fi
  sleep 1
done
if [ "$go_ready" != "1" ]; then
  warn "主服务健康检查超时, 日志: journalctl -u $GO_SERVICE -n 50"
  exit 1
fi

log ""
log "部署完成"
log "  服务地址: http://127.0.0.1:$HEALTH_PORT"
log "  安装目录: $INSTALL_DIR"
log "  配置文件: $CONFIG_FILE"
log "  检测模式: $(read_config NB_DETECT_MODE both)"
log ""
log "常用命令:"
log "  查看状态: $INSTALL_DIR/status.sh"
log "  重启服务: $INSTALL_DIR/restart.sh"
log "  卸载服务: $INSTALL_DIR/uninstall.sh"
log "  查看日志: journalctl -u $GO_SERVICE -f"
