#!/usr/bin/env python3
"""对运行中的 livetv-server 做虎牙真实 FLV 播放冒烟测试。"""

from __future__ import annotations

import argparse
import json
import math
import re
import socket
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path


ROOM_RE = re.compile(r"/stream/huya/(\d+)")
UA = "Mozilla/5.0 (livetv-server live smoke test)"


def percentile(values: list[float], quantile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, math.ceil(quantile * len(ordered)) - 1))
    return ordered[index]


def fetch_room_ids(playlist_url: str, wait_seconds: int, limit: int) -> list[str]:
    """等待频道同步完成，并从 M3U 里取若干个当前虎牙房间。"""
    deadline = time.monotonic() + wait_seconds
    last_error = "M3U 中还没有虎牙频道"
    while time.monotonic() < deadline:
        try:
            req = urllib.request.Request(playlist_url, headers={"User-Agent": UA})
            with urllib.request.urlopen(req, timeout=10) as response:
                body = response.read(4 * 1024 * 1024).decode("utf-8", "replace")
            rooms = list(dict.fromkeys(ROOM_RE.findall(body)))
            if rooms:
                return rooms[:limit]
            last_error = f"{playlist_url} 中没有 /stream/huya/ 房间"
        except (OSError, urllib.error.URLError) as exc:
            last_error = str(exc)
        time.sleep(3)
    raise RuntimeError(f"等待频道同步超时：{last_error}")


def probe_room(
    proxy_url: str,
    room_id: str,
    duration: int,
    min_bytes: int,
    max_gap: float,
) -> dict[str, object]:
    stream_url = f"{proxy_url.rstrip('/')}/stream/huya/{room_id}?fresh=1"
    started = time.monotonic()
    result: dict[str, object] = {
        "room_id": room_id,
        "url": stream_url,
        "passed": False,
        "bytes": 0,
    }

    try:
        req = urllib.request.Request(
            stream_url,
            headers={"User-Agent": UA, "Accept": "video/x-flv,*/*"},
        )
        with urllib.request.urlopen(req, timeout=max(10.0, max_gap + 3.0)) as response:
            connected = time.monotonic()
            result["http_status"] = response.status
            result["content_type"] = response.headers.get("Content-Type", "")
            result["connect_seconds"] = round(connected - started, 3)

            total = 0
            chunks = 0
            first = bytearray()
            longest_gap = 0.0
            gaps: list[float] = []
            previous = connected
            deadline = connected + duration
            # read() 会尽量等满请求长度，把“填充 32 KiB 的耗时”误算成断粮。
            # read1() 只做一次底层读取，更接近播放器实际收到网络数据的节奏。
            read_available = getattr(response, "read1", response.read)

            while time.monotonic() < deadline:
                chunk = read_available(4 * 1024)
                now = time.monotonic()
                gap = now - previous
                gaps.append(gap)
                longest_gap = max(longest_gap, gap)
                previous = now
                if not chunk:
                    raise RuntimeError("代理在测试时间结束前返回 EOF")
                if len(first) < 3:
                    first.extend(chunk[: 3 - len(first)])
                total += len(chunk)
                chunks += 1

            elapsed = max(time.monotonic() - connected, 0.001)
            result.update(
                {
                    "duration_seconds": round(elapsed, 3),
                    "bytes": total,
                    "chunks": chunks,
                    "average_mbps": round(total * 8 / elapsed / 1_000_000, 3),
                    "gap_p95_seconds": round(percentile(gaps, 0.95), 3),
                    "gap_p99_seconds": round(percentile(gaps, 0.99), 3),
                    "longest_gap_seconds": round(longest_gap, 3),
                    "flv_magic": bytes(first).decode("ascii", "replace"),
                }
            )
            problems: list[str] = []
            if bytes(first) != b"FLV":
                problems.append(f"首字节不是 FLV：{bytes(first)!r}")
            if total < min_bytes:
                problems.append(f"接收字节不足：{total} < {min_bytes}")
            if longest_gap > max_gap:
                problems.append(f"最长数据间隔过大：{longest_gap:.2f}s > {max_gap:.2f}s")
            result["problems"] = problems
            result["passed"] = not problems
    except urllib.error.HTTPError as exc:
        result["error"] = f"HTTP {exc.code}: {exc.reason}"
    except (RuntimeError, OSError, socket.timeout, urllib.error.URLError) as exc:
        result["error"] = str(exc)

    result["total_seconds"] = round(time.monotonic() - started, 3)
    return result


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--playlist-url", default="http://127.0.0.1:8081/allinone.m3u")
    parser.add_argument("--proxy-url", default="http://127.0.0.1:19090")
    parser.add_argument("--room-id", help="指定虎牙房间；留空时从 M3U 自动选择")
    parser.add_argument("--duration", type=int, default=30, help="每个成功候选的实播秒数")
    parser.add_argument("--min-bytes", type=int, default=1024 * 1024)
    parser.add_argument("--max-gap", type=float, default=0.5, help="允许的最长无数据秒数")
    parser.add_argument("--candidates", type=int, default=5, help="最多尝试的当前直播房间数")
    parser.add_argument("--playlist-wait", type=int, default=180)
    parser.add_argument("--report", default="huya-smoke-report.json")
    args = parser.parse_args()
    if not 10 <= args.duration <= 300:
        parser.error("--duration 必须在 10 到 300 秒之间")
    if args.min_bytes < 1:
        parser.error("--min-bytes 必须大于 0")
    if args.max_gap <= 0:
        parser.error("--max-gap 必须大于 0")
    if not 1 <= args.candidates <= 20:
        parser.error("--candidates 必须在 1 到 20 之间")
    return args


def main() -> int:
    args = parse_args()
    report: dict[str, object] = {
        "tested_at": datetime.now(timezone.utc).isoformat(),
        "playlist_url": args.playlist_url,
        "proxy_url": args.proxy_url,
        "requirements": {
            "duration_seconds": args.duration,
            "minimum_bytes": args.min_bytes,
            "maximum_gap_seconds": args.max_gap,
        },
        "attempts": [],
        "passed": False,
    }

    try:
        rooms = [args.room_id] if args.room_id else fetch_room_ids(
            args.playlist_url, args.playlist_wait, args.candidates
        )
        for room_id in rooms:
            print(f"[smoke] 测试虎牙房间 {room_id}", flush=True)
            attempt = probe_room(
                args.proxy_url,
                room_id,
                args.duration,
                args.min_bytes,
                args.max_gap,
            )
            report["attempts"].append(attempt)
            if attempt["passed"]:
                report["passed"] = True
                report["selected_room_id"] = room_id
                print(
                    "[smoke] PASS "
                    f"FLV={attempt.get('flv_magic')} "
                    f"bytes={attempt.get('bytes')} "
                    f"avg={attempt.get('average_mbps')}Mbps "
                    f"p99_gap={attempt.get('gap_p99_seconds')}s "
                    f"max_gap={attempt.get('longest_gap_seconds')}s",
                    flush=True,
                )
                break
            print(f"[smoke] FAIL {attempt.get('error') or attempt.get('problems')}", flush=True)
    except (RuntimeError, OSError) as exc:
        report["error"] = str(exc)

    report_path = Path(args.report)
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"[smoke] 报告：{report_path}")
    if report["passed"]:
        return 0
    print("[smoke] 没有找到通过实播门槛的虎牙房间", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
