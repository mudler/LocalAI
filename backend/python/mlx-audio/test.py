#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import os
from pathlib import Path
import subprocess
import struct
import sys
import tempfile
import threading
import importlib.util
import json
from types import SimpleNamespace
import unittest
import wave

sys.path.insert(0, os.path.dirname(__file__))

from runtime import (
    BackendFailure,
    MLXAudioRuntime,
    configured_role,
    ensure_torch_free,
    pcm16le_from_tts_segment,
    request_cancelled,
    tts_generation_options,
)
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


class ClosingResults:
    def __init__(self, results):
        self._results = iter(results)
        self.closed = False

    def __iter__(self):
        return self

    def __next__(self):
        return next(self._results)

    def close(self):
        self.closed = True


class BlockingResults:
    def __init__(self, started, release):
        self.started = started
        self.release = release
        self.closed = False
        self._yielded = False

    def __iter__(self):
        return self

    def __next__(self):
        if self._yielded:
            raise StopIteration
        self.started.set()
        self.release.wait(timeout=2)
        self._yielded = True
        return SimpleNamespace(audio=[0.25], sample_rate=24000)

    def close(self):
        self.closed = True


class FakeReply:
    def __init__(self, message=b"", audio=b""):
        self.message = message
        self.audio = audio


class FakeStreamContext:
    def __init__(self):
        self.cancelled_value = False
        self.code = None
        self.details = None

    def cancelled(self):
        return self.cancelled_value

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details


