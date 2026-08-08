# SPDX-License-Identifier: MIT
"""Role-aware MLX-Audio runtime without eager MLX/model imports."""

from __future__ import annotations

import gc
import importlib.metadata
import math
import os
from pathlib import Path
import struct
import tempfile
import threading
import wave


ALLOWED_ROLES = frozenset({"vad", "asr", "tts"})
FORBIDDEN_DISTRIBUTIONS = frozenset({"torch", "torchaudio"})
VAD_SAMPLE_RATE = 16000
TTS_SAMPLE_RATE = 24000
TTS_STREAMING_INTERVALS = frozenset({0.16, 0.32, 0.64})
SUPPORTED_LANGUAGES = {
    "en": "English",
    "en-us": "English",
    "en-gb": "English",
    "english": "English",
    "ko": "Korean",
    "ko-kr": "Korean",
    "korean": "Korean",
    "ja": "Japanese",
    "ja-jp": "Japanese",
    "japanese": "Japanese",
}
TTS_VOICES = {
    "ryan": "Ryan",
    "sohee": "Sohee",
    "ono_anna": "Ono_Anna",
}
TTS_DEFAULTS = {
    "temperature": 0.7,
    "max_tokens": 1200,
    "top_k": 50,
    "top_p": 1.0,
    "repetition_penalty": 1.05,
    "stream": True,
    "streaming_interval": 0.32,
}


class BackendFailure(Exception):
    """Failure carrying the gRPC status category the servicer must expose."""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message


def parse_options(options):
    parsed = {}
    for option in options:
        key, separator, value = option.partition(":")
        if separator:
            parsed[key.strip()] = value.strip()
    return parsed


def configured_role(options):
    role = parse_options(options).get("role", "").lower()
    if not role:
        raise BackendFailure(
            "INVALID_ARGUMENT",
            "mlx-audio requires an explicit role option: role:vad, role:asr, or role:tts",
        )
    if role not in ALLOWED_ROLES:
        raise BackendFailure("INVALID_ARGUMENT", f"unsupported mlx-audio role: {role}")
    return role


def forbidden_distributions(distributions=None):
    installed = distributions
    if installed is None:
        installed = (dist.metadata.get("Name", "") for dist in importlib.metadata.distributions())
    normalized = {name.lower().replace("_", "-") for name in installed if name}
    return sorted(normalized & FORBIDDEN_DISTRIBUTIONS)


def ensure_torch_free(distributions=None):
    forbidden = forbidden_distributions(distributions)
    if forbidden:
        raise BackendFailure(
            "FAILED_PRECONDITION",
            "forbidden serving dependencies installed: " + ", ".join(forbidden),
        )


def _local_snapshot_path(model_path, model_root=None):
    path = Path(model_path)
    root = None
    if model_root:
        try:
            root = Path(model_root).resolve(strict=True)
        except OSError as err:
            raise BackendFailure("NOT_FOUND", "mlx-audio model root does not exist") from err
        if not root.is_dir():
            raise BackendFailure("INVALID_ARGUMENT", "mlx-audio model root must be a directory")
        if not path.is_absolute():
            path = root / path
    elif not path.is_absolute():
        raise BackendFailure(
            "INVALID_ARGUMENT",
            "mlx-audio model must be absolute when the controller does not provide a model root",
        )
    try:
        resolved = path.resolve(strict=True)
    except OSError as err:
        raise BackendFailure("NOT_FOUND", "mlx-audio model snapshot does not exist") from err
    if root is not None:
        try:
            resolved.relative_to(root)
        except ValueError as err:
            raise BackendFailure(
                "INVALID_ARGUMENT",
                "mlx-audio model snapshot escapes the configured model root",
            ) from err
    if not resolved.is_dir():
        raise BackendFailure("INVALID_ARGUMENT", "mlx-audio model snapshot must be a directory")
    return str(resolved)


def _production_loader(role):
    if role == "vad":
        from mlx_audio.vad import load
    elif role == "asr":
        from mlx_audio.stt import load
    else:
        from mlx_audio.tts import load
    return load


def _production_writer(path, audio_segments, sample_rate):
    import numpy as np
    from mlx_audio.audio_io import write

    audio = np.concatenate([np.asarray(segment).reshape(-1) for segment in audio_segments])
    write(path, audio, sample_rate, format="wav")


def _production_seed(seed):
    import mlx.core as mx

    mx.random.seed(seed)


