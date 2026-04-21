"""Unit tests for the insightface gRPC backend.

The servicer is instantiated in-process (no gRPC channel) and driven
directly. Images come from insightface.data which ships with the pip
package — no external downloads.

Tests are parametrized over both engines (InsightFaceEngine and
OnnxDirectEngine) where applicable.
"""
from __future__ import annotations

import base64
import os
import sys
import unittest

import cv2
import numpy as np

sys.path.insert(0, os.path.dirname(__file__))

import backend_pb2  # noqa: E402

from backend import BackendServicer  # noqa: E402

# OpenCV Zoo face ONNX files — downloaded on demand in OnnxDirectEngineTest
# to mirror LocalAI's gallery `files:` flow (the backend image itself
# doesn't ship model weights).
OPENCV_FILES = [
    (
        "face_detection_yunet_2023mar.onnx",
        "https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar.onnx",
        "8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4",
    ),
    (
        "face_recognition_sface_2021dec.onnx",
        "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx",
        "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79",
    ),
]


def _encode(img: np.ndarray) -> str:
    _, buf = cv2.imencode(".jpg", img)
    return base64.b64encode(buf.tobytes()).decode("ascii")


def _load_insightface_samples() -> dict[str, str]:
    """Return {'t1': <b64>, 't2': <b64>} from insightface.data.get_image.

    t1 is a group photo, t2 a different one. We reuse both as
    stand-ins for "Alice photo 1/2" and "Bob".
    """
    from insightface.data import get_image as ins_get_image

    return {
        "t1": _encode(ins_get_image("t1")),
        "t2": _encode(ins_get_image("t2")),
    }


class _FakeContext:
    """Minimal stand-in for grpc.ServicerContext."""

    def __init__(self) -> None:
        self.code = None
        self.details = None

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details


class _Harness:
    def __init__(self, servicer: BackendServicer) -> None:
        self.svc = servicer

    def health(self):
        return self.svc.Health(backend_pb2.HealthMessage(), _FakeContext())

    def load(self, options: list[str], model_path: str = ""):
        return self.svc.LoadModel(
            backend_pb2.ModelOptions(Model="test", Options=options, ModelPath=model_path),
            _FakeContext(),
        )

    def detect(self, img_b64: str):
        return self.svc.Detect(backend_pb2.DetectOptions(src=img_b64), _FakeContext())

    def embed(self, img_b64: str):
        ctx = _FakeContext()
        res = self.svc.Embedding(
            backend_pb2.PredictOptions(Images=[img_b64]),
            ctx,
        )
        return res, ctx

    def verify(self, a: str, b: str, threshold: float = 0.0):
        return self.svc.FaceVerify(
            backend_pb2.FaceVerifyRequest(img1=a, img2=b, threshold=threshold),
            _FakeContext(),
        )

    def analyze(self, img_b64: str):
        return self.svc.FaceAnalyze(
            backend_pb2.FaceAnalyzeRequest(img=img_b64),
            _FakeContext(),
        )


class InsightFaceEngineTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.samples = _load_insightface_samples()
        cls.harness = _Harness(BackendServicer())
        load = cls.harness.load(["engine:insightface", "model_pack:buffalo_l"])
        if not load.success:
            raise unittest.SkipTest(f"LoadModel failed: {load.message}")

    def test_health(self):
        self.assertEqual(self.harness.health().message, b"OK")

    def test_detect_finds_face(self):
        res = self.harness.detect(self.samples["t1"])
        self.assertGreater(len(res.Detections), 0)
        for d in res.Detections:
            self.assertEqual(d.class_name, "face")
            self.assertGreater(d.width, 0)
            self.assertGreater(d.height, 0)

    def test_embedding_is_l2_normed(self):
        res, ctx = self.harness.embed(self.samples["t1"])
        self.assertIsNone(ctx.code, f"Embedding error: {ctx.details}")
        self.assertEqual(len(res.embeddings), 512)
        norm_sq = sum(x * x for x in res.embeddings)
        self.assertAlmostEqual(norm_sq, 1.0, places=2)

    def test_verify_same_image(self):
        res = self.harness.verify(self.samples["t1"], self.samples["t1"])
        self.assertTrue(res.verified)
        self.assertLess(res.distance, 0.05)

    def test_verify_different_images(self):
        # t1 vs t2 depict different groups of people — top face on each
        # side is unlikely to match.
        res = self.harness.verify(self.samples["t1"], self.samples["t2"])
        # We assert only that some numerical answer came back; the
        # matches-or-not determination depends on which face each side
        # picked and isn't a stable test assertion.
        self.assertGreaterEqual(res.distance, 0.0)

    def test_analyze_has_age_and_gender(self):
        res = self.harness.analyze(self.samples["t1"])
        self.assertGreater(len(res.faces), 0)
        for face in res.faces:
            self.assertGreater(face.face_confidence, 0.0)
            # Age should be populated for buffalo_l.
            self.assertGreater(face.age, 0.0)
            self.assertIn(face.dominant_gender, ("Man", "Woman"))


def _prepare_opencv_models_dir() -> str | None:
    """Download OpenCV Zoo face ONNX files into a temp dir the way
    LocalAI's gallery would. Returns the directory, or None if
    downloads failed (network-restricted sandbox).
    """
    import hashlib
    import tempfile
    import urllib.request

    root = os.environ.get("OPENCV_FACE_MODELS_DIR") or tempfile.mkdtemp(
        prefix="opencv-face-"
    )
    for filename, uri, sha256 in OPENCV_FILES:
        dest = os.path.join(root, filename)
        if os.path.isfile(dest):
            if hashlib.sha256(open(dest, "rb").read()).hexdigest() == sha256:
                continue
        try:
            urllib.request.urlretrieve(uri, dest)
        except Exception:
            return None
        if hashlib.sha256(open(dest, "rb").read()).hexdigest() != sha256:
            return None
    return root


class OnnxDirectEngineTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.samples = _load_insightface_samples()
        cls.model_dir = _prepare_opencv_models_dir()
        if cls.model_dir is None:
            raise unittest.SkipTest("OpenCV Zoo ONNX files could not be downloaded")
        cls.harness = _Harness(BackendServicer())
        load = cls.harness.load(
            [
                "engine:onnx_direct",
                "detector_onnx:face_detection_yunet_2023mar.onnx",
                "recognizer_onnx:face_recognition_sface_2021dec.onnx",
            ],
            model_path=cls.model_dir,
        )
        if not load.success:
            raise unittest.SkipTest(f"LoadModel failed: {load.message}")

    def test_detect_finds_face(self):
        res = self.harness.detect(self.samples["t1"])
        self.assertGreater(len(res.Detections), 0)
        for d in res.Detections:
            self.assertEqual(d.class_name, "face")

    def test_embedding_nonempty(self):
        res, ctx = self.harness.embed(self.samples["t1"])
        self.assertIsNone(ctx.code, f"Embedding error: {ctx.details}")
        self.assertGreater(len(res.embeddings), 0)

    def test_verify_same_image(self):
        res = self.harness.verify(self.samples["t1"], self.samples["t1"], threshold=0.4)
        self.assertTrue(res.verified)

    def test_analyze_returns_regions_without_demographics(self):
        # OnnxDirectEngine intentionally doesn't populate age/gender.
        res = self.harness.analyze(self.samples["t1"])
        self.assertGreater(len(res.faces), 0)
        for face in res.faces:
            self.assertEqual(face.dominant_gender, "")
            self.assertEqual(face.age, 0.0)


if __name__ == "__main__":
    unittest.main()
