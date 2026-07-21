# KOVA current feature baseline

This short baseline replaces an imported historical audit. It describes the
current KOVA product and must not be used as a source of legacy product names,
old notebook links, or UI language requirements.

## Product identity

- Product: **KOVA — Video Localization Studio**.
- Primary application: Wails + React desktop shell, with Vietnamese and
  English application locales.
- The browser surface is diagnostic only; it is not the primary product UI.
- Legacy Fyne screens remain compatibility UI and are branded KOVA.

## Explicit review-first workflow

| Stage | User action | Required check before next stage |
|---|---|---|
| 01 Video source | Start source extraction | Inspect source subtitle/input artifact |
| 02 Translation and subtitles | Start translation | Edit and approve target SRT/script |
| 03 Fixed dubbing voice | Start audio generation | Select one fixed profile or one TTS preset; inspect audio/timing |
| 04 Video output and tuning | Start video mux/preview | Inspect preview, subtitles, and timing |
| 05 Outputs | Start final output stage | Inspect and accept final artifacts |

The timeline never starts a successor automatically. Editing/re-running a
stage marks downstream review state stale.

## Voice and translation

- Translation defaults to the KOVA API Gateway free-model dropdown (default
  `oc/deepseek-v4-flash-free`); KOVA does not install Ollama or a local model.
- KOVA Voice Studio is a separate consent-aware service using
  `k2-fsa/OmniVoice` when its user-controlled GPU worker is asked to generate.
- Google and Edge TTS are selectable dropdown presets through the configured
  gateway. They are alternatives, not voice cloning.
- Voice reference audio and worker token do not belong in a KOVA video project.

See [KOVA 2 implementation report](docs/KOVA_2_IMPLEMENTATION_REPORT.md) for
the implementation evidence, deployment requirements, and non-GPU test scope.
