#!/usr/bin/env python3
"""gRPC server for the insightface face recognition backend.

Implements Health / LoadModel / Status plus the face-specific methods:
Embedding, Detect, FaceVerify, FaceAnalyze. The heavy lifting is
delegated to engines.py — this file is just the gRPC plumbing.
"""
import argparse
import base64
import os
import signal
import sys
import time
from concurrent import futures
from io import BytesIO

import backend_pb2
import backend_pb2_grpc
import cv2
import grpc
import numpy as np

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
from grpc_auth import get_auth_interceptors  # noqa: E402

from engines import FaceEngine, build_engine  # noqa: E402

_ONE_DAY = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))

# Default cosine-distance threshold for "same person" on buffalo_l
# ArcFace R50. Clients can override per-request; clients using SFace
# should pass threshold≈0.4 since the distance distribution is wider.
DEFAULT_VERIFY_THRESHOLD = 0.35


def _decode_image(src: str) -> np.ndarray | None:
    """Decode a base64-encoded image into an OpenCV BGR numpy array."""
    if not src:
        return None
    try:
        data = base64.b64decode(src, validate=False)
    except Exception:
        return None
    arr = np.frombuffer(data, dtype=np.uint8)
    if arr.size == 0:
        return None
    img = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    return img


def _parse_options(raw: list[str]) -> dict[str, str]:
    out: dict[str, str] = {}
    for entry in raw:
        if ":" not in entry:
            continue
        k, v = entry.split(":", 1)
        out[k.strip()] = v.strip()
    return out


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self) -> None:
        self.engine: FaceEngine | None = None
        self.engine_name: str = ""
        self.model_name: str = ""
        self.verify_threshold: float = DEFAULT_VERIFY_THRESHOLD

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", "utf-8"))

    def LoadModel(self, request, context):
        options = _parse_options(list(request.Options))
        # Surface LocalAI's models directory (ModelPath) so engines can
        # anchor relative paths — OnnxDirectEngine's detector_onnx /
        # recognizer_onnx point at gallery-managed files that LocalAI
        # dropped there, and InsightFaceEngine auto-downloads its packs
        # into that same directory alongside every other managed model.
        # Private key to avoid clashing with user-provided options.
        if request.ModelPath:
            options["_model_dir"] = request.ModelPath

        engine_name = options.get("engine", "insightface")
        try:
            self.engine = build_engine(engine_name)
            self.engine.prepare(options)
        except Exception as err:  # pragma: no cover - exercised via e2e
            return backend_pb2.Result(success=False, message=f"Failed to load face engine: {err}")

        self.engine_name = engine_name
        self.model_name = request.Model or options.get("model_pack", "")
        if "verify_threshold" in options:
            try:
                self.verify_threshold = float(options["verify_threshold"])
            except ValueError:
                pass
        print(f"[insightface] engine={engine_name} model={self.model_name} loaded", file=sys.stderr)
        return backend_pb2.Result(success=True, message="Model loaded successfully")

    def Status(self, request, context):
        state = (
            backend_pb2.StatusResponse.READY
            if self.engine is not None
            else backend_pb2.StatusResponse.UNINITIALIZED
        )
        return backend_pb2.StatusResponse(state=state)

    def Embedding(self, request, context):
        if self.engine is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("face model not loaded")
            return backend_pb2.EmbeddingResult()
        if not request.Images:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("Embedding requires Images[0] to be a base64 image")
            return backend_pb2.EmbeddingResult()

        img = _decode_image(request.Images[0])
        if img is None:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("failed to decode image")
            return backend_pb2.EmbeddingResult()

        vec = self.engine.embed(img)
        if vec is None:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details("no face detected")
            return backend_pb2.EmbeddingResult()
        return backend_pb2.EmbeddingResult(embeddings=[float(x) for x in vec])

    def Detect(self, request, context):
        if self.engine is None:
            return backend_pb2.DetectResponse()
        img = _decode_image(request.src)
        if img is None:
            return backend_pb2.DetectResponse()
        detections = []
        for d in self.engine.detect(img):
            x1, y1, x2, y2 = d.bbox
            detections.append(
                backend_pb2.Detection(
                    x=float(x1),
                    y=float(y1),
                    width=float(x2 - x1),
                    height=float(y2 - y1),
                    confidence=float(d.score),
                    class_name="face",
                )
            )
        return backend_pb2.DetectResponse(Detections=detections)

    def FaceVerify(self, request, context):
        if self.engine is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("face model not loaded")
            return backend_pb2.FaceVerifyResponse()

        img1 = _decode_image(request.img1)
        img2 = _decode_image(request.img2)
        if img1 is None or img2 is None:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("failed to decode one or both images")
            return backend_pb2.FaceVerifyResponse()

        threshold = request.threshold if request.threshold > 0 else self.verify_threshold

        start = time.time()
        e1 = self.engine.embed(img1)
        e2 = self.engine.embed(img2)
        if e1 is None or e2 is None:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details("no face detected in one or both images")
            return backend_pb2.FaceVerifyResponse()

        # Both engines return L2-normalized vectors, so the dot product
        # is the cosine similarity directly.
        sim = float(np.dot(e1, e2))
        distance = 1.0 - sim
        verified = distance < threshold
        confidence = max(0.0, min(100.0, (1.0 - distance / threshold) * 100.0)) if threshold > 0 else 0.0

        # Detect once per image — region is needed for the response and
        # potentially for the antispoof crop. Returns the highest-score face.
        def _best_detection(img):
            dets = self.engine.detect(img)
            if not dets:
                return None
            return max(dets, key=lambda d: d.score)

        def _region(det) -> backend_pb2.FacialArea:
            if det is None:
                return backend_pb2.FacialArea()
            x1, y1, x2, y2 = det.bbox
            return backend_pb2.FacialArea(x=x1, y=y1, w=x2 - x1, h=y2 - y1)

        det1 = _best_detection(img1)
        det2 = _best_detection(img2)

        img1_is_real = False
        img1_score = 0.0
        img2_is_real = False
        img2_score = 0.0
        if request.anti_spoofing:
            spoof1 = self.engine.antispoof(img1, det1.bbox) if det1 is not None else None
            spoof2 = self.engine.antispoof(img2, det2.bbox) if det2 is not None else None
            if spoof1 is None or spoof2 is None:
                context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
                context.set_details(
                    "anti_spoofing requested but no antispoof model is loaded — "
                    "install `silent-face-antispoofing` or pick a gallery entry "
                    "that bundles MiniFASNet weights"
                )
                return backend_pb2.FaceVerifyResponse()
            img1_is_real, img1_score = spoof1.is_real, spoof1.score
            img2_is_real, img2_score = spoof2.is_real, spoof2.score
            # Failed liveness vetoes verification regardless of similarity.
            if not (img1_is_real and img2_is_real):
                verified = False

        return backend_pb2.FaceVerifyResponse(
            verified=verified,
            distance=float(distance),
            threshold=float(threshold),
            confidence=float(confidence),
            model=self.model_name or self.engine_name,
            img1_area=_region(det1),
            img2_area=_region(det2),
            processing_time_ms=float((time.time() - start) * 1000.0),
            img1_is_real=img1_is_real,
            img1_antispoof_score=float(img1_score),
            img2_is_real=img2_is_real,
            img2_antispoof_score=float(img2_score),
        )

    def FaceAnalyze(self, request, context):
        if self.engine is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("face model not loaded")
            return backend_pb2.FaceAnalyzeResponse()
        img = _decode_image(request.img)
        if img is None:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("failed to decode image")
            return backend_pb2.FaceAnalyzeResponse()

        faces = []
        for attrs in self.engine.analyze(img):
            x, y, w, h = attrs.region
            fa = backend_pb2.FaceAnalysis(
                region=backend_pb2.FacialArea(x=float(x), y=float(y), w=float(w), h=float(h)),
                face_confidence=float(attrs.face_confidence),
            )
            if attrs.age is not None:
                fa.age = float(attrs.age)
            if attrs.dominant_gender:
                fa.dominant_gender = attrs.dominant_gender
            for k, v in attrs.gender.items():
                fa.gender[k] = float(v)
            if request.anti_spoofing:
                bbox = (float(x), float(y), float(x + w), float(y + h))
                spoof = self.engine.antispoof(img, bbox)
                if spoof is None:
                    context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
                    context.set_details(
                        "anti_spoofing requested but no antispoof model is loaded — "
                        "install `silent-face-antispoofing` or pick a gallery entry "
                        "that bundles MiniFASNet weights"
                    )
                    return backend_pb2.FaceAnalyzeResponse()
                fa.is_real = spoof.is_real
                fa.antispoof_score = float(spoof.score)
            faces.append(fa)
        return backend_pb2.FaceAnalyzeResponse(faces=faces)


def serve(address: str) -> None:
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 50 * 1024 * 1024),
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("[insightface] Server started. Listening on: " + address, file=sys.stderr)

    def _stop(sig, frame):  # pragma: no cover
        print("[insightface] shutting down")
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)

    try:
        while True:
            time.sleep(_ONE_DAY)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the insightface gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    args = parser.parse_args()
    print(f"[insightface] startup: {args}", file=sys.stderr)
    serve(args.addr)
