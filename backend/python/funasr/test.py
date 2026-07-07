import importlib
import os
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
    backends = types.SimpleNamespace(mps=types.SimpleNamespace(is_available=lambda: False))


class _FakeAutoModel:
    instances = []

    def __init__(self, **kwargs):
        self.kwargs = kwargs
        self.generate_calls = []
        _FakeAutoModel.instances.append(self)

    def generate(self, **kwargs):
        self.generate_calls.append(kwargs)
        return [{"text": "hello"}, {"text": " world"}]


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
    sys.modules["funasr"] = types.SimpleNamespace(AutoModel=_FakeAutoModel)


def _load_backend():
    _install_stubs()
    sys.modules.pop("backend", None)
    _FakeAutoModel.instances.clear()
    return importlib.import_module("backend")


class TestFunASRBackend(unittest.TestCase):
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
