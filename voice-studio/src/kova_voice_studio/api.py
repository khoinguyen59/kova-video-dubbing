"""HTTP contract for the independent Voice Studio service."""

from __future__ import annotations

import hashlib
from io import BytesIO
import os
from pathlib import Path
import re

from fastapi import Depends, FastAPI, File, Form, HTTPException, Request, UploadFile
from fastapi.responses import Response
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field

from .engine import engine
from .store import ProfileStore


class CreateProfileRequest(BaseModel):
    name: str = Field(min_length=1, max_length=120)
    language: str = "vi"
    reference_filename: str = Field(min_length=1, max_length=255)
    reference_sha256: str = Field(min_length=64, max_length=64, pattern=r"^[a-fA-F0-9]{64}$")
    consent: bool
    engine: str = "omnivoice"
    engine_version: str = "pending"


MAX_REFERENCE_BYTES = 256 * 1024 * 1024
MIN_REFERENCE_SECONDS = 3.0
MAX_REFERENCE_SECONDS = 30.0
SAFE_FILENAME = re.compile(r"[^A-Za-z0-9._-]+")


def create_app(database_path: str | Path | None = None) -> FastAPI:
    path = Path(database_path or os.environ.get("KOVA_VOICE_DATA_DIR", "data"))
    if path.suffix != ".db":
        path = path / "voice-studio.db"
    store = ProfileStore(path)
    app = FastAPI(title="KOVA Voice Studio", version="1.0.0")
    app.state.profile_store = store

    def require_token(request: Request) -> None:
        expected = os.environ.get("KOVA_VOICE_API_TOKEN", "").strip()
        if not expected:
            return
        authorization = request.headers.get("Authorization", "")
        if authorization != f"Bearer {expected}":
            raise HTTPException(status_code=401, detail="invalid worker token")

    def health_payload() -> dict[str, object]:
        return {
            "status": "ready" if engine.ready() else "installed",
            "ready": True,
            "device": engine.device,
            "dtype": engine.dtype,
            "api_version": "1.0",
            "name": "KOVA Voice Studio",
        }

    @app.get("/health", dependencies=[Depends(require_token)])
    @app.get("/v1/health", dependencies=[Depends(require_token)])
    def health() -> dict[str, object]:
        return health_payload()

    @app.get("/v1/capabilities", dependencies=[Depends(require_token)])
    def capabilities() -> dict[str, object]:
        return {
            "engine": "omnivoice",
            "profile_versions": True,
            "reference_audio_min_seconds": MIN_REFERENCE_SECONDS,
            "reference_audio_recommended_seconds": 10,
            "reference_audio_max_seconds": MAX_REFERENCE_SECONDS,
            "sample_rate_hz": 24000,
            "formats": ["wav", "mp3", "flac"],
        }

    @app.get("/v1/profiles", dependencies=[Depends(require_token)])
    def profiles() -> list[dict[str, object]]:
        return [profile.__dict__ for profile in store.list_profiles()]

    @app.get("/v1/profiles/{profile_id}", dependencies=[Depends(require_token)])
    def profile_detail(profile_id: str) -> dict[str, object]:
        profile = store.get_profile(profile_id)
        version = store.latest_version(profile_id)
        if profile is None or version is None:
            raise HTTPException(status_code=404, detail="voice profile was not found")
        return {"profile": profile.__dict__, "version": safe_version(version)}

    @app.post("/v1/profiles", status_code=201, dependencies=[Depends(require_token)])
    def create_profile_json(request: CreateProfileRequest) -> dict[str, object]:
        try:
            profile, version = store.create_profile(**request.model_dump())
        except ValueError as error:
            raise HTTPException(status_code=422, detail=str(error)) from error
        return {"profile": profile.__dict__, "version": safe_version(version)}

    @app.post("/profiles", status_code=201, dependencies=[Depends(require_token)])
    async def create_profile_upload(
        name: str = Form(...),
        consent_confirmed: bool = Form(False),
        ref_text: str = Form(""),
        language: str = Form("vi"),
        ref_audio: UploadFile = File(...),
    ) -> dict[str, object]:
        if not consent_confirmed:
            raise HTTPException(status_code=422, detail="voice consent is required")
        safe_name = SAFE_FILENAME.sub("_", Path(ref_audio.filename or "reference.wav").name).strip("._") or "reference.wav"
        if Path(safe_name).suffix.lower() not in {".wav", ".mp3", ".flac"}:
            raise HTTPException(status_code=422, detail="reference audio must be WAV, MP3, or FLAC")
        blob = await ref_audio.read(MAX_REFERENCE_BYTES + 1)
        if not blob or len(blob) > MAX_REFERENCE_BYTES:
            raise HTTPException(status_code=413, detail="reference audio is empty or exceeds the upload limit")
        duration_seconds = reference_duration_seconds(blob)
        if duration_seconds < MIN_REFERENCE_SECONDS or duration_seconds > MAX_REFERENCE_SECONDS:
            raise HTTPException(
                status_code=422,
                detail=f"reference audio must be between {MIN_REFERENCE_SECONDS:g} and {MAX_REFERENCE_SECONDS:g} seconds",
            )
        digest = hashlib.sha256(blob).hexdigest()
        profile, version = store.create_profile(
            name=name,
            language=language,
            reference_filename=safe_name,
            reference_sha256=digest,
            consent=True,
            reference_path="",
            reference_duration_seconds=duration_seconds,
            engine="omnivoice",
            engine_version=os.environ.get("KOVA_OMNIVOICE_MODEL", "k2-fsa/OmniVoice"),
        )
        reference_path = path.parent / "references" / profile.id / version.id / safe_name
        reference_path.parent.mkdir(parents=True, exist_ok=True)
        reference_path.write_bytes(blob)
        # Reference data remains owned by Voice Studio. Only its opaque profile
        # ID and version are ever returned to KOVA.
        store.set_reference_path(version.id, str(reference_path))
        version = store.latest_version(profile.id)
        assert version is not None
        return {"id": profile.id, "profile": profile.__dict__, "version": safe_version(version)}

    @app.get("/v1/voices", dependencies=[Depends(require_token)])
    def voices(status: str = "ready") -> list[dict[str, object]]:
        if status != "ready":
            return []
        return [
            {"id": profile.id, "name": profile.name, "language": profile.language, "status": profile.status}
            for profile in store.list_profiles()
            if profile.status == "ready"
        ]

    @app.post("/generate", dependencies=[Depends(require_token)])
    async def generate(
        text: str = Form(...),
        profile_id: str = Form(...),
        ref_text: str = Form(""),
        language: str = Form("vi"),
        speed: float = Form(1.0),
        num_step: int = Form(32),
        duration: float | None = Form(None),
        output_format: str = Form("wav"),
    ) -> Response:
        if output_format.lower() != "wav":
            raise HTTPException(status_code=422, detail="only WAV output is currently supported")
        if not text.strip() or len(text) > 10_000:
            raise HTTPException(status_code=422, detail="text is required and must not exceed 10000 characters")
        if speed <= 0 or speed > 2.0 or num_step < 1 or num_step > 64 or (duration is not None and duration <= 0):
            raise HTTPException(status_code=422, detail="invalid synthesis settings")
        version = store.latest_version(profile_id)
        if version is None or not version.reference_path or not Path(version.reference_path).is_file():
            raise HTTPException(status_code=404, detail="voice profile reference was not found")
        try:
            audio = engine.synthesize(
                text=text.strip(), reference_audio=version.reference_path, reference_text=ref_text,
                language=language, speed=speed, duration=duration, num_steps=num_step,
            )
        except Exception as error:
            raise HTTPException(status_code=503, detail=f"OmniVoice generation failed: {type(error).__name__}") from error
        return Response(content=audio, media_type="audio/wav", headers={"Cache-Control": "no-store"})

    # Voice Studio is a separate product, not a hidden KOVA settings panel.
    # Its lightweight local UI is intentionally served by the same worker so
    # the profile library works both in development and when the service is
    # deployed to a user-controlled Colab runtime. The bearer token stays in
    # browser memory; the UI never writes it to a project, cookie, or disk.
    ui_root = Path(__file__).resolve().parents[2] / "static"
    app.mount("/", StaticFiles(directory=ui_root, html=True), name="voice_studio_ui")
    return app


def safe_version(version: object) -> dict[str, object]:
    values = version.__dict__.copy()  # type: ignore[attr-defined]
    values.pop("reference_path", None)
    return values


def reference_duration_seconds(blob: bytes) -> float:
    """Decode the uploaded reference before persisting it.

    `soundfile` is also required by the actual OmniVoice worker, so this check
    ensures a profile cannot be created from arbitrary bytes that would fail
    only much later during cloning.
    """
    try:
        import soundfile as sound_file

        with sound_file.SoundFile(BytesIO(blob)) as audio:
            if audio.samplerate <= 0 or audio.frames <= 0:
                raise ValueError("reference audio has no samples")
            return float(audio.frames) / float(audio.samplerate)
    except Exception as error:
        raise HTTPException(status_code=422, detail="reference audio cannot be decoded") from error


app = create_app()


def main() -> None:
    import uvicorn

    uvicorn.run("kova_voice_studio.api:app", host="127.0.0.1", port=3920, reload=False)
