# Kova Video Dubbing

Public runtime assets for Kova Desktop's video dubbing workflow.

## Google Colab GPU worker

Kova Desktop opens this notebook directly in Chrome/Google Colab:

[Open `Kova_OmniVoice_Colab.ipynb` in Google Colab](https://colab.research.google.com/github/khoinguyen59/kova-video-dubbing/blob/main/notebooks/Kova_OmniVoice_Colab.ipynb)

The notebook runs OmniVoice only on a Colab GPU and prints a short-lived worker URL plus session token for Kova Desktop. It does not create a voice profile until the desktop user chooses an authorized reference clip and confirms consent.