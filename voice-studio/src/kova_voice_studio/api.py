"""HTTP contract for the independent Voice Studio service."""

from __future__ import annotations

import hashlib
from io import BytesIO
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
from threading import Lock

from fastapi import Depends, FastAPI, File, Form, HTTPException, Request, UploadFile
from fastapi.responses import FileResponse, Response
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
    pairing_lock = Lock()
    pairing_claimed = False

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
            "reference_vocal_separation": os.environ.get("KOVA_VOICE_SEPARATE_REFERENCE", "") == "1",
        }

    @app.get("/v1/pairing/{code}")
    def claim_desktop_pairing(code: str) -> dict[str, str]:
        """Exchange a one-time notebook code for the in-memory bearer token.

        The custom protocol URL contains the public worker URL and this opaque
        code, never the bearer token. A code can be claimed once only, so it
        cannot be replayed from browser history after the desktop receives it.
        """
        nonlocal pairing_claimed
        expected_code = os.environ.get("KOVA_VOICE_PAIR_CODE", "")
        token = os.environ.get("KOVA_VOICE_API_TOKEN", "")
        if not expected_code or not token or not secrets.compare_digest(code, expected_code):
            raise HTTPException(status_code=404, detail="pairing link is invalid or expired")
        with pairing_lock:
            if pairing_claimed:
                raise HTTPException(status_code=410, detail="pairing link was already used")
            pairing_claimed = True
        return {"token": token}

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

    @app.get("/v1/profiles/{profile_id}/reference", dependencies=[Depends(require_token)])
    def profile_reference(profile_id: str) -> FileResponse:
        """Return the consented reference only to the authenticated owner.

        KOVA uses this endpoint once to back up a profile created by an older
        desktop build. The worker never exposes reference paths in JSON.
        """
        profile = store.get_profile(profile_id)
        version = store.latest_version(profile_id)
        if profile is None or version is None:
            raise HTTPException(status_code=404, detail="voice profile was not found")
        reference_path = Path(version.reference_path)
        if not version.reference_path or not reference_path.is_file():
            raise HTTPException(status_code=404, detail="profile reference audio is unavailable")
        return FileResponse(
            path=reference_path,
            filename=version.reference_filename,
            media_type="application/octet-stream",
        )

    @app.delete("/v1/profiles/{profile_id}", dependencies=[Depends(require_token)])
    def delete_profile(profile_id: str) -> dict[str, object]:
        try:
            references = store.delete_profile(profile_id)
        except KeyError as error:
            raise HTTPException(status_code=404, detail="voice profile was not found") from error
        for reference in references:
            reference_path = Path(reference)
            try:
                if reference_path.is_file():
                    reference_path.unlink()
                parent = reference_path.parent
                # Only remove folders below the worker's own references root.
                if parent.name and parent.parent.name == "references":
                    parent.rmdir()
            except OSError:
                pass
        return {"deleted": True, "id": profile_id}

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
        # The notebook's CUDA Demucs pass removes music before OmniVoice ever
        # receives or stores a profile reference.  Refusing a failed split is
        # intentional: silently cloning a mixed song would make the profile
        # reproduce accompaniment in every later utterance.
        try:
            clean_blob, clean_filename = vocal_only_reference(blob, safe_name, path.parent)
        except RuntimeError as error:
            raise HTTPException(status_code=503, detail=str(error)) from error
        duration_seconds = reference_duration_seconds(clean_blob)
        if duration_seconds < MIN_REFERENCE_SECONDS or duration_seconds > MAX_REFERENCE_SECONDS:
            raise HTTPException(
                status_code=422,
                detail=f"reference audio must be between {MIN_REFERENCE_SECONDS:g} and {MAX_REFERENCE_SECONDS:g} seconds",
            )
        digest = hashlib.sha256(clean_blob).hexdigest()
        profile, version = store.create_profile(
            name=name,
            language=language,
            reference_filename=clean_filename,
            reference_sha256=digest,
            consent=True,
            reference_path="",
            reference_duration_seconds=duration_seconds,
            engine="omnivoice",
            engine_version=os.environ.get("KOVA_OMNIVOICE_MODEL", "k2-fsa/OmniVoice"),
        )
        reference_path = path.parent / "references" / profile.id / version.id / clean_filename
        reference_path.parent.mkdir(parents=True, exist_ok=True)
        reference_path.write_bytes(clean_blob)
        # Reference data remains owned by Voice Studio. Only its opaque profile
        # ID and version are ever returned to KOVA.
        store.set_reference_path(version.id, str(reference_path))
        version = store.latest_version(profile.id)
        assert version is not None
        profile_payload = profile.__dict__ | {
            "reference_clean": os.environ.get("KOVA_VOICE_SEPARATE_REFERENCE", "") == "1",
            # An explicit marker lets the desktop make the safety property
            # visible: OmniVoice receives the vocal stem, never the uploaded
            # music mix. This is metadata only; no source audio is exposed.
            "reference_processing": "demucs_cuda_vocals",
        }
        return {"id": profile.id, "profile": profile_payload, "version": safe_version(version)}

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


def vocal_only_reference(blob: bytes, filename: str, work_root: Path) -> tuple[bytes, str]:
    """Return a clean voice stem when the CUDA notebook enabled separation.

    The switch is deliberately opt-in at the worker level so developers can
    run the lightweight API tests without downloading Demucs. KOVA's shipped
    Colab notebook sets it to 1 and installs Demucs on its GPU runtime.
    """
    if os.environ.get("KOVA_VOICE_SEPARATE_REFERENCE", "") != "1":
        return blob, filename
    suffix = Path(filename).suffix.lower() or ".wav"
    try:
        with tempfile.TemporaryDirectory(prefix="kova-voice-separate-", dir=work_root) as temporary:
            root = Path(temporary)
            input_path = root / ("reference" + suffix)
            input_path.write_bytes(blob)
            result = subprocess.run(
                [
                    sys.executable, "-m", "demucs", "-n", "htdemucs", "--two-stems", "vocals",
                    "--device", "cuda", "--out", str(root / "out"), str(input_path),
                ],
                capture_output=True,
                text=True,
                timeout=300,
                check=False,
            )
            vocals = root / "out" / "htdemucs" / input_path.stem / "vocals.wav"
            if result.returncode != 0 or not vocals.is_file() or vocals.stat().st_size == 0:
                detail = (result.stderr or result.stdout or "Demucs did not create vocals.wav")[-2000:]
                raise RuntimeError("CUDA voice/music separation for the clone reference failed: " + detail)
            return vocals.read_bytes(), "voice_reference_vocals.wav"
    except subprocess.TimeoutExpired as error:
        raise RuntimeError("CUDA voice/music separation for the clone reference timed out after 5 minutes") from error
    except OSError as error:
        raise RuntimeError("cannot start CUDA voice/music separation for the clone reference") from error


app = create_app()


def main() -> None:
    import uvicorn

    uvicorn.run("kova_voice_studio.api:app", host="127.0.0.1", port=3920, reload=False)
