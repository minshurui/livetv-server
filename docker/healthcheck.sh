#!/bin/sh
set -eu

for name in livetv iptv-main iptv-nginx iptv-flask huya-res huya-flv nginx; do
  pid_file="/run/livetv-${name}.pid"
  [ -s "$pid_file" ] || exit 1
  pid=$(cat "$pid_file")
  kill -0 "$pid" 2>/dev/null || exit 1
done

curl -fsS --max-time 5 "http://127.0.0.1:${LIVETV_HTTP_PORT:-8081}/allinone.m3u" \
  | grep -q '^#EXTM3U'
curl -fsS --max-time 5 "http://127.0.0.1:${NGINX_HTTP_PORT:-8080}/" >/dev/null
