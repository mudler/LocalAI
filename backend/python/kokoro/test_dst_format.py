"""Regression test: a TTS destination without a file extension.

In distributed mode the worker hands the backend a staging file such as
/staging/localai-output-1234.tmp, so soundfile cannot infer the audio format
from the extension. No model and no backend dependencies are needed: the
torch/kokoro/soundfile modules are replaced by stubs that keep libsndfile's
extension check.
"""
import importlib.util
import os
import sys
import tempfile
import types
import unittest

BACKEND = os.path.join(os.path.dirname(os.path.abspath(__file__)), "backend.py")


def _module(name, **attrs):
    mod = types.ModuleType(name)
    for key, value in attrs.items():
        setattr(mod, key, value)
    sys.modules[name] = mod
    return mod


def _soundfile_write(file, data, samplerate, subtype=None, endian=None, format=None, closefd=True):
    if format is None:
        ext = os.path.splitext(str(file))[1].lstrip(".").upper()
        if ext in ("WAV", "FLAC", "OGG"):
            format = ext
        else:
            raise TypeError(
                "No format specified and unable to get format from file extension: "
                "'%s'" % file
            )
    with open(file, "wb") as handle:
        handle.write(b"RIFF")


def _load_backend():
    class Result:
        def __init__(self, message="", success=False):
            self.message = message
            self.success = success

    class BackendServicer:
        pass

    _module("torch", cat=lambda tensors, dim=0: tensors[0])
    _module("kokoro", KPipeline=lambda **kwargs: None)
    _module("soundfile", write=_soundfile_write)
    _module("grpc", server=lambda *args, **kwargs: None)
    _module("backend_pb2", Result=Result, HealthMessage=object)
    _module("backend_pb2_grpc", BackendServicer=BackendServicer, BackendStub=object)
    _module("grpc_auth", get_auth_interceptors=lambda: [])

    spec = importlib.util.spec_from_file_location("kokoro_backend", BACKEND)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class TestExtensionlessDestination(unittest.TestCase):
    def test_tts_writes_to_extensionless_destination(self):
        backend = _load_backend()
        servicer = backend.BackendServicer()
        servicer.pipeline = lambda text, voice: iter([(None, None, [0.0, 0.1])])

        with tempfile.TemporaryDirectory() as tmp:
            dst = os.path.join(tmp, "localai-output-1234.tmp")
            request = types.SimpleNamespace(text="Bonjour.", voice=None, dst=dst)
            result = servicer.TTS(request, None)

            self.assertTrue(result.success, result.message)
            self.assertGreater(os.path.getsize(dst), 0)


if __name__ == "__main__":
    unittest.main()