def _write_vad_wav(path, audio):
    pcm = bytearray()
    for sample in audio:
        if not isinstance(sample, (int, float)) or not math.isfinite(sample):
            raise BackendFailure("INVALID_ARGUMENT", "VAD audio must contain finite float samples")
        value = max(-1.0, min(1.0, float(sample)))
        pcm.extend(struct.pack("<h", int(round(value * 32767.0))))
    with wave.open(path, "wb") as output:
        output.setnchannels(1)
        output.setsampwidth(2)
        output.setframerate(VAD_SAMPLE_RATE)
        output.writeframes(pcm)


def _validate_wav(path):
    try:
        with wave.open(path, "rb") as audio:
            channels = audio.getnchannels()
            sample_rate = audio.getframerate()
            width = audio.getsampwidth()
            frames = audio.readframes(audio.getnframes())
    except (EOFError, OSError, wave.Error) as err:
        raise BackendFailure("INTERNAL", "MLX-Audio TTS produced an invalid WAV") from err
    if channels != 1 or sample_rate <= 0 or width not in (1, 2, 3, 4):
        raise BackendFailure("INTERNAL", "MLX-Audio TTS produced unsupported WAV audio")
    if not frames:
        raise BackendFailure("INTERNAL", "MLX-Audio TTS produced empty audio")
    peak = max(
        abs(int.from_bytes(frames[index:index + width], "little", signed=True))
        for index in range(0, len(frames) - width + 1, width)
    )
    if peak == 0:
        raise BackendFailure("INTERNAL", "MLX-Audio TTS produced silent audio")


def normalize_language(language):
    value = (language or "English").strip().lower().replace("_", "-")
    selected = SUPPORTED_LANGUAGES.get(value)
    if selected is None:
        raise BackendFailure(
            "INVALID_ARGUMENT",
            "mlx-audio language must be English, Korean, or Japanese",
        )
    return selected


def normalize_voice(voice, _language):
    value = (voice or "").strip()
    if not value:
        return "Ryan"
    selected = TTS_VOICES.get(value.lower())
    if selected is None:
        raise BackendFailure(
            "INVALID_ARGUMENT",
            "mlx-audio voice must be Ryan, Sohee, or Ono_Anna",
        )
    return selected


def tts_generation_options(params=None, configured=None):
    options = dict(TTS_DEFAULTS)
    values = dict(configured or {})
    values.update(params or {})
    parsers = {
        "temperature": float,
        "max_tokens": int,
        "top_k": int,
        "top_p": float,
        "repetition_penalty": float,
        "streaming_interval": float,
    }
    for name, parser in parsers.items():
        if name not in values:
            continue
        try:
            options[name] = parser(values[name])
        except (TypeError, ValueError) as err:
            raise BackendFailure("INVALID_ARGUMENT", f"invalid MLX-Audio TTS parameter: {name}") from err
    if not 0 < options["temperature"] <= 2:
        raise BackendFailure("INVALID_ARGUMENT", "MLX-Audio TTS temperature must be in (0, 2]")
    if not 1 <= options["max_tokens"] <= TTS_DEFAULTS["max_tokens"]:
        raise BackendFailure("INVALID_ARGUMENT", "MLX-Audio TTS max_tokens must be from 1 through 1200")
    if options["top_k"] < 1:
        raise BackendFailure("INVALID_ARGUMENT", "MLX-Audio TTS top_k must be positive")
    if not 0 < options["top_p"] <= 1:
        raise BackendFailure("INVALID_ARGUMENT", "MLX-Audio TTS top_p must be in (0, 1]")
    if options["repetition_penalty"] <= 0:
        raise BackendFailure("INVALID_ARGUMENT", "MLX-Audio TTS repetition_penalty must be positive")
    if options["streaming_interval"] not in TTS_STREAMING_INTERVALS:
        raise BackendFailure(
            "INVALID_ARGUMENT",
            "MLX-Audio TTS streaming_interval must be 0.16, 0.32, or 0.64 seconds",
        )
    diagnostic_seed = values.get("diagnostic_seed", values.get("seed"))
    if diagnostic_seed is not None:
        try:
            options["seed"] = int(diagnostic_seed)
        except (TypeError, ValueError) as err:
            raise BackendFailure("INVALID_ARGUMENT", "invalid MLX-Audio TTS parameter: diagnostic_seed") from err
    return options


