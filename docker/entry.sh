#!/bin/sh
# livetv-allinone 容器入口：启动、持久化、定时更新和进程守护。
set -eu

APP_WORKDIR=/iptv-api
DATA="${DATA:-/data}"
LOG_DIR="$DATA/lnmp/logs"
APPLECMS_DIR="$DATA/lnmp/applecms"
HLS_DIR="$DATA/lnmp/allinone/hls"
IPTV_DATA_DIR="$DATA/iptv-api"

PUBLIC_HOST="${PUBLIC_HOST:-}"
AIO_PORT="${AIO_PORT:-35455}"
PROXY_PORT="${PROXY_PORT:-19090}"
PY_PORT="${PY_PORT:-19091}"
RES_PORT="${RES_PORT:-35456}"
PUBLIC_AIO_PORT="${PUBLIC_AIO_PORT:-$AIO_PORT}"
PUBLIC_PROXY_PORT="${PUBLIC_PROXY_PORT:-$PROXY_PORT}"
PUBLIC_PY_PORT="${PUBLIC_PY_PORT:-$PY_PORT}"
APP_PORT="${APP_PORT:-5180}"
NGINX_HTTP_PORT="${SERVICE_PORT:-${NGINX_HTTP_PORT:-8080}}"
NGINX_RTMP_PORT="${NGINX_RTMP_PORT:-1935}"
LIVETV_HTTP_PORT="${LIVETV_HTTP_PORT:-8081}"
IPTV_MIN_CHANNELS="${IPTV_MIN_CHANNELS:-20}"
IPTV_REJECT_VOD="${IPTV_REJECT_VOD:-1}"
IPTV_BLOCKLIST="${IPTV_BLOCKLIST:-}"
IPTV_BLOCKLIST_FILE="${IPTV_BLOCKLIST_FILE:-$APPLECMS_DIR/iptv-blocklist.txt}"

export APP_WORKDIR DATA PUBLIC_HOST AIO_PORT PROXY_PORT PY_PORT RES_PORT APP_PORT
export PUBLIC_AIO_PORT PUBLIC_PROXY_PORT PUBLIC_PY_PORT
export NGINX_HTTP_PORT NGINX_RTMP_PORT LIVETV_HTTP_PORT IPTV_MIN_CHANNELS
export IPTV_REJECT_VOD IPTV_BLOCKLIST IPTV_BLOCKLIST_FILE
export IPTV_API_PLAIN_OUTPUT=1 IPTV_API_SKIP_VERSION_CHECK=1

GO_LOG="$LOG_DIR/livetv.log"
PY_LOG="$LOG_DIR/huya-proxy.log"
IPTV_LOG="$LOG_DIR/iptv-api.log"
IPTV_UPDATE_LOG="$LOG_DIR/iptv-api-update.log"

validate_port() {
  name=$1
  value=$2
  case "$value" in
    ''|*[!0-9]*) echo "[entry] 无效端口 $name=$value" >&2; exit 2 ;;
  esac
  if [ "$value" -lt 1 ] || [ "$value" -gt 65535 ]; then
    echo "[entry] 端口超出范围 $name=$value" >&2
    exit 2
  fi
}

for pair in \
  "AIO_PORT:$AIO_PORT" "PROXY_PORT:$PROXY_PORT" "PY_PORT:$PY_PORT" \
  "RES_PORT:$RES_PORT" "APP_PORT:$APP_PORT" \
  "PUBLIC_AIO_PORT:$PUBLIC_AIO_PORT" "PUBLIC_PROXY_PORT:$PUBLIC_PROXY_PORT" \
  "PUBLIC_PY_PORT:$PUBLIC_PY_PORT" \
  "NGINX_HTTP_PORT:$NGINX_HTTP_PORT" "NGINX_RTMP_PORT:$NGINX_RTMP_PORT" \
  "LIVETV_HTTP_PORT:$LIVETV_HTTP_PORT"; do
  validate_port "${pair%%:*}" "${pair#*:}"
