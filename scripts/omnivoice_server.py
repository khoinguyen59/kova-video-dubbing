#!/usr/bin/env python3
"""Long-lived local HTTP worker for k2-fsa/OmniVoice.

The worker deliberately imports OmniVoice, PyTorch, and SoundFile only after
argument parsing.  This keeps ``--help`` usable while the model environment is
still being installed.
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import logging
import math
import os
import re
from collections import OrderedDict
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Lock
from typing import Any
from urllib.parse import urlsplit


DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 11435
DEFAULT_MODEL = "k2-fsa/OmniVoice"
DEFAULT_ASR_MODEL = "openai/whisper-large-v3-turbo"
DEFAULT_NUM_STEPS = 32
DEFAULT_MAX_REQUEST_BYTES = 1024 * 1024
DEFAULT_MAX_TEXT_CHARS = 20_000
DEFAULT_MAX_REFERENCE_BYTES = 50 * 1024 * 1024
DEFAULT_REFERENCE_DIR = "omnivoice-references"


class RequestValidationError(ValueError):
    """A client-safe request validation error."""

    def __init__(self, code: str, message: str, status: int = 400) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status


@dataclass(frozen=True)
class SynthesisRequest:
    text: str
    ref_audio: Path | None
    ref_text: str | None
    language: str | None
    instruct: str | None
    speed: float | None
    duration: float | None
    num_steps: int


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Run a local OmniVoice HTTP worker that loads the model once and "
            "serves WAV audio from POST /synthesize."
        )
    )
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument(
        "--model",
        default=DEFAULT_MODEL,
        help="Hugging Face model ID or a local checkpoint directory.",
    )
    parser.add_argument(
        "--device",
        default="auto",
        help="auto, cpu, cuda, cuda:0, xpu, or mps (default: auto).",
    )
    parser.add_argument(
        "--asr-model",
        default=DEFAULT_ASR_MODEL,
        help=(
            "Whisper model used once to transcribe a reference when ref_text "
            "is omitted."
        ),
    )
    parser.add_argument(
        "--prompt-cache-size",
        type=int,
        default=8,
        help="Maximum number of reusable voice-clone prompts kept in memory.",
    )
    parser.add_argument(
        "--max-request-bytes",
        type=int,
        default=DEFAULT_MAX_REQUEST_BYTES,
    )
    parser.add_argument(
        "--max-text-chars",
        type=int,
        default=DEFAULT_MAX_TEXT_CHARS,
    )
    parser.add_argument(
        "--reference-dir",
        default=DEFAULT_REFERENCE_DIR,
        help="Directory for reference audio uploaded by remote desktop clients.",
    )
    parser.add_argument(
        "--max-reference-bytes",
        type=int,
        default=DEFAULT_MAX_REFERENCE_BYTES,
        help="Maximum bytes accepted by POST /reference.",
    )
    parser.add_argument(
        "--log-level",
        choices=("DEBUG", "INFO", "WARNING", "ERROR"),
        default="INFO",
    )
    return parser


def _load_optional_dependencies() -> tuple[Any, Any, Any]:
    try:
        import torch
    except ImportError as exc:  # pragma: no cover - depends on local install
        raise RuntimeError(
            "PyTorch is not installed in this Python environment"
        ) from exc

    try:
        import soundfile
    except ImportError as exc:  # pragma: no cover - depends on local install
        raise RuntimeError(
            "SoundFile is not installed in this Python environment"
        ) from exc

    try:
        from omnivoice import OmniVoice
    except ImportError as exc:  # pragma: no cover - depends on local install
        raise RuntimeError(
            "OmniVoice is not installed in this Python environment"
        ) from exc

    return torch, soundfile, OmniVoice


def select_device(torch_module: Any, requested: str) -> str:
    requested = requested.strip()
    if requested and requested.lower() != "auto":
        return requested

    if torch_module.cuda.is_available():
        return "cuda"
    if hasattr(torch_module, "xpu") and torch_module.xpu.is_available():
        return "xpu"
    if torch_module.backends.mps.is_available():
        return "mps"
    return "cpu"


def select_dtype(torch_module: Any, device: str) -> Any:
    # Float16 inference is used on accelerators. CPU float16 support varies by
    # operation, so CPU intentionally stays on float32.
    if device.lower().split(":", 1)[0] == "cpu":
        return torch_module.float32
    return torch_module.float16


def _optional_string(value: Any, field: str) -> str | None:
    if value is None:
        return None
    if not isinstance(value, str):
        raise RequestValidationError(
            "invalid_request", f"{field} must be a string or null"
        )
    value = value.strip()
    return value or None


def _validate_reference_file(path: Path) -> Path:
    try:
        path = path.expanduser().resolve(strict=True)
    except (OSError, RuntimeError) as exc:
        raise RequestValidationError(
            "invalid_ref_audio", "ref_audio does not exist"
        ) from exc

    try:
        is_file = path.is_file()
        size = path.stat().st_size
    except OSError as exc:
        raise RequestValidationError(
            "invalid_ref_audio", "ref_audio cannot be read"
        ) from exc
    if not is_file:
        raise RequestValidationError(
            "invalid_ref_audio", "ref_audio must be a regular file"
        )
    if size <= 0:
        raise RequestValidationError(
            "invalid_ref_audio", "ref_audio must not be empty"
        )
    return path


def validate_reference_audio(
    value: Any, reference_dir: Path | None = None
) -> Path | None:
    raw = _optional_string(value, "ref_audio")
    if raw is None:
        return None
    if raw.startswith("reference:"):
        if reference_dir is None:
            raise RequestValidationError(
                "invalid_ref_audio", "uploaded references are not available"
            )
        reference_id = raw[len("reference:") :]
        if not re.fullmatch(r"[a-f0-9]{64}(?:\.[a-z0-9]{1,10})?", reference_id):
            raise RequestValidationError(
                "invalid_ref_audio", "ref_audio has an invalid reference id"
            )
        root = reference_dir.resolve()
        candidate = (root / reference_id).resolve()
        if candidate.parent != root:
            raise RequestValidationError(
                "invalid_ref_audio", "ref_audio has an invalid reference id"
            )
        return _validate_reference_file(candidate)
    if raw.startswith("local:"):
        raw = raw[len("local:") :]
    if not raw or "://" in raw:
        raise RequestValidationError(
            "invalid_ref_audio", "ref_audio must be a local file path"
        )

    return _validate_reference_file(Path(raw))


# Kept as a narrow helper for local callers and existing tests. Remote Colab
# requests use the explicit reference:<sha256> form after POST /reference.
def validate_local_ref_audio(value: Any) -> Path | None:
    return validate_reference_audio(value)


def parse_synthesis_request(
    payload: Any,
    max_text_chars: int = DEFAULT_MAX_TEXT_CHARS,
    reference_dir: Path | None = None,
) -> SynthesisRequest:
    if not isinstance(payload, dict):
        raise RequestValidationError(
            "invalid_request", "request body must be a JSON object"
        )

    text = payload.get("text")
    if not isinstance(text, str) or not text.strip():
        raise RequestValidationError(
            "invalid_text", "text must be a non-empty string"
        )
    text = text.strip()
    if len(text) > max_text_chars:
        raise RequestValidationError(
            "text_too_large", f"text exceeds {max_text_chars} characters", 413
        )

    ref_audio = validate_reference_audio(payload.get("ref_audio"), reference_dir)
    ref_text = _optional_string(payload.get("ref_text"), "ref_text")
    if ref_text is not None and ref_audio is None:
        raise RequestValidationError(
            "invalid_ref_text", "ref_text requires ref_audio"
        )

    language = _optional_string(payload.get("language"), "language")
    if language is not None and len(language) > 100:
        raise RequestValidationError(
            "invalid_language", "language is too long"
        )

    instruct = _optional_string(payload.get("instruct"), "instruct")
    if instruct is not None and len(instruct) > 1_000:
        raise RequestValidationError(
            "invalid_instruct", "instruct is too long"
        )

    speed_value = payload.get("speed")
    speed: float | None
    if speed_value is None:
        speed = None
    elif isinstance(speed_value, bool) or not isinstance(speed_value, (int, float)):
        raise RequestValidationError(
            "invalid_speed", "speed must be a positive number or null"
        )
    else:
        speed = float(speed_value)
        if not math.isfinite(speed) or speed <= 0:
            raise RequestValidationError(
            "invalid_speed", "speed must be a positive finite number"
        )

    duration_value = payload.get("duration")
    duration: float | None
    if duration_value is None:
        duration = None
    elif isinstance(duration_value, bool) or not isinstance(
        duration_value, (int, float)
    ):
        raise RequestValidationError(
            "invalid_duration", "duration must be a positive number or null"
        )
    else:
        duration = float(duration_value)
        if not math.isfinite(duration) or duration <= 0:
            raise RequestValidationError(
                "invalid_duration", "duration must be a positive finite number"
            )

    num_steps_value = payload.get("num_steps", DEFAULT_NUM_STEPS)
    if isinstance(num_steps_value, bool) or not isinstance(num_steps_value, int):
        raise RequestValidationError(
            "invalid_num_steps", "num_steps must be an integer"
        )
    if not 1 <= num_steps_value <= 256:
        raise RequestValidationError(
            "invalid_num_steps", "num_steps must be between 1 and 256"
        )

    return SynthesisRequest(
        text=text,
        ref_audio=ref_audio,
        ref_text=ref_text,
        language=language,
        instruct=instruct,
        speed=speed,
        duration=duration,
        num_steps=num_steps_value,
    )


class OmniVoiceRuntime:
    """Owns one loaded model and serializes access to it."""

    def __init__(
        self,
        model: Any,
        soundfile_module: Any,
        *,
        model_name: str,
        device: str,
        dtype_name: str,
        asr_model_name: str,
        prompt_cache_size: int,
    ) -> None:
        self.model = model
        self.soundfile = soundfile_module
        self.model_name = model_name
        self.device = device
        self.dtype_name = dtype_name
        self.asr_model_name = asr_model_name
        self.prompt_cache_size = prompt_cache_size
        self._lock = Lock()
        self._asr_loaded = False
        self._prompt_cache: OrderedDict[tuple[Any, ...], Any] = OrderedDict()

    def health(self) -> dict[str, Any]:
        return {
            "status": "ok",
            "ready": True,
            "device": self.device,
            "dtype": self.dtype_name,
        }

    def _voice_prompt(self, request: SynthesisRequest) -> Any | None:
        if request.ref_audio is None:
            return None

        stat = request.ref_audio.stat()
        key = (
            os.fspath(request.ref_audio),
            stat.st_mtime_ns,
            stat.st_size,
            request.ref_text,
        )
        cached = self._prompt_cache.get(key)
        if cached is not None:
            self._prompt_cache.move_to_end(key)
            return cached

        if request.ref_text is None and not self._asr_loaded:
            self.model.load_asr_model(model_name=self.asr_model_name)
            self._asr_loaded = True

        prompt = self.model.create_voice_clone_prompt(
            ref_audio=os.fspath(request.ref_audio),
            ref_text=request.ref_text,
        )
        self._prompt_cache[key] = prompt
        self._prompt_cache.move_to_end(key)
        while len(self._prompt_cache) > self.prompt_cache_size:
            self._prompt_cache.popitem(last=False)
        return prompt

    def synthesize(self, request: SynthesisRequest) -> bytes:
        with self._lock:
            prompt = self._voice_prompt(request)
            kwargs: dict[str, Any] = {
                "text": request.text,
                "num_step": request.num_steps,
            }
            if request.language is not None:
                kwargs["language"] = request.language
            if request.instruct is not None:
                kwargs["instruct"] = request.instruct
            if request.speed is not None:
                kwargs["speed"] = request.speed
            if request.duration is not None:
                kwargs["duration"] = request.duration
            if prompt is not None:
                kwargs["voice_clone_prompt"] = prompt

            audios = self.model.generate(**kwargs)
            if not isinstance(audios, list) or not audios:
                raise RuntimeError("OmniVoice returned no audio")

            # Some very short duration-constrained generations can return an
            # empty array. soundfile then serializes it as a 44-byte WAV header
            # and callers mistake it for valid speech. Reject it here so the
            # Go client can retry without the native duration constraint.
            audio = audios[0]
            try:
                sample_count = len(audio)
            except TypeError as exc:
                raise RuntimeError("OmniVoice returned audio with no samples") from exc
            if sample_count <= 0:
                raise RuntimeError("OmniVoice returned empty audio samples")

            sampling_rate = int(self.model.sampling_rate)
            output = io.BytesIO()
            self.soundfile.write(
                output,
                audio,
                sampling_rate,
                format="WAV",
            )
            wav = output.getvalue()
            if not wav:
                raise RuntimeError("OmniVoice returned an empty WAV")
            return wav


class OmniVoiceHTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(
        self,
        server_address: tuple[str, int],
        runtime: OmniVoiceRuntime,
        max_request_bytes: int,
        max_text_chars: int,
        reference_dir: Path,
        max_reference_bytes: int,
    ) -> None:
        self.runtime = runtime
        self.max_request_bytes = max_request_bytes
        self.max_text_chars = max_text_chars
        # Resolve and create this once; uploaded references never accept a
        # caller-controlled directory or path.
        self.reference_dir = reference_dir.expanduser().resolve()
        self.reference_dir.mkdir(parents=True, exist_ok=True)
        self.max_reference_bytes = max_reference_bytes
        super().__init__(server_address, OmniVoiceRequestHandler)


class OmniVoiceRequestHandler(BaseHTTPRequestHandler):
    server_version = "OmniVoiceLocal/1.0"
    sys_version = ""

    @property
    def omnivoice_server(self) -> OmniVoiceHTTPServer:
        return self.server  # type: ignore[return-value]

    def version_string(self) -> str:
        return self.server_version

    def log_message(self, message_format: str, *args: Any) -> None:
        logging.getLogger("omnivoice.http").info(
            "%s - %s", self.client_address[0], message_format % args
        )

    def _send_bytes(self, status: int, content_type: str, payload: bytes) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(payload)

    def _send_json(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(
            payload, ensure_ascii=False, separators=(",", ":")
        ).encode("utf-8")
        self._send_bytes(status, "application/json; charset=utf-8", body)

    def _send_error_json(self, error: RequestValidationError) -> None:
        self._send_json(
            error.status,
            {"error": {"code": error.code, "message": error.message}},
        )

    def _read_content_length(self, maximum: int) -> int:
        raw_length = self.headers.get("Content-Length")
        if raw_length is None:
            raise RequestValidationError(
                "length_required", "Content-Length is required", 411
            )
        try:
            content_length = int(raw_length)
        except ValueError as exc:
            raise RequestValidationError(
                "invalid_content_length", "Content-Length is invalid"
            ) from exc
        if content_length <= 0:
            raise RequestValidationError(
                "invalid_request", "request body must not be empty"
            )
        if content_length > maximum:
            raise RequestValidationError(
                "request_too_large", "request body is too large", 413
            )
        return content_length

    def _receive_reference(self) -> None:
        content_length = self._read_content_length(
            self.omnivoice_server.max_reference_bytes
        )
        raw_name = self.headers.get("X-OmniVoice-Reference-Name", "reference.wav")
        suffix = Path(raw_name).suffix.lower()
        if not re.fullmatch(r"\.[a-z0-9]{1,10}", suffix):
            suffix = ".wav"
        payload = self.rfile.read(content_length)
        if len(payload) != content_length:
            raise RequestValidationError(
                "invalid_request", "reference upload was incomplete"
            )
        digest = hashlib.sha256(payload).hexdigest()
        target = self.omnivoice_server.reference_dir / f"{digest}{suffix}"
        if not target.exists():
            temp = self.omnivoice_server.reference_dir / f".{digest}.upload"
            temp.write_bytes(payload)
            temp.replace(target)
        self._send_json(201, {"reference": f"reference:{target.name}"})

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        if urlsplit(self.path).path != "/health":
            self._send_json(
                404,
                {"error": {"code": "not_found", "message": "not found"}},
            )
            return
        self._send_json(200, self.omnivoice_server.runtime.health())

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        path = urlsplit(self.path).path
        if path not in {"/synthesize", "/reference"}:
            self._send_json(
                404,
                {"error": {"code": "not_found", "message": "not found"}},
            )
            return

        try:
            if path == "/reference":
                self._receive_reference()
                return
            content_type = self.headers.get("Content-Type", "")
            if content_type.split(";", 1)[0].strip().lower() != "application/json":
                raise RequestValidationError(
                    "unsupported_media_type",
                    "Content-Type must be application/json",
                    415,
                )

            content_length = self._read_content_length(
                self.omnivoice_server.max_request_bytes
            )

            raw_body = self.rfile.read(content_length)
            try:
                payload = json.loads(raw_body)
            except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                raise RequestValidationError(
                    "invalid_json", "request body is not valid JSON"
                ) from exc

            request = parse_synthesis_request(
                payload,
                max_text_chars=self.omnivoice_server.max_text_chars,
                reference_dir=self.omnivoice_server.reference_dir,
            )
            wav = self.omnivoice_server.runtime.synthesize(request)
            self._send_bytes(200, "audio/wav", wav)
        except RequestValidationError as exc:
            self._send_error_json(exc)
        except Exception:
            logging.getLogger("omnivoice").exception("Synthesis failed")
            self._send_json(
                500,
                {
                    "error": {
                        "code": "synthesis_failed",
                        "message": "synthesis failed",
                    }
                },
            )


def build_server(
    host: str,
    port: int,
    runtime: OmniVoiceRuntime,
    *,
    max_request_bytes: int = DEFAULT_MAX_REQUEST_BYTES,
    max_text_chars: int = DEFAULT_MAX_TEXT_CHARS,
    reference_dir: Path | str = DEFAULT_REFERENCE_DIR,
    max_reference_bytes: int = DEFAULT_MAX_REFERENCE_BYTES,
) -> OmniVoiceHTTPServer:
    return OmniVoiceHTTPServer(
        (host, port),
        runtime,
        max_request_bytes=max_request_bytes,
        max_text_chars=max_text_chars,
        reference_dir=Path(reference_dir),
        max_reference_bytes=max_reference_bytes,
    )


def main(argv: list[str] | None = None) -> int:
    args = build_argument_parser().parse_args(argv)
    logging.basicConfig(
        level=getattr(logging, args.log_level),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    if not 1 <= args.port <= 65535:
        raise SystemExit("--port must be between 1 and 65535")
    if args.prompt_cache_size < 1:
        raise SystemExit("--prompt-cache-size must be at least 1")
    if args.max_request_bytes < 1:
        raise SystemExit("--max-request-bytes must be at least 1")
    if args.max_text_chars < 1:
        raise SystemExit("--max-text-chars must be at least 1")
    if args.max_reference_bytes < 1:
        raise SystemExit("--max-reference-bytes must be at least 1")

    logger = logging.getLogger("omnivoice")
    try:
        torch, soundfile, OmniVoice = _load_optional_dependencies()
        device = select_device(torch, args.device)
        dtype = select_dtype(torch, device)
        dtype_name = str(dtype).removeprefix("torch.")

        logger.info(
            "Loading model once: model=%s device=%s dtype=%s",
            args.model,
            device,
            dtype_name,
        )
        model = OmniVoice.from_pretrained(
            args.model,
            device_map=device,
            dtype=dtype,
        )
        runtime = OmniVoiceRuntime(
            model,
            soundfile,
            model_name=args.model,
            device=device,
            dtype_name=dtype_name,
            asr_model_name=args.asr_model,
            prompt_cache_size=args.prompt_cache_size,
        )
        server = build_server(
            args.host,
            args.port,
            runtime,
            max_request_bytes=args.max_request_bytes,
            max_text_chars=args.max_text_chars,
            reference_dir=Path(args.reference_dir),
            max_reference_bytes=args.max_reference_bytes,
        )
    except Exception:
        logger.exception("Failed to initialize OmniVoice worker")
        return 1

    logger.info(
        "OmniVoice worker ready at http://%s:%d",
        args.host,
        args.port,
    )
    try:
        server.serve_forever(poll_interval=0.25)
    except KeyboardInterrupt:
        logger.info("Stopping OmniVoice worker")
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
