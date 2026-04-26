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


class AnalysisHead:
    """Age / gender / emotion head, lazy-loaded on first analyze call.

    Wraps two open-licence HuggingFace checkpoints:

      * audeering/wav2vec2-large-robust-24-ft-age-gender — age
        regression (0–100 years) + 3-way gender (female/male/child).
        Apache 2.0.
      * superb/wav2vec2-base-superb-er — 4-way emotion classification
        (neutral / happy / angry / sad). Apache 2.0.

    Either model is optional — the head degrades gracefully to only the
    attributes it could load. Override the checkpoint with the
    `age_gender_model` / `emotion_model` option if you want something
    else. Set either to an empty string to disable that head.
    """

    # Age + gender is OFF by default: the high-accuracy Apache-2.0
    # checkpoint (Audeering wav2vec2-large-robust-24-ft-age-gender) uses a
    # custom multi-task head that AutoModelForAudioClassification silently
    # mangles — it drops the age weights as UNEXPECTED and re-initialises
    # the classifier head with random values, so the output is noise. Users
    # who have a cleanly loadable age/gender classifier can opt in with
    # `age_gender_model:<repo>` in options. The emotion default below
    # (superb/wav2vec2-base-superb-er) loads via the standard audio-
    # classification pipeline with no such caveat.
    DEFAULT_AGE_GENDER_MODEL = ""
    DEFAULT_EMOTION_MODEL = "superb/wav2vec2-base-superb-er"
    AGE_GENDER_LABELS = ("female", "male", "child")

    def __init__(self, options: dict[str, str]):
        self._options = options
        self._age_gender = None
        self._age_gender_processor = None
        self._age_gender_loaded = False
        self._age_gender_error: str | None = None
        self._emotion = None
        self._emotion_loaded = False
        self._emotion_error: str | None = None

    # --- age / gender -------------------------------------------------
    def _ensure_age_gender(self):
        if self._age_gender_loaded:
            return
        self._age_gender_loaded = True
        model_id = self._options.get(
            "age_gender_model", self.DEFAULT_AGE_GENDER_MODEL
        )
        if not model_id:
            self._age_gender_error = "disabled"
            return
        try:
            # Late imports — torch / transformers are heavy and only
            # pulled in when the analyze head actually runs.
            import torch  # type: ignore
            from transformers import AutoFeatureExtractor, AutoModelForAudioClassification  # type: ignore

            self._torch = torch
            self._age_gender_processor = AutoFeatureExtractor.from_pretrained(model_id)
            self._age_gender = AutoModelForAudioClassification.from_pretrained(model_id)
            self._age_gender.eval()
        except Exception as exc:  # noqa: BLE001
            self._age_gender_error = f"{type(exc).__name__}: {exc}"

    def _infer_age_gender(self, waveform_16k) -> dict[str, Any]:
        self._ensure_age_gender()
        if self._age_gender is None:
            return {}
        import numpy as np

        try:
            inputs = self._age_gender_processor(
                waveform_16k, sampling_rate=16000, return_tensors="pt"
            )
            with self._torch.no_grad():
                outputs = self._age_gender(**inputs)

            # Audeering's checkpoint is published with a custom head: the
            # official recipe exposes `(hidden_states, logits_age, logits_gender)`.
            # AutoModelForAudioClassification flattens that into a single
            # `logits` tensor of shape [batch, 4] — [age_regression, female, male, child].
            # Fall back gracefully when the shape is different (e.g. a
            # user-supplied age_gender_model checkpoint that returns a proper tuple).
            hidden = getattr(outputs, "logits", outputs)
            age_years = None
            gender_logits = None
            if isinstance(hidden, (tuple, list)) and len(hidden) >= 2:
                age_years = float(hidden[0].squeeze().item()) * 100.0
                gender_logits = hidden[1]
            else:
                flat = hidden.squeeze()
                if flat.ndim == 1 and flat.numel() >= 4:
                    age_years = float(flat[0].item()) * 100.0
                    gender_logits = flat[1:4]
                elif flat.ndim == 1 and flat.numel() == 1:
                    age_years = float(flat.item()) * 100.0

            if age_years is None and gender_logits is None:
                return {}

            result: dict[str, Any] = {}
            if age_years is not None:
                result["age"] = age_years
            if gender_logits is not None:
                probs = self._torch.softmax(gender_logits, dim=-1).cpu().numpy()
                probs = np.asarray(probs).reshape(-1)
                gender_map = {
                    label: float(probs[i])
                    for i, label in enumerate(self.AGE_GENDER_LABELS[: len(probs)])
                }
                result["gender"] = gender_map
                if gender_map:
                    dom = max(gender_map.items(), key=lambda kv: kv[1])[0]
                    result["dominant_gender"] = {
                        "female": "Female",
                        "male": "Male",
                        "child": "Child",
                    }.get(dom, dom.capitalize())
            return result
        except Exception as exc:  # noqa: BLE001
            # Analyze is a best-effort feature — never take down the
            # whole analyze call because the age/gender head had a bad
            # day. Mark the failure so the emotion branch still runs.
            self._age_gender_error = f"runtime: {type(exc).__name__}: {exc}"
            return {}

    # --- emotion ------------------------------------------------------
    def _ensure_emotion(self):
        if self._emotion_loaded:
            return
        self._emotion_loaded = True
        model_id = self._options.get("emotion_model", self.DEFAULT_EMOTION_MODEL)
        if not model_id:
            self._emotion_error = "disabled"
            return
        try:
            from transformers import pipeline  # type: ignore

            self._emotion = pipeline("audio-classification", model=model_id)
        except Exception as exc:  # noqa: BLE001
            self._emotion_error = f"{type(exc).__name__}: {exc}"

    def _infer_emotion(self, audio_path: str) -> dict[str, Any]:
        self._ensure_emotion()
        if self._emotion is None:
            return {}
        try:
            raw = self._emotion(audio_path, top_k=8)
        except Exception as exc:  # noqa: BLE001
            # Second-line defense: don't fail the whole analyze call
            # over a runtime inference hiccup.
            self._emotion_error = f"runtime: {type(exc).__name__}: {exc}"
            return {}
        emotion_map = {row["label"].lower(): float(row["score"]) for row in raw}
        if not emotion_map:
            return {}
        dom = max(emotion_map.items(), key=lambda kv: kv[1])[0]
        return {"emotion": emotion_map, "dominant_emotion": dom}

    # --- orchestrator -------------------------------------------------
    def analyze(self, audio_path: str, waveform_16k, actions: Iterable[str]) -> dict[str, Any]:
        wanted = {a.strip().lower() for a in actions} if actions else {"age", "gender", "emotion"}
        result: dict[str, Any] = {}
        if "age" in wanted or "gender" in wanted:
            ag = self._infer_age_gender(waveform_16k)
            if "age" in wanted and "age" in ag:
                result["age"] = ag["age"]
            if "gender" in wanted:
                if "gender" in ag:
                    result["gender"] = ag["gender"]
                if "dominant_gender" in ag:
                    result["dominant_gender"] = ag["dominant_gender"]
        if "emotion" in wanted:
            em = self._infer_emotion(audio_path)
            result.update(em)
        return result


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
        self._analysis = AnalysisHead(options)

    def _load_waveform(self, path: str):
        # Use soundfile + torch directly — torchaudio.load in torchaudio
        # 2.8+ requires the torchcodec package for decoding, which adds
        # another heavy ffmpeg-linked dep. soundfile covers WAV/FLAC
        # which is what we care about here.
        import numpy as np
        import soundfile as sf  # type: ignore
        import torch  # type: ignore

        audio, sr = sf.read(path, always_2d=False)
        if audio.ndim > 1:
            audio = audio.mean(axis=1)
        audio = np.asarray(audio, dtype=np.float32)
        if sr != 16000:
            # Simple linear resample — good enough for 16kHz downsampling
            # from 44.1/48kHz, and we expect 16kHz inputs in practice.
            ratio = 16000 / float(sr)
            n = int(round(len(audio) * ratio))
            audio = np.interp(
                np.linspace(0, len(audio), n, endpoint=False),
                np.arange(len(audio)),
                audio,
            ).astype(np.float32)
        return torch.from_numpy(audio).unsqueeze(0)  # [1, T]

    def embed(self, audio_path: str) -> list[float]:
        waveform = self._load_waveform(audio_path)
        vec = self._model.encode_batch(waveform).squeeze().detach().cpu().numpy()
        return [float(x) for x in vec]

    def compare(self, audio1: str, audio2: str) -> float:
        return _cosine_distance(self.embed(audio1), self.embed(audio2))

    def analyze(self, audio_path: str, actions):
        # Age / gender / emotion aren't produced by ECAPA-TDNN itself;
        # delegate to AnalysisHead which wraps separate Apache-2.0
        # checkpoints. Returns a single segment spanning the clip —
        # segmentation / diarisation is a future enhancement.
        waveform = self._load_waveform(audio_path)
        mono = waveform.squeeze().detach().cpu().numpy()
        attrs = self._analysis.analyze(audio_path, mono, actions)
        if not attrs:
            raise NotImplementedError(
                "analyze head failed to load — install transformers + torch or pass age_gender_model/emotion_model options"
            )
        duration = float(mono.shape[-1]) / 16000.0 if mono.size else 0.0
        return [dict(start=0.0, end=duration, **attrs)]


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
        input_meta = self._session.get_inputs()[0]
        self._input_name = input_meta.name
        # Pre-exported speaker encoders come in two shapes:
        #   rank-2  [batch, samples]          — some 3D-Speaker exports feed raw waveform.
        #   rank-3  [batch, frames, n_mels]   — WeSpeaker and most Kaldi-lineage encoders
        #                                        expect pre-computed Kaldi FBank features.
        # We detect this at load time and branch in embed(), because feeding raw audio
        # into a rank-3 graph is exactly what triggered
        # "Invalid rank for input: feats Got: 2 Expected: 3".
        self._input_rank = len(input_meta.shape) if input_meta.shape is not None else 2
        self._expected_sr = int(options.get("sample_rate", "16000"))
        self._fbank_mels = int(options.get("fbank_num_mel_bins", "80"))
        self._fbank_frame_length_ms = float(options.get("fbank_frame_length_ms", "25"))
        self._fbank_frame_shift_ms = float(options.get("fbank_frame_shift_ms", "10"))
        # Per-utterance cepstral mean normalisation — on for WeSpeaker by default,
        # toggleable for encoders that expect raw FBank.
        self._fbank_cmn = options.get("fbank_cmn", "true").lower() in ("1", "true", "yes")
        self._analysis = AnalysisHead(options)

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
        if self._input_rank >= 3:
            feats = self._extract_fbank(audio)        # [frames, n_mels]
            feed = feats[np.newaxis, :, :]             # [1, frames, n_mels]
        else:
            feed = audio.reshape(1, -1)                # [1, samples]
        out = self._session.run(None, {self._input_name: feed})
        vec = np.asarray(out[0]).reshape(-1)
        return [float(x) for x in vec]

    def _extract_fbank(self, audio):
        """Compute Kaldi-style 80-dim FBank features for speaker encoders that
        expect pre-featurised input (WeSpeaker, most 3D-Speaker exports).
        torchaudio is already a backend dependency for SpeechBrain — no new
        package required."""
        import numpy as np
        import torch  # type: ignore
        import torchaudio.compliance.kaldi as kaldi  # type: ignore

        tensor = torch.from_numpy(audio).unsqueeze(0)  # [1, samples]
        feats = kaldi.fbank(
            tensor,
            sample_frequency=self._expected_sr,
            num_mel_bins=self._fbank_mels,
            frame_length=self._fbank_frame_length_ms,
            frame_shift=self._fbank_frame_shift_ms,
            dither=0.0,
        )  # [frames, n_mels]
        if self._fbank_cmn:
            feats = feats - feats.mean(dim=0, keepdim=True)
        return feats.numpy().astype(np.float32)

    def compare(self, audio1: str, audio2: str) -> float:
        return _cosine_distance(self.embed(audio1), self.embed(audio2))

    def analyze(self, audio_path: str, actions):
        # AnalysisHead expects 16kHz mono; _load_waveform already
        # resamples to self._expected_sr. If the user configured a
        # non-16k expected rate, resample one more time for analyze.
        audio = self._load_waveform(audio_path)
        if self._expected_sr != 16000:
            import numpy as np

            ratio = 16000 / float(self._expected_sr)
            n = int(round(len(audio) * ratio))
            audio = np.interp(
                np.linspace(0, len(audio), n, endpoint=False),
                np.arange(len(audio)),
                audio,
            ).astype("float32")
        attrs = self._analysis.analyze(audio_path, audio, actions)
        if not attrs:
            raise NotImplementedError(
                "analyze head failed to load — install transformers + torch or pass age_gender_model/emotion_model options"
            )
        duration = float(len(audio)) / 16000.0 if len(audio) else 0.0
        return [dict(start=0.0, end=duration, **attrs)]


def build_engine(model_name: str, options: dict[str, str]) -> tuple[SpeakerEngine, str]:
    """Pick an engine based on the options. ONNX path takes priority:
    if the gallery has dropped a `model_path:` or `onnx:` option, run
    the direct ONNX engine. Otherwise, fall back to SpeechBrain.
    """
    engine_kind = (options.get("engine") or "").lower()
    if engine_kind == "onnx" or options.get("model_path") or options.get("onnx"):
        return OnnxDirectEngine(model_name, options), OnnxDirectEngine.name
    return SpeechBrainEngine(model_name, options), SpeechBrainEngine.name
