#!/usr/bin/env python3
"""Kova Visual Subtitle OCR bridge.

The desktop starts this script with a video, a normalized ROI and GPU/CPU
preference. It uses only local video frames and a locally installed PaddleOCR
environment. Paddle model files must be provisioned once by the user; after
that the bridge does not submit frames or subtitles to an online service.

The last stdout line is JSON so the Go desktop can show a concrete SRT output
instead of treating an OCR process exit as a completed subtitle job.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable


@dataclass
class Cue:
    start_ms: int
    end_ms: int
    text: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Kova offline visual subtitle OCR")
    parser.add_argument("--input", required=True, help="input video path")
    parser.add_argument("--output", required=True, help="output SRT path")
    parser.add_argument("--roi", required=True, help="x,y,width,height normalized to 0..1")
    parser.add_argument("--lang", default="en", help="PaddleOCR language")
    parser.add_argument("--device", choices=("gpu", "cpu"), default="cpu")
    parser.add_argument("--interval-ms", type=int, default=250)
    parser.add_argument("--merge-gap-ms", type=int, default=450)
    return parser.parse_args()


def parse_roi(raw: str) -> tuple[float, float, float, float]:
    try:
        x, y, width, height = (float(part.strip()) for part in raw.split(","))
    except (TypeError, ValueError) as exc:
        raise ValueError("ROI must be x,y,width,height") from exc
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        raise ValueError("ROI must remain inside the normalized video frame")
    return x, y, width, height


def paddle_language(value: str) -> str:
    key = value.strip().lower().replace("_", "-")
    aliases = {"zh": "ch", "zh-cn": "ch", "zh-tw": "chinese_cht", "ja": "japan", "ko": "korean"}
    return aliases.get(key, key or "en")


def make_ocr(language: str, device: str) -> Any:
    try:
        from paddleocr import PaddleOCR
    except ImportError as exc:
        raise RuntimeError(
            "PaddleOCR is not installed in the selected Python environment. "
            "Install paddlepaddle/paddleocr locally, then retry."
        ) from exc

    lang = paddle_language(language)
    # PaddleOCR v3 prefers ``device``; v2 uses ``use_gpu``. Supporting both
    # keeps the Kova bridge independent from a specific wheel release.
    common = {"lang": lang}
    try:
        return PaddleOCR(
            **common,
            device="gpu:0" if device == "gpu" else "cpu",
            use_doc_orientation_classify=False,
            use_doc_unwarping=False,
            use_textline_orientation=False,
        )
    except TypeError:
        return PaddleOCR(**common, use_gpu=device == "gpu", use_angle_cls=False)


def normalized_text(value: str) -> str:
    value = unicodedata.normalize("NFKC", value)
    value = re.sub(r"\s+", " ", value).strip()
    # CJK subtitle OCR commonly emits one space per glyph. Preserve word
    # spacing around Latin text but remove artificial CJK-internal spacing.
    cjk = r"\u3400-\u9fff\uf900-\ufaff\u3040-\u30ff\uac00-\ud7af"
    value = re.sub(rf"(?<=[{cjk}])\s+(?=[{cjk}])", "", value)
    return value


def walk_texts(value: Any) -> Iterable[str]:
    """Extract recognised strings from PaddleOCR v2 and v3 result shapes."""
    if isinstance(value, dict):
        for key in ("rec_texts", "texts", "text", "rec_text"):
            item = value.get(key)
            if isinstance(item, str):
                yield item
            elif isinstance(item, list):
                for text in item:
                    if isinstance(text, str):
                        yield text
        for item in value.values():
            yield from walk_texts(item)
        return
    if isinstance(value, (list, tuple)):
        if len(value) == 2 and isinstance(value[0], str) and isinstance(value[1], (float, int)):
            yield value[0]
            return
        for item in value:
            yield from walk_texts(item)


def read_text(ocr: Any, image: Any) -> str:
    def joined_unique(values: Iterable[str]) -> str:
        seen: set[str] = set()
        cleaned: list[str] = []
        for value in values:
            normalized = normalized_text(value)
            if normalized and normalized not in seen:
                seen.add(normalized)
                cleaned.append(normalized)
        return normalized_text(" ".join(cleaned))

    try:
        predicted = ocr.predict(image)
        rows = []
        for item in predicted:
            if hasattr(item, "json"):
                payload = item.json
                if callable(payload):
                    payload = payload()
                if isinstance(payload, str):
                    payload = json.loads(payload)
                rows.extend(walk_texts(payload))
            elif hasattr(item, "to_dict"):
                rows.extend(walk_texts(item.to_dict()))
            else:
                rows.extend(walk_texts(item))
        return joined_unique(rows)
    except AttributeError:
        legacy = ocr.ocr(image, cls=False)
        return joined_unique(walk_texts(legacy))


def srt_time(value_ms: int) -> str:
    value_ms = max(0, value_ms)
    hours, rest = divmod(value_ms, 3_600_000)
    minutes, rest = divmod(rest, 60_000)
    seconds, milliseconds = divmod(rest, 1_000)
    return f"{hours:02d}:{minutes:02d}:{seconds:02d},{milliseconds:03d}"


def write_srt(path: Path, cues: list[Cue]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines: list[str] = []
    for index, cue in enumerate(cues, 1):
        lines.extend((str(index), f"{srt_time(cue.start_ms)} --> {srt_time(cue.end_ms)}", cue.text, ""))
    path.write_text("\n".join(lines), encoding="utf-8")


def main() -> int:
    args = parse_args()
    roi = parse_roi(args.roi)
    input_path = Path(args.input).expanduser().resolve()
    output_path = Path(args.output).expanduser().resolve()
    if not input_path.is_file():
        raise FileNotFoundError(f"Input video does not exist: {input_path}")
    if args.interval_ms < 40 or args.interval_ms > 5000:
        raise ValueError("interval-ms must be 40..5000")

    # Avoid a hosted model registry being selected accidentally by a shell
    # environment. A user can still deliberately configure a local Paddle
    # model directory before launch.
    os.environ.setdefault("PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK", "True")
    import cv2

    ocr = make_ocr(args.lang, args.device)
    capture = cv2.VideoCapture(str(input_path))
    if not capture.isOpened():
        raise RuntimeError(f"Cannot open video: {input_path}")
    fps = capture.get(cv2.CAP_PROP_FPS) or 0.0
    frame_total = int(capture.get(cv2.CAP_PROP_FRAME_COUNT) or 0)
    step = max(1, int(round((fps or 30.0) * args.interval_ms / 1000.0)))
    cue_end_extension = max(args.interval_ms, 80)
    frame_index = 0
    processed = 0
    dropped = 0
    cues: list[Cue] = []
    x_norm, y_norm, width_norm, height_norm = roi

    while True:
        ok, frame = capture.read()
        if not ok:
            break
        if frame_index % step:
            frame_index += 1
            continue
        height, width = frame.shape[:2]
        left = max(0, min(width - 1, int(round(x_norm * width))))
        top = max(0, min(height - 1, int(round(y_norm * height))))
        right = max(left + 1, min(width, int(round((x_norm + width_norm) * width))))
        bottom = max(top + 1, min(height, int(round((y_norm + height_norm) * height))))
        crop = frame[top:bottom, left:right]
        timestamp_ms = int(round(capture.get(cv2.CAP_PROP_POS_MSEC)))
        processed += 1
        text = read_text(ocr, crop)
        if not text:
            dropped += 1
        elif cues and cues[-1].text == text and timestamp_ms - cues[-1].end_ms <= args.merge_gap_ms:
            cues[-1].end_ms = max(cues[-1].end_ms, timestamp_ms + cue_end_extension)
        else:
            cues.append(Cue(timestamp_ms, timestamp_ms + cue_end_extension, text))
        frame_index += 1

    capture.release()
    write_srt(output_path, cues)
    print(
        json.dumps(
            {
                "srt_path": str(output_path),
                "device": args.device,
                "frame_count": processed,
                "cue_count": len(cues),
                "dropped_frames": dropped,
                "normalized_cjk": True,
                "fallback_to_cpu": False,
                "video_frame_count": frame_total,
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # Deliberately preserve traceback for Kova's error dialog.
        print(f"Kova Visual OCR failed: {exc}", file=sys.stderr)
        raise
