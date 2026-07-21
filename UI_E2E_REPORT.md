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