def load_backend_servicer_module():
    original_modules = {name: sys.modules.get(name) for name in (
        "backend_pb2", "backend_pb2_grpc", "grpc", "grpc_auth", "mlx_audio_backend_under_test",
    )}
    sys.modules["backend_pb2"] = SimpleNamespace(
        Reply=FakeReply,
        Result=lambda **kwargs: SimpleNamespace(**kwargs),
        VADResponse=lambda **kwargs: SimpleNamespace(**kwargs),
        VADSegment=lambda **kwargs: SimpleNamespace(**kwargs),
        TranscriptResult=lambda **kwargs: SimpleNamespace(**kwargs),
    )
    sys.modules["backend_pb2_grpc"] = SimpleNamespace(BackendServicer=object)
    sys.modules["grpc"] = SimpleNamespace(
        StatusCode=SimpleNamespace(
            INTERNAL="INTERNAL",
            CANCELLED="CANCELLED",
            INVALID_ARGUMENT="INVALID_ARGUMENT",
            FAILED_PRECONDITION="FAILED_PRECONDITION",
        )
    )
    sys.modules["grpc_auth"] = SimpleNamespace(get_auth_interceptors=lambda **kwargs: [])
    try:
        source = Path(__file__).with_name("backend.py")
        spec = importlib.util.spec_from_file_location("mlx_audio_backend_under_test", source)
        module = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = module
        spec.loader.exec_module(module)
        return module
    finally:
        for name, previous in original_modules.items():
            if previous is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = previous


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

        self.seeds = []
        self.runtime = MLXAudioRuntime(
            loader_provider=loader_provider,
            writer=write_wav,
            temp_dir=str(self.root),
            cache_clear=clear_cache,
            seed_setter=self.seeds.append,
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
            "runtime.py": (
                "class BackendFailure(Exception):\n    pass\n"
                "class MLXAudioRuntime:\n    pass\n"
                "def request_cancelled(context):\n    return False\n"
            ),
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

    def test_model_root_resolves_relative_snapshots_and_rejects_escape(self):
        nested = self.root / "models" / "snapshot"
        nested.mkdir(parents=True)
        self.assertEqual(
            self.runtime.load(
                "snapshot",
                ["role:vad"],
                distributions=[],
                model_root=str(nested.parent),
            ),
            "vad",
        )
        self.assertEqual(self.loaded[-1], ("vad", str(nested.resolve()), True))
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.load(
                str(self.snapshot),
                ["role:vad"],
                distributions=[],
                model_root=str(nested.parent),
            ),
        )

        external = self.root / "external"
        external.mkdir()
        (nested.parent / "linked").symlink_to(external, target_is_directory=True)
        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.load(
                "linked",
                ["role:vad"],
                distributions=[],
                model_root=str(nested.parent),
            ),
        )

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
            [{
                "text": "hello",
                "speaker": "Ryan",
                "language": "English",
                "temperature": 0.7,
                "max_tokens": 1200,
                "top_k": 50,
                "top_p": 1.0,
                "repetition_penalty": 1.05,
                "stream": True,
                "streaming_interval": 0.32,
            }],
        )

    def test_tts_generation_parameters_are_bounded(self):
        options = tts_generation_options(
            {
                "temperature": "0.5",
                "max_tokens": "600",
                "streaming_interval": "0.16",
            }
        )
        self.assertEqual(options["temperature"], 0.5)
        self.assertEqual(options["max_tokens"], 600)
        self.assertTrue(options["stream"])
        self.assertEqual(options["streaming_interval"], 0.16)
        for values in (
            {"max_tokens": "1201"},
            {"top_p": "0"},
            {"top_k": "bad"},
            {"streaming_interval": "0.08"},
            {"streaming_interval": "0.2"},
            {"streaming_interval": "bad"},
        ):
            self.assert_failure("INVALID_ARGUMENT", lambda values=values: tts_generation_options(values))

    def test_stream_pcm_is_little_endian_pcm16_with_clipping(self):
        pcm = pcm16le_from_tts_segment(
            SimpleNamespace(audio=[1.5, -1.5, 0.5], sample_rate=24000)
        )
        self.assertEqual(pcm, struct.pack("<hhh", 32767, -32767, 16383))
        self.assertEqual(len(pcm) % 2, 0)

    def test_stream_pcm_rejects_empty_nonfinite_and_wrong_rate_results(self):
        for segment in (
            SimpleNamespace(audio=[], sample_rate=24000),
            SimpleNamespace(audio=[float("nan")], sample_rate=24000),
            SimpleNamespace(audio=[0.1], sample_rate=16000),
            SimpleNamespace(audio=object(), sample_rate=24000),
        ):
            self.assert_failure("INTERNAL", lambda segment=segment: pcm16le_from_tts_segment(segment))

    def test_stream_yields_immediate_pcm_without_writing_a_wav(self):
        self.models["tts"].results = [
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ]
        self.load("tts")

        stream = self.runtime.synthesize_stream("hello", "Ryan", "English")
        self.assertEqual(next(stream), struct.pack("<h", 8191))
        self.assertEqual(list(self.root.glob("*.wav")), [])
        self.assertEqual(list(stream), [struct.pack("<h", -8191)])

    def test_stream_cancellation_before_and_after_results_closes_generator(self):
        results = ClosingResults([
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ])
        self.models["tts"].results = results
        self.load("tts")
        cancelled = [True]
        stream = self.runtime.synthesize_stream(
            "hello", "Ryan", "English", cancelled=lambda: cancelled[0]
        )
        self.assert_failure("CANCELLED", lambda: next(stream))
        self.assertTrue(results.closed)

        results = ClosingResults([
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ])
        self.models["tts"].results = results
        cancelled[0] = False
        stream = self.runtime.synthesize_stream(
            "hello", "Ryan", "English", cancelled=lambda: cancelled[0]
        )
        self.assertEqual(next(stream), struct.pack("<h", 8191))
        cancelled[0] = True
        self.assert_failure("CANCELLED", lambda: next(stream))
        self.assertTrue(results.closed)

    def test_stream_serializes_unload_until_generation_finishes(self):
        started = threading.Event()
        release = threading.Event()
        results = BlockingResults(started, release)
        self.models["tts"].results = results
        self.load("tts")
        stream = self.runtime.synthesize_stream("hello", "Ryan", "English")
        produced = []
        worker = threading.Thread(target=lambda: produced.append(next(stream)))
        worker.start()
        self.assertTrue(started.wait(timeout=1))

        unload_done = threading.Event()
        unloader = threading.Thread(target=lambda: (self.runtime.unload(), unload_done.set()))
        unloader.start()
        self.assertFalse(unload_done.wait(timeout=0.05))
        release.set()
        worker.join(timeout=1)
        stream.close()
        unloader.join(timeout=1)
        self.assertEqual(produced, [struct.pack("<h", 8191)])
        self.assertTrue(unload_done.is_set())
        self.assertIsNone(self.runtime.model)

    def test_tts_stream_sends_one_metadata_frame_then_pcm_frames(self):
        backend_module = load_backend_servicer_module()
        self.models["tts"].results = [
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ]
        self.load("tts")
        context = FakeStreamContext()
        request = SimpleNamespace(text="hello", voice="Ryan", language="English", params={})

        replies = list(backend_module.BackendServicer(self.runtime).TTSStream(request, context))

        self.assertEqual(len(replies), 3)
        self.assertEqual(json.loads(replies[0].message), {"sample_rate": 24000})
        self.assertTrue(all(not reply.message for reply in replies[1:]))
        self.assertEqual([reply.audio for reply in replies[1:]], [
            struct.pack("<h", 8191), struct.pack("<h", -8191),
        ])
        self.assertIsNone(context.code)

    def test_tts_stream_uses_typed_cancellation_without_error_frames(self):
        backend_module = load_backend_servicer_module()
        results = ClosingResults([
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ])
        self.models["tts"].results = results
        self.load("tts")
        context = FakeStreamContext()
        request = SimpleNamespace(text="hello", voice="Ryan", language="English", params={})
        stream = backend_module.BackendServicer(self.runtime).TTSStream(request, context)

        self.assertEqual(json.loads(next(stream).message), {"sample_rate": 24000})
        self.assertEqual(next(stream).audio, struct.pack("<h", 8191))
        context.cancelled_value = True
        self.assertEqual(list(stream), [])
        self.assertEqual(context.code, "CANCELLED")
        self.assertTrue(results.closed)

    def test_tts_stream_cancellation_before_metadata_returns_only_a_typed_status(self):
        backend_module = load_backend_servicer_module()
        self.load("tts")
        context = FakeStreamContext()
        context.cancelled_value = True
        request = SimpleNamespace(text="hello", voice="Ryan", language="English", params={})

        self.assertEqual(
            list(backend_module.BackendServicer(self.runtime).TTSStream(request, context)), []
        )
        self.assertEqual(context.code, "CANCELLED")

    def test_tts_stream_reports_malformed_output_with_a_typed_status(self):
        backend_module = load_backend_servicer_module()
        self.models["tts"].results = [SimpleNamespace(audio=[], sample_rate=24000)]
        self.load("tts")
        context = FakeStreamContext()
        request = SimpleNamespace(text="hello", voice="Ryan", language="English", params={})

        replies = list(backend_module.BackendServicer(self.runtime).TTSStream(request, context))

        self.assertEqual(replies, [])
        self.assertEqual(context.code, "INTERNAL")
        self.assertFalse(any(reply.message.startswith(b"Error:") for reply in replies))

    def test_tts_load_options_bound_generation_and_add_diagnostic_seed(self):
        self.runtime.load(
            str(self.snapshot),
            [
                "role:tts",
                "max_tokens:256",
                "diagnostic_seed:17",
                "streaming_interval:0.16",
            ],
            distributions=[],
        )
        destination = self.root / "configured.wav"

        self.runtime.synthesize(
            "hello", "Ryan", "English", str(destination), params={"max_tokens": "192"}
        )

        self.assertEqual(self.models["tts"].calls[-1]["max_tokens"], 192)
        self.assertEqual(self.models["tts"].calls[-1]["streaming_interval"], 0.16)
        self.assertNotIn("seed", self.models["tts"].calls[-1])
        self.assertEqual(self.seeds, [17])

    def test_invalid_tts_load_options_leave_the_current_model_untouched(self):
        self.load("vad")
        loaded_model = self.runtime.model

        self.assert_failure(
            "INVALID_ARGUMENT",
            lambda: self.runtime.load(
                str(self.snapshot), ["role:tts", "max_tokens:1201"], distributions=[]
            ),
        )

        self.assertIs(self.runtime.model, loaded_model)
        self.assertEqual(self.runtime.role, "vad")

    def test_health_does_not_wait_for_a_stream_holding_model_state(self):
        backend_module = load_backend_servicer_module()
        started = threading.Event()
        release = threading.Event()
        self.models["tts"].results = BlockingResults(started, release)
        self.load("tts")
        stream = self.runtime.synthesize_stream("hello", "Ryan", "English")
        worker = threading.Thread(target=lambda: next(stream))
        worker.start()
        self.assertTrue(started.wait(timeout=1))

        reply = backend_module.BackendServicer(self.runtime).Health(SimpleNamespace(), FakeStreamContext())

        self.assertEqual(reply.message, b"OK")
        release.set()
        worker.join(timeout=1)
        stream.close()

    def test_cancellation_supports_aio_and_sync_grpc_contexts(self):
        self.assertTrue(request_cancelled(SimpleNamespace(cancelled=lambda: True)))
        self.assertFalse(request_cancelled(SimpleNamespace(cancelled=lambda: False)))
        self.assertTrue(request_cancelled(SimpleNamespace(is_active=lambda: False)))
        self.assertFalse(request_cancelled(SimpleNamespace(is_active=lambda: True)))
        self.assertFalse(request_cancelled(SimpleNamespace()))

    def test_tts_cancellation_removes_partial_output(self):
        self.models["tts"].results = [
            SimpleNamespace(audio=[0.25], sample_rate=24000),
            SimpleNamespace(audio=[-0.25], sample_rate=24000),
        ]
        self.load("tts")
        destination = self.root / "cancelled.wav"
        checks = iter((False, True))
        self.assert_failure(
            "CANCELLED",
            lambda: self.runtime.synthesize(
                "hello",
                "Ryan",
                "English",
                str(destination),
                cancelled=lambda: next(checks),
            ),
        )
        self.assertFalse(destination.exists())

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
