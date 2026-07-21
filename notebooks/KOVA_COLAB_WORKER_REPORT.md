# KOVA Colab worker note

The active notebook is
[`../voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb`](../voice-studio/notebooks/Kova_Voice_Studio_GPU.ipynb).
It replaces the retired legacy notebook in this directory.

The active notebook:

1. asks the user to select a Google Colab GPU runtime and run all cells;
2. builds an isolated Python 3.11 environment;
3. installs the official `k2-fsa/OmniVoice` package with pinned compatible
   dependencies;
4. checks CUDA and the required tokenizer import before the HTTP worker starts;
5. refuses a CPU fallback; and
6. prints a temporary HTTPS worker URL and bearer token for the KOVA Voice
   Studio connection fields.

The notebook is never started by KOVA automatically. The user explicitly opens
it from the desktop button, runs it under their own Google account, then pastes
the printed connection details into the app. See the implementation report for
the exact requirements and validation boundaries.
