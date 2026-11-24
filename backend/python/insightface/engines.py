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


@dataclass
class SpoofResult:
    is_real: bool
    score: float  # averaged probability of the "real" class, 0.0-1.0


class FaceEngine(Protocol):
    """Minimal interface every engine must implement."""

    def prepare(self, options: dict[str, str]) -> None: ...
    def detect(self, img: np.ndarray) -> list[FaceDetection]: ...
    def embed(self, img: np.ndarray) -> np.ndarray | None: ...
    def analyze(self, img: np.ndarray) -> list[FaceAttributes]: ...
    # Optional: returns None when no antispoof model is loaded.
    def antispoof(self, img: np.ndarray, bbox: tuple[float, float, float, float]) -> SpoofResult | None: ...


# ─── Antispoofer (Silent-Face MiniFASNet) ──────────────────────────────

class Antispoofer:
    """Liveness detector using the Silent-Face MiniFASNet ensemble.

    Loads up to two ONNX exports (MiniFASNetV2 at scale 2.7 and
    MiniFASNetV1SE at scale 4.0). Both are 80x80 BGR-float32-input
    classifiers with 3 output logits where index 1 = "real". When both
    are loaded, softmax outputs are averaged before argmax — the same
    ensembling the upstream `test.py` does.

    Preprocessing matches yakhyo/face-anti-spoofing's reference impl:
    each model gets its own scale-expanded crop centered on the face
    bbox, resized to 80x80, fed straight as float32 BGR (no /255, no
    mean/std). See `_crop_face` for the bbox math.

    A single model also works (the missing one is simply skipped).
    """

    INPUT_SIZE = (80, 80)  # h, w
    REAL_CLASS_IDX = 1

    def __init__(self) -> None:
        self._sessions: list[tuple[Any, float, str, str]] = []  # (session, scale, input_name, output_name)
        self.threshold: float = 0.5

    def load(self, model_paths: list[tuple[str, float]], threshold: float = 0.5) -> None:
        """Load one or more (path, scale) pairs."""
        import onnxruntime as ort

        providers = ["CUDAExecutionProvider", "CPUExecutionProvider"]
        for path, scale in model_paths:
            session = ort.InferenceSession(path, providers=providers)
            input_name = session.get_inputs()[0].name
            output_name = session.get_outputs()[0].name
            self._sessions.append((session, float(scale), input_name, output_name))
        self.threshold = float(threshold)

    @property
    def loaded(self) -> bool:
        return bool(self._sessions)

    def _crop_face(self, img: np.ndarray, bbox: tuple[float, float, float, float], scale: float) -> np.ndarray:
        # bbox is (x1, y1, x2, y2) in source-image coordinates.
        src_h, src_w = img.shape[:2]
        x1, y1, x2, y2 = bbox
        box_w = max(1.0, x2 - x1)
        box_h = max(1.0, y2 - y1)

        # Clamp scale so the expanded crop fits inside the source image.
        scale = min((src_h - 1) / box_h, (src_w - 1) / box_w, scale)
        new_w = box_w * scale
        new_h = box_h * scale

        cx = x1 + box_w / 2.0
        cy = y1 + box_h / 2.0

        cx1 = max(0, int(cx - new_w / 2.0))
        cy1 = max(0, int(cy - new_h / 2.0))
        cx2 = min(src_w - 1, int(cx + new_w / 2.0))
        cy2 = min(src_h - 1, int(cy + new_h / 2.0))

        cropped = img[cy1 : cy2 + 1, cx1 : cx2 + 1]
        if cropped.size == 0:
            cropped = img
        out_h, out_w = self.INPUT_SIZE
        return cv2.resize(cropped, (out_w, out_h))

    @staticmethod
    def _softmax(x: np.ndarray) -> np.ndarray:
        e = np.exp(x - np.max(x, axis=1, keepdims=True))
        return e / e.sum(axis=1, keepdims=True)

    def predict(self, img: np.ndarray, bbox: tuple[float, float, float, float]) -> SpoofResult:
        if not self._sessions:
            raise RuntimeError("Antispoofer.predict called with no models loaded")
        accum = np.zeros((1, 3), dtype=np.float32)
        for session, scale, input_name, output_name in self._sessions:
            face = self._crop_face(img, bbox, scale).astype(np.float32)
            tensor = np.transpose(face, (2, 0, 1))[np.newaxis, ...]
            logits = session.run([output_name], {input_name: tensor})[0]
            accum += self._softmax(logits)
        accum /= float(len(self._sessions))
        real_prob = float(accum[0, self.REAL_CLASS_IDX])
        is_real = int(np.argmax(accum)) == self.REAL_CLASS_IDX and real_prob >= self.threshold
        return SpoofResult(is_real=is_real, score=real_prob)


