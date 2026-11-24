#!/usr/bin/env python3
"""Smoke-test every face recognition model configuration shipped in the
gallery. Simulates what LocalAI does at runtime: for each config, sets
up a models directory, fetches any required files via URL (as the
gallery's `files:` list would), then loads + detects + embeds via the
in-process BackendServicer — matching the gRPC surface end users hit.

Run inside the built backend image (venv already has insightface /
onnxruntime / opencv-python-headless):

    python smoke.py

Network is required for the insightface packs (fetched via upstream's
FaceAnalysis auto-download at first LoadModel) and for downloading
the OpenCV Zoo ONNX files on first run.
"""
from __future__ import annotations

import base64
import hashlib
import os
import sys
import traceback
import urllib.request

import cv2
import numpy as np

sys.path.insert(0, os.path.dirname(__file__))

import backend_pb2  # noqa: E402
from backend import BackendServicer  # noqa: E402


# Gallery `files:` for the OpenCV variants — same URIs + SHA-256s as
# gallery/index.yaml lists. Tuples: (filename, uri, sha256).
OPENCV_FILES = {
    "fp32": [
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
    ],
    "int8": [
        (
            "face_detection_yunet_2023mar_int8.onnx",
            "https://github.com/opencv/opencv_zoo/raw/main/models/face_detection_yunet/face_detection_yunet_2023mar_int8.onnx",
            "321aa5a6afabf7ecc46a3d06bfab2b579dc96eb5c3be7edd365fa04502ad9294",
        ),
        (
            "face_recognition_sface_2021dec_int8.onnx",
            "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec_int8.onnx",
            "2b0e941e6f16cc048c20aee0c8e31f569118f65d702914540f7bfdc14048d78a",
        ),
    ],
}


CONFIGS = [
    {
        "name": "insightface-buffalo-l",
        "options": ["engine:insightface", "model_pack:buffalo_l"],
        "has_analyze": True,
        "needs_opencv_files": None,
    },
    {
        "name": "insightface-buffalo-sc",
        "options": ["engine:insightface", "model_pack:buffalo_sc"],
        # buffalo_sc has recognizer only — no landmarks, no genderage.
        "has_analyze": False,
        "needs_opencv_files": None,
    },
    {
        "name": "insightface-buffalo-s",
        "options": ["engine:insightface", "model_pack:buffalo_s"],
        "has_analyze": True,
        "needs_opencv_files": None,
    },
    {
        "name": "insightface-buffalo-m",
        "options": ["engine:insightface", "model_pack:buffalo_m"],
        "has_analyze": True,
        "needs_opencv_files": None,
    },
    {
        "name": "insightface-antelopev2",
        "options": ["engine:insightface", "model_pack:antelopev2"],
        "has_analyze": True,
        "needs_opencv_files": None,
    },
    {
        "name": "insightface-opencv",
        "options": [
            "engine:onnx_direct",
            "detector_onnx:face_detection_yunet_2023mar.onnx",
            "recognizer_onnx:face_recognition_sface_2021dec.onnx",
        ],
        "has_analyze": False,
        "needs_opencv_files": "fp32",
    },
    {
        "name": "insightface-opencv-int8",
        "options": [
            "engine:onnx_direct",
            "detector_onnx:face_detection_yunet_2023mar_int8.onnx",
            "recognizer_onnx:face_recognition_sface_2021dec_int8.onnx",
        ],
        "has_analyze": False,
        "needs_opencv_files": "int8",
    },
]


class _FakeContext:
    def __init__(self) -> None:
        self.code = None
        self.details = None

    def set_code(self, code):
        self.code = code

    def set_details(self, details):
        self.details = details


def _encode_image(img: np.ndarray) -> str:
    _, buf = cv2.imencode(".jpg", img)
    return base64.b64encode(buf.tobytes()).decode("ascii")


def _load_sample_image() -> str:
    from insightface.data import get_image as ins_get_image

    return _encode_image(ins_get_image("t1"))


def _download_if_missing(model_dir: str, filename: str, uri: str, sha256: str) -> None:
    dest = os.path.join(model_dir, filename)
    if os.path.isfile(dest):
        h = hashlib.sha256(open(dest, "rb").read()).hexdigest()
        if h == sha256:
            return
    sys.stderr.write(f"  fetching {filename} from {uri}\n")
    sys.stderr.flush()
    urllib.request.urlretrieve(uri, dest)
    h = hashlib.sha256(open(dest, "rb").read()).hexdigest()
    if h != sha256:
        raise RuntimeError(f"sha256 mismatch for {filename}: want {sha256}, got {h}")


