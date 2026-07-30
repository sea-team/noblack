#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
DATA_DIR="$ROOT/data"
LOG_DIR="$ROOT/logs"
GO_PID_FILE="$ROOT/data/noblack.pid"
MODEL_PID_FILE="$ROOT/data/noblack-model.pid"
GO_LOG="$ROOT/logs/noblack.log"
MODEL_LOG="$ROOT/logs/noblack-model.log"

load_config() {
  local line key value existing
  [ -f "$ROOT/config.env" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      ""|\#*) continue ;;
      *=*)
        key="${line%%=*}"
        value="${line#*=}"
        case "$key" in
          NB_[A-Z0-9_]*)
            eval "existing=\${$key-}"
            if [ -z "$existing" ]; then
              export "$key=$value"
            fi
            ;;
        esac
        ;;
    esac
  done < "$ROOT/config.env"
}

pid_matches() {
  local pid="$1"
  local expected="$2"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null &&
    [ -r "/proc/$pid/exe" ] &&
    [ "$(readlink "/proc/$pid/exe")" = "$expected" ]
}

assert_not_running() {
  local pid_file="$1"
  local executable="$2"
  if [ -f "$pid_file" ]; then
    local pid
    pid="$(sed -n '1p' "$pid_file")"
    if pid_matches "$pid" "$executable"; then
      echo "[noblack] already running: $executable (pid=$pid)" >&2
      exit 1
    fi
    rm -f "$pid_file"
  fi
}

tcp_open() {
  local port="$1"
  (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null
}

http_get() {
  local port="$1"
  local path="$2"
  {
    exec 3<>"/dev/tcp/127.0.0.1/$port"
    printf 'GET %s HTTP/1.0\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n' "$path" >&3
    cat <&3
  } 2>/dev/null
}

cleanup_failed_start() {
  "$ROOT/stop.sh" >/dev/null 2>&1 || true
}

load_config
: "${NB_ADDR:=:8080}"
: "${NB_MODEL_PORT:=8091}"
: "${NB_MODEL_THREADS:=2}"
: "${NB_MODEL_COMBINE_POLICY:=max}"
: "${NB_MODEL_PASS_THRESHOLD:=0.15}"
: "${NB_MODEL_BLOCK_THRESHOLD:=0.5}"
: "${NB_WATCH:=true}"

mkdir -p "$DATA_DIR" "$LOG_DIR"
if [ ! -f "$DATA_DIR/words.json" ]; then
  cp "$ROOT/words.json" "$DATA_DIR/words.json"
fi

assert_not_running "$GO_PID_FILE" "$ROOT/noblack"
if [ "${NB_KEYWORDS_ONLY:-0}" != "1" ]; then
  assert_not_running "$MODEL_PID_FILE" "$ROOT/noblack-model"
fi

go_port="${NB_ADDR##*:}"
if tcp_open "$go_port"; then
  echo "[noblack] port already in use: $go_port" >&2
  exit 1
fi
if [ "${NB_KEYWORDS_ONLY:-0}" != "1" ] && tcp_open "$NB_MODEL_PORT"; then
  echo "[noblack] model port already in use: $NB_MODEL_PORT" >&2
  exit 1
fi

model_url=""
if [ "${NB_KEYWORDS_ONLY:-0}" != "1" ]; then
  export NB_PACKAGE_ROOT="$ROOT"
  export NB_MODEL_HOST="127.0.0.1"
  export NB_MODEL_PORT
  export NB_MODEL_THREADS
  export NB_MODEL_COMBINE_POLICY
  export NB_MODEL_PASS_THRESHOLD
  export NB_MODEL_BLOCK_THRESHOLD
  export NB_LITE_MODEL="${NB_LITE_MODEL:-$ROOT/models/lite-production-v1}"
  export NB_MACBERT_MODEL="${NB_MACBERT_MODEL:-$ROOT/models/macbert-production-v1}"

  nohup "$ROOT/noblack-model" >>"$MODEL_LOG" 2>&1 &
  model_pid=$!
  printf '%s\n' "$model_pid" > "$MODEL_PID_FILE"

  model_ready=0
  for _ in $(seq 1 180); do
    if ! pid_matches "$model_pid" "$ROOT/noblack-model"; then
      echo "[noblack] model service exited; see $MODEL_LOG" >&2
      cleanup_failed_start
      exit 1
    fi
    model_health="$(http_get "$NB_MODEL_PORT" "/health" || true)"
    if [[ "$model_health" == *'"ok":true'* &&
          "$model_health" == *'"lite"'* &&
          "$model_health" == *'"macbert"'* &&
          "$model_health" == *'"device":"cpu"'* ]]; then
      model_ready=1
      break
    fi
    sleep 1
  done
  if [ "$model_ready" != "1" ]; then
    echo "[noblack] model health check timed out; see $MODEL_LOG" >&2
    cleanup_failed_start
    exit 1
  fi
  model_url="http://127.0.0.1:$NB_MODEL_PORT"
fi

go_args=(
  -addr "$NB_ADDR"
  -words "$DATA_DIR/words.json"
  -watch="$NB_WATCH"
  -model-service-url "$model_url"
)
if [ -n "${NB_STATS:-}" ]; then
  go_args+=(-stats-file "$NB_STATS")
fi
if [ -n "${NB_TOKEN:-}" ]; then
  go_args+=(-token "$NB_TOKEN")
fi
if [ "${NB_CI:-false}" = "true" ]; then
  go_args+=(-ci)
fi

nohup "$ROOT/noblack" "${go_args[@]}" >>"$GO_LOG" 2>&1 &
go_pid=$!
printf '%s\n' "$go_pid" > "$GO_PID_FILE"

go_ready=0
for _ in $(seq 1 30); do
  if ! pid_matches "$go_pid" "$ROOT/noblack"; then
    echo "[noblack] Go service exited; see $GO_LOG" >&2
    cleanup_failed_start
    exit 1
  fi
  go_health="$(http_get "$go_port" "/health" || true)"
  if [[ "$go_health" == *'"code":200'* ]]; then
    go_ready=1
    break
  fi
  sleep 1
done
if [ "$go_ready" != "1" ]; then
  echo "[noblack] Go health check timed out; see $GO_LOG" >&2
  cleanup_failed_start
  exit 1
fi

if [ "${NB_KEYWORDS_ONLY:-0}" = "1" ]; then
  echo "[noblack] keyword service ready: http://127.0.0.1:$go_port"
else
  echo "[noblack] keyword + dual-model service ready: http://127.0.0.1:$go_port"
fi
