#!/usr/bin/env python3
# ============================================================
# allinone 流代理 (stream-proxy v4)
# 模式1 /stream/{platform}/{rid}: FLV 直通（低延迟，保留用于回滚）
# 模式2 /hls/{platform}/{rid}/index.m3u8: ffmpeg 转 HLS（推荐）
#   - ffmpeg 带 -reconnect 自动重连 CDN 断流
#   - 播放器按 ts 分片拉取, 短暂抖动无感
#   - 空闲 5 分钟自动回收 ffmpeg 进程
# ============================================================
import os
import shutil
import signal
import subprocess
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("PROXY_PORT", "9090"))
if __name__ == "__main__" and len(sys.argv) > 1:
    PORT = int(sys.argv[1])
ALLINONE_BASE = os.environ.get("ALLINONE_BASE", "http://127.0.0.1:35455")
UPSTREAM_PROXY = os.environ.get("UPSTREAM_PROXY", "")
IPV6_PLATFORMS = tuple(x.strip() for x in os.environ.get("IPV6_PLATFORMS", "").split(",") if x.strip())
CHUNK = 4 * 1024
HLS_ROOT = os.environ.get("HLS_ROOT", os.path.join(os.environ.get("DATA", os.path.expanduser("~")), "lnmp", "allinone", "hls"))
HLS_IDLE = float(os.environ.get("HLS_IDLE", "300"))
HLS_STALE = float(os.environ.get("HLS_STALE", "25"))
HLS_WARMUP = float(os.environ.get("HLS_WARMUP", "45"))

hls_lock = threading.Lock()
hls_state = {}  # key "platform_rid" -> {"proc","dir","last"}


def valid_hls_filename(name):
    if name == "index.m3u8":
        return True
    if not (name.startswith("seg_") and name.endswith(".ts")):
        return False
    return name[4:-3].isdigit()


def stop_process(proc):
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass
    try:
        proc.kill()
    except ProcessLookupError:
        pass
    try:
        proc.wait(timeout=3)
    except (subprocess.TimeoutExpired, ChildProcessError):
        pass


def resolve_with_curl(path):
    """用 curl 拿签名 URL: -I 请求 allinone, 提取 Location (301)"""
    try:
        out = subprocess.run(
            ["curl", "-sS", "-m", "15", "-D", "-", "-o", "/dev/null",
             ALLINONE_BASE + path],
            capture_output=True, text=True, timeout=20)
        for line in out.stdout.splitlines():
            if line.lower().startswith("location:"):
                return line.split(":", 1)[1].strip()
        out2 = subprocess.run(
            ["curl", "-sS", "-m", "15", "-L", "-o", "/dev/null",
             "-w", "%{url_effective}", ALLINONE_BASE + path],
            capture_output=True, text=True, timeout=20)
        return out2.stdout.strip() or None
    except Exception as e:
        sys.stderr.write(f"  resolve err: {e}\n")
        return None


def start_ffmpeg(platform, rid):
    key = f"{platform}_{rid}"
    with hls_lock:
        st = hls_state.get(key)
        if st and st["proc"].poll() is None:
            return st
    # 先 resolve 拿 CDN 签名 URL; 拿不到再退回本机 FLV 直通
    real_url = resolve_with_curl(f"/{platform}/{rid}?fresh=1")
    if not real_url:
        self_base = os.environ.get("SELF_BASE", f"http://127.0.0.1:{PORT}")
        real_url = f"{self_base}/stream/{platform}/{rid}"
    if not real_url:
        sys.stderr.write(f"  [hls] resolve failed {key}\n")
        return None
    d = os.path.join(HLS_ROOT, key)
    os.makedirs(d, exist_ok=True)
    for f in os.listdir(d):
        try:
            os.remove(os.path.join(d, f))
        except Exception:
            pass
    cmd = ["ffmpeg", "-hide_banner", "-loglevel", "error",
           "-user_agent", "Mozilla/5.0",
           "-reconnect", "1", "-reconnect_streamed", "1",
           "-reconnect_delay_max", "5"]
    cmd += ["-i", real_url,
            "-map", "0:v:0", "-map", "0:a:0", "-c", "copy",
            "-f", "hls", "-hls_time", "4", "-hls_list_size", "20",
            "-hls_flags", "delete_segments",
            "-hls_segment_filename", os.path.join(d, "seg_%05d.ts"),
            os.path.join(d, "index.m3u8")]
    errfile = open(os.path.join(d, "ffmpeg.err"), "ab")
    proc = subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=errfile,
                            start_new_session=True)
    with hls_lock:
        hls_state[key] = {"proc": proc, "dir": d, "last": time.time(),
                          "started": time.time(), "err": errfile}
    sys.stderr.write(f"  [hls] start {key} pid={proc.pid}\n")
    return hls_state[key]


