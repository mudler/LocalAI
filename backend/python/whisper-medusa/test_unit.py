import importlib.util
import pathlib
import sys
import types
import unittest


class _Message:
    def __init__(self, **kwargs):
        self.__dict__.update(kwargs)


backend_pb2 = types.ModuleType("backend_pb2")
for name in ("Reply", "Result", "TranscriptResult", "TranscriptSegment"):
    setattr(backend_pb2, name, _Message)
sys.modules["backend_pb2"] = backend_pb2

backend_pb2_grpc = types.ModuleType("backend_pb2_grpc")
backend_pb2_grpc.BackendServicer = object
backend_pb2_grpc.add_BackendServicer_to_server = lambda *args: None
sys.modules["backend_pb2_grpc"] = backend_pb2_grpc

grpc = types.ModuleType("grpc")
grpc.server = lambda *args, **kwargs: None
sys.modules["grpc"] = grpc

grpc_auth = types.ModuleType("grpc_auth")
grpc_auth.get_auth_interceptors = lambda: []
sys.modules["grpc_auth"] = grpc_auth

model_utils = types.ModuleType("model_utils")
model_utils.resolve_model_reference = lambda request, default: (request.Model or default, False)
sys.modules["model_utils"] = model_utils

spec = importlib.util.spec_from_file_location(
    "whisper_medusa_backend", pathlib.Path(__file__).with_name("backend.py")
)
backend = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backend)


class FakeTensor:
    def __init__(self, channels=1, samples=16000):
        self.channels = channels
        self.samples = samples
        self.shape = (channels, samples)
        self.mean_calls = []

    def mean(self, dim, keepdim):
        self.mean_calls.append((dim, keepdim))
        return FakeTensor(1, self.samples)

    def squeeze(self):
        return self

    def to(self, device):
        return self


class FakeTorchaudio:
    class transforms:
        class Resample:
            def __init__(self, source, target):
                self.source = source
                self.target = target

            def __call__(self, waveform):
                return FakeTensor(waveform.channels, waveform.samples * self.target // self.source)

    @staticmethod
    def load(path):
        return FakeTensor(2, 8000), 8000


class BackendHelpersTest(unittest.TestCase):
    def test_parse_options_converts_supported_scalar_types(self):
        self.assertEqual(
            backend._parse_options(["regulation_start:120", "regulation_factor:1.05", "ignored"]),
            {"regulation_start": 120, "regulation_factor": 1.05},
        )

    def test_prepare_audio_mixes_to_mono_and_resamples_to_16khz(self):
        waveform, sample_rate = backend._prepare_audio("clip.wav", FakeTorchaudio)
        self.assertEqual(sample_rate, 16000)
        self.assertEqual(waveform.shape, (1, 16000))


if __name__ == "__main__":
    unittest.main()