def _build_antispoofer(options: dict[str, str], model_dir: str | None) -> Antispoofer | None:
    """Instantiate an Antispoofer from option keys, or return None.

    Recognised options:
        antispoof_v2_onnx     — path/filename of MiniFASNetV2 (scale 2.7)
        antispoof_v1se_onnx   — path/filename of MiniFASNetV1SE (scale 4.0)
        antispoof_threshold   — real-class probability threshold, default 0.5

    Either or both can be provided. Returns None when neither is set.
    """
    pairs: list[tuple[str, float]] = []
    v2 = options.get("antispoof_v2_onnx", "")
    if v2:
        pairs.append((_resolve_model_path(v2, model_dir=model_dir), 2.7))
    v1se = options.get("antispoof_v1se_onnx", "")
    if v1se:
        pairs.append((_resolve_model_path(v1se, model_dir=model_dir), 4.0))
    if not pairs:
        return None
    threshold = float(options.get("antispoof_threshold", "0.5"))
    spoofer = Antispoofer()
    spoofer.load(pairs, threshold=threshold)
    return spoofer


# ─── InsightFaceEngine ────────────────────────────────────────────────

# Canonical ONNX manifest for each upstream insightface pack (v0.7 release
# at github.com/deepinsight/insightface/releases). LocalAI's gallery extracts
# these zips flat into the models directory, so when multiple packs or other
# backends drop their own ONNX files alongside, the glob-the-directory
# approach picks up foreign files and insightface's model_zoo.get_model()
# raises IndexError trying to index `input_shape[2]` on a tensor that isn't
# shaped like a face model. The manifest lets us pre-filter to only the
# files that actually belong to the requested pack — deterministic, correct
# pack choice, no crashes on neighbour ONNX files.
_KNOWN_PACK_MANIFESTS: dict[str, frozenset[str]] = {
    "buffalo_l": frozenset({
        "det_10g.onnx",
        "w600k_r50.onnx",
        "genderage.onnx",
        "2d106det.onnx",
        "1k3d68.onnx",
    }),
    "buffalo_sc": frozenset({
        "det_500m.onnx",
        "w600k_mbf.onnx",
    }),
}


