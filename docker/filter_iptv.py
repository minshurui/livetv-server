#!/usr/bin/env python3
"""Validate an iptv-api M3U snapshot before publishing it.

This is deliberately a conservative post-filter.  iptv-api already performs the
network, speed, resolution and HLS advertisement checks; this script rejects
entries that are structurally VOD or match an operator-maintained blocklist and
then atomically publishes the last known-good snapshot.
"""

from __future__ import annotations

import argparse
import os
import re
import tempfile
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlsplit


EXTINF_DURATION = re.compile(r"^#EXTINF:\s*([+-]?\d+(?:\.\d+)?)", re.IGNORECASE)
VOD_SUFFIXES = {".avi", ".mkv", ".mov", ".mp4", ".m4v", ".webm", ".wmv"}
ALLOWED_SCHEMES = {"http", "https", "rtmp", "rtmps", "rtsp", "udp", "rtp"}


@dataclass(frozen=True)
class M3UEntry:
    lines: tuple[str, ...]
    url: str


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


def filter_m3u(text: str, blocklist: list[str], reject_vod: bool = True):
    header, entries = parse_entries(text)
    kept: list[M3UEntry] = []
    rejected: dict[str, int] = {}
    for entry in entries:
        reason = reject_reason(entry, blocklist, reject_vod)
        if reason:
            rejected[reason] = rejected.get(reason, 0) + 1
        else:
            kept.append(entry)

    m3u_header = next((line for line in header if line.upper().startswith("#EXTM3U")), "#EXTM3U")
    lines = [m3u_header, *(line for line in header if not line.upper().startswith("#EXTM3U"))]
    for entry in kept:
        lines.extend(entry.lines)
    return "\n".join(lines) + "\n", len(kept), rejected


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


def main() -> int:
    parser = argparse.ArgumentParser(description="过滤明显录播/VOD并原子发布 IPTV M3U")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--blocklist-file")
    parser.add_argument("--blocklist", default="")
    parser.add_argument("--min-channels", type=int, default=20)
    parser.add_argument("--allow-vod", action="store_true")
    args = parser.parse_args()

    source = Path(args.input)
    if not source.is_file():
        print(f"[filter] 输入不存在: {source}")
        return 2

    blocklist = load_blocklist(args.blocklist_file, args.blocklist)
    result, count, rejected = filter_m3u(
        source.read_text(encoding="utf-8-sig", errors="replace"),
        blocklist,
        reject_vod=not args.allow_vod,
    )
    if count < max(1, args.min_channels):
        print(f"[filter] 仅剩 {count} 个频道，低于下限 {args.min_channels}，保留旧快照")
        return 3

    atomic_write(Path(args.output), result)
    details = ", ".join(f"{key}={value}" for key, value in sorted(rejected.items())) or "无"
    print(f"[filter] 已发布 {count} 个频道；剔除: {details}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