done

mkdir -p "$LOG_DIR" "$APPLECMS_DIR" "$HLS_DIR" \
  "$IPTV_DATA_DIR/config" "$IPTV_DATA_DIR/output"

# iptv-api 的配置和结果都持久化到 /data。旧镜像只桥接最终 M3U，容器重启后
# 上游 output/config 会丢失，且更新进程可能不再启动。
if [ ! -f "$IPTV_DATA_DIR/config/config.ini" ]; then
  echo "[entry] 初始化持久化 iptv-api 配置"
  cp -a /iptv-api-config/. "$IPTV_DATA_DIR/config/"
fi
if [ ! -f "$IPTV_BLOCKLIST_FILE" ]; then
  mkdir -p "$(dirname "$IPTV_BLOCKLIST_FILE")"
  touch "$IPTV_BLOCKLIST_FILE"
fi

rm -rf /iptv-api/config /iptv-api/output
ln -s "$IPTV_DATA_DIR/config" /iptv-api/config
ln -s "$IPTV_DATA_DIR/output" /iptv-api/output

# shellcheck source=/dev/null
. "$APP_WORKDIR/.venv/bin/activate"

if [ -f /proc/net/if_inet6 ]; then
  IPV6_HTTP_LISTEN="listen [::]:${NGINX_HTTP_PORT};"
else
  IPV6_HTTP_LISTEN=""
fi

sed -e "s/\${APP_PORT}/$APP_PORT/g" \
    -e "s/\${NGINX_HTTP_PORT}/$NGINX_HTTP_PORT/g" \
    -e "s/\${NGINX_RTMP_PORT}/$NGINX_RTMP_PORT/g" \
    -e "s|\${IPV6_HTTP_LISTEN}|$IPV6_HTTP_LISTEN|g" \
    /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf

sed -e "s/\${LIVETV_HTTP_PORT}/$LIVETV_HTTP_PORT/g" \
    -e "s/\${AIO_PORT}/$AIO_PORT/g" \
    /etc/nginx/livetv/nginx.conf.template > /etc/nginx/livetv/nginx.conf

write_pid() {
  printf '%s\n' "$2" > "/run/livetv-$1.pid"
}

start_iptv_main() {
  echo "[entry] 启动 iptv-api 定时更新"
  (cd "$APP_WORKDIR" && exec python -u main.py) >> "$IPTV_UPDATE_LOG" 2>&1 &
  write_pid iptv-main "$!"
}

start_iptv_nginx() {
  echo "[entry] 启动 iptv-api nginx ($NGINX_HTTP_PORT)"
  nginx -g 'daemon off;' >> "$IPTV_LOG" 2>&1 &
  write_pid iptv-nginx "$!"
}

start_iptv_flask() {
  echo "[entry] 启动 iptv-api API ($APP_PORT)"
  (cd "$APP_WORKDIR" && exec python -u -m gunicorn service.app:app \
    -b "127.0.0.1:$APP_PORT" --workers=1 --timeout=1000) >> "$IPTV_LOG" 2>&1 &
  write_pid iptv-flask "$!"
}

start_livetv() {
  echo "[entry] 启动 Go livetv (AIO=$AIO_PORT PROXY=$PROXY_PORT)"
  /usr/local/bin/livetv >> "$GO_LOG" 2>&1 &
  write_pid livetv "$!"
}

start_huya_res() {
  echo "[entry] 启动虎牙解析 ($RES_PORT)"
  PORT="$RES_PORT" python3 /opt/livetv/allinone.py >> "$PY_LOG" 2>&1 &
  write_pid huya-res "$!"
}

