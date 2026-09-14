#!/usr/bin/env python3
"""Validate an iptv-api M3U snapshot before publishing it.

The upstream result is only a candidate snapshot.  This final gate limits the
published channel set, rejects structural VOD entries, and can require FFmpeg to
decode a video frame from every retained URL.  Failed refreshes never overwrite
the last known-good snapshot.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import fcntl
import os
import re
import subprocess
import tempfile
import time
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Callable
from urllib.parse import urlsplit


EXTINF_DURATION = re.compile(r"^#EXTINF:\s*([+-]?\d+(?:\.\d+)?)", re.IGNORECASE)
VOD_SUFFIXES = {".avi", ".mkv", ".mov", ".mp4", ".m4v", ".webm", ".wmv"}
ALLOWED_SCHEMES = {"http", "https", "rtmp", "rtmps", "rtsp", "udp", "rtp"}
GROUP_ATTR = re.compile(r'(\bgroup-title\s*=\s*")[^"]*(")', re.IGNORECASE)
TVG_NAME_ATTR = re.compile(r'\btvg-name\s*=\s*"([^"]*)"', re.IGNORECASE)
CCTV_NAME = re.compile(
    r"^(?:CCTV-?(?:1[0-7]|[1-9])(?:\+)?(?:[^0-9]|$)|CETV-?[1-4](?:[^0-9]|$))",
    re.IGNORECASE,
)

# Provincial satellite channels in the bundled iptv-api template.  Requiring a
# known broadcaster prefix prevents arbitrary names containing the word 卫视
# from being placed in the core television groups.
SATELLITE_PREFIXES = (
    "安徽", "北京", "兵团", "重庆", "东方", "东南", "甘肃", "广东", "广西",
    "贵州", "海南", "河北", "河南", "黑龙江", "湖北", "湖南", "吉林", "江苏",
    "江西", "辽宁", "内蒙古", "宁夏", "青海", "山东", "山西", "陕西", "深圳",
    "四川", "天津", "西藏", "新疆", "云南", "浙江", "三沙", "大湾区", "香港",
    "澳门", "凤凰", "延边", "厦门", "康巴", "农林",
)


@dataclass(frozen=True)
class M3UEntry:
    lines: tuple[str, ...]
    url: str


def _split_extinf(line: str) -> tuple[str, str]:
    """Split EXTINF attributes and display name at the first unquoted comma."""
    quoted = False
    for index, char in enumerate(line):
        if char == '"':
            quoted = not quoted
        elif char in ",，" and not quoted:
            return line[:index], line[index + 1:].strip()
    return line, ""


def entry_name(entry: M3UEntry) -> str:
    attrs, display = _split_extinf(entry.lines[0])
    if display:
        return display
    match = TVG_NAME_ATTR.search(attrs)
    return match.group(1).strip() if match else ""


def normalized_channel_name(name: str) -> str:
    value = unicodedata.normalize("NFKC", name).upper().strip()
    value = re.sub(r"^[^0-9A-Z\u4e00-\u9fff]+", "", value)
    return re.sub(r"[\s_·—–]+", "", value)


def classify_core_channel(name: str) -> str | None:
    """Return cctv/satellite only for recognized core TV channel names."""
    value = normalized_channel_name(name)
    if CCTV_NAME.match(value) or value.startswith("央视"):
        return "cctv"
    if "卫视" in value and any(value.startswith(prefix) for prefix in SATELLITE_PREFIXES):
        return "satellite"
    return None


def with_group(entry: M3UEntry, group: str) -> M3UEntry:
    first = entry.lines[0]
    if GROUP_ATTR.search(first):
        first = GROUP_ATTR.sub(lambda match: match.group(1) + group + match.group(2), first, count=1)
    else:
        attrs, display = _split_extinf(first)
        first = f'{attrs} group-title="{group}",{display}'
    return M3UEntry((first, *entry.lines[1:]), entry.url)


def parse_entries(text: str) -> tuple[list[str], list[M3UEntry]]:
    """Return header lines and EXTINF records without losing per-entry options."""
    header: list[str] = []
    entries: list[M3UEntry] = []
    pending: list[str] | None = None

    for raw in text.replace("\r\n", "\n").replace("\r", "\n").split("\n"):
        line = raw.strip()
        if not line:
            continue
        if pending is None and line.upper().startswith("#EXTM3U"):
            if not any(item.upper().startswith("#EXTM3U") for item in header):
                header.append(line)
            continue
        if line.upper().startswith("#EXTINF:"):
            # A malformed previous record must never leak into the next record.
            pending = [line]
            continue
        if pending is not None:
            if line.startswith("#"):
                pending.append(line)
                continue
            entries.append(M3UEntry(tuple(pending + [line]), line))
            pending = None
            continue
        header.append(line)

    return header, entries


def load_blocklist(path: str | None, inline: str | None) -> list[str]:
    values: list[str] = []
    if path:
        try:
            values.extend(Path(path).read_text(encoding="utf-8").splitlines())
        except FileNotFoundError:
            pass
    if inline:
        values.extend(inline.split(","))
    return [value.strip().lower() for value in values
            if value.strip() and not value.lstrip().startswith("#")]


def reject_reason(entry: M3UEntry, blocklist: list[str], reject_vod: bool) -> str | None:
    url = entry.url.strip()
    low_url = url.lower()
    stream_url = url.partition("$")[0].strip()
    scheme = urlsplit(stream_url).scheme.lower()
    if scheme not in ALLOWED_SCHEMES:
        return "unsupported_scheme"
    if any(keyword in low_url for keyword in blocklist):
        return "blocklist"

    if reject_vod:
        match = EXTINF_DURATION.match(entry.lines[0])
        if match and float(match.group(1)) > 0:
            return "positive_duration"
        suffix = Path(urlsplit(stream_url).path).suffix.lower()
        if suffix in VOD_SUFFIXES:
            return "vod_file"
    return None


def _probe_key(entry: M3UEntry) -> tuple[str, tuple[str, ...]]:
    options = tuple(line for line in entry.lines[1:-1] if line.upper().startswith("#EXTVLCOPT:"))
    return entry.url, options


def ffmpeg_probe(entry: M3UEntry, timeout: float) -> bool:
    """Require one decodable video frame using the same FFmpeg as the image."""
    stream_url = entry.url.partition("$")[0].strip()
    command = [
        "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error",
        "-rw_timeout", str(max(1, int(timeout * 1_000_000))),
    ]
    extra_headers: list[str] = []
    for line in entry.lines[1:-1]:
        if not line.upper().startswith("#EXTVLCOPT:"):
            continue
        key, separator, value = line[len("#EXTVLCOPT:"):].partition("=")
        if not separator:
            continue
        value = value.replace("\r", "").replace("\n", "").strip()
        key = key.strip().lower()
        if key == "http-user-agent" and value:
            command.extend(("-user_agent", value))
        elif key in {"http-referrer", "http-referer"} and value:
            extra_headers.append(f"Referer: {value}")
        elif key == "http-origin" and value:
            extra_headers.append(f"Origin: {value}")
    if extra_headers:
        command.extend(("-headers", "\r\n".join(extra_headers) + "\r\n"))
    command.extend((
        "-i", stream_url, "-map", "0:v:0", "-frames:v", "1", "-f", "null", "-",
    ))
    try:
        result = subprocess.run(
            command,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            timeout=timeout + 3,
            check=False,
        )
    except (FileNotFoundError, subprocess.TimeoutExpired, OSError):
        return False
    return result.returncode == 0


def verify_entries(
        entries: list[M3UEntry], timeout: float, workers: int,
        probe: Callable[[M3UEntry, float], bool] = ffmpeg_probe,
) -> list[bool]:
    """Probe duplicate URL/header combinations once while preserving M3U order."""
    representatives: dict[tuple[str, tuple[str, ...]], M3UEntry] = {}
    for entry in entries:
        representatives.setdefault(_probe_key(entry), entry)
    results: dict[tuple[str, tuple[str, ...]], bool] = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=max(1, workers)) as pool:
        pending = {
            pool.submit(probe, entry, timeout): key
            for key, entry in representatives.items()
        }
        for future in concurrent.futures.as_completed(pending):
            key = pending[future]
            try:
                results[key] = bool(future.result())
            except Exception:
                results[key] = False
    return [results.get(_probe_key(entry), False) for entry in entries]


def verify_channel_candidates(
        entries: list[M3UEntry], timeout: float, workers: int, limit: int,
        probe: Callable[[M3UEntry, float], bool] = ffmpeg_probe,
) -> tuple[list[M3UEntry], int, int]:
    """Probe candidates round-robin until each channel has enough good URLs."""
    grouped: dict[str, list[int]] = {}
    for index, entry in enumerate(entries):
        key = normalized_channel_name(entry_name(entry)) or entry.url
        grouped.setdefault(key, []).append(index)

    positions = {key: 0 for key in grouped}
    successes = {key: 0 for key in grouped}
    accepted: set[int] = set()
    failures = 0
    while True:
        batch: list[tuple[str, int]] = []
        for key, indexes in grouped.items():
            if successes[key] >= limit or positions[key] >= len(indexes):
                continue
            index = indexes[positions[key]]
            positions[key] += 1
            batch.append((key, index))
        if not batch:
            break
        checks = verify_entries(
            [entries[index] for _, index in batch], timeout, workers, probe
        )
        for (key, index), playable in zip(batch, checks):
            if playable:
                accepted.add(index)
                successes[key] += 1
            else:
                failures += 1

    skipped = sum(
        len(indexes) - positions[key]
        for key, indexes in grouped.items()
        if successes[key] >= limit
    )
    return [entry for index, entry in enumerate(entries) if index in accepted], failures, skipped


def filter_m3u(
        text: str,
        blocklist: list[str],
        reject_vod: bool = True,
        channel_scope: str = "all",
        verify_streams: bool = False,
        verify_timeout: float = 12,
        verify_workers: int = 6,
        urls_per_channel: int = 0,
        probe: Callable[[M3UEntry, float], bool] = ffmpeg_probe,
):
    header, entries = parse_entries(text)
    candidates: list[M3UEntry] = []
    rejected: dict[str, int] = {}
    for entry in entries:
        reason = reject_reason(entry, blocklist, reject_vod)
        if reason:
            rejected[reason] = rejected.get(reason, 0) + 1
            continue
        if channel_scope == "cctv_satellite":
            channel_class = classify_core_channel(entry_name(entry))
            if channel_class is None:
                rejected["outside_scope"] = rejected.get("outside_scope", 0) + 1
                continue
            group = "IPTV·央视" if channel_class == "cctv" else "IPTV·卫视"
            entry = with_group(entry, group)
        candidates.append(entry)

    if verify_streams and candidates:
        if urls_per_channel > 0:
            candidates, failures, skipped = verify_channel_candidates(
                candidates, verify_timeout, verify_workers, urls_per_channel, probe
            )
            if failures:
                rejected["unplayable"] = rejected.get("unplayable", 0) + failures
            if skipped:
                rejected["url_limit"] = rejected.get("url_limit", 0) + skipped
        else:
            checks = verify_entries(candidates, verify_timeout, verify_workers, probe)
            verified: list[M3UEntry] = []
            for entry, playable in zip(candidates, checks):
                if playable:
                    verified.append(entry)
                else:
                    rejected["unplayable"] = rejected.get("unplayable", 0) + 1
            candidates = verified

    kept: list[M3UEntry] = []
    per_channel: dict[str, int] = {}
    for entry in candidates:
        key = normalized_channel_name(entry_name(entry)) or entry.url
        count = per_channel.get(key, 0)
        if urls_per_channel > 0 and count >= urls_per_channel:
            rejected["url_limit"] = rejected.get("url_limit", 0) + 1
            continue
        per_channel[key] = count + 1
        kept.append(entry)

    m3u_header = next((line for line in header if line.upper().startswith("#EXTM3U")), "#EXTM3U")
    lines = [m3u_header, *(line for line in header if not line.upper().startswith("#EXTM3U"))]
    for entry in kept:
        lines.extend(entry.lines)
    return "\n".join(lines) + "\n", len(kept), rejected


def unique_channel_count(text: str) -> int:
    _, entries = parse_entries(text)
    return len({normalized_channel_name(entry_name(entry)) or entry.url for entry in entries})


def atomic_write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}-", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(tmp_name, 0o644)
        os.replace(tmp_name, path)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass
        raise


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="筛选频道、验证实际播放并原子发布 IPTV M3U")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--blocklist-file")
    parser.add_argument("--blocklist", default="")
    parser.add_argument("--min-channels", type=int, default=20)
    parser.add_argument("--allow-vod", action="store_true")
    parser.add_argument(
        "--channel-scope", choices=("cctv_satellite", "all"), default="all",
        help="cctv_satellite 仅发布央视/省级卫视；all 保留上游全部分类",
    )
    parser.add_argument("--verify-streams", action="store_true", help="发布前用 FFmpeg 解码首帧")
    parser.add_argument("--verify-timeout", type=float, default=12)
    parser.add_argument("--verify-workers", type=int, default=6)
    parser.add_argument("--urls-per-channel", type=int, default=2)
    return parser


def publish(args: argparse.Namespace) -> int:
    started = time.monotonic()

    source = Path(args.input)
    if not source.is_file():
        print(f"[filter] 输入不存在: {source}")
        return 2

    blocklist = load_blocklist(args.blocklist_file, args.blocklist)
    result, count, rejected = filter_m3u(
        source.read_text(encoding="utf-8-sig", errors="replace"),
        blocklist,
        reject_vod=not args.allow_vod,
        channel_scope=args.channel_scope,
        verify_streams=args.verify_streams,
        verify_timeout=max(2, min(args.verify_timeout, 60)),
        verify_workers=max(1, min(args.verify_workers, 32)),
        urls_per_channel=max(0, args.urls_per_channel),
    )
    channels = unique_channel_count(result)
    if channels < max(1, args.min_channels):
        print(
            f"[filter] 仅剩 {channels} 个可用频道/{count} 条线路，"
            f"低于下限 {args.min_channels}，保留旧快照"
        )
        return 3

    atomic_write(Path(args.output), result)
    details = ", ".join(f"{key}={value}" for key, value in sorted(rejected.items())) or "无"
    result_kind = "已验证线路" if args.verify_streams else "结构过滤后线路"
    print(
        f"[filter] 已发布 {channels} 个频道/{count} 条{result_kind}；"
        f"耗时 {time.monotonic() - started:.1f}s；剔除: {details}"
    )
    return 0


def main() -> int:
    args = build_parser().parse_args()
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with open(str(output) + ".lock", "a", encoding="utf-8") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("[filter] 已有 IPTV 发布任务运行，跳过本次")
            return 0
        return publish(args)


if __name__ == "__main__":
    raise SystemExit(main())