def pcm16le_from_tts_segment(segment):
    """Materialize a generated segment as one nonempty mono PCM16LE frame."""
    try:
        import numpy as np

        sample_rate = int(getattr(segment, "sample_rate", 0))
        if sample_rate != TTS_SAMPLE_RATE:
            raise BackendFailure("INTERNAL", "MLX-Audio TTS returned an unsupported sample rate")
        audio = getattr(segment, "audio", None)
        if audio is None:
            raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
        samples = np.array(audio, copy=True).reshape(-1)
        if samples.size == 0 or not np.isfinite(samples).all():
            raise BackendFailure("INTERNAL", "MLX-Audio TTS returned invalid audio")
        pcm = (np.clip(samples, -1.0, 1.0) * 32767.0).astype(np.dtype("<i2"), copy=True)
        data = pcm.tobytes()
    except BackendFailure:
        raise
    except Exception as err:
        raise BackendFailure("INTERNAL", "MLX-Audio TTS returned malformed audio") from err
    if not data or len(data) % 2:
        raise BackendFailure("INTERNAL", "MLX-Audio TTS returned invalid PCM audio")
    return data


def request_cancelled(context):
    """Support both grpc.aio and synchronous migration-thread contexts."""
    cancelled = getattr(context, "cancelled", None)
    if callable(cancelled):
        return bool(cancelled())
    is_active = getattr(context, "is_active", None)
    if callable(is_active):
        return not bool(is_active())
    return False