start_huya_flv() {
  echo "[entry] 启动虎牙 FLV 代理 ($PY_PORT)"
  ALLINONE_BASE="http://127.0.0.1:$AIO_PORT" \
  HLS_ROOT="$HLS_DIR" \
  python3 /opt/livetv/stream-proxy.py "$PY_PORT" >> "$PY_LOG" 2>&1 &
  write_pid huya-flv "$!"
}

start_livetv_nginx() {
  echo "[entry] 启动聚合入口 nginx ($LIVETV_HTTP_PORT)"
  nginx -p /etc/nginx/livetv/ -c nginx.conf -g 'daemon off;' \
    >> "$LOG_DIR/nginx.log" 2>&1 &
  write_pid nginx "$!"
}

start_iptv_main
start_iptv_nginx
start_iptv_flask
start_livetv
start_huya_res
start_huya_flv
start_livetv_nginx

# 首次同步直播平台当前开播房间；失败时 sync_channels.py 会保留旧文件。
echo "[entry] 后台同步虎牙/斗鱼当前开播房间"
python3 /opt/livetv/sync_channels.py \
  --out "$DATA/lnmp/allinone/channels.json" >> "$LOG_DIR/sync-channels.log" 2>&1 &

# 等待 iptv-api 首次生成 result.m3u，然后立即桥接；不必等到第一个 15 分钟周期。
(
  attempts=0
  while [ "$attempts" -lt 90 ]; do
    if [ -s /iptv-api/output/result.m3u ] && /opt/livetv/scripts/bridge_iptv.sh; then
      exit 0
    fi
    attempts=$((attempts + 1))
    sleep 10
  done
) &

# 显式写 crontab，避免依赖 Alpine 是否预置 /etc/periodic/6h 调度规则。
cat > /etc/crontabs/root <<'CRONEOF'
*/15 * * * * /opt/livetv/scripts/bridge_iptv.sh
17 */6 * * * python3 /opt/livetv/sync_channels.py --out "$DATA/lnmp/allinone/channels.json" >> "$DATA/lnmp/logs/sync-channels.log" 2>&1
CRONEOF
if [ -n "${NAS_HOST:-}" ] && [ -n "${NAS_USER:-}" ] && [ -n "${NAS_M3U:-}" ]; then
  printf '7,22,37,52 * * * * /opt/livetv/scripts/sync_iptv_from_nas.sh\n' >> /etc/crontabs/root
  echo "[entry] 已启用可选 NAS 同步"
elif [ -n "${NAS_HOST:-}${NAS_USER:-}${NAS_M3U:-}" ]; then
  echo "[entry] NAS 同步未启用：NAS_HOST、NAS_USER、NAS_M3U 必须同时设置" >&2
fi
crond -b -l 8

pid_alive() {
  pid_file="/run/livetv-$1.pid"
  [ -s "$pid_file" ] || return 1
  pid=$(cat "$pid_file")
  kill -0 "$pid" 2>/dev/null
}

shutdown() {
  echo "[entry] 收到退出信号，停止子进程"
  for pid_file in /run/livetv-*.pid; do
    [ -f "$pid_file" ] || continue
    pid=$(cat "$pid_file" 2>/dev/null || true)
    case "$pid" in ''|*[!0-9]*) continue ;; esac
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  exit 0
}
trap shutdown INT TERM

echo "[entry] 全部服务已启动；播放列表: http://<服务器>:$LIVETV_HTTP_PORT/allinone.m3u"
while :; do
  sleep 20
  for service in iptv-main iptv-nginx iptv-flask livetv huya-res huya-flv nginx; do
    if pid_alive "$service"; then
      continue
    fi
    echo "[guard] $service 已退出，重新启动"
    case "$service" in
      iptv-main) start_iptv_main ;;
      iptv-nginx) start_iptv_nginx ;;
      iptv-flask) start_iptv_flask ;;
      livetv) start_livetv ;;
      huya-res) start_huya_res ;;
      huya-flv) start_huya_flv ;;
      nginx) start_livetv_nginx ;;
    esac
  done
done
