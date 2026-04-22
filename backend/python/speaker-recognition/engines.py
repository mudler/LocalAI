"""Speaker-recognition engines.

Two engines are offered, mirroring the insightface backend's split:

  * SpeechBrainEngine: full PyTorch / SpeechBrain path. Uses the
    ECAPA-TDNN recipe trained on VoxCeleb; 192-d L2-normalized
    embeddings, cosine distance for verification. Auto-downloads the
    checkpoint into LocalAI's models directory on first LoadModel.

  * OnnxDirectEngine: CPU-friendly fallback that runs pre-exported
    ONNX speaker encoders (WeSpeaker ResNet34, 3D-Speaker ERes2Net,
    CAM++, etc.). Model paths come from the model config — the gallery
    `files:` flow drops them into the models directory.

Engine selection follows the same gallery-driven convention face
recognition uses (insightface commits 9c6da0f7 / 405fec0b): the
Python backend reads `engine` / `model_path` / `checkpoint` from the
options dict and picks an engine accordingly.
"""
from __future__ import annotations

import os
from typing import Any, Iterable, Protocol


class SpeakerEngine(Protocol):
    """Interface both concrete engines satisfy."""

    name: str

    def embed(self, audio_path: str) -> list[float]:  # pragma: no cover - interface
        ...

    def compare(self, audio1: str, audio2: str) -> float:  # pragma: no cover
        ...

    def analyze(self, audio_path: str, actions: Iterable[str]) -> list[dict[str, Any]]:  # pragma: no cover
        ...


def _cosine_distance(a, b) -> float:
    import numpy as np

    va = np.asarray(a, dtype=np.float32).reshape(-1)
    vb = np.asarray(b, dtype=np.float32).reshape(-1)
    na = float(np.linalg.norm(va))
    nb = float(np.linalg.norm(vb))
    if na == 0.0 or nb == 0.0:
        return 1.0
    return float(1.0 - np.dot(va, vb) / (na * nb))


class SpeechBrainEngine:
    """ECAPA-TDNN via SpeechBrain. Auto-downloads on first use."""

    name = "speechbrain-ecapa-tdnn"

    def __init__(self, model_name: str, options: dict[str, str]):
        # Late imports so the module can be introspected / tested
        # without torch / speechbrain being installed.
        from speechbrain.inference.speaker import EncoderClassifier  # type: ignore

        source = options.get("source") or model_name or "speechbrain/spkrec-ecapa-voxceleb"
        savedir = options.get("_model_path") or os.environ.get("HF_HOME") or "./pretrained_models"
        self._model = EncoderClassifier.from_hparams(source=source, savedir=savedir)

    def _load_waveform(self, path: str):
        # torchaudio is a SpeechBrain dep; keep it lazy so import works
        # without it being preloaded when the test suite introspects
        # this file.
        import torchaudio  # type: ignore

        waveform, sr = torchaudio.load(path)
        # Model expects 16kHz mono.
        if sr != 16000:
            waveform = torchaudio.functional.resample(waveform, sr, 16000)
        if waveform.shape[0] > 1:
            waveform = waveform.mean(dim=0, keepdim=True)
        return waveform

    def embed(self, audio_path: str) -> list[float]:
        waveform = self._load_waveform(audio_path)
        vec = self._model.encode_batch(waveform).squeeze().detach().cpu().numpy()
        return [float(x) for x in vec]

    def compare(self, audio1: str, audio2: str) -> float:
        return _cosine_distance(self.embed(audio1), self.embed(audio2))

    def analyze(self, audio_path: str, actions):
        # Optional age/gender/emotion head isn't part of ECAPA-TDNN.
        # Return UNIMPLEMENTED so the servicer maps it to a clean gRPC
        # status rather than a generic INTERNAL error.
        raise NotImplementedError("SpeechBrainEngine does not implement analyze")


class OnnxDirectEngine:
    """Run a pre-exported ONNX speaker encoder (WeSpeaker / 3D-Speaker)."""

    name = "onnx-direct"

    def __init__(self, model_name: str, options: dict[str, str]):
        import onnxruntime as ort  # type: ignore

        # The gallery is expected to have dropped the ONNX file under
        # the models directory; accept either an absolute path or a
        # filename relative to _model_path.
        onnx_path = options.get("model_path") or options.get("onnx")
        if not onnx_path:
            raise ValueError("OnnxDirectEngine requires `model_path: <file.onnx>` in options")
        if not os.path.isabs(onnx_path):
            onnx_path = os.path.join(options.get("_model_path", ""), onnx_path)
        if not os.path.isfile(onnx_path):
            raise FileNotFoundError(f"ONNX model not found: {onnx_path}")

        providers = options.get("providers")
        if providers:
            provider_list = [p.strip() for p in providers.split(",") if p.strip()]
        else:
            provider_list = ["CPUExecutionProvider"]
        self._session = ort.InferenceSession(onnx_path, providers=provider_list)
        self._input_name = self._session.get_inputs()[0].name
        self._expected_sr = int(options.get("sample_rate", "16000"))

    def _load_waveform(self, path: str):
        import numpy as np
        import soundfile as sf  # type: ignore

        audio, sr = sf.read(path, always_2d=False)
        if sr != self._expected_sr:
            # Cheap linear resample — good enough for sanity; callers
            # should pre-resample for production.
            ratio = self._expected_sr / float(sr)
            n = int(round(len(audio) * ratio))
            audio = np.interp(
                np.linspace(0, len(audio), n, endpoint=False),
                np.arange(len(audio)),
                audio,
            )
        if audio.ndim > 1:
            audio = audio.mean(axis=1)
        return audio.astype("float32")

    def embed(self, audio_path: str) -> list[float]:
        import numpy as np

        audio = self._load_waveform(audio_path)
        feed = audio.reshape(1, -1)
        out = self._session.run(None, {self._input_name: feed})
        vec = np.asarray(out[0]).reshape(-1)
        return [float(x) for x in vec]

    def compare(self, audio1: str, audio2: str) -> float:
        return _cosine_distance(self.embed(audio1), self.embed(audio2))

    def analyze(self, audio_path: str, actions):
        raise NotImplementedError("OnnxDirectEngine does not implement analyze")


def build_engine(model_name: str, options: dict[str, str]) -> tuple[SpeakerEngine, str]:
    """Pick an engine based on the options. ONNX path takes priority:
    if the gallery has dropped a `model_path:` or `onnx:` option, run
    the direct ONNX engine. Otherwise, fall back to SpeechBrain.
    """
    engine_kind = (options.get("engine") or "").lower()
    if engine_kind == "onnx" or options.get("model_path") or options.get("onnx"):
        return OnnxDirectEngine(model_name, options), OnnxDirectEngine.name
    return SpeechBrainEngine(model_name, options), SpeechBrainEngine.name
