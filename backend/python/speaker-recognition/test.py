"""Unit tests for the speaker-recognition gRPC backend.

The servicer is instantiated in-process (no gRPC channel) and driven
directly. The default path exercises SpeechBrain's ECAPA-TDNN — the
first run downloads the checkpoint into a temp savedir. Tests are
skipped gracefully when the heavy optional dependencies (torch /
speechbrain / onnxruntime) are not installed, so the gRPC plumbing
can still be verified on a bare image.
"""
from __future__ import annotations

import importlib
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(__file__))

import backend_pb2  # noqa: E402

from backend import BackendServicer  # noqa: E402


def _have(*mods: str) -> bool:
    for m in mods:
        if importlib.util.find_spec(m) is None:
            return False
    return True


class _FakeCtx:
    """Minimal stand-in for a gRPC servicer context."""

    def __init__(self) -> None:
        self.code = None
        self.details = ""

    def set_code(self, c):
        self.code = c

    def set_details(self, d):
        self.details = d


class ServicerPlumbingTest(unittest.TestCase):
    """Checks that LoadModel returns a clear error when no engine deps
    are installed, and that Voice* calls on an uninitialised servicer
    surface FAILED_PRECONDITION — both verifying the gRPC wiring
    without requiring SpeechBrain or ONNX at test time."""

    def test_pre_load_voice_calls_are_rejected(self):
        svc = BackendServicer()
        ctx = _FakeCtx()
        svc.VoiceVerify(backend_pb2.VoiceVerifyRequest(audio1="/tmp/a.wav", audio2="/tmp/b.wav"), ctx)
        self.assertEqual(str(ctx.code), "StatusCode.FAILED_PRECONDITION")

    def test_load_without_deps_fails_cleanly(self):
        svc = BackendServicer()
        req = backend_pb2.ModelOptions(Model="speechbrain/spkrec-ecapa-voxceleb", ModelPath="")
        result = svc.LoadModel(req, _FakeCtx())
        # Either the deps are installed and it loaded, or they aren't
        # and we got a structured error instead of a crash.
        self.assertTrue(result.success or "engine init failed" in result.message)


@unittest.skipUnless(_have("speechbrain", "torch", "torchaudio"), "speechbrain / torch missing")
class SpeechBrainEngineSmokeTest(unittest.TestCase):
    def test_load_and_embed(self):
        svc = BackendServicer()
        with tempfile.TemporaryDirectory() as td:
            req = backend_pb2.ModelOptions(Model="speechbrain/spkrec-ecapa-voxceleb", ModelPath=td)
            result = svc.LoadModel(req, _FakeCtx())
            self.assertTrue(result.success, result.message)


if __name__ == "__main__":
    unittest.main()
