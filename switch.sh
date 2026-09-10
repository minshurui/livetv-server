#!/bin/bash
# Termux/Linux 进程管理：Go 服务 + 虎牙 Python 流代理。
set -u

PROJECT_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
DATA_ROOT="${DATA:-$HOME}"
RUN_DIR="$DATA_ROOT/lnmp/run"
LOG_DIR="$DATA_ROOT/lnmp/logs"
CHANNELS_FILE="$DATA_ROOT/lnmp/allinone/channels.json"
AIO_PORT="${AIO_PORT:-35455}"
PROXY_PORT="${PROXY_PORT:-19090}"
PY_PORT="${PY_PORT:-19091}"
PUBLIC_AIO_PORT="${PUBLIC_AIO_PORT:-$AIO_PORT}"
PUBLIC_PROXY_PORT="${PUBLIC_PROXY_PORT:-$PROXY_PORT}"
PUBLIC_PY_PORT="${PUBLIC_PY_PORT:-$PY_PORT}"

export DATA="$DATA_ROOT" AIO_PORT PROXY_PORT PY_PORT
export PUBLIC_AIO_PORT PUBLIC_PROXY_PORT PUBLIC_PY_PORT

mkdir -p "$RUN_DIR" "$LOG_DIR" "$(dirname "$CHANNELS_FILE")"

pid_file() { printf '%s/%s.pid' "$RUN_DIR" "$1"; }

is_running() {
  local file pid
  file="$(pid_file "$1")"
  [[ -s "$file" ]] || return 1
  pid="$(<"$file")"
  [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null
}

start_service() {
  if is_running livetv; then
    echo "[skip] Go livetv 已运行，PID $(<"$(pid_file livetv)")"
  else
    nohup "$PROJECT_DIR/livetv" >> "$LOG_DIR/livetv.log" 2>&1 &
    echo "$!" > "$(pid_file livetv)"
  fi

  if is_running huya-proxy; then
    echo "[skip] 虎牙代理已运行，PID $(<"$(pid_file huya-proxy)")"
  else
    ALLINONE_BASE="http://127.0.0.1:$AIO_PORT" \
      HLS_ROOT="$DATA_ROOT/lnmp/allinone/hls" \
      nohup python3 "$PROJECT_DIR/docker/stream-proxy.py" "$PY_PORT" \
      >> "$LOG_DIR/huya-proxy.log" 2>&1 &
    echo "$!" > "$(pid_file huya-proxy)"
  fi

  python3 "$PROJECT_DIR/docker/sync_channels.py" --out "$CHANNELS_FILE" \
    >> "$LOG_DIR/sync-channels.log" 2>&1 &
  sleep 1
  status_service
}

stop_one() {
  local name=$1 file pid
  file="$(pid_file "$name")"
  if is_running "$name"; then
    pid="$(<"$file")"
    kill "$pid" 2>/dev/null || true
    echo "[stop] $name PID $pid"
  fi
  rm -f "$file"
}

stop_service() {
  stop_one huya-proxy
  stop_one livetv
}

status_service() {
  local failed=0
  for name in livetv huya-proxy; do
    if is_running "$name"; then
      echo "[ok] $name PID $(<"$(pid_file "$name")")"
    else
      echo "[fail] $name 未运行"
      failed=1
    fi
  done
  return "$failed"
}

case "${1:-}" in
  start) start_service ;;
  stop) stop_service ;;
  restart) stop_service; sleep 1; start_service ;;
  status) status_service ;;
  *) echo "用法: $0 {start|stop|restart|status}"; exit 2 ;;
esac
