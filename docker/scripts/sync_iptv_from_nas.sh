#!/bin/bash
# ============================================================
# sync_iptv_from_nas.sh — 央视/卫视直播源自愈同步 (Termux/Android 端)
#
# 背景: 手机上的电视直播源(.iptv-result.m3u)最初是 NAS 静态复制来的,
#       之后不再更新, 没有自愈机制。而 NAS 上的 iptv-api 管道每 30 分钟
#       自愈刷新一次 result.m3u(公网源校验, 失效即换)。
#
# 本脚本: 免密 SSH 到 NAS, 拉取自愈源, 原子替换本地死快照。
#         带 last-good 保护: 拉取失败/内容为空时不破坏现有文件。
#
# 用法:   bash sync_iptv_from_nas.sh
# 定时:   crontab 每 30 分钟调用(crontab -e):
#         */30 * * * * bash ~/lnmp/scripts/sync_iptv_from_nas.sh \
#             >> ~/lnmp/logs/iptv-sync.log 2>&1
#
# 环境变量(均可覆盖):
#   NAS_HOST    NAS Tailscale IP  (默认 100.105.60.99)
#   NAS_USER    SSH 用户          (默认 minshurui)
#   NAS_M3U     NAS 自愈源路径    (默认 /vol2/apps/iptv-api/output/result.m3u)
#   DST_FILE    本地输出路径      (默认 applecms/.iptv-result.m3u)
#   MIN_LINES   内容保护: 少于此时频道数视为失败不覆盖 (默认 20)
# ============================================================
set -euo pipefail

NAS_HOST="${NAS_HOST:-100.105.60.99}"
NAS_USER="${NAS_USER:-minshurui}"
NAS_M3U="${NAS_M3U:-/vol2/apps/iptv-api/output/result.m3u}"
HOME_DIR="/data/data/com.termux/files/home"
DST_FILE="${DST_FILE:-${HOME_DIR}/lnmp/applecms/.iptv-result.m3u}"
MIN_LINES="${MIN_LINES:-20}"
SSH_OPT="-o StrictHostKeyChecking=no -o ConnectTimeout=8 -o BatchMode=yes"

log() { echo "[$(date '+%F %T')] $*"; }

# 1) 从 NAS 拉取(经 SSH cat)
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
if ! timeout 20 ssh $SSH_OPT "${NAS_USER}@${NAS_HOST}" "cat '${NAS_M3U}'" >"$TMP" 2>/dev/null; then
    log "FAIL 拉取 NAS 自愈源失败 (${NAS_HOST}) — 保留本地旧文件"
    exit 1
fi

# 2) 内容校验: 必须含 #EXTINF 行, 且频道数达标
CNT=$(grep -c '^#EXTINF' "$TMP" 2>/dev/null || echo 0)
if [ "$CNT" -lt "$MIN_LINES" ]; then
    log "FAIL 拉到的频道数=${CNT} < ${MIN_LINES}, 视为坏数据 — 保留旧文件"
    exit 1
fi

# 3) 再校验: 必须包含央视或卫视标记(避免拉到空/错误内容)
if ! grep -qE '央视频道|卫视频道|CCTV' "$TMP"; then
    log "FAIL 内容不含央视/卫视频道标记 — 保留旧文件"
    exit 1
fi

# 4) 原子替换 (先写临时, mv 保证原子性)
mkdir -p "$(dirname "$DST_FILE")"
TMP2="${DST_FILE}.new.$$"
cp "$TMP" "$TMP2"
chmod 644 "$TMP2"
mv -f "$TMP2" "$DST_FILE"

log "OK  已同步 NAS 自愈源: 频道=${CNT}, 输出=${DST_FILE}"
