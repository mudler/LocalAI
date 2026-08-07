#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import os
from pathlib import Path
import subprocess
import struct
import sys
import tempfile
from types import SimpleNamespace
import unittest
import wave

sys.path.insert(0, os.path.dirname(__file__))

from runtime import BackendFailure, MLXAudioRuntime, configured_role, ensure_torch_free
from check_dependencies import verify_serving_environment


def write_wav(path, audio_segments, sample_rate):
    audio = [sample for segment in audio_segments for sample in segment]
    with wave.open(path, "wb") as output:
        output.setnchannels(1)
        output.setsampwidth(2)
        output.setframerate(sample_rate)
        output.writeframes(b"".join(struct.pack("<h", int(sample * 32767)) for sample in audio))


class FakeVAD:
    def __init__(self, segments=None):
        self.segments = segments or []
        self.calls = []

    def get_speech_timestamps(self, path, return_seconds=False):
        with wave.open(path, "rb") as source:
            self.calls.append((source.getframerate(), source.getnchannels(), return_seconds))
        return self.segments


class FakeASR:
    def __init__(self, text="hello"):
        self.text = text
        self.calls = []

    def generate(self, path, language=None):
        self.calls.append((path, language))
        return SimpleNamespace(text=self.text)


class FakeTTS:
    def __init__(self, results=None):
        self.results = results
        self.calls = []

    def generate_custom_voice(self, **kwargs):
        self.calls.append(kwargs)
        if self.results is None:
            return [SimpleNamespace(audio=[0.25, -0.25], sample_rate=24000)]
        return self.results


class MLXAudioRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.snapshot = self.root / "snapshot"
        self.snapshot.mkdir()
        self.loaded = []
        self.models = {"vad": FakeVAD(), "asr": FakeASR(), "tts": FakeTTS()}

        def loader_provider(role):
            def load(path, strict=False):
                self.loaded.append((role, path, strict))
                return self.models[role]
            return load

        self.cache_clears = 0

        def clear_cache():
            self.cache_clears += 1

        self.runtime = MLXAudioRuntime(
            loader_provider=loader_provider,
            writer=write_wav,
            temp_dir=str(self.root),
            cache_clear=clear_cache,
        )

    def tearDown(self):
        self.temp.cleanup()

    def assert_failure(self, code, callback):
        with self.assertRaises(BackendFailure) as raised:
            callback()
        self.assertEqual(raised.exception.code, code)

    def test_backend_starts_with_safe_path_enabled(self):
        source = Path(__file__).with_name("backend.py")
        isolated = self.root / "backend"
        isolated.mkdir()
        (isolated / "backend.py").write_text(source.read_text(encoding="utf-8"), encoding="utf-8")
        stubs = {
            "backend_pb2.py": "",
            "backend_pb2_grpc.py": "class BackendServicer:\n    pass\n",
            "grpc.py": "class StatusCode:\n    INTERNAL = 'internal'\n",
            "grpc_auth.py": "def get_auth_interceptors(**kwargs):\n    return []\n",
            "runtime.py": "class BackendFailure(Exception):\n    pass\nclass MLXAudioRuntime:\n    pass\n",
        }
        for name, contents in stubs.items():
            (isolated / name).write_text(contents, encoding="utf-8")
        env = os.environ.copy()
        env["PYTHONSAFEPATH"] = "1"
        result = subprocess.run(
            [sys.executable, str(isolated / "backend.py"), "--help"],
            cwd=self.root,
            env=env,
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("LocalAI MLX-Audio backend", result.stdout)

    def load(self, role):
        return self.runtime.load(str(self.snapshot), [f"role:{role}"], distributions=[])

    def test_role_is_explicit_and_never_inferred_from_filename(self):
        self.assert_failure("INVALID_ARGUMENT", lambda: configured_role([]))
        self.assert_failure("INVALID_ARGUMENT", lambda: configured_role(["role:llm"]))
        self.assertEqual(configured_role(["role:asr"]), "asr")

        misleading = self.root / "whisper-tts-vad"
        misleading.mkdir()
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.load(str(misleading), [], distributions=[]),
        )

    def test_load_requires_a_local_snapshot_and_uses_strict_public_loader(self):
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.load("repository/model", ["role:vad"], distributions=[]),
        )
        self.assert_failure(
            "NOT_FOUND",
            lambda: self.runtime.load(str(self.root / "missing"), ["role:vad"], distributions=[]),
        )
        self.assertEqual(self.load("vad"), "vad")
        self.assertEqual(self.loaded, [("vad", str(self.snapshot.resolve()), True)])

    def test_loader_failures_are_typed(self):
        def failing_provider(role):
            def load(path, strict=False):
                raise RuntimeError("model loader detail")
            return load

        runtime = MLXAudioRuntime(loader_provider=failing_provider, cache_clear=lambda: None)
        self.assert_failure(
            "INTERNAL",
            lambda: runtime.load(str(self.snapshot), ["role:vad"], distributions=[]),
        )

    def test_torch_and_torchaudio_are_rejected(self):
        ensure_torch_free(["mlx-audio", "numpy"])
        self.assert_failure("FAILED_PRECONDITION", lambda: ensure_torch_free(["Torch"]))
        self.assert_failure("FAILED_PRECONDITION", lambda: ensure_torch_free(["torchaudio"]))

    def test_serving_environment_check_enforces_pinned_versions(self):
        versions = {"grpcio": "1.78.0", "mlx-audio": "0.4.7"}
        verify_serving_environment([], versions.__getitem__)
        versions["mlx-audio"] = "0.4.8"
        self.assert_failure(
            "FAILED_PRECONDITION",
            lambda: verify_serving_environment([], versions.__getitem__),
        )

    def test_vad_maps_silence_and_speech_and_cleans_temporary_wavs(self):
        self.load("vad")
        self.assertEqual(self.runtime.vad([0.0, 0.0]), [])
        self.models["vad"].segments = [{"start": 0.25, "end": 0.75}]
        self.assertEqual(self.runtime.vad([0.0, 0.1, -0.1]), [(0.25, 0.75)])
        self.assertEqual(self.models["vad"].calls, [(16000, 1, True), (16000, 1, True)])
        self.assertEqual(list(self.root.glob("*.wav")), [])

    def test_vad_validates_input_and_model_output(self):
        self.load("vad")
        self.assert_failure("INVALID_ARGUMENT", lambda: self.runtime.vad([]))
        self.assert_failure("INVALID_ARGUMENT", lambda: self.runtime.vad([float("nan")]))
        self.models["vad"].segments = [{"start": 1.0, "end": 0.5}]
        self.assert_failure("INTERNAL", lambda: self.runtime.vad([0.1]))
        self.assertEqual(list(self.root.glob("*.wav")), [])

        def fail_inference(path, return_seconds=False):
            raise RuntimeError("VAD detail")

        self.models["vad"].get_speech_timestamps = fail_inference
        self.assert_failure("INTERNAL", lambda: self.runtime.vad([0.1]))
        self.assertEqual(list(self.root.glob("*.wav")), [])

    def test_transcription_maps_language_and_rejects_empty_output(self):
        source = self.root / "input.wav"
        write_wav(str(source), [[0.2]], 16000)
        self.load("asr")
        self.assertEqual(self.runtime.transcribe(str(source), "ko-KR"), ("hello", "Korean"))
        self.assertEqual(self.models["asr"].calls, [(str(source), "Korean")])

        self.models["asr"].text = "  "
        self.assert_failure("INTERNAL", lambda: self.runtime.transcribe(str(source), "English"))
        self.assert_failure("INVALID_ARGUMENT", lambda: self.runtime.transcribe("", "English"))

        def fail_transcription(path, language=None):
            raise RuntimeError("ASR detail")

        self.models["asr"].generate = fail_transcription
        self.assert_failure("INTERNAL", lambda: self.runtime.transcribe(str(source), "English"))

    def test_tts_uses_custom_voice_api_and_validates_output(self):
        self.load("tts")
        destination = self.root / "speech.wav"
        self.assertEqual(
            self.runtime.synthesize("hello", "Ryan", "en-US", str(destination)),
            str(destination),
        )
        self.assertTrue(destination.is_file())
        self.assertEqual(
            self.models["tts"].calls,
            [{"text": "hello", "speaker": "Ryan", "language": "English"}],
        )

    def test_tts_concatenates_public_api_segments(self):
        self.models["tts"].results = [
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ]
        self.load("tts")
        destination = self.root / "segments.wav"
        self.runtime.synthesize("hello", "Ryan", "English", str(destination))
        with wave.open(str(destination), "rb") as source:
            self.assertEqual(source.getnframes(), 2)

    def test_tts_removes_partial_output_on_failure(self):
        def invalid_writer(path, audio, sample_rate):
            Path(path).write_bytes(b"not a wav")

        self.runtime._writer = invalid_writer
        self.load("tts")
        destination = self.root / "partial.wav"
        self.assert_failure(
            "INTERNAL",
            lambda: self.runtime.synthesize("hello", "Ryan", "English", str(destination)),
        )
        self.assertFalse(destination.exists())

        self.models["tts"].results = []
        self.assert_failure(
            "INTERNAL",
            lambda: self.runtime.synthesize("hello", "Ryan", "English", str(destination)),
        )
        self.assertFalse(destination.exists())

        def fail_generation(**kwargs):
            raise RuntimeError("TTS detail")

        self.models["tts"].generate_custom_voice = fail_generation
        self.assert_failure(
            "INTERNAL",
            lambda: self.runtime.synthesize("hello", "Ryan", "English", str(destination)),
        )
        self.assertFalse(destination.exists())

    def test_role_mismatch_and_language_failures_are_typed(self):
        self.load("vad")
        self.assert_failure(
            "FAILED_PRECONDITION",
            lambda: self.runtime.transcribe(str(self.root / "missing.wav"), "English"),
        )
        self.load("tts")
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.synthesize("hello", "Sohee", "English", str(self.root / "x.wav")),
        )
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.synthesize("hello", "Ryan", "French", str(self.root / "x.wav")),
        )

    def test_unload_and_reload_drop_model_and_clear_cache(self):
        self.load("vad")
        first = self.models["vad"]
        self.load("asr")
        self.assertIsNot(self.runtime.model, first)
        self.assertEqual(self.runtime.role, "asr")
        self.runtime.unload()
        self.assertIsNone(self.runtime.model)
        self.assertIsNone(self.runtime.role)
        self.assertGreaterEqual(self.cache_clears, 3)


if __name__ == "__main__":
    unittest.main()
