# KOVA API Gateway integration report

Date: 2026-07-21

## Configured contract

- Base URL: `http://3.27.172.90/v1`
- Chat endpoint: `POST /v1/chat/completions` using the OpenAI-compatible SSE
  stream KOVA's translator already consumes.
- Preset TTS endpoint: `POST /v1/audio/speech` with a Bearer credential and
  `model` plus `input` JSON fields. The response is binary MP3.
- Translation is deliberately restricted to these verified free model IDs:
  `oc/big-pickle`, `oc/deepseek-v4-flash-free`, `oc/mimo-v2.5-free`,
  `oc/hy3-free`, `oc/nemotron-3-ultra-free`, and
  `oc/north-mini-code-free`.
- Default translation model: `oc/deepseek-v4-flash-free`.
- Verified preset voice IDs exposed by the dropdown: `google-tts/vi`,
  `google-tts/en`, `edge-tts/vi-VN-HoaiMyNeural`, and
  `edge-tts/vi-VN-NamMinhNeural`.

## Live contract checks before integration

Short requests using the supplied credential were made before the code was
wired in. No source video, user media, or audio output file was retained.

| Check | Result |
|---|---|
| `GET /v1/models` | HTTP 200; all six requested free IDs were present in a 464-model response. |
| Non-streaming chat with `oc/deepseek-v4-flash-free` | HTTP 200; a short English-to-Vietnamese response was returned. |
| Streaming chat with the same model | HTTP 200; the response began with a valid `data:` SSE chunk. |
| Google TTS `google-tts/vi` | HTTP 200 `audio/mp3`; 26,112 bytes for a short Vietnamese sample. |
| Edge TTS `edge-tts/vi-VN-HoaiMyNeural` | HTTP 200 `audio/mp3`; 26,640 bytes for a short Vietnamese sample. |

## Implementation

- `config/gateway.go` owns the exact URL, free-model allowlist and credential
  resolution. A non-free model cannot be selected through the Wails flow.
- The Wails main app exposes a translation-model dropdown and sends the chosen
  ID only when the user explicitly starts the translation stage.
- Both Wails and the legacy Fyne compatibility UI point to the fixed gateway;
  the old Ollama/custom-provider cards are not offered in the KOVA UI.
- Google/Edge entries are dropdown values, not text fields. OmniVoice remains
  the separate fixed voice-cloning worker and is not replaced by preset TTS.
- The Colab notebook contains an optional runtime-only lookup of the Colab
  secret named `KOVA_API_GATEWAY_API_KEY`. It is not required to start the
  OmniVoice worker and contains no real credential.

## Credential handling

The supplied credential is present only in this machine's ignored
`config/config.toml`. KOVA resolves a key in this order: temporary desktop
session, ignored local config, then the environment variable
`KOVA_API_GATEWAY_API_KEY`. The credential is not written to project files,
exported through the configuration API, put in the notebook, or committed to
GitHub.

The supplied gateway is HTTP rather than HTTPS. It should therefore be used
only on a trusted network; migrate the endpoint to HTTPS when the gateway
offers it.

## Publication

The runnable KOVA source, the revised Colab notebook and this report are on
the repository's `main` branch. The old published Colab-only history is also
preserved in branch `legacy-colab-history` because the previous main history
could not accept a normal pack update.
