#!/usr/bin/env python3
"""Kova-owned bridge from a Kova draft specification to a CapCut draft.

This script deliberately imports ``pycapcut`` as an external dependency; no
pyCapCut source is copied into Kova. It is selected only when the user enables
the *pycapcut* compiler in the desktop app and points Kova at an existing
CapCut Draft directory. Its most important responsibility is to generate a
real regional censor: a duplicate visual layer receives a blur filter and the
Circle/Rectangle mask chosen in Kova's preview.

It prints one JSON object as its final stdout line so the Go desktop can prove
the resulting draft folder before announcing completion.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any, Iterable


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build a CapCut draft from a Kova specification")
    parser.add_argument("--spec", required=True, help="Kova kova-capcut-draft-spec.json")
    parser.add_argument("--draft-root", required=True, help="Existing folder that contains CapCut drafts")
    parser.add_argument("--draft-name", required=True, help="Human-readable Kova project name")
    return parser.parse_args()


def number(value: Any, default: float = 0.0) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


def microseconds(seconds: float) -> int:
    return int(round(max(0.0, seconds) * 1_000_000))


def timerange(cc: Any, start: float, duration: float) -> Any:
    return cc.trange(microseconds(start), microseconds(duration))


def colour(value: Any, fallback: tuple[float, float, float]) -> tuple[float, float, float]:
    text = str(value or "").strip().lstrip("#")
    if len(text) != 6 or not re.fullmatch(r"[0-9A-Fa-f]{6}", text):
        return fallback
    return tuple(int(text[index : index + 2], 16) / 255.0 for index in (0, 2, 4))  # type: ignore[return-value]


def safe_draft_name(value: str, seed: Any) -> str:
    base = re.sub(r"[^A-Za-z0-9._ -]+", "_", value).strip(" ._") or "Kova_Auto_Builder"
    base = base[:80]
    suffix = re.sub(r"[^0-9A-Za-z_-]+", "", str(seed or "session"))[:24]
    return f"{base}_{suffix or 'session'}"


def by_ref(operations: Iterable[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    result: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for operation in operations:
        if operation.get("op") == "keyframe" and isinstance(operation.get("target"), str):
            result[operation["target"]].append(operation)
    for key in result:
        result[key].sort(key=lambda item: number(item.get("time")))
    return result


def interpolate(points: list[dict[str, Any]], at: float) -> float | None:
    if not points:
        return None
    before = points[0]
    after = points[-1]
    for point in points:
        if number(point.get("time")) <= at:
            before = point
        if number(point.get("time")) >= at:
            after = point
            break
    left, right = number(before.get("time")), number(after.get("time"))
    left_value, right_value = number(before.get("value")), number(after.get("value"))
    if right <= left:
        return left_value
    fraction = min(1.0, max(0.0, (at - left) / (right - left)))
    return left_value + (right_value - left_value) * fraction


def apply_keyframes(cc: Any, segment: Any, operations: list[dict[str, Any]], source_offset: float, duration: float) -> None:
    """Copy Kova motion or ducking keys to a shortened pyCapCut segment.

    A blur overlay may begin halfway through a moving scene. Adding values at
    both boundaries keeps the overlay aligned with the underlying scene rather
    than briefly exposing the unblurred source while it pans or zooms.
    """

    grouped: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for operation in operations:
        property_name = str(operation.get("property") or "")
        if property_name:
            grouped[property_name].append(operation)
    property_map = {
        "uniform_scale": cc.KeyframeProperty.uniform_scale,
        "position_x": cc.KeyframeProperty.position_x,
        "position_y": cc.KeyframeProperty.position_y,
        "volume": cc.KeyframeProperty.volume,
    }
    for property_name, points in grouped.items():
        keyframe_property = property_map.get(property_name)
        if keyframe_property is None:
            continue
        points.sort(key=lambda item: number(item.get("time")))
        times = {0.0, duration}
        for point in points:
            original = number(point.get("time"))
            if source_offset < original < source_offset + duration:
                times.add(original - source_offset)
        for local_time in sorted(times):
            value = interpolate(points, source_offset + local_time)
            if value is not None:
                # pycapcut exposes two intentionally different signatures:
                # VisualSegment.add_keyframe(property, time, value) and
                # AudioSegment.add_keyframe(time, volume).  BGM ducking is
                # emitted as ``volume`` keys, therefore passing the visual
                # property enum here would make every project with BGM fail
                # at compile time with a TypeError.
                if property_name == "volume":
                    segment.add_keyframe(microseconds(local_time), value)
                else:
                    segment.add_keyframe(keyframe_property, microseconds(local_time), value)


def visual_segment(cc: Any, item: dict[str, Any], *, source_offset: float = 0.0, duration: float | None = None, clip: Any = None) -> Any:
    source_duration = number(item.get("duration"))
    duration = source_duration if duration is None else duration
    start = number(item.get("start")) + source_offset
    source_start = number(item.get("sourceStart")) + source_offset
    return cc.VideoSegment(
        str(item["path"]),
        timerange(cc, start, duration),
        source_timerange=timerange(cc, source_start, duration),
        clip_settings=clip,
    )


def add_visual_timeline(cc: Any, script: Any, track_name: str, items: list[dict[str, Any]], keyframes: dict[str, list[dict[str, Any]]], operations: list[dict[str, Any]]) -> None:
    transitions = {str(item.get("target")): item for item in operations if item.get("op") == "transition"}
    for item in items:
        segment = visual_segment(cc, item)
        apply_keyframes(cc, segment, keyframes.get(str(item.get("ref")), []), 0.0, number(item.get("duration")))
        transition = transitions.get(str(item.get("ref")))
        if transition and str(transition.get("slug", "")).lower() == "blur":
            # The pyCapCut enum identifier is encoded below so Kova's own
            # source and user-facing strings remain Vietnamese/English. The
            # external dependency owns the CapCut effect identifier.
            segment.add_transition(getattr(cc.TransitionType, "\u8f6c\u573a_\u6a21\u7cca"), duration=microseconds(number(transition.get("duration"), 0.35)))
        script.add_segment(segment, track_name)


def add_blur_masks(cc: Any, script: Any, visual_items: list[dict[str, Any]], masks: list[dict[str, Any]], keyframes: dict[str, list[dict[str, Any]]], timeline_duration: float, warnings: list[str]) -> None:
    for mask_index, mask in enumerate(masks, 1):
        mask_start = number(mask.get("start"))
        mask_end = number(mask.get("end")) or timeline_duration
        if mask_end <= mask_start:
            continue
        track_name = f"kova_blur_mask_{mask_index:02d}"
        script.add_track(cc.TrackType.video, track_name, relative_index=mask_index)
        shape = str(mask.get("shape") or "rectangle").lower()
        mask_type = getattr(cc.MaskType, "\u5706\u5f62") if shape == "circle" else getattr(cc.MaskType, "\u77e9\u5f62")
        for item in visual_items:
            item_start = number(item.get("start"))
            item_end = item_start + number(item.get("duration"))
            overlap_start, overlap_end = max(item_start, mask_start), min(item_end, mask_end)
            if overlap_end <= overlap_start:
                continue
            item_offset = overlap_start - item_start
            duration = overlap_end - overlap_start
            segment = visual_segment(cc, item, source_offset=item_offset, duration=duration)
            apply_keyframes(cc, segment, keyframes.get(str(item.get("ref")), []), item_offset, duration)
            try:
                segment.add_filter(cc.FilterType.Blur)
            except Exception as exc:  # A given pyCapCut release can rename this filter.
                warnings.append(f"Blur filter metadata unavailable for mask {mask_index}: {exc}")
                segment.add_effect(getattr(cc.VideoCharacterEffectType, "\u5c40\u90e8\u6a21\u7cca"))
            center_x = (number(mask.get("x")) + number(mask.get("width")) / 2.0 - 0.5) * segment.material_size[0]
            center_y = (0.5 - (number(mask.get("y")) + number(mask.get("height")) / 2.0)) * segment.material_size[1]
            segment.add_mask(
                mask_type,
                center_x=center_x,
                center_y=center_y,
                size=number(mask.get("height")),
                rect_width=number(mask.get("width")),
                feather=number(mask.get("feather")) * 100.0,
            )
            script.add_segment(segment, track_name)


def add_audio_track(cc: Any, script: Any, track_name: str, items: list[dict[str, Any]], keyframes: dict[str, list[dict[str, Any]]]) -> None:
    if not items:
        return
    script.add_track(cc.TrackType.audio, track_name)
    for item in items:
        duration = number(item.get("duration"))
        segment = cc.AudioSegment(
            str(item["path"]),
            timerange(cc, number(item.get("start")), duration),
            source_timerange=timerange(cc, number(item.get("sourceStart")), duration),
            volume=number(item.get("volume"), 1.0),
        )
        apply_keyframes(cc, segment, keyframes.get(str(item.get("ref")), []), 0.0, duration)
        script.add_segment(segment, track_name)


def add_watermark(cc: Any, script: Any, items: list[dict[str, Any]]) -> None:
    if not items:
        return
    script.add_track(cc.TrackType.video, "kova_watermark", relative_index=200)
    for item in items:
        scale = number(item.get("scale"), 0.2)
        clip = cc.ClipSettings(
            alpha=number(item.get("opacity"), 1.0),
            scale_x=scale,
            scale_y=scale,
            transform_x=number(item.get("x")),
            transform_y=number(item.get("y")),
        )
        script.add_segment(visual_segment(cc, item, clip=clip), "kova_watermark")


def add_text_track(cc: Any, script: Any, track_name: str, items: list[dict[str, Any]], warnings: list[str]) -> None:
    if not items:
        return
    script.add_track(cc.TrackType.text, track_name)
    warned_fonts: set[str] = set()
    alignments = {"left": 0, "center": 1, "right": 2}
    for item in items:
        background_alpha = number(item.get("bgAlpha"))
        shadow_alpha = number(item.get("shadowAlpha"))
        requested_font = str(item.get("fontFamily") or "").strip()
        # pyCapCut's FontType enum lists CapCut metadata fonts, not arbitrary
        # Windows font families. Preserve the requested family in Kova's spec
        # and warn once; users can still select it after opening the draft.
        if requested_font and requested_font not in warned_fonts:
            warnings.append(f"Requested Windows font '{requested_font}' is retained in the Kova spec; this pyCapCut release uses CapCut font metadata, so set that font in CapCut if it is not mapped.")
            warned_fonts.add(requested_font)
        style = cc.TextStyle(
            size=max(1.0, min(100.0, number(item.get("fontSize"), 46.0))),
            bold=bool(item.get("bold")),
            italic=bool(item.get("italic")),
            color=colour(item.get("color"), (1.0, 1.0, 1.0)),
            align=alignments.get(str(item.get("alignment") or "center").lower(), 1),
            auto_wrapping=True,
        )
        border = cc.TextBorder(
            color=colour(item.get("borderColor"), (0.0, 0.0, 0.0)),
            width=max(0.0, min(100.0, number(item.get("borderWidth")) * 10.0)),
        )
        background = None
        if background_alpha > 0.0:
            background = cc.TextBackground(color=str(item.get("bgColor") or "#000000"), alpha=min(1.0, background_alpha))
        shadow = None
        if shadow_alpha > 0.0:
            shadow = cc.TextShadow(
                color=colour(item.get("shadowColor"), (0.0, 0.0, 0.0)),
                alpha=min(1.0, shadow_alpha),
                distance=max(0.0, min(100.0, number(item.get("shadow")) * 10.0)),
            )
        segment = cc.TextSegment(
            str(item.get("text") or ""),
            timerange(cc, number(item.get("start")), number(item.get("duration"))),
            style=style,
            border=border,
            background=background,
            shadow=shadow,
            clip_settings=cc.ClipSettings(transform_y=number(item.get("y"))),
        )
        script.add_segment(segment, track_name)


def main() -> int:
    args = parse_args()
    spec_path = Path(args.spec).expanduser().resolve()
    draft_root = Path(args.draft_root).expanduser().resolve()
    if not spec_path.is_file():
        raise FileNotFoundError(f"Kova spec does not exist: {spec_path}")
    if not draft_root.is_dir():
        raise NotADirectoryError(f"CapCut Draft Root does not exist: {draft_root}")
    try:
        import pycapcut as cc
    except ImportError as exc:
        raise RuntimeError("pycapcut is not installed in this Python. Install it in the configured environment: pip install pycapcut") from exc

    spec = json.loads(spec_path.read_text(encoding="utf-8"))
    tracks = {str(track.get("name")): track.get("items") or [] for track in spec.get("tracks") or []}
    operations = [item for item in spec.get("operations") or [] if isinstance(item, dict)]
    metadata = spec.get("kova_metadata") or {}
    timeline_duration = number(metadata.get("timeline_duration"))
    keyframes = by_ref(operations)
    visual_items = list(tracks.get("kova_source") or [])
    if not visual_items:
        raise RuntimeError("Kova spec has no kova_source video track")

    draft_name = safe_draft_name(str(args.draft_name), metadata.get("random_seed"))
    folder = cc.DraftFolder(str(draft_root))
    script = folder.create_draft(draft_name, int(number(spec.get("width"), 1920)), int(number(spec.get("height"), 1080)), int(number(spec.get("fps"), 30)), allow_replace=True)
    warnings: list[str] = []

    script.add_track(cc.TrackType.video, "kova_source")
    add_visual_timeline(cc, script, "kova_source", visual_items, keyframes, operations)
    add_blur_masks(cc, script, visual_items, list(metadata.get("blur_masks") or []), keyframes, timeline_duration, warnings)
    add_watermark(cc, script, list(tracks.get("watermark") or []))
    add_audio_track(cc, script, "kova_voiceover", list(tracks.get("voiceover") or []), keyframes)
    add_audio_track(cc, script, "kova_background_music", list(tracks.get("background_music") or []), keyframes)
    for track_name, items in tracks.items():
        if track_name.startswith("subtitles_source_"):
            add_text_track(cc, script, track_name, list(items), warnings)
        if track_name.startswith("subtitles_target_"):
            add_text_track(cc, script, track_name, list(items), warnings)
    script.save()

    print(json.dumps({"draft_directory": str(draft_root / draft_name), "compiler": "pycapcut", "warnings": warnings}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"Kova PyCapCut builder failed: {exc}", file=sys.stderr)
        raise
