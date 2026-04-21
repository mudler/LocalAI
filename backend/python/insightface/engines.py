"""Face recognition engine implementations for the LocalAI insightface backend.

Two engines are provided:

    * InsightFaceEngine  — wraps insightface.app.FaceAnalysis. Supports
                           buffalo_l / buffalo_s / antelopev2 model packs
                           with SCRFD detector + ArcFace recognizer +
                           genderage head. NON-COMMERCIAL research use
                           only (upstream license).

    * OnnxDirectEngine   — loads detector + recognizer ONNX files directly
                           via onnxruntime. Used for OpenCV Zoo models
                           (YuNet + SFace) and any future Apache-licensed
                           model set. Does not support analyze().

Both engines expose the same interface so the gRPC servicer (backend.py)
can dispatch without knowing which one is active.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Protocol

import cv2
import numpy as np


@dataclass
class FaceDetection:
    bbox: tuple[float, float, float, float]  # x1, y1, x2, y2
    score: float
    landmarks: np.ndarray | None = None      # 5x2 keypoints when available


@dataclass
class FaceAttributes:
    region: tuple[float, float, float, float]  # x, y, w, h
    face_confidence: float
    age: float | None = None
    dominant_gender: str | None = None
    gender: dict[str, float] = field(default_factory=dict)


class FaceEngine(Protocol):
    """Minimal interface every engine must implement."""

    def prepare(self, options: dict[str, str]) -> None: ...
    def detect(self, img: np.ndarray) -> list[FaceDetection]: ...
    def embed(self, img: np.ndarray) -> np.ndarray | None: ...
    def analyze(self, img: np.ndarray) -> list[FaceAttributes]: ...


# ─── InsightFaceEngine ────────────────────────────────────────────────

class InsightFaceEngine:
    # Default root where install.sh bakes the insightface model packs.
    # The `root` option can override it (e.g. for local dev with
    # pre-existing ~/.insightface downloads).
    DEFAULT_ROOT = "/models/insightface"

    def __init__(self) -> None:
        self.app: Any = None
        self.model_pack: str = "buffalo_l"
        self.det_size: tuple[int, int] = (640, 640)
        self.det_thresh: float = 0.5
        self._providers: list[str] = ["CPUExecutionProvider"]

    def prepare(self, options: dict[str, str]) -> None:
        import os

        from insightface.app import FaceAnalysis

        self.model_pack = options.get("model_pack", "buffalo_l")
        self.det_size = _parse_det_size(options.get("det_size", "640x640"))
        self.det_thresh = float(options.get("det_thresh", "0.5"))

        # Resolve the model root. Order:
        #   1. explicit `root:` option wins;
        #   2. LoadModel's `model_dir` (dirname of ModelOptions.ModelFile) —
        #      this is the LocalAI models directory, matching how every
        #      other gallery-managed model is stored. insightface's
        #      FaceAnalysis will auto-download packs to <root>/models/<name>/
        #      so they live alongside other LocalAI-managed models;
        #   3. <script_dir>/models/insightface — dev fallback;
        #   4. ~/.insightface — upstream's own default (last resort).
        root = options.get("root")
        if not root:
            model_dir = options.get("_model_dir")
            script_dir_root = os.path.join(
                os.path.dirname(os.path.abspath(__file__)), "models", "insightface"
            )
            for candidate in (model_dir, self.DEFAULT_ROOT, script_dir_root):
                if candidate and os.path.isdir(candidate):
                    root = candidate
                    break
            if not root:
                root = model_dir or "~/.insightface"

        # CUDAExecutionProvider is picked automatically by onnxruntime-gpu
        # when available; falling back to CPU keeps the CPU-only image
        # working.
        self._providers = ["CUDAExecutionProvider", "CPUExecutionProvider"]

        # Trigger the pack download first via ensure_available (not
        # FaceAnalysis) so we can normalize the extracted layout before
        # the router enumerates it. Upstream is inconsistent: buffalo_l
        # and buffalo_s zips expand flat (ONNX files at pack root);
        # buffalo_m and antelopev2 wrap their ONNX files in a redundant
        # <name>/ subdirectory, and insightface's own loader looks one
        # level too shallow.
        from insightface.utils import ensure_available

        expanded_root = os.path.expanduser(root)
        ensure_available("models", self.model_pack, root=expanded_root)
        _flatten_insightface_pack(expanded_root, self.model_pack)

        self.app = FaceAnalysis(name=self.model_pack, root=root, providers=self._providers)
        # ctx_id=0 selects the first available GPU; onnxruntime falls back to
        # CPU automatically when CUDAExecutionProvider isn't present.
        self.app.prepare(ctx_id=0, det_size=self.det_size, det_thresh=self.det_thresh)

    def detect(self, img: np.ndarray) -> list[FaceDetection]:
        if self.app is None:
            return []
        faces = self.app.get(img)
        return [
            FaceDetection(
                bbox=tuple(float(v) for v in f.bbox),
                score=float(f.det_score),
                landmarks=np.array(f.kps) if getattr(f, "kps", None) is not None else None,
            )
            for f in faces
        ]

    def embed(self, img: np.ndarray) -> np.ndarray | None:
        if self.app is None:
            return None
        faces = self.app.get(img)
        if not faces:
            return None
        # Pick the highest-confidence face.
        best = max(faces, key=lambda f: float(f.det_score))
        if getattr(best, "normed_embedding", None) is None:
            return None
        return np.asarray(best.normed_embedding, dtype=np.float32)

    def analyze(self, img: np.ndarray) -> list[FaceAttributes]:
        if self.app is None:
            return []
        out: list[FaceAttributes] = []
        for f in self.app.get(img):
            x1, y1, x2, y2 = (float(v) for v in f.bbox)
            region = (x1, y1, x2 - x1, y2 - y1)
            attrs = FaceAttributes(region=region, face_confidence=float(f.det_score))
            age = getattr(f, "age", None)
            if age is not None:
                attrs.age = float(age)
            gender = getattr(f, "gender", None)
            if gender is not None:
                # buffalo_l genderage head gives argmax, not probabilities —
                # emit a one-hot dict to keep the API stable.
                attrs.dominant_gender = "Man" if int(gender) == 1 else "Woman"
                attrs.gender = {
                    "Man": 1.0 if int(gender) == 1 else 0.0,
                    "Woman": 0.0 if int(gender) == 1 else 1.0,
                }
            out.append(attrs)
        return out


# ─── OnnxDirectEngine ─────────────────────────────────────────────────

class OnnxDirectEngine:
    """Loads detector + recognizer ONNX files directly.

    Supports the OpenCV Zoo YuNet + SFace pair out of the box. YuNet
    exposes a C++-level API via cv2.FaceDetectorYN which accepts the
    ONNX file directly; SFace is driven through cv2.FaceRecognizerSF.
    Both are Apache 2.0 licensed.
    """

    def __init__(self) -> None:
        self.detector_path: str = ""
        self.recognizer_path: str = ""
        self.input_size: tuple[int, int] = (320, 320)
        self.det_thresh: float = 0.5
        self._detector: Any = None
        self._recognizer: Any = None

    def prepare(self, options: dict[str, str]) -> None:
        raw_det = options.get("detector_onnx", "")
        raw_rec = options.get("recognizer_onnx", "")
        if not raw_det or not raw_rec:
            raise ValueError(
                "onnx_direct engine requires both detector_onnx and recognizer_onnx options"
            )
        model_dir = options.get("_model_dir")
        self.detector_path = _resolve_model_path(raw_det, model_dir=model_dir)
        self.recognizer_path = _resolve_model_path(raw_rec, model_dir=model_dir)
        self.input_size = _parse_det_size(options.get("det_size", "320x320"))
        self.det_thresh = float(options.get("det_thresh", "0.5"))

        # YuNet is a fixed-size detector; size is reset per detect() call to
        # match the input frame.
        self._detector = cv2.FaceDetectorYN.create(
            self.detector_path,
            "",
            self.input_size,
            score_threshold=self.det_thresh,
            nms_threshold=0.3,
            top_k=5000,
        )
        self._recognizer = cv2.FaceRecognizerSF.create(self.recognizer_path, "")

    def detect(self, img: np.ndarray) -> list[FaceDetection]:
        if self._detector is None:
            return []
        h, w = img.shape[:2]
        self._detector.setInputSize((w, h))
        retval, faces = self._detector.detect(img)
        if faces is None:
            return []
        out: list[FaceDetection] = []
        for row in faces:
            x, y, fw, fh = float(row[0]), float(row[1]), float(row[2]), float(row[3])
            # Landmarks at columns 4..13 are (lx1,ly1,...,lx5,ly5).
            landmarks = np.array(row[4:14], dtype=np.float32).reshape(5, 2) if len(row) >= 14 else None
            score = float(row[-1])
            out.append(FaceDetection(bbox=(x, y, x + fw, y + fh), score=score, landmarks=landmarks))
        return out

    def embed(self, img: np.ndarray) -> np.ndarray | None:
        if self._detector is None or self._recognizer is None:
            return None
        h, w = img.shape[:2]
        self._detector.setInputSize((w, h))
        retval, faces = self._detector.detect(img)
        if faces is None or len(faces) == 0:
            return None
        # Pick the highest-score face (last column is score).
        best = max(faces, key=lambda r: float(r[-1]))
        aligned = self._recognizer.alignCrop(img, best)
        feat = self._recognizer.feature(aligned)
        vec = np.asarray(feat, dtype=np.float32).flatten()
        # SFace outputs a 128-dim feature; L2-normalize to make dot-product
        # comparable to buffalo_l's already-normed 512-dim embedding.
        norm = float(np.linalg.norm(vec))
        if norm == 0:
            return None
        return vec / norm

    def analyze(self, img: np.ndarray) -> list[FaceAttributes]:
        # OpenCV Zoo does not ship a demographic classifier; report
        # only the face-detection regions so callers can still see
        # how many faces were detected.
        return [
            FaceAttributes(
                region=(
                    d.bbox[0],
                    d.bbox[1],
                    d.bbox[2] - d.bbox[0],
                    d.bbox[3] - d.bbox[1],
                ),
                face_confidence=d.score,
            )
            for d in self.detect(img)
        ]


# ─── helpers ──────────────────────────────────────────────────────────

def _parse_det_size(raw: str) -> tuple[int, int]:
    raw = raw.strip().lower().replace(" ", "")
    if "x" in raw:
        w, h = raw.split("x", 1)
        return (int(w), int(h))
    n = int(raw)
    return (n, n)


def _flatten_insightface_pack(root: str, name: str) -> None:
    """Work around upstream's inconsistent zip packaging.

    Some insightface packs (buffalo_l, buffalo_s, buffalo_sc) ship with
    ONNX files at the zip's top level, so extraction into
    `<root>/models/<name>/` places them directly where FaceAnalysis
    expects them. Others (buffalo_m, antelopev2) wrap their files in a
    redundant `<name>/` directory inside the zip, leaving
    `<root>/models/<name>/<name>/*.onnx` after extraction — which
    FaceAnalysis then can't find.

    Detect the nested layout and move the ONNX files up one level.
    """
    import os
    import shutil

    pack_dir = os.path.join(root, "models", name)
    if not os.path.isdir(pack_dir):
        return

    # If the pack dir already contains ONNX files, we're flat — done.
    if any(fn.endswith(".onnx") for fn in os.listdir(pack_dir)):
        return

    nested = os.path.join(pack_dir, name)
    if not os.path.isdir(nested):
        return
    if not any(fn.endswith(".onnx") for fn in os.listdir(nested)):
        return

    for fn in os.listdir(nested):
        src = os.path.join(nested, fn)
        dst = os.path.join(pack_dir, fn)
        if os.path.exists(dst):
            continue
        shutil.move(src, dst)
    try:
        os.rmdir(nested)
    except OSError:
        pass  # leave behind non-onnx leftovers if any


def _resolve_model_path(path: str, model_dir: str | None = None) -> str:
    """Resolve an ONNX file path across the paths LocalAI might deliver it from.

    Search order:
      1. The path itself if it already resolves (absolute, or relative to CWD).
      2. `model_dir` (typically `os.path.dirname(ModelOptions.ModelFile)`) —
         this is how LocalAI surfaces gallery-managed files. When the gallery
         entry lists `files:`, each one lands under the models directory and
         backends load them via filename anchored by ModelFile.
      3. `<script_dir>/<path-without-leading-slash>` — covers dev layouts
         where someone manually dropped weights inside the backend dir.

    If none hit, return the literal input so cv2/insightface surfaces a
    clearer error naming the actually-attempted path.
    """
    import os

    if os.path.isfile(path):
        return path
    stripped = path.lstrip("/")
    candidates: list[str] = []
    if model_dir:
        candidates.append(os.path.join(model_dir, os.path.basename(path)))
        candidates.append(os.path.join(model_dir, stripped))
    script_dir = os.path.dirname(os.path.abspath(__file__))
    candidates.append(os.path.join(script_dir, stripped))
    for c in candidates:
        if os.path.isfile(c):
            return c
    return path


def build_engine(name: str) -> FaceEngine:
    """Factory for the engine selected by LoadModel options."""
    key = name.strip().lower()
    if key in ("", "insightface"):
        return InsightFaceEngine()
    if key in ("onnx_direct", "onnx-direct", "opencv"):
        return OnnxDirectEngine()
    raise ValueError(f"unknown engine: {name!r}")
