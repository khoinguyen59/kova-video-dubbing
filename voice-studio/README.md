# KOVA Voice Studio

KOVA Voice Studio is an independent, consent-aware voice-cloning service. It
can run separately from KOVA Desktop (locally for development or as the GPU
worker in Google Colab). The desktop application only selects an opaque,
ready profile ID; it never stores reference audio, profile paths, or the
worker token in a video project.

It also serves its own bilingual profile-library UI at `/`. This is a separate
Voice Studio surface for creating and reviewing consented profiles; KOVA
Desktop integrates it only by loading ready, opaque profile IDs. The worker
token is kept in the browser page memory and is not stored by that UI.

## Safety and consistency contract

- Creating a profile requires explicit consent.
- Reference audio is decoded before storage and must be WAV, MP3, or FLAC,
  between 3 and 30 seconds (10 seconds is recommended).
- Every initial profile gets immutable version `1`. The profile version records
  hash, engine and reference duration; its local reference path is never in an
  API response.
- A KOVA dubbing job passes `profile:<id>` for every subtitle cue. It does not
  upload a new reference clip per cue, preventing accidental voice drift.
- Set `KOVA_VOICE_API_TOKEN` for a Colab worker. KOVA sends it only as a bearer
  header kept in runtime memory.

## API

```text
GET  /health
GET  /v1/health
GET  /v1/capabilities
GET  /v1/profiles
GET  /v1/profiles/{profile_id}
POST /v1/profiles               JSON metadata contract
POST /profiles                  consented multipart reference upload
GET  /v1/voices?status=ready    KOVA dropdown source
POST /generate                  multipart synthesis for profile:<id>
```

`POST /generate` loads OmniVoice lazily. Health/profile routes never load a
model or start inference. To run inference on Colab, use
[`notebooks/Kova_Voice_Studio_GPU.ipynb`](notebooks/Kova_Voice_Studio_GPU.ipynb),
select a GPU runtime, run all cells, then paste its printed URL and token into
KOVA Desktop's **Giọng lồng tiếng cố định / Fixed dubbing voice** stage.

## Development checks

```powershell
$env:PYTHONPATH = "src"
python -m pytest -q tests
```

These tests exercise profile consent, token authentication, API privacy, and
audio decoding only. They do not load OmniVoice or invoke a GPU.