def hls_watchdog():
    while True:
        time.sleep(5)
        now = time.time()
        with hls_lock:
            keys = list(hls_state.keys())
        for key in keys:
            with hls_lock:
                st = hls_state.get(key)
                if not st:
                    continue
                proc = st["proc"]
                m3u = os.path.join(st["dir"], "index.m3u8")
                started = st.get("started", now)
                age = now - started
                dead = proc.poll() is not None
                if os.path.exists(m3u):
                    # m3u8 已生成: 超过 HLS_STALE 没更新才算卡死
                    stale = (now - os.path.getmtime(m3u)) > HLS_STALE
                else:
                    # 冷启动中: 给 HLS_WARMUP 秒宽限, 不要误杀正在起的 ffmpeg
                    stale = age > HLS_WARMUP
                idle = now - st["last"]
                d = st["dir"]
            if idle > HLS_IDLE:
                stop_process(proc)
                st["err"].close()
                with hls_lock:
                    hls_state.pop(key, None)
                shutil.rmtree(d, ignore_errors=True)
                sys.stderr.write(f"  [hls] idle reaped {key}\n")
            elif dead or stale:
                platform, rid = key.split("_", 1)
                stop_process(proc)
                st["err"].close()
                with hls_lock:
                    hls_state.pop(key, None)
                sys.stderr.write(f"  [hls] restart {key} (dead={dead} stale={stale})\n")
                start_ffmpeg(platform, rid)


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write(f"[{time.strftime('%H:%M:%S')}] {self.client_address[0]} {fmt % args}\n")

    def send_static(self, path, content_type):
        try:
            with open(path, "rb") as f:
                data = f.read()
        except Exception:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def handle_hls(self, path):
        parts = [p for p in path.strip("/").split("/") if p]
        if len(parts) < 4 or parts[0] != "hls":
            self.send_error(404, "bad hls path")
            return
        platform, rid, fname = parts[1], parts[2], parts[3]
        if platform not in ("huya", "douyu") or not valid_hls_filename(fname):
            self.send_error(404, "bad hls target")
            return
        key = f"{platform}_{rid}"
        st = start_ffmpeg(platform, rid)
        if not st:
            self.send_error(502, "hls start failed")
            return
        st["last"] = time.time()
        fp = os.path.join(st["dir"], fname)
        # 冷启动等 30s(ffmpeg 首片 8-15s), 避免播放器拿到 404 放弃
        for _ in range(300):
            if os.path.exists(fp):
                break
            if st["proc"].poll() is not None:
                break
            time.sleep(0.1)
        if fname.endswith(".m3u8"):
            self.send_static(fp, "application/vnd.apple.mpegurl")
        else:
            self.send_static(fp, "video/mp2t")

    def do_GET(self):
        path = self.path.split("?")[0]
        # HLS 模式: 保留 /hls/ 前缀直接处理
        if path.startswith("/hls/"):
            self.handle_hls(path)
            return
        # 兼容 CF /stream/ 前缀（路径透传）
        if path.startswith("/stream/"):
            path = path[len("/stream"):]
        if not path.startswith(("/huya/", "/douyu/", "/bilibili/", "/yy/", "/douyin/")):
            self.send_error(404, f"not proxied: {path}")
            return

        # ---- FLV 直通模式 ----
        real_url = resolve_with_curl(path)
        if not real_url:
            self.send_error(502, "resolve failed")
            return
        # 上游回退到测试流(jsdelivr m3u8): 该房间未开播/已失效 → 明确 404, 不转发假流
        if "jsdelivr" in real_url or "testvideo" in real_url or "feiyang666999" in real_url \
                or real_url.endswith(".m3u8") or ".m3u8?" in real_url:
            sys.stderr.write(f"  -> {path} offline (test fallback) 404\n")
            self.send_error(404, "offline (room not live)")
            return

        def open_stream(url):
            cmd = ["curl", "-sS", "-f", "-L", "-N", "--max-time", "7200", "--speed-limit", "1", "--speed-time", "20"]
            platform = path.strip("/").split("/", 1)[0]
            if platform in IPV6_PLATFORMS:
                cmd.append("-6")
            else:
                cmd.append("-4")
            if UPSTREAM_PROXY:
                cmd.extend(["-x", UPSTREAM_PROXY])
            cmd.extend(["-A", "Mozilla/5.0", "-o", "-", url])
            # 虎牙 CDN 防盗链: 部分房间(aldirect/多线路房)强制校验 Referer, 不带则 403
            # 2026-09-02: 补上 Referer → 403/502 房间可播 (如 26355768)
            rid = path.strip("/").split("/", 1)[1].split("?")[0] if platform in ("huya", "douyu") else ""
            ref = {
                "huya": "https://www.huya.com/" + rid,
                "douyu": "https://www.douyu.com/" + rid,
            }.get(platform)
            if ref:
                cmd.extend(["-H", "Referer: " + ref])
            return subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True)

        proc = open_stream(real_url)
        first = proc.stdout.read(4096)
        # FLV 魔数校验: 首字节应为 "FLV\x01" (46 4c 56 01); 非 FLV(HTML/m3u8文本)视为坏流
        def looks_like_flv(b):
            return len(b) >= 4 and b[:3] == b"FLV"
        if not first or not looks_like_flv(first):
            err = proc.stderr.read(200).decode(errors="replace")
            sys.stderr.write(f"  bad stream first={first[:8]!r} ({err}), fresh retry\n")
            stop_process(proc)
            real_url2 = resolve_with_curl(path + "?fresh=1")
            if real_url2 and "jsdelivr" not in real_url2 and "testvideo" not in real_url2:
                proc = open_stream(real_url2)
                first = proc.stdout.read(4096)
            if not first or not looks_like_flv(first):
                err = proc.stderr.read(200).decode(errors="replace")
                sys.stderr.write(f"  bad stream after retry first={first[:16]!r} ({err})\n")
                stop_process(proc)
                self.send_error(502, "stream not FLV")
                return

        self.send_response(200)
        self.send_header("Content-Type", "video/x-flv")
        self.send_header("Transfer-Encoding", "chunked")
        self.send_header("Cache-Control", "no-store")
        self.end_headers()

        total = 0
        buf = first
        try:
            while buf:
                self.wfile.write(f"{len(buf):X}\r\n".encode())
                self.wfile.write(buf)
                self.wfile.write(b"\r\n")
                self.wfile.flush()
                total += len(buf)
                buf = proc.stdout.read(CHUNK)
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            try:
                self.wfile.write(b"0\r\n\r\n")
            except Exception:
                pass
            stop_process(proc)
            sys.stderr.write(f"  -> {path} {total/1024/1024:.1f}MB forwarded\n")


if __name__ == "__main__":
    os.makedirs(HLS_ROOT, exist_ok=True)
    t = threading.Thread(target=hls_watchdog, daemon=True)
    t.start()
    srv = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    srv.daemon_threads = True
    sys.stderr.write(f"stream-proxy v4 :{PORT} (hls_root={HLS_ROOT})\n")
    srv.serve_forever()
