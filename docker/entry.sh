#!/bin/sh
# livetv-allinone 容器入口：单 Alpine 容器整合全部直播服务
#   [自研] Go livetv(35455 m3u/301 + 19090 斗鱼FLV)
#   [自研] Python 虎牙(allinone.py 35456 解析 + stream-proxy.py 19091 FLV直通)
#   [自研] nginx(8081 反代 /allinone.m3u)
#   [开源] guovern/iptv-api 电视源聚合(8080 UI + 自愈更新), 输出 result.m3u 供 m3u 引用
set -u

APP_WORKDIR=/iptv-api
DATA="/data"
GOLOG="$DATA/logs/livetv.log"
PYLOG="$DATA/logs/huya-proxy.log"
IPTVLOG="$DATA/logs/iptv-api.log"
ASTARLOG="$DATA/logs/iptv-api-update.log"

# 日志目录 + 数据目录(挂载卷)
mkdir -p "$DATA/logs" "$DATA/applecms" "$DATA/allinone/hls"

# ---------- 环境变量(默认值, 通过 -e 覆盖) ----------
PUBLIC_HOST="${PUBLIC_HOST:-}"
AIO_PORT="${AIO_PORT:-35455}"
PROXY_PORT="${PROXY_PORT:-19090}"
PY_PORT="${PY_PORT:-19091}"       # Python 虎牙 FLV
RES_PORT="${RES_PORT:-35456}"     # Python 虎牙 301 解析
export PUBLIC_HOST AIO_PORT PROXY_PORT PY_PORT RES_PORT DATA

