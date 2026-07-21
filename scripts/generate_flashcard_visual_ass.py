#!/usr/bin/env python3
"""Create Vietnamese replacement labels for the fixed flashcard video layout."""

from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Cue:
    index: int
    start: float
    end: float
    text: str


def parse_srt_time(value: str) -> float:
    hours, minutes, tail = value.strip().split(":")
    seconds, millis = tail.split(",")
    return int(hours) * 3600 + int(minutes) * 60 + int(seconds) + int(millis) / 1000


def ass_time(seconds: float) -> str:
    centiseconds = round(seconds * 100)
    hours, remainder = divmod(centiseconds, 360000)
    minutes, remainder = divmod(remainder, 6000)
    secs, hundredths = divmod(remainder, 100)
    return f"{hours}:{minutes:02d}:{secs:02d}.{hundredths:02d}"


def parse_srt(path: Path) -> list[Cue]:
    cues: list[Cue] = []
    for block in re.split(r"\r?\n\s*\r?\n", path.read_text(encoding="utf-8").strip()):
        lines = [line.strip() for line in block.splitlines()]
        if len(lines) < 3 or " --> " not in lines[1]:
            raise ValueError(f"Invalid SRT block: {block!r}")
        start, end = lines[1].split(" --> ", 1)
        cues.append(Cue(int(lines[0]), parse_srt_time(start), parse_srt_time(end), " ".join(lines[2:])))
    return cues


def ass_escape(text: str) -> str:
    return text.replace("\\", r"\\").replace("{", r"\{").replace("}", r"\}")


def wrap_example(text: str, limit: int = 29) -> str:
    words = text.split()
    lines: list[str] = []
    line = ""
    for word in words:
        proposal = f"{line} {word}".strip()
        if line and len(proposal) > limit:
            lines.append(line)
            line = word
        else:
            line = proposal
    if line:
        lines.append(line)
    return r"\N".join(lines[:2])


def dialogue(style: str, start: float, end: float, text: str, tag: str) -> str:
    return f"Dialogue: 0,{ass_time(start)},{ass_time(end)},{style},,0,0,0,,{{{tag}}}{ass_escape(text)}"


def build_ass(cues: list[Cue]) -> str:
    header = """[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080
WrapStyle: 0
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding
Style: FlashTitle,Arial,66,&H00280F95,&H000000FF,&H00FFFFFF,&H00000000,1,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1
Style: FlashExample,Arial,31,&H00280F95,&H000000FF,&H00FFFFFF,&H00000000,1,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1

[Events]
Format: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text
"""
    lines = [header.rstrip()]
    odds = [cue for cue in cues if cue.index % 2 == 1]
    for position, title in enumerate(odds):
        end = odds[position + 1].start if position + 1 < len(odds) else cues[-1].end
        lines.append(dialogue("FlashTitle", title.start, end, title.text, r"\an5\pos(960,235)"))
    for example in (cue for cue in cues if cue.index % 2 == 0):
        next_title = next((cue.start for cue in odds if cue.start > example.start), cues[-1].end)
        lines.append(dialogue("FlashExample", example.start, next_title, wrap_example(example.text), r"\an5\pos(490,840)"))
    return "\n".join(lines) + "\n"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-srt", required=True, type=Path)
    parser.add_argument("--output-ass", required=True, type=Path)
    args = parser.parse_args()
    cues = parse_srt(args.input_srt)
    if len(cues) < 2:
        raise SystemExit("At least two subtitle cues are required")
    args.output_ass.parent.mkdir(parents=True, exist_ok=True)
    args.output_ass.write_text(build_ass(cues), encoding="utf-8", newline="\n")
    print(f"generated {args.output_ass} from {len(cues)} cues")


if __name__ == "__main__":
    main()