class InsightFaceEngine:
    """Drives insightface's model_zoo directly — no FaceAnalysis wrapper.

    FaceAnalysis is a thin 50-line orchestration (glob for ONNX files
    in `<root>/models/<name>/`, route each through `model_zoo.get_model`,
    build a `{taskname: model}` dict, then loop per-face at inference).
    We reimplement the same loop here so we can:

      1. Load packs from whatever directory LocalAI's gallery extracted
         them into — flat (buffalo_l/s/sc — ONNX at `<dir>/*.onnx`) or
         nested (buffalo_m/antelopev2 — ONNX at `<dir>/<name>/*.onnx`)
         without needing a specific layout on disk.
      2. Skip insightface's built-in auto-download entirely: weight
         delivery is LocalAI's gallery `files:` job now, checksum-
         verified and cached alongside every other managed model.

    The actual inference classes (RetinaFace, ArcFaceONNX, Attribute,
    Landmark) stay in insightface — we only reimplement the ~50 lines
    of glue around them.
    """

    def __init__(self) -> None:
        self.models: dict[str, Any] = {}
        self.det_model: Any = None
        self.model_pack: str = "buffalo_l"
        self.det_size: tuple[int, int] = (640, 640)
        self.det_thresh: float = 0.5
        self._providers: list[str] = ["CPUExecutionProvider"]
        self._antispoofer: Antispoofer | None = None

    def prepare(self, options: dict[str, str]) -> None:
        import glob
        import os

        from insightface.model_zoo import model_zoo

        self.model_pack = options.get("model_pack", "buffalo_l")
        self.det_size = _parse_det_size(options.get("det_size", "640x640"))
        self.det_thresh = float(options.get("det_thresh", "0.5"))
        self._antispoofer = _build_antispoofer(options, options.get("_model_dir"))

        pack_dir = _locate_insightface_pack(options, self.model_pack)
        if pack_dir is None:
            raise ValueError(
                f"no insightface pack '{self.model_pack}' found — install via "
                f"`local-ai models install insightface-{self.model_pack.replace('_', '-')}`"
            )

        onnx_files = sorted(glob.glob(os.path.join(pack_dir, "*.onnx")))
        # When the pack extracts flat into a shared models directory it
        # mixes with ONNX files from other backends (opencv face engine,
        # MiniFASNet antispoof, WeSpeaker voice embedding, other buffalo
        # packs installed earlier). Feeding those into model_zoo.get_model()
        # blows up inside insightface's router — it assumes a 4-D NCHW
        # input and indexes `input_shape[2]` on tensors that aren't shaped
        # like a face model, raising IndexError. For the upstream packs we
        # know the exact ONNX manifest; scoping to it makes the load
        # deterministic (without it, det_10g.onnx from buffalo_l sorts
        # before det_500m.onnx from buffalo_sc and silently wins).
        manifest = _KNOWN_PACK_MANIFESTS.get(self.model_pack)
        if manifest is not None:
            scoped = [f for f in onnx_files if os.path.basename(f) in manifest]
            if scoped:
                onnx_files = scoped
        if not onnx_files:
            raise ValueError(f"no ONNX files in pack directory: {pack_dir}")

        # CUDAExecutionProvider is picked automatically by onnxruntime-gpu
        # when available; falling back to CPU keeps the CPU-only image
        # working. ctx_id=0 means "first GPU if any, else CPU".
        self._providers = ["CUDAExecutionProvider", "CPUExecutionProvider"]

        self.models = {}
        skipped: list[tuple[str, str]] = []
        for onnx_file in onnx_files:
            try:
                m = model_zoo.get_model(onnx_file, providers=self._providers)
            except Exception as err:
                # Foreign ONNX (wrong rank/shape, non-insightface model) —
                # older insightface versions raise IndexError / ValueError
                # instead of returning None. Keep loading the rest.
                skipped.append((os.path.basename(onnx_file), str(err)))
                continue
            if m is None:
                skipped.append((os.path.basename(onnx_file), "unknown taskname"))
                continue
            # First occurrence of each taskname wins (matches FaceAnalysis).
            if m.taskname not in self.models:
                self.models[m.taskname] = m

        if skipped:
            import sys
            print(
                f"[insightface] skipped {len(skipped)} non-pack ONNX file(s) in {pack_dir}: "
                + ", ".join(f"{n} ({why})" for n, why in skipped),
                file=sys.stderr,
            )

        if "detection" not in self.models:
            raise ValueError(f"no detector (taskname='detection') found in {pack_dir}")
        self.det_model = self.models["detection"]

        self.det_model.prepare(0, input_size=self.det_size, det_thresh=self.det_thresh)
        for name, m in self.models.items():
            if name != "detection":
                m.prepare(0)

    def _faces(self, img: np.ndarray) -> list[Any]:
        """Run detection + all non-detection models per face."""
        if self.det_model is None:
            return []
        from insightface.app.common import Face

        bboxes, kpss = self.det_model.detect(img, max_num=0)
        if bboxes is None or bboxes.shape[0] == 0:
            return []
        faces: list[Any] = []
        for i in range(bboxes.shape[0]):
            bbox = bboxes[i, 0:4]
            det_score = bboxes[i, 4]
            kps = kpss[i] if kpss is not None else None
            face = Face(bbox=bbox, kps=kps, det_score=det_score)
            for name, m in self.models.items():
                if name == "detection":
                    continue
                m.get(img, face)
            faces.append(face)
        return faces

    def detect(self, img: np.ndarray) -> list[FaceDetection]:
        return [
            FaceDetection(
                bbox=tuple(float(v) for v in f.bbox),
                score=float(f.det_score),
                landmarks=np.array(f.kps) if getattr(f, "kps", None) is not None else None,
            )
            for f in self._faces(img)
        ]

    def embed(self, img: np.ndarray) -> np.ndarray | None:
        faces = self._faces(img)
        if not faces:
            return None
        best = max(faces, key=lambda f: float(f.det_score))
        if getattr(best, "normed_embedding", None) is None:
            return None
        return np.asarray(best.normed_embedding, dtype=np.float32)

    def analyze(self, img: np.ndarray) -> list[FaceAttributes]:
        out: list[FaceAttributes] = []
        for f in self._faces(img):
            x1, y1, x2, y2 = (float(v) for v in f.bbox)
            region = (x1, y1, x2 - x1, y2 - y1)
            attrs = FaceAttributes(region=region, face_confidence=float(f.det_score))
            age = getattr(f, "age", None)
            if age is not None:
                attrs.age = float(age)
            gender = getattr(f, "gender", None)
            if gender is not None:
                # genderage head emits argmax, not probabilities —
                # one-hot dict keeps the API stable.
                attrs.dominant_gender = "Man" if int(gender) == 1 else "Woman"
                attrs.gender = {
                    "Man": 1.0 if int(gender) == 1 else 0.0,
                    "Woman": 0.0 if int(gender) == 1 else 1.0,
                }
            out.append(attrs)
        return out

    def antispoof(self, img: np.ndarray, bbox: tuple[float, float, float, float]) -> SpoofResult | None:
        if self._antispoofer is None or not self._antispoofer.loaded:
            return None
        return self._antispoofer.predict(img, bbox)


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
        self._antispoofer: Antispoofer | None = None

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
        self._antispoofer = _build_antispoofer(options, model_dir)

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

    def antispoof(self, img: np.ndarray, bbox: tuple[float, float, float, float]) -> SpoofResult | None:
        if self._antispoofer is None or not self._antispoofer.loaded:
            return None
        return self._antispoofer.predict(img, bbox)