def _run_one(cfg: dict, img_b64: str, model_dir: str) -> tuple[bool, str]:
    # Mirror LocalAI's gallery flow: populate model_dir with the
    # gallery's listed files before calling LoadModel.
    if cfg["needs_opencv_files"]:
        for filename, uri, sha256 in OPENCV_FILES[cfg["needs_opencv_files"]]:
            _download_if_missing(model_dir, filename, uri, sha256)

    svc = BackendServicer()
    ctx = _FakeContext()

    load_res = svc.LoadModel(
        backend_pb2.ModelOptions(
            Model=cfg["name"],
            Options=cfg["options"],
            # ModelPath is what the Go loader sets to ml.ModelPath —
            # LocalAI's models directory. The backend anchors relative
            # paths and insightface auto-download root here.
            ModelPath=model_dir,
        ),
        ctx,
    )
    if not load_res.success:
        return False, f"LoadModel: {load_res.message}"

    det_res = svc.Detect(backend_pb2.DetectOptions(src=img_b64), _FakeContext())
    if len(det_res.Detections) == 0:
        return False, "Detect returned no faces"
    for d in det_res.Detections:
        if d.class_name != "face":
            return False, f"Detect returned class_name={d.class_name!r}"

    emb_ctx = _FakeContext()
    emb_res = svc.Embedding(backend_pb2.PredictOptions(Images=[img_b64]), emb_ctx)
    if emb_ctx.code is not None:
        return False, f"Embedding set error code {emb_ctx.code}: {emb_ctx.details}"
    if len(emb_res.embeddings) == 0:
        return False, "Embedding returned empty vector"
    norm_sq = sum(float(x) * float(x) for x in emb_res.embeddings)
    if not (0.8 <= norm_sq <= 1.2):
        return False, f"Embedding not L2-normed (sum(x^2)={norm_sq:.3f})"

    ver_ctx = _FakeContext()
    ver_res = svc.FaceVerify(
        backend_pb2.FaceVerifyRequest(img1=img_b64, img2=img_b64), ver_ctx
    )
    if ver_ctx.code is not None:
        return False, f"FaceVerify set error code {ver_ctx.code}: {ver_ctx.details}"
    if not ver_res.verified:
        return False, f"Same-image FaceVerify not verified (dist={ver_res.distance:.3f})"
    if ver_res.distance > 0.1:
        return False, f"Same-image distance suspiciously high ({ver_res.distance:.3f})"

    if cfg["has_analyze"]:
        an_ctx = _FakeContext()
        an_res = svc.FaceAnalyze(backend_pb2.FaceAnalyzeRequest(img=img_b64), an_ctx)
        if an_ctx.code is not None:
            return False, f"FaceAnalyze set error code {an_ctx.code}: {an_ctx.details}"
        if len(an_res.faces) == 0:
            return False, "FaceAnalyze returned no faces"
        f0 = an_res.faces[0]
        if f0.age <= 0:
            return False, f"FaceAnalyze age not populated (age={f0.age})"
        if f0.dominant_gender not in ("Man", "Woman"):
            return False, f"FaceAnalyze dominant_gender={f0.dominant_gender!r}"

    n_dets = len(det_res.Detections)
    dim = len(emb_res.embeddings)
    return True, f"faces={n_dets} dim={dim} same-dist={ver_res.distance:.3f}"


def main() -> int:
    # Honor LOCALAI_MODELS_PATH to re-use cached downloads across runs;
    # default to a fresh temp dir.
    model_dir = os.environ.get("LOCALAI_MODELS_PATH")
    if not model_dir:
        import tempfile

        model_dir = tempfile.mkdtemp(prefix="face-smoke-")
    os.makedirs(model_dir, exist_ok=True)
    print(f"model_dir={model_dir}", file=sys.stderr)

    print("Preparing sample image from insightface.data...", file=sys.stderr)
    img_b64 = _load_sample_image()

    results: list[tuple[str, bool, str]] = []
    for cfg in CONFIGS:
        sys.stderr.write(f"\n=== {cfg['name']} ===\n")
        sys.stderr.flush()
        try:
            ok, detail = _run_one(cfg, img_b64, model_dir)
        except Exception:
            ok, detail = False, traceback.format_exc().splitlines()[-1]
        results.append((cfg["name"], ok, detail))
        print(f"{'PASS' if ok else 'FAIL'}: {cfg['name']:30s}  {detail}")
        sys.stdout.flush()

    print("\n=== summary ===")
    passed = sum(1 for _, ok, _ in results if ok)
    total = len(results)
    for name, ok, detail in results:
        mark = "✓" if ok else "✗"
        print(f"  {mark} {name:30s} {detail}")
    print(f"\n{passed}/{total} passed")
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
