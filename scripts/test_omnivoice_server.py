from __future__ import annotations

import json
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import omnivoice_server as worker


class FakeSoundFile:
    @staticmethod
    def write(output, audio, sampling_rate, *, format):
        assert audio == [0.0, 0.25]
        assert sampling_rate == 24000
        assert format == "WAV"
        output.write(b"RIFF-fake-wav")


class FakeModel:
    sampling_rate = 24000

    def __init__(self):
        self.asr_calls = []
        self.prompt_calls = []
        self.generate_calls = []

    def load_asr_model(self, *, model_name):
        self.asr_calls.append(model_name)

    def create_voice_clone_prompt(self, *, ref_audio, ref_text):
        prompt = {"ref_audio": ref_audio, "ref_text": ref_text}
        self.prompt_calls.append(prompt)
        return prompt

    def generate(self, **kwargs):
        self.generate_calls.append(kwargs)
        return [[0.0, 0.25]]


class EmptyAudioModel(FakeModel):
    def generate(self, **kwargs):
        self.generate_calls.append(kwargs)
        return [[]]


def make_runtime(model=None):
    return worker.OmniVoiceRuntime(
        model or FakeModel(),
        FakeSoundFile,
        model_name="fake",
        device="cpu",
        dtype_name="float32",
        asr_model_name="fake-whisper",
        prompt_cache_size=2,
    )


class OmniVoiceWorkerTests(unittest.TestCase):
    def test_parse_vietnamese_clone_request(self):
        with tempfile.NamedTemporaryFile(suffix=".wav") as reference:
            reference.write(b"not-empty")
            reference.flush()
            parsed = worker.parse_synthesis_request(
                {
                    "text": "Xin chào Việt Nam",
                    "ref_audio": f"local:{reference.name}",
                    "ref_text": "Đây là giọng mẫu.",
                    "language": "vi",
                    "instruct": "female, low pitch",
                    "speed": 1.1,
                    "num_steps": 16,
                }
            )

        self.assertEqual(parsed.text, "Xin chào Việt Nam")
        self.assertEqual(parsed.language, "vi")
        self.assertEqual(parsed.instruct, "female, low pitch")
        self.assertEqual(parsed.speed, 1.1)
        self.assertEqual(parsed.num_steps, 16)

    def test_remote_reference_is_rejected(self):
        with self.assertRaises(worker.RequestValidationError) as caught:
            worker.parse_synthesis_request(
                {"text": "hello", "ref_audio": "https://example.test/ref.wav"}
            )
        self.assertEqual(caught.exception.code, "invalid_ref_audio")

    def test_runtime_reuses_model_and_voice_prompt(self):
        model = FakeModel()
        runtime = make_runtime(model)
        with tempfile.NamedTemporaryFile(suffix=".wav") as reference:
            reference.write(b"not-empty")
            reference.flush()
            request = worker.parse_synthesis_request(
                {
                    "text": "Xin chào",
                    "ref_audio": reference.name,
                    "language": "vi",
                    "instruct": "female, low pitch",
                }
            )
            first = runtime.synthesize(request)
            second = runtime.synthesize(request)

        self.assertEqual(first, b"RIFF-fake-wav")
        self.assertEqual(second, b"RIFF-fake-wav")
        self.assertEqual(model.asr_calls, ["fake-whisper"])
        self.assertEqual(len(model.prompt_calls), 1)
        self.assertEqual(len(model.generate_calls), 2)
        self.assertEqual(model.generate_calls[0]["num_step"], 32)
        self.assertEqual(model.generate_calls[0]["language"], "vi")
        self.assertEqual(
            model.generate_calls[0]["instruct"], "female, low pitch"
        )
        self.assertIn("voice_clone_prompt", model.generate_calls[0])

    def test_runtime_rejects_empty_audio_samples(self):
        runtime = make_runtime(EmptyAudioModel())
        request = worker.parse_synthesis_request({"text": "Bức tượng.", "language": "vi"})
        with self.assertRaisesRegex(RuntimeError, "empty audio samples"):
            runtime.synthesize(request)

    def test_http_health_and_synthesize_contract(self):
        runtime = make_runtime()
        server = worker.build_server("127.0.0.1", 0, runtime)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        host, port = server.server_address
        base_url = f"http://{host}:{port}"
        try:
            with urllib.request.urlopen(f"{base_url}/health") as response:
                health = json.load(response)
            self.assertTrue(health["ready"])
            self.assertEqual(health["dtype"], "float32")

            body = json.dumps({"text": "Xin chào", "language": "vi"}).encode()
            request = urllib.request.Request(
                f"{base_url}/synthesize",
                data=body,
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(request) as response:
                self.assertEqual(response.headers.get_content_type(), "audio/wav")
                self.assertEqual(response.read(), b"RIFF-fake-wav")

            bad_request = urllib.request.Request(
                f"{base_url}/synthesize",
                data=b"{}",
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with self.assertRaises(urllib.error.HTTPError) as caught:
                urllib.request.urlopen(bad_request)
            error = json.loads(caught.exception.read())
            self.assertEqual(error["error"]["code"], "invalid_text")
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_http_reference_upload_is_used_by_synthesis(self):
        model = FakeModel()
        runtime = make_runtime(model)
        with tempfile.TemporaryDirectory() as reference_dir:
            server = worker.build_server(
                "127.0.0.1", 0, runtime, reference_dir=reference_dir
            )
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            host, port = server.server_address
            base_url = f"http://{host}:{port}"
            try:
                upload = urllib.request.Request(
                    f"{base_url}/reference",
                    data=b"uploaded-reference",
                    headers={
                        "Content-Type": "application/octet-stream",
                        "X-OmniVoice-Reference-Name": "speaker.m4a",
                    },
                    method="POST",
                )
                with urllib.request.urlopen(upload) as response:
                    uploaded = json.load(response)
                self.assertTrue(uploaded["reference"].startswith("reference:"))

                body = json.dumps(
                    {
                        "text": "Xin chào",
                        "ref_audio": uploaded["reference"],
                        "ref_text": "Giọng tham chiếu.",
                        "language": "vi",
                    }
                ).encode()
                request = urllib.request.Request(
                    f"{base_url}/synthesize",
                    data=body,
                    headers={"Content-Type": "application/json"},
                    method="POST",
                )
                with urllib.request.urlopen(request) as response:
                    self.assertEqual(response.read(), b"RIFF-fake-wav")
                self.assertEqual(len(model.prompt_calls), 1)
                self.assertEqual(
                    Path(model.prompt_calls[0]["ref_audio"]).resolve().parent,
                    Path(reference_dir).resolve(),
                )
            finally:
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