class MLXAudioRuntime:
    def __init__(
        self,
        loader_provider=None,
        writer=None,
        temp_dir=None,
        cache_clear=None,
        seed_setter=None,
    ):
        self._loader_provider = loader_provider or _production_loader
        self._writer = writer or _production_writer
        self._temp_dir = temp_dir
        self._cache_clear = cache_clear
        self._seed_setter = seed_setter or _production_seed
        self.model = None
        self.model_path = None
        self.role = None
        self._state_lock = threading.Lock()
        self._tts_config = {}

    def load(self, model_path, options, distributions=None, model_root=None):
        with self._state_lock:
            ensure_torch_free(distributions)
            role = configured_role(options)
            local_path = _local_snapshot_path(model_path, model_root)
            tts_config = tts_generation_options(configured=parse_options(options)) if role == "tts" else {}
            try:
                model = self._loader_provider(role)(local_path, strict=True)
            except BackendFailure:
                raise
            except Exception as err:
                raise BackendFailure("INTERNAL", f"failed to load mlx-audio {role} model") from err
            if model is None:
                raise BackendFailure("INTERNAL", f"mlx-audio {role} loader returned no model")
            self._unload_locked()
            self.model = model
            self.model_path = local_path
            self.role = role
            self._tts_config = tts_config
            return role

    def _require_role(self, role):
        if self.model is None:
            raise BackendFailure("FAILED_PRECONDITION", "mlx-audio model is not loaded")
        if self.role != role:
            raise BackendFailure("FAILED_PRECONDITION", f"loaded mlx-audio role is {self.role}, not {role}")

    def _tts_options(self, params):
        options = tts_generation_options(params, configured=self._tts_config)
        seed = options.pop("seed", None)
        if seed is not None:
            try:
                self._seed_setter(seed)
            except Exception as err:
                raise BackendFailure("INTERNAL", "failed to seed MLX-Audio TTS generation") from err
        return options

    def vad(self, audio):
        with self._state_lock:
            self._require_role("vad")
            if not audio:
                raise BackendFailure("INVALID_ARGUMENT", "VAD audio is empty")
            path = None
            try:
                handle = tempfile.NamedTemporaryFile(suffix=".wav", dir=self._temp_dir, delete=False)
                path = handle.name
                handle.close()
                _write_vad_wav(path, audio)
                raw_segments = self.model.get_speech_timestamps(path, return_seconds=True)
            except BackendFailure:
                raise
            except Exception as err:
                raise BackendFailure("INTERNAL", "MLX-Audio VAD inference failed") from err
            finally:
                if path is not None:
                    try:
                        os.remove(path)
                    except OSError:
                        pass

        if raw_segments is None:
            return []
        if not hasattr(raw_segments, "__iter__"):
            raise BackendFailure("INTERNAL", "MLX-Audio VAD returned malformed output")
        segments = []
        for segment in raw_segments or []:
            try:
                start = float(segment["start"])
                end = float(segment["end"])
            except (KeyError, TypeError, ValueError) as err:
                raise BackendFailure("INTERNAL", "MLX-Audio VAD returned a malformed segment") from err
            if not math.isfinite(start) or not math.isfinite(end) or start < 0 or end <= start:
                raise BackendFailure("INTERNAL", "MLX-Audio VAD returned an invalid segment")
            segments.append((start, end))
        return segments

    def transcribe(self, audio_path, language):
        with self._state_lock:
            self._require_role("asr")
            path = Path(audio_path)
            if not audio_path or not path.is_file():
                raise BackendFailure("INVALID_ARGUMENT", "transcription audio path must name a readable file")
            normalized_language = normalize_language(language)
            try:
                result = self.model.generate(str(path), language=normalized_language)
            except Exception as err:
                raise BackendFailure("INTERNAL", "MLX-Audio transcription failed") from err
            text = getattr(result, "text", None)
            if not isinstance(text, str) or not text.strip():
                raise BackendFailure("INTERNAL", "MLX-Audio transcription returned empty text")
            return text.strip(), normalized_language

    def synthesize(self, text, voice, language, destination, params=None, cancelled=None):
        with self._state_lock:
            self._require_role("tts")
            if not isinstance(text, str) or not text.strip():
                raise BackendFailure("INVALID_ARGUMENT", "TTS text is empty")
            if not destination:
                raise BackendFailure("INVALID_ARGUMENT", "TTS destination path is required")
            normalized_language = normalize_language(language)
            speaker = normalize_voice(voice, normalized_language)
            generation_options = self._tts_options(params)
            output = Path(destination)
            results = None
            audio_segments = []
            try:
                output.parent.mkdir(parents=True, exist_ok=True)
                results = self.model.generate_custom_voice(
                    text=text,
                    speaker=speaker,
                    language=normalized_language,
                    **generation_options,
                )
                sample_rate = 0
                for segment in results:
                    if cancelled is not None and cancelled():
                        raise BackendFailure("CANCELLED", "MLX-Audio TTS request was cancelled")
                    audio = getattr(segment, "audio", None)
                    segment_rate = getattr(segment, "sample_rate", 0)
                    if audio is None or not segment_rate:
                        raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
                    if sample_rate and int(segment_rate) != sample_rate:
                        raise BackendFailure("INTERNAL", "MLX-Audio TTS returned inconsistent sample rates")
                    sample_rate = int(segment_rate)
                    audio_segments.append(audio)
                if not audio_segments:
                    raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
                self._writer(str(output), audio_segments, sample_rate)
                _validate_wav(str(output))
            except BackendFailure:
                try:
                    output.unlink()
                except FileNotFoundError:
                    pass
                raise
            except Exception as err:
                try:
                    output.unlink()
                except FileNotFoundError:
                    pass
                raise BackendFailure("INTERNAL", "MLX-Audio TTS generation failed") from err
            finally:
                close = getattr(results, "close", None)
                if callable(close):
                    try:
                        close()
                    except Exception:
                        pass
                audio_segments.clear()
                self._clear_cache()
            # LocalAI owns the successful staged file and removes it after reading.
            return str(output)

    def synthesize_stream(self, text, voice, language, params=None, cancelled=None):
        """Yield validated PCM16LE chunks without staging a WAV file."""
        results = None
        with self._state_lock:
            self._require_role("tts")
            if not isinstance(text, str) or not text.strip():
                raise BackendFailure("INVALID_ARGUMENT", "TTS text is empty")
            normalized_language = normalize_language(language)
            speaker = normalize_voice(voice, normalized_language)
            generation_options = self._tts_options(params)
            try:
                results = self.model.generate_custom_voice(
                    text=text,
                    speaker=speaker,
                    language=normalized_language,
                    **generation_options,
                )
                emitted = False
                iterator = iter(results)
                while True:
                    if cancelled is not None and cancelled():
                        raise BackendFailure("CANCELLED", "MLX-Audio TTS request was cancelled")
                    try:
                        segment = next(iterator)
                    except StopIteration:
                        break
                    if cancelled is not None and cancelled():
                        raise BackendFailure("CANCELLED", "MLX-Audio TTS request was cancelled")
                    pcm = pcm16le_from_tts_segment(segment)
                    if cancelled is not None and cancelled():
                        raise BackendFailure("CANCELLED", "MLX-Audio TTS request was cancelled")
                    emitted = True
                    yield pcm
                if not emitted:
                    raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
            except BackendFailure:
                raise
            except Exception as err:
                raise BackendFailure("INTERNAL", "MLX-Audio TTS generation failed") from err
            finally:
                close = getattr(results, "close", None)
                if callable(close):
                    try:
                        close()
                    except Exception:
                        pass
                self._clear_cache()

    def _clear_cache(self):
        try:
            if self._cache_clear is not None:
                self._cache_clear()
            else:
                import mlx.core as mx
                if hasattr(mx, "clear_cache"):
                    mx.clear_cache()
                elif hasattr(mx, "metal") and hasattr(mx.metal, "clear_cache"):
                    mx.metal.clear_cache()
        except Exception:
            # Cache eviction is best-effort during shutdown and stream cleanup.
            pass

    def unload(self):
        with self._state_lock:
            self._unload_locked()

    def _unload_locked(self):
        self.model = None
        self.model_path = None
        self.role = None
        self._tts_config = {}
        gc.collect()
        self._clear_cache()
