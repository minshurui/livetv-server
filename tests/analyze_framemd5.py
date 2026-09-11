#!/usr/bin/env python3
"""分析 FFmpeg framemd5 输出，识别冻结画面和短片段循环。"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def read_hashes(path: Path) -> list[str]:
    hashes: list[str] = []
    for raw_line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        fields = [field.strip() for field in line.split(",")]
        if len(fields) >= 6 and fields[-1]:
            hashes.append(fields[-1])
    return hashes


def longest_identical_run(hashes: list[str]) -> tuple[int, int]:
    best_start = 0
    best_length = 0
    start = 0
    for index, value in enumerate(hashes):
        if index and value != hashes[index - 1]:
            start = index
        length = index - start + 1
        if length > best_length:
            best_start, best_length = start, length
    return best_start, best_length


def longest_repeated_sequence(
    hashes: list[str], max_period_frames: int, min_cycles: int
) -> dict[str, int] | None:
    """寻找连续重复至少 min_cycles 次的完全相同帧序列。"""
    best: dict[str, int] | None = None
    size = len(hashes)
    for period in range(2, min(max_period_frames, size // min_cycles) + 1):
        for start in range(0, size - period * min_cycles + 1):
            pattern = hashes[start : start + period]
            # 全相同帧属于冻结，由 longest_identical_run 单独报告。
            if len(set(pattern)) == 1:
                continue
            cycles = 1
            while start + (cycles + 1) * period <= size:
                current = hashes[start + cycles * period : start + (cycles + 1) * period]
                if current != pattern:
                    break
                cycles += 1
            if cycles < min_cycles:
                continue
            span = cycles * period
            if best is None or span > best["span_frames"]:
                best = {
                    "start_frame": start,
                    "period_frames": period,
                    "cycles": cycles,
                    "span_frames": span,
                }
    return best


def analyze(
    hashes: list[str],
    fps: float,
    min_frames: int,
    max_freeze_seconds: float,
    max_loop_period_seconds: float,
    min_loop_cycles: int,
    min_loop_span_seconds: float,
) -> dict[str, object]:
    freeze_start, freeze_frames = longest_identical_run(hashes)
    loop = longest_repeated_sequence(
        hashes,
        max(2, int(max_loop_period_seconds * fps)),
        min_loop_cycles,
    )
    problems: list[str] = []
    freeze_seconds = freeze_frames / fps if fps else 0.0
    if len(hashes) < min_frames:
        problems.append(f"解码帧不足：{len(hashes)} < {min_frames}")
    if freeze_seconds > max_freeze_seconds:
        problems.append(
            f"连续相同画面 {freeze_seconds:.2f}s，超过 {max_freeze_seconds:.2f}s"
        )

    loop_result: dict[str, object] | None = None
    if loop is not None:
        loop_result = {
            **loop,
            "period_seconds": round(loop["period_frames"] / fps, 3),
            "span_seconds": round(loop["span_frames"] / fps, 3),
        }
        if loop_result["span_seconds"] >= min_loop_span_seconds:
            problems.append(
                "检测到重复画面序列："
                f"周期 {loop_result['period_seconds']}s，"
                f"连续 {loop_result['cycles']} 次"
            )

    return {
        "passed": not problems,
        "frames": len(hashes),
        "unique_frames": len(set(hashes)),
        "unique_ratio": round(len(set(hashes)) / len(hashes), 4) if hashes else 0,
        "fps_sample": fps,
        "longest_identical_run": {
            "start_frame": freeze_start,
            "frames": freeze_frames,
            "seconds": round(freeze_seconds, 3),
        },
        "longest_repeated_sequence": loop_result,
        "problems": problems,
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--fps", type=float, default=2.0)
    parser.add_argument("--min-frames", type=int, default=60)
    parser.add_argument("--max-freeze-seconds", type=float, default=8.0)
    parser.add_argument("--max-loop-period-seconds", type=float, default=10.0)
    parser.add_argument("--min-loop-cycles", type=int, default=3)
    parser.add_argument("--min-loop-span-seconds", type=float, default=12.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    hashes = read_hashes(args.input)
    report = analyze(
        hashes,
        args.fps,
        args.min_frames,
        args.max_freeze_seconds,
        args.max_loop_period_seconds,
        args.min_loop_cycles,
        args.min_loop_span_seconds,
    )
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if report["passed"]:
        return 0
    print("[frame-check] 视频解码或画面连续性不合格", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