# ============================================================
# 1. iptv-api 电视源聚合 ======================================
# ============================================================
echo "[entry] 启动 iptv-api 电视源聚合 (8080)"
if [ -d "$APP_WORKDIR" ] && [ -f "$APP_WORKDIR/main.py" ]; then
  cd "$APP_WORKDIR"
  # 首次启动: 从内置模板 config 初始化(若容器内 config 无文件)
  if [ -d /iptv-api-config ] && [ ! -f "$APP_WORKDIR/config/config.ini" ]; then
    echo "[entry]   初始化 iptv-api config (从 /iptv-api-config)"
    [ -d "$APP_WORKDIR/config" ] || mkdir -p "$APP_WORKDIR/config"
    for f in /iptv-api-config/*; do
      [ -e "$APP_WORKDIR/config/$(basename "$f")" ] || cp -r "$f" "$APP_WORKDIR/config/" 2>/dev/null
    done
  fi
  # 启用 venv
  . "$APP_WORKDIR/.venv/bin/activate" 2>/dev/null || echo "[entry]   ⚠ iptv-api venv 缺失"
  export IPTV_API_PLAIN_OUTPUT=1 IPTV_API_SKIP_VERSION_CHECK=1
  # 渲染 iptv-api 的 nginx 模板(8080/r	tmp)
  APP_PATH="$APP_WORKDIR"
  if [ -n "${SERVICE_PORT:-}" ]; then NGINX_HTTP_PORT="$SERVICE_PORT"; fi
  sed -e "s/\${APP_PORT}/$APP_PORT/g" \
      -e "s/\${NGINX_HTTP_PORT}/$NGINX_HTTP_PORT/g" \
      -e "s/\${NGINX_RTMP_PORT}/$NGINX_RTMP_PORT/g" \
      -e "s|\${IPV6_HTTP_LISTEN}||g" \
      /etc/nginx/nginx.conf.template > /etc/nginx/nginx.conf
  # 若 result.m3u 缺失/过期(>6h), 先跑一次 main.py 生成电视源(数据挂在 /data)
  [ -d "$APP_WORKDIR/output" ] || mkdir -p "$APP_WORKDIR/output"
  if [ ! -f /data/applecms/.iptv-result.m3u ]; then
    echo "[entry]   result.m3u 缺失, 后台首先生成电视源"
    python -u "$APP_WORKDIR/main.py" >> "$ASTARLOG" 2>&1 &
    echo $! > /run/iptv-main.pid
  fi
  # iptv-api 自带编译 nginx(8080, 带 rtmp + flask 反代)
  if [ -x /usr/local/nginx/sbin/nginx ]; then
    /usr/local/nginx/sbin/nginx -g 'daemon off;' >> "$IPTVLOG" 2>&1 &
    echo $! > /run/iptv-nginx.pid
    echo "[entry]   iptv-api nginx(8080) pid=$!"
  fi
  # 常驻 flask API(5180) 供 nginx 反代 + /stat rtmp 统计
  (cd "$APP_WORKDIR" && exec python -u -m gunicorn service.app:app -b 127.0.0.1:$APP_PORT --workers=1 --timeout=1000) >> "$IPTVLOG" 2>&1 &
  echo $! > /run/iptv-flask.pid
  echo "[entry]   iptv-api flask(5180) pid=$!"
  # 主循环自愈更新(iptv-api 常驻 main; 若与上面首启重复, 由本项目 crond 接管定时)
else
  echo "[entry]   ⚠ iptv-api 未安装, 跳过电视源聚合(仅直播服务)"
fi

# ============================================================
# 2. Go livetv ================================================
# ============================================================
echo "[entry] 启动 Go livetv (AIO=$AIO_PORT PROXY=$PROXY_PORT)"
if [ -x /usr/local/bin/livetv ]; then
  /usr/local/bin/livetv >> "$GOLOG" 2>&1 &
  echo $! > /run/livetv.pid
  echo "[entry]   Go livetv pid=$(cat /run/livetv.pid)"
else
  echo "[entry]   ⚠ 未找到 livetv 二进制"
fi

# ============================================================
# 3. Python 虎牙 ==============================================
# ============================================================
echo "[entry] 启动 Python 虎牙解析 (RES=$RES_PORT)"
PORT=$RES_PORT python3 /opt/livetv/allinone.py >> "$PYLOG" 2>&1 &
echo $! > /run/huya-res.pid
echo "[entry]   虎牙解析 pid=$!"

echo "[entry] 启动 Python 虎牙 FLV 直通 (PY=$PY_PORT)"
SELF_BASE="http://127.0.0.1:$PY_PORT" \
HLS_ROOT="$DATA/allinone/hls" \
python3 /opt/livetv/stream-proxy.py $PY_PORT >> "$PYLOG" 2>&1 &
echo $! > /run/huya-flv.pid
echo "[entry]   虎牙FLV pid=$!"

# ============================================================
# 4. 自研 nginx (8081 反代 allinone.m3u) =====================
# ============================================================
echo "[entry] 启动 nginx 反代 (8081)"
if [ -f /etc/nginx/livetv/nginx.conf ]; then
  nginx -p /etc/nginx/livetv -c /etc/nginx/livetv/nginx.conf -g "daemon off;" >> "$DATA/logs/nginx.log" 2>&1 &
  echo $! > /run/nginx.pid
  echo "[entry]   nginx pid=$!"
else
  echo "[entry]   ⚠ nginx 配置缺失, 跳过反代"
fi

# ============================================================
# 4.5 直播白名单自动同步(自研 sync_channels.py, 官方API抓取)=
# ============================================================
# 首启: 后台同步一次, 不阻塞服务启动(失败则保留已有/空白名单, Go 全量保底)
if [ -f /opt/livetv/sync_channels.py ]; then
  echo "[entry] 同步直播白名单(官方API, 后台)..."
  python3 /opt/livetv/sync_channels.py --out "$DATA/lnmp/allinone/channels.json"     >> "$DATA/logs/sync-channels.log" 2>&1 &
  # cron 每6小时刷新一次(保持房间列表新鲜)
  cat > /etc/periodic/6h/sync-channels <<'SYNCEOF'
#!/bin/sh
python3 /opt/livetv/sync_channels.py --out "$DATA/lnmp/allinone/channels.json"   >> "$DATA/logs/sync-channels.log" 2>&1
SYNCEOF
  chmod +x /etc/periodic/6h/sync-channels 2>/dev/null
  echo "[entry]   首次同步已后台启动 + 每6小时cron"
else
  echo "[entry]   ⚠ sync_channels.py 缺失, 跳过白名单同步(用预置/空白名单)"
fi

# ============================================================
# 5. 电视源自愈 crond ========================================
# ============================================================
echo "[entry] 启动 crond (电视源自愈 + iptv-api输出桥接)"
# 桥接: iptv-api 的 output/result.m3u → Go livetv 读取的 /data/lnmp/applecms/.iptv-result.m3u
# 每15分钟: 若 iptv-api 有新 result.m3u 且有效, 复制到 Go 能读的位置(带 last-good 保护)
cat > /etc/periodic/15min/bridge-iptv <<'BRIDGEEOF'
#!/bin/sh
SRC=/iptv-api/output/result.m3u
DST=/data/lnmp/applecms/.iptv-result.m3u
[ -f "$SRC" ] || exit 0
CNT=$(grep -c "^#EXTINF" "$SRC" 2>/dev/null || echo 0)
if [ "$CNT" -ge 20 ] && grep -qE "央视|卫视|CCTV|📺" "$SRC" 2>/dev/null; then
  mkdir -p "$(dirname "$DST")"
  cp -f "$SRC" "$DST.tmp" 2>/dev/null && mv -f "$DST.tmp" "$DST" 2>/dev/null \
    && echo "[bridge] $(date) 已同步 $CNT 频道电视源" >> /data/logs/bridge.log
fi
BRIDGEEOF
chmod +x /etc/periodic/15min/bridge-iptv
# NAS 同步脚本可选用(若用户仍想从 NAS 拉源); 容器内默认用 iptv-api 自产源
if [ -f /opt/livetv/scripts/sync_iptv_from_nas.sh ] && [ -n "${NAS_HOST:-}" ]; then
  cp /opt/livetv/scripts/sync_iptv_from_nas.sh /etc/periodic/15min/sync-iptv 2>/dev/null
  chmod +x /etc/periodic/15min/sync-iptv 2>/dev/null
  echo "[entry]   NAS 同步已启用 (NAS_HOST=$NAS_HOST)"
fi
crond -b 2>&1 || echo "[entry]   crond 启动跳过"
echo "[entry]   定时任务: /etc/periodic/15min/ (每15分钟)"

# ============================================================
# 存活守护: 任一服务停止自动拉起 ==============================
# ============================================================
trap 'echo "[entry] 收到信号, 清理退出"; for p in /run/*.pid; do kill $(cat $p) 2>/dev/null; done; exit 0' INT TERM
echo "[entry] ★ 全部服务启动完成, 进入守护循环"
while :; do
  sleep 20
  for svc in livetv huya-res huya-flv nginx; do
    [ -f /run/$svc.pid ] || continue
    PID=$(cat /run/$svc.pid 2>/dev/null)
    if ! kill -0 "$PID" 2>/dev/null; then
      case "$svc" in
        livetv)   echo "[guard] Go livetv 挂了, 重启"; /usr/local/bin/livetv >> "$GOLOG" 2>&1 & echo $! > /run/livetv.pid;;
        huya-res) echo "[guard] 虎牙解析挂了, 重启"; PORT=$RES_PORT python3 /opt/livetv/allinone.py >> "$PYLOG" 2>&1 & echo $! > /run/huya-res.pid;;
        huya-flv) echo "[guard] 虎牙FLV挂了, 重启"; HLS_ROOT="$DATA/allinone/hls" python3 /opt/livetv/stream-proxy.py $PY_PORT >> "$PYLOG" 2>&1 & echo $! > /run/huya-flv.pid;;
        nginx)    echo "[guard] nginx挂了, 重启"; nginx -p /etc/nginx/livetv -c /etc/nginx/livetv/nginx.conf -g "daemon off;" >> "$DATA/logs/nginx.log" 2>&1 & echo $! > /run/nginx.pid;;
      esac
    fi
  done
done