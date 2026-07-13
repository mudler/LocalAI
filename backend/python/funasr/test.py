import importlib
import os
import re
import sys
import types
import unittest


class _Reply:
    def __init__(self, message=b""):
        self.message = message


class _Result:
    def __init__(self, message="", success=False):
        self.message = message
        self.success = success


class _TranscriptSegment:
    def __init__(self, id=0, start=0, end=0, text=""):
        self.id = id
        self.start = start
        self.end = end
        self.text = text


class _TranscriptResult:
    def __init__(self, segments=None, text=""):
        self.segments = segments or []
        self.text = text


class _FakeBackendServicer:
    pass


class _FakeTorch:
    cuda = types.SimpleNamespace(is_available=lambda: False)
    xpu = types.SimpleNamespace(is_available=lambda: False)
    backends = types.SimpleNamespace(mps=types.SimpleNamespace(is_available=lambda: False))


class _FakeAutoModel:
    instances = []

    def __init__(self, **kwargs):
        self.kwargs = kwargs
        self.generate_calls = []
        self.results = [{"text": "hello"}, {"text": " world"}]
        _FakeAutoModel.instances.append(self)

    def generate(self, **kwargs):
        self.generate_calls.append(kwargs)
        return self.results


def _install_stubs():
    sys.modules["backend_pb2"] = types.SimpleNamespace(
        Reply=_Reply,
        Result=_Result,
        TranscriptSegment=_TranscriptSegment,
        TranscriptResult=_TranscriptResult,
    )
    sys.modules["backend_pb2_grpc"] = types.SimpleNamespace(
        BackendServicer=_FakeBackendServicer,
        add_BackendServicer_to_server=lambda *args, **kwargs: None,
    )
    sys.modules["grpc"] = types.SimpleNamespace(
        RpcMethodHandler=object,
        ServerInterceptor=object,
        StatusCode=types.SimpleNamespace(UNAUTHENTICATED="UNAUTHENTICATED"),
        aio=types.SimpleNamespace(ServerInterceptor=object),
        server=lambda *args, **kwargs: None,
    )
    sys.modules["torch"] = _FakeTorch
    funasr = types.ModuleType("funasr")
    funasr.__path__ = []
    funasr.AutoModel = _FakeAutoModel
    funasr_utils = types.ModuleType("funasr.utils")
    funasr_utils.__path__ = []
    postprocess_utils = types.ModuleType("funasr.utils.postprocess_utils")
    postprocess_utils.rich_transcription_postprocess = lambda text: re.sub(
        r"<\|.*?\|>", "", text
    )
    sys.modules["funasr"] = funasr
    sys.modules["funasr.utils"] = funasr_utils
    sys.modules["funasr.utils.postprocess_utils"] = postprocess_utils


def _load_backend():
    _install_stubs()
    sys.modules.pop("backend", None)
    _FakeAutoModel.instances.clear()
    return importlib.import_module("backend")


class TestFunASRBackend(unittest.TestCase):
    def test_torch_profiles_install_torchaudio(self):
        backend_dir = os.path.dirname(__file__)

        for profile in (
            "cpu",
            "cublas12",
            "cublas13",
            "hipblas",
            "intel",
            "l4t12",
            "l4t13",
            "mps",
        ):
            with self.subTest(profile=profile):
                requirements_path = os.path.join(
                    backend_dir, f"requirements-{profile}.txt"
                )
                with open(requirements_path, encoding="utf-8") as requirements_file:
                    requirements = {
                        line.strip().split("=", 1)[0]
                        for line in requirements_file
                        if line.strip() and not line.lstrip().startswith(("#", "--"))
                    }

                self.assertIn("torchaudio", requirements)

    def test_accelerator_profiles_use_current_pytorch_indexes(self):
        backend_dir = os.path.dirname(__file__)
        expected_indexes = {
            "cublas13": "https://download.pytorch.org/whl/cu130",
            "hipblas": "https://download.pytorch.org/whl/rocm7.0",
            "intel": "https://download.pytorch.org/whl/xpu",
            "l4t12": "https://pypi.jetson-ai-lab.io/jp6/cu129/",
            "l4t13": "https://download.pytorch.org/whl/cu130",
        }

        for profile, expected_index in expected_indexes.items():
            with self.subTest(profile=profile):
                requirements_path = os.path.join(
                    backend_dir, f"requirements-{profile}.txt"
                )
                with open(requirements_path, encoding="utf-8") as requirements_file:
                    requirements = requirements_file.read()

                self.assertIn(
                    f"--extra-index-url {expected_index}\n",
                    requirements,
                )

    def test_health_returns_ok(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()

        reply = servicer.Health(types.SimpleNamespace(), None)

        self.assertEqual(reply.message, b"OK")

    def test_load_model_uses_default_sensevoice_model_on_cpu(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()

        result = servicer.LoadModel(types.SimpleNamespace(Model="", CUDA=False), None)

        self.assertTrue(result.success, result.message)
        self.assertEqual(result.message, "Model loaded successfully")
        self.assertEqual(_FakeAutoModel.instances[0].kwargs["model"], "iic/SenseVoiceSmall")
        self.assertEqual(_FakeAutoModel.instances[0].kwargs["vad_model"], "fsmn-vad")
        self.assertEqual(_FakeAutoModel.instances[0].kwargs["device"], "cpu")
        self.assertTrue(_FakeAutoModel.instances[0].kwargs["disable_update"])

    def test_load_model_uses_xpu_when_available(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()
        original_xpu = _FakeTorch.xpu
        _FakeTorch.xpu = types.SimpleNamespace(is_available=lambda: True)
        self.addCleanup(setattr, _FakeTorch, "xpu", original_xpu)

        result = servicer.LoadModel(
            types.SimpleNamespace(Model="iic/SenseVoiceSmall", CUDA=False), None
        )

        self.assertTrue(result.success, result.message)
        self.assertEqual(_FakeAutoModel.instances[0].kwargs["device"], "xpu")

    def test_audio_transcription_passes_language_and_builds_segments(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()
        servicer.model = _FakeAutoModel()
        audio_path = os.path.abspath(__file__)
        request = types.SimpleNamespace(dst=audio_path, language=" zh ")

        result = servicer.AudioTranscription(request, None)

        self.assertEqual(result.text, "hello world")
        self.assertEqual([segment.text for segment in result.segments], ["hello", " world"])
        self.assertEqual(servicer.model.generate_calls, [{"input": audio_path, "language": "zh"}])

    def test_audio_transcription_cleans_sensevoice_tags(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()
        servicer.model = _FakeAutoModel()
        servicer.model.results = [
            {"text": "<|zh|><|NEUTRAL|><|Speech|><|woitn|>hello"}
        ]

        result = servicer.AudioTranscription(
            types.SimpleNamespace(dst=os.path.abspath(__file__), language=""),
            None,
        )

        self.assertEqual(result.text, "hello")
        self.assertEqual([segment.text for segment in result.segments], ["hello"])

    def test_audio_transcription_missing_file_returns_empty_result(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()
        servicer.model = _FakeAutoModel()

        result = servicer.AudioTranscription(
            types.SimpleNamespace(dst="/tmp/localai-funasr-missing.wav", language=""),
            None,
        )

        self.assertEqual(result.text, "")
        self.assertEqual(result.segments, [])
        self.assertEqual(servicer.model.generate_calls, [])


if __name__ == "__main__":
    unittest.main()
