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
import wave


ALLOWED_ROLES = frozenset({"vad", "asr", "tts"})
FORBIDDEN_DISTRIBUTIONS = frozenset({"torch", "torchaudio"})
VAD_SAMPLE_RATE = 16000


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


def _local_snapshot_path(model_path):
    path = Path(model_path)
    if not path.is_absolute():
        raise BackendFailure("INVALID_ARGUMENT", "mlx-audio model must be an absolute local snapshot path")
    try:
        resolved = path.resolve(strict=True)
    except OSError as err:
        raise BackendFailure("NOT_FOUND", "mlx-audio model snapshot does not exist") from err
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
    if value in {"en", "en-us", "en-gb", "english"}:
        return "English"
    if value in {"ko", "ko-kr", "korean"}:
        return "Korean"
    raise BackendFailure("INVALID_ARGUMENT", "mlx-audio supports only English and Korean in this profile")


def normalize_voice(voice, language):
    value = (voice or "").strip()
    if not value:
        return "Ryan"
    aliases = {"ryan": "Ryan", "sohee": "Sohee"}
    selected = aliases.get(value.lower())
    if selected is None:
        raise BackendFailure("INVALID_ARGUMENT", "mlx-audio voice must be Ryan or Sohee")
    if selected == "Sohee" and language != "Korean":
        raise BackendFailure("INVALID_ARGUMENT", "Sohee is configured only for Korean synthesis")
    return selected


class MLXAudioRuntime:
    def __init__(self, loader_provider=None, writer=None, temp_dir=None, cache_clear=None):
        self._loader_provider = loader_provider or _production_loader
        self._writer = writer or _production_writer
        self._temp_dir = temp_dir
        self._cache_clear = cache_clear
        self.model = None
        self.model_path = None
        self.role = None

    def load(self, model_path, options, distributions=None):
        ensure_torch_free(distributions)
        role = configured_role(options)
        local_path = _local_snapshot_path(model_path)
        try:
            model = self._loader_provider(role)(local_path, strict=True)
        except BackendFailure:
            raise
        except Exception as err:
            raise BackendFailure("INTERNAL", f"failed to load mlx-audio {role} model") from err
        if model is None:
            raise BackendFailure("INTERNAL", f"mlx-audio {role} loader returned no model")
        self.unload()
        self.model = model
        self.model_path = local_path
        self.role = role
        return role

    def _require_role(self, role):
        if self.model is None:
            raise BackendFailure("FAILED_PRECONDITION", "mlx-audio model is not loaded")
        if self.role != role:
            raise BackendFailure("FAILED_PRECONDITION", f"loaded mlx-audio role is {self.role}, not {role}")

    def vad(self, audio):
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

    def synthesize(self, text, voice, language, destination):
        self._require_role("tts")
        if not isinstance(text, str) or not text.strip():
            raise BackendFailure("INVALID_ARGUMENT", "TTS text is empty")
        if not destination:
            raise BackendFailure("INVALID_ARGUMENT", "TTS destination path is required")
        normalized_language = normalize_language(language)
        speaker = normalize_voice(voice, normalized_language)
        output = Path(destination)
        try:
            output.parent.mkdir(parents=True, exist_ok=True)
            results = list(self.model.generate_custom_voice(
                text=text,
                speaker=speaker,
                language=normalized_language,
            ))
            if not results:
                raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
            audio_segments = []
            sample_rate = 0
            for segment in results:
                audio = getattr(segment, "audio", None)
                segment_rate = getattr(segment, "sample_rate", 0)
                if audio is None or not segment_rate:
                    raise BackendFailure("INTERNAL", "MLX-Audio TTS returned empty audio")
                if sample_rate and int(segment_rate) != sample_rate:
                    raise BackendFailure("INTERNAL", "MLX-Audio TTS returned inconsistent sample rates")
                sample_rate = int(segment_rate)
                audio_segments.append(audio)
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
        # LocalAI owns the successful staged file and removes it after reading.
        return str(output)

    def unload(self):
        self.model = None
        self.model_path = None
        self.role = None
        gc.collect()
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
            # Cache eviction is best-effort during shutdown; model references have
            # already been dropped and unload must remain idempotent.
            pass
