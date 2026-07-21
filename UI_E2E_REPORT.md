# KOVA UI verification baseline

This file replaces a stale manual UI report. The current primary UI is the
Vietnamese/English Wails + React desktop shell under `frontend/`; legacy Fyne
screens are compatibility UI only.

The verification contract is deliberately review-first:

- every workflow stage has its own explicit Start action;
- worker completion is read through an explicit Refresh outputs action;
- the user edits/saves a review draft and presses Approve output before the
  next stage can begin;
- source, translation, dubbing audio, preview/render, and final outputs are
  separate project state stages;
- the TTS field is a dropdown with KOVA Voice Studio, Google, and Edge
  presets; and
- the Colab notebook is opened only by an explicit button and the user runs it
  in Chrome/Colab before a URL/token can exist.

No real UI, GPU, video download, cloud tunnel, TTS, or model execution is
performed by the automated source checks. Consult the implementation report
for exact test commands and the user-run acceptance checklist.

## TTS provider handoff regression — 1.0.1.2

**Observed symptom:** The desktop dropdown showed Google TTS, but a later
dubbing job used a cached OmniVoice client and eventually failed with an
OmniVoice clone-consent message.

**Correction:** KOVA now rebuilds its TTS client both at the dubbing HTTP
boundary and immediately before the workflow starts. The selected Gateway
provider is checked for endpoint, key, and model synchronously; a missing or
invalid local configuration is returned before any subtitle cue is prepared.
An existing in-memory KOVA Gateway key is reused for preset Google/Edge TTS
only when no separate TTS key is configured. No key is written into a project
or report.

**Automated evidence:** the unit suite covers replacement of a deliberately
stale OmniVoice client with `gatewaytts.Client`, the Google TTS dropdown
payload being clone-free, and session-key reuse. These are contract tests only;
the user remains responsible for a live gateway/voice acceptance run.
