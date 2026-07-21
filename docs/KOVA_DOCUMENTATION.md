# Kova technical guide / Hướng dẫn kỹ thuật Kova

Kova is a desktop-first video localization application. The desktop UI is
Vietnamese/English; the optional local HTTP page is only a diagnostic surface.

## Main workflow / Luồng chính

1. **Video source / Nguồn video** — select local media or a public URL, then
   choose timed **Speech-to-text** or **Visual OCR** of visible hardcoded
   captions. Both produce the same editable source SRT/script review gate.
2. **Translation / Dịch và phụ đề** — use the KOVA API Gateway free-model
   dropdown; edit and approve the translated SRT.
3. **Fixed voice / Giọng lồng tiếng cố định** — choose one TTS engine and one
   approved profile/reference for the complete job. OmniVoice runs on the
   configured remote Colab GPU worker; Google/Edge are selectable Gateway TTS
   preset voices, not voice clones.
4. **Video output / Xuất hình** — configure output and explicitly start the
   final render only after audio review.
5. **CapCut Auto-Builder & Visual OCR** — optionally build a reviewable Kova
   CapCut specification, extract hard subtitles locally, and compile a CapCut
   draft after the user has configured an external compiler.

## Auto-Builder and CapCut

Kova always writes `kova-capcut-draft-spec.json` first. It contains local media
references, random seed, audio/BGM timeline, motion keys, independent source
and target subtitle tracks, subtitle styles, watermark placement and mask
metadata. Review this file before enabling compilation.

- Use **pycapcut** when a project contains Circle/Rectangle Blur Masks. Set
  `creator.capcut_draft_root` to an existing CapCut Draft directory and install
  the external Python package in `creator.python_path`:

  ```powershell
  py -m pip install pycapcut
  ```

- Use **capcut-cli** only for projects without blur masks. Kova refuses this
  backend for a masked project rather than creating a draft without censoring.
- `ffprobe` is required to measure source/voice/BGM duration. Node.js and
  capcut-cli are only required when that compiler is selected.

## Visual Subtitle OCR

`scripts/kova_visual_ocr.py` runs locally with OpenCV and PaddleOCR. Kova
first asks the selected Python environment for CUDA (`gpu`), then retries the
same video and red ROI on CPU if GPU initialization fails. Paddle model files
must be installed/provisioned locally once; video frames and subtitles are not
sent to an online OCR API by this bridge.

In the main desktop workflow, choose **OCR phụ đề hiển thị trong video** in
stage 01. KOVA downloads the source video, scans the selected normalised ROI
(the lower subtitle band by default), saves `origin-language.srt` plus the
source script, and waits for the user's edit/approval before translation. OCR
does not require an STT Colab worker or an API Gateway key. It is appropriate
only when the intended text is visible in the video; use STT for spoken audio.

## Translation and voice

- Default translator: the OpenAI-compatible KOVA API Gateway at
  `http://3.27.172.90/v1`, using `oc/deepseek-v4-flash-free`. Kova does not
  install Ollama or a local LLM.
- The translation dropdown is intentionally restricted to the verified free
  IDs: `oc/big-pickle`, `oc/deepseek-v4-flash-free`, `oc/mimo-v2.5-free`,
  `oc/hy3-free`, `oc/nemotron-3-ultra-free`, and `oc/north-mini-code-free`.
  The bearer credential is read from `KOVA_API_GATEWAY_API_KEY` or the ignored
  local config; it is never serialized into a project or returned by KOVA's
  configuration API.
- Gateway preset TTS uses `POST /v1/audio/speech`; the verified dropdown IDs
  are `google-tts/vi`, `google-tts/en`, `edge-tts/vi-VN-HoaiMyNeural`, and
  `edge-tts/vi-VN-NamMinhNeural`.
- Fixed voice: **KOVA Voice Studio** runs as an independent service using the
  official `k2-fsa/OmniVoice` core in a user-started Colab worker. It requires
  the temporary HTTPS worker URL, session token, and authorized reference
  audio. One opaque remote profile is reused for every cue in one job.

Never commit API keys, cookies, Colab session tokens, reference audio, or
generated user media.
