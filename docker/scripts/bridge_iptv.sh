#!/bin/sh
set -eu

SRC="${IPTV_SOURCE_FILE:-/iptv-api/output/result.m3u}"
DST="${IPTV_TARGET_FILE:-/data/lnmp/applecms/.iptv-result.m3u}"
BLOCKLIST_FILE="${IPTV_BLOCKLIST_FILE:-/data/lnmp/applecms/iptv-blocklist.txt}"
LOG="${IPTV_BRIDGE_LOG:-/data/lnmp/logs/bridge.log}"
MIN_CHANNELS="${IPTV_MIN_CHANNELS:-20}"

[ -f "$SRC" ] || exit 0

mkdir -p "$(dirname "$DST")" "$(dirname "$LOG")"
set -- /opt/livetv/filter_iptv.py \
  --input "$SRC" \
  --output "$DST" \
  --blocklist-file "$BLOCKLIST_FILE" \
  --blocklist "${IPTV_BLOCKLIST:-}" \
  --min-channels "$MIN_CHANNELS"

if [ "${IPTV_REJECT_VOD:-1}" = "0" ]; then
  set -- "$@" --allow-vod
fi

if output=$(python3 "$@" 2>&1); then
  printf '[bridge] %s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$output" >> "$LOG"
else
  status=$?
  printf '[bridge] %s 失败(code=%s): %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$status" "$output" >> "$LOG"
  exit "$status"
fi