# ─── helpers ──────────────────────────────────────────────────────────

def _parse_det_size(raw: str) -> tuple[int, int]:
    raw = raw.strip().lower().replace(" ", "")
    if "x" in raw:
        w, h = raw.split("x", 1)
        return (int(w), int(h))
    n = int(raw)
    return (n, n)


def _locate_insightface_pack(options: dict[str, str], name: str) -> str | None:
    """Find the directory holding the insightface pack's ONNX files.

    LocalAI's gallery `files:` extracts the pack zip straight into the
    models directory. Upstream packs are inconsistent:

      buffalo_l/s/sc  — flat zip, ONNX lands at `<models_dir>/*.onnx`
      buffalo_m, antelopev2  — wrapped zip, ONNX lands at `<models_dir>/<name>/*.onnx`

    We search, in order:
      1. `<models_dir>/<name>/`  — wrapped-zip layout, or insightface's
         own FaceAnalysis-style `<root>/models/<name>/` layout.
      2. `<models_dir>/models/<name>/`  — insightface's FaceAnalysis
         auto-download lands here (handy for dev environments that
         still have old `~/.insightface` caches).
      3. `<models_dir>/`  — flat-zip layout directly in models dir.

    Returns the first directory whose contents include `*.onnx`.
    """
    import glob
    import os

    model_dir = options.get("_model_dir") or ""
    explicit_root = options.get("root")

    candidates: list[str] = []
    if model_dir:
        candidates.append(os.path.join(model_dir, name))
        candidates.append(os.path.join(model_dir, "models", name))
        candidates.append(model_dir)
    if explicit_root:
        expanded = os.path.expanduser(explicit_root)
        candidates.append(os.path.join(expanded, "models", name))
        candidates.append(os.path.join(expanded, name))
        candidates.append(expanded)

    for c in candidates:
        if os.path.isdir(c) and glob.glob(os.path.join(c, "*.onnx")):
            return c
    return None


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
