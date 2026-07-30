#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
GO_PID_FILE="$ROOT/data/noblack.pid"
MODEL_PID_FILE="$ROOT/data/noblack-model.pid"

pid_matches() {
  local pid="$1"
  local expected="$2"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null &&
    [ -r "/proc/$pid/exe" ] &&
    [ "$(readlink "/proc/$pid/exe")" = "$expected" ]
}

stop_recorded_process() {
  local pid_file="$1"
  local executable="$2"
  local label="$3"
  [ -f "$pid_file" ] || return 0

  local pid
  pid="$(sed -n '1p' "$pid_file")"
  if ! pid_matches "$pid" "$executable"; then
    echo "[noblack] stale $label PID removed: ${pid:-empty}"
    rm -f "$pid_file"
    return 0
  fi

  kill "$pid"
  for _ in $(seq 1 10); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "[noblack] stopped $label"
      return 0
    fi
    sleep 1
  done
  if pid_matches "$pid" "$executable"; then
    kill -KILL "$pid"
  fi
  rm -f "$pid_file"
  echo "[noblack] force-stopped $label"
}

stop_recorded_process "$GO_PID_FILE" "$ROOT/noblack" "Go service"
stop_recorded_process "$MODEL_PID_FILE" "$ROOT/noblack-model" "model service"
