#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
轻量版 allinone — 对标 NAS 的 allinone(35455 端口)
功能: /huya/{rid} /douyu/{rid} /douyin/{rid} /yy/{rid} → 301 Location 到真实签名流
由 reverse-skill 逆向 NAS allinone 行为 + real-url 开源算法复刻
"""
import re
import sys
import time
import json
import base64
import hashlib
import urllib.parse
import http.server
import threading
import os

import requests

PORT = int(os.environ.get("PORT", "35455"))
UA_ANDROID = ("Mozilla/5.0 (Linux; Android 5.0; SM-G900P Build/LRX21T) "
              "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.100 Mobile Safari/537.36")
UA_PC = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
         "(KHTML, like Gecko) Chrome/95.0.4638.69 Safari/537.36")
TIMEOUT = 15
FAIL_CACHE_TTL = 15  # 解析失败(None)的缓存秒数: 刚开播/限流恢复的房间最多 15s 内脱离"坏房"状态
TEST_STREAM = "https://cdn.jsdelivr.net/gh/feiyang666999/testvideo/sdr1080pvideo/playlist.m3u8"

_cache = {}
_cache_lock = threading.Lock()


def cached(key, ttl):
    def deco(fn):
        def wrapper(*a, **kw):
            now = time.time()
            # nocache=1 → 跳过缓存强制重新解析(CDN 签名 URL 失效时重试用)
            nocache = kw.pop("nocache", False)
            ck = f"{key}:{':'.join(map(str, a))}:{':'.join(f'{k}={v}' for k,v in sorted(kw.items()))}"
            if not nocache:
                with _cache_lock:
                    hit = _cache.get(ck)
                    if hit and now - hit[0] < hit[2]:
                        return hit[1]
            val = fn(*a, **kw)
            # 失败/无流结果短缓存(FAIL_CACHE_TTL), 房间恢复开播后可较快脱离测试片; 成功结果按 ttl
            eff_ttl = ttl if val is not None else min(ttl, FAIL_CACHE_TTL)
            with _cache_lock:
                _cache[ck] = (now, val, eff_ttl)
                if len(_cache) > 4096:
                    for ek in [k for k, (t, v, tt) in _cache.items() if now - t >= tt]:
                        _cache.pop(ek, None)
            return val
        return wrapper
    return deco


# ---------------- 虎牙(复刻 NAS: www页面官方签名 + 真实uid重签名 + 无fm) ----------------
@cached("huya", ttl=60)
def resolve_huya(rid):
    """虎牙: www.huya.com 房间页 → sFlvUrl/sStreamName/sFlvAntiCode(官方参数)
    重签名: MD5(fm前缀_uid_streamname_seqid_wsTime)
    关键: URL 不带 fm 参数(带 fm 会被 CDN 限流断开)
    节点: al.flv.huya.com(阿里云, 稳定)
    2026-09-02 修复: 页面含多条线路(aldirect/tx/hs/al)。aldirect(直连域)带 IPv6
    防盗链跳转易 403, 死取第一条会播不了。按优先级选稳定域:
    al > tx > hs > aldirect(最后兜底)。签名参数跨域通用, 已验证。"""
    try:
        s = requests.Session()
        s.headers.update({"User-Agent": UA_PC})
        page = s.get(f"https://www.huya.com/{rid}", timeout=TIMEOUT).text
        lines = re.findall(r'"sFlvUrl":"([^"]+)","sFlvUrlSuffix":"flv","sFlvAntiCode":"([^"]+)"', page)
        if not lines:
            return None

        def _pref(item):
            u = item[0]
            if "aldirect" in u:
                return 5
            if "al.flv.huya.com" in u:
                return 1
            if "tx.flv.huya.com" in u:
                return 2
            if "hs.flv.huya.com" in u:
                return 3
            return 4

        lines.sort(key=_pref)
        flv_base, anti = lines[0]
        stream = re.search(r'"sStreamName":"([^"]+)"', page).group(1)
        anti_dec = anti.replace('\\"', '"')
        params = dict(p.split("=", 1) for p in anti_dec.split("&"))
        wsTime = params["wsTime"]
        fm = urllib.parse.unquote(params["fm"])
        dec = base64.b64decode(fm + "==").decode(errors="replace")
        fm_pre = dec.split("_")[0]
        # 主播 uid: 2026 新页面用 lPresenterUid(可能 13 位, 不截断); 兜底旧 "uid" 格式
        # u=0 签名会被 CDN 403, 必须取到真实 uid
        uids = re.findall(r'"lPresenterUid":\s*"?(\d+)"?', page) \
            or re.findall(r'"uid":\s*"?(\d{5,12})"?', page) \
            or re.findall(r'"lUid":(\d{5,12})', page)
        u = uids[0] if uids else "0"
        seqid = str(int(time.time() * 1e7))
        wsSecret = hashlib.md5("_".join([fm_pre, u, stream, seqid, wsTime]).encode()).hexdigest()
        # NAS 参数集: 无 fm
        url = f"{flv_base}/{stream}.flv?wsSecret={wsSecret}&wsTime={wsTime}&u={u}&seqid={seqid}" \
              f"&txyp={params.get('txyp','')}&fs={params.get('fs','')}" \
              f"&sphdcdn=&sphdDC=&sphd=&exsphd=&ratio=0"
        return url
    except Exception as e:
        import sys
        sys.stderr.write(f"[huya {rid}] err: {e}\n")
        return None


# ---------------- 抖音(webcast API 直取) ----------------
@cached("douyin", ttl=60)
def resolve_douyin(rid):
    """抖音: webcast.amemv.com reflow/info API 直接返回 rtmp_pull_url"""
    try:
        s = requests.Session()
        s.headers.update({"User-Agent": UA_PC})
        url = ("https://webcast.amemv.com/webcast/room/reflow/info/"
               f"?room_id={rid}&app_id=1128&live_id=1")
        r = s.get(url, timeout=TIMEOUT).json()
        data = r.get("data", {}) or {}
        room = data.get("room", {}) or {}
        stream = room.get("stream_url", {}) or {}
        flv = stream.get("rtmp_pull_url") or stream.get("flv_pull_url") or stream.get("hls_pull_url")
        return flv or None
    except Exception as e:
        import sys
        sys.stderr.write(f"[douyin {rid}] err: {e}\n")
        return None


# ---------------- YY(interface.yy.com JSONP) ----------------
@cached("yy", ttl=60)
def resolve_yy(rid):
    """YY: JSONP 拿流地址 → 二次请求 stream URL"""
    try:
        s = requests.Session()
        s.headers.update({"User-Agent": UA_PC})
        url = f"https://interface.yy.com/hls/new/get/{rid}/{rid}/1200?source=wapyy&callback=cb"
        r = s.get(url, timeout=TIMEOUT).text
        m = re.search(r'cb\((.*)\)', r, re.S)
        if not m:
            return None
        j = json.loads(m.group(1))
        if j.get("code") != 0:
            return None
        data = j.get("data", {}) or {}
        # data 里可能有 xa/stream 等, 二次请求拿真实流
        return None  # 占位: YY 后续补全
    except Exception as e:
        import sys
        sys.stderr.write(f"[yy {rid}] err: {e}\n")
        return None


# ---------------- 斗鱼(hlsH5Preview 官方签名) ----------------
def douyu_md5(s):
    return hashlib.md5(s.encode()).hexdigest()


def _dw_log(rid, msg):
    import sys
    sys.stderr.write(f"[douyu {rid}] {msg}\n")


def _page_show_age_hours(page):
    """从 m.douyu 页面提取 showTime(开播/展示时间), 返回距今小时数; 无数据返回 None。
    实测: 742017 死房(视频号/一起看/轮播)页面 showTime 已是数天前、isLive:1 为缓存残留;
    在播房 error=0 不会走到该分支。"""
    if not page:
        return None
    m = re.search(r'"showTime"\s*:\s*(\d{10})', page)
    if not m:
        return None
    return (time.time() - int(m.group(1))) / 3600.0


@cached("douyu", ttl=120)
def resolve_douyu(rid):
    """斗鱼: m.douyu.com 页面 → playweb hlsH5Preview → rtmp_url+rtmp_live → FLV

    主链路与 NAS/real-url 一致(2026-09 实测有效, 在播房 error=0):
      POST https://playweb.douyucdn.cn/lapi/live/hlsH5Preview/{real_rid}
        headers: rid / time=t13(毫秒) / auth=MD5(rid+t13)
        body:    rid / did(默认 1000...01501)
      error 码: 0=有流; 102=不存在; 103=封禁; 104=未开播; 2002/2005=鉴权错;
        742017=该房间当前无直播流(死房/视频号/一起看/轮播未推流的标准返回)。

    失败分支(仅此分支改动, 主链路原样):
      - 742017 且页面 showTime 已很旧(>=24h)→ 判为死房, 直接 None, 不再做无谓重试;
      - 742017 且 showTime 新/缺失(疑似瞬断或刚停播)→ 第二路(移动 UA+随机 did)重试一次;
      - 其余错误码同样短路径返回。失败(None)只缓存 FAIL_CACHE_TTL(15s)。
    返回 None → do_GET 兜底测试片。只改斗鱼, 不碰其他平台。"""
    s = requests.Session()
    s.headers.update({"User-Agent": UA_PC})
    real_rid = ""
    page = ""
    try:
        page = s.get(f"https://m.douyu.com/{rid}", timeout=TIMEOUT).text
        m = re.search(r'rid":(\d{1,8}),"vipId', page)
        real_rid = m.group(1) if m else rid
    except Exception as e:
        _dw_log(rid, f"page err: {e}; 用 URL rid 直连")
        real_rid = rid
    url = "https://playweb.douyucdn.cn/lapi/live/hlsH5Preview/" + real_rid
    for attempt in (1, 2):
        t13 = str(int(time.time() * 1000))
        did = "10000000000000000000000000001501"
        try:
            if attempt == 2:  # 第二路: H5 移动 UA + 随机 did
                s.headers["User-Agent"] = UA_ANDROID
                did = douyu_md5(str(time.time() + time.time()))
            auth = douyu_md5(real_rid + t13)
            r = s.post(url, headers={"rid": real_rid, "time": t13, "auth": auth},
                       data={"rid": real_rid, "did": did}, timeout=TIMEOUT)
            j = r.json()
        except Exception as e:
            _dw_log(rid, f"api attempt{attempt} err: {e}")
            continue
        err = j.get("error")
        if err == 0:
            d = j.get("data") or {}
            rtmp_url = d.get("rtmp_url", "")
            rtmp_live = d.get("rtmp_live", "")
            if rtmp_url and rtmp_live:
                # 完整 URL: rtmp_url 是 host 前缀, rtmp_live 是 key+签名(HLS)
                full = f"{rtmp_url}/{rtmp_live}"
                return full.replace(".m3u8", ".flv") if ".m3u8" in full else full
            _dw_log(rid, "error=0 但 rtmp 字段为空")
            return None
        if err in (102, 103, 104, 2002, 2005):
            _dw_log(rid, f"err {err} ({j.get('msg')}) → 无流, 判死")
            return None
        if err == 742017:
            age_h = _page_show_age_hours(page)
            if age_h is not None and age_h >= 24:
                # 页面 isLive:1 是缓存残留, 房间早已停播(无推流源) → 不重试
                _dw_log(rid, f"err 742017 → 无直播流(死房, showTime {age_h:.1f}h 前)")
                return None
            _dw_log(rid, f"attempt{attempt} err 742017 (疑似瞬断, showTime {('%.1fh' % age_h) if age_h is not None else '未知'}前), 重试")
            continue
        _dw_log(rid, f"attempt{attempt} err {err} ({j.get('msg')})")
    return None


# ---------------- HTTP Handler ----------------
class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        import sys
        sys.stderr.write(f"[{time.strftime('%H:%M:%S')}] {self.client_address[0]} {fmt % args}\n")

    def serve_line_m3u(self, platform):
        """线路 m3u: /huyayqk.m3u /douyuyqk.m3u — 对齐 NAS 闭源版行为
        输出: #EXTINF 频道列表, URL = http://<本机IP>:PORT/{platform}/{rid}
        斗鱼: 读取 douyu-health.sh 产出的存活白名单(douyu-alive.txt), 只输出可播频道;
              白名单缺失或过期(>3h)则全量输出(保底, 不因过滤缺失而空列表)"""
        cf = os.path.join(os.path.dirname(os.path.abspath(__file__)), "channels.json")
        try:
            with open(cf) as f:
                channels = json.load(f)
        except Exception:
            channels = {}
        entries = channels.get(platform, [])
        host = os.environ.get("PUBLIC_HOST", "127.0.0.1")

        # 斗鱼存活白名单过滤
        alive = None
        if platform == "douyu":
            data_root = os.environ.get("DATA", os.path.expanduser("~"))
            alive_path = os.path.join(data_root, "lnmp", "applecms", "douyu-alive.txt")
            stamp_path = os.path.join(data_root, "lnmp", "applecms", ".douyu-health.stamp")
            try:
                if os.path.exists(stamp_path) and os.path.exists(alive_path):
                    age = time.time() - float(open(stamp_path).read().strip())
                    if age < 10800:  # 3 小时内刷新过才启用过滤
                        with open(alive_path) as f:
                            alive = set(f.read().split())
                        sys.stderr.write(f"[m3u douyu] whitelist {len(alive)} (age {int(age)}s)\n")
            except Exception as e:
                sys.stderr.write(f"[m3u douyu] whitelist err: {e}\n")

        lines = ["#EXTM3U"]
        prefix = "虎牙" if platform == "huya" else "斗鱼"
        for rid, inf in entries:
            if alive is not None and rid not in alive:
                continue
            # 分组名加平台前缀, 避免播放器里虎牙/斗鱼同名分组混排(如"一起看")
            if 'group-title="' in inf and f'{prefix}·' not in inf:
                inf = inf.replace('group-title="', f'group-title="{prefix}·', 1)
            lines.append(inf.rstrip("\ufffd").rstrip())
            lines.append(f"http://{host}:{PORT}/{platform}/{rid}")
        body = ("\n".join(lines) + "\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "audio/x-mpegurl")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = self.path.split("?")[0]
        # 线路 m3u: /huyayqk.m3u /douyuyqk.m3u /yylunbo.m3u
        if path in ("/huyayqk.m3u", "/douyuyqk.m3u", "/yylunbo.m3u"):
            plat = {"huyayqk": "huya", "douyuyqk": "douyu"}.get(path[1:-4])
            if plat:
                self.serve_line_m3u(plat)
            else:
                # yylunbo(央视频线路)全为测试流, 输出空 m3u (与 NAS 一致)
                body = b"#EXTM3U\n"
                self.send_response(200)
                self.send_header("Content-Type", "audio/x-mpegurl")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            return
        parts = [p for p in path.strip("/").split("/") if p]
        if len(parts) != 2 or parts[0] not in ("huya", "douyu", "douyin", "yy"):
            self.send_error(404, f"not supported: {path}")
            return
        platform, rid = parts
        resolver = {"huya": resolve_huya, "douyu": resolve_douyu,
                    "douyin": resolve_douyin, "yy": resolve_yy}[platform]
        # ?fresh=1 → 强制重新解析(跳过缓存), 供 stream-proxy 重试时拿全新签名 URL
        qs = self.path.split("?", 1)[1] if "?" in self.path else ""
        fresh = "fresh=1" in qs
        target = resolver(rid, nocache=True) if fresh else resolver(rid)
        if not target:
            target = TEST_STREAM
        self.send_response(301)
        self.send_header("Location", target)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", "0")
        self.end_headers()


def main():
    server = http.server.ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(f"轻量版 allinone 监听 :{PORT}")
    server.serve_forever()


if __name__ == "__main__":
    main()
