# SPDX-License-Identifier: MIT

import json
import math
import os
from urllib.parse import urlparse


BASE_MODEL_ID = "meituan-longcat/LongCat-Video"
AVATAR_MODEL_ID = "meituan-longcat/LongCat-Video-Avatar-1.5"
MODEL_KIND_BASE = "base"
MODEL_KIND_AVATAR = "avatar"

ATTENTION_OVERRIDES = {
    "auto": {},
    "sdpa": {
        "enable_flashattn2": False,
        "enable_flashattn3": False,
        "enable_xformers": False,
    },
    "flash2": {
        "enable_flashattn2": True,
        "enable_flashattn3": False,
        "enable_xformers": False,
    },
    "flash3": {
        "enable_flashattn2": False,
        "enable_flashattn3": True,
        "enable_xformers": False,
    },
    "xformers": {
        "enable_flashattn2": False,
        "enable_flashattn3": False,
        "enable_xformers": True,
    },
}


def parse_options(values):
    options = {}
    for raw in values:
        if ":" not in raw:
            options[raw.strip()] = True
            continue
        key, value = raw.split(":", 1)
        key = key.strip()
        value = value.strip()
        if not key:
            continue
        lower = value.lower()
        if lower in {"true", "false"}:
            options[key] = lower == "true"
            continue
        try:
            options[key] = int(value)
            continue
        except ValueError:
            pass
        try:
            options[key] = float(value)
            continue
        except ValueError:
            pass
        options[key] = value
    return options


def select_known_options(options, known):
    """Split parsed options into the subset this backend understands and the
    unknown keys to ignore.

    LocalAI injects serving defaults (e.g. the llama.cpp cache_reuse / parallel
    options) onto every model config regardless of backend. A backend should
    tolerate options it does not understand rather than refuse to load, matching
    the other LocalAI Python backends; the caller logs the ignored keys.

    Returns (kept, ignored) where kept preserves the known entries and ignored is
    the sorted list of dropped keys.
    """
    ignored = sorted(key for key in options if key not in known)
    kept = {key: value for key, value in options.items() if key in known}
    return kept, ignored


def require_bool(value, name):
    if isinstance(value, bool):
        return value
    if isinstance(value, str) and value.lower() in {"true", "false"}:
        return value.lower() == "true"
    raise ValueError(f"{name} must be true or false")


def require_int(value, name, minimum=None, maximum=None):
    try:
        parsed = int(value)
    except (TypeError, ValueError) as err:
        raise ValueError(f"{name} must be an integer") from err
    if minimum is not None and parsed < minimum:
        raise ValueError(f"{name} must be at least {minimum}")
    if maximum is not None and parsed > maximum:
        raise ValueError(f"{name} must be at most {maximum}")
    return parsed


def require_float(value, name, minimum=None, maximum=None):
    try:
        parsed = float(value)
    except (TypeError, ValueError) as err:
        raise ValueError(f"{name} must be a number") from err
    if minimum is not None and parsed < minimum:
        raise ValueError(f"{name} must be at least {minimum}")
    if maximum is not None and parsed > maximum:
        raise ValueError(f"{name} must be at most {maximum}")
    return parsed


def attention_overrides(name):
    try:
        return dict(ATTENTION_OVERRIDES[name])
    except KeyError as err:
        choices = ", ".join(ATTENTION_OVERRIDES)
        raise ValueError(f"attention_backend must be one of: {choices}") from err


def _model_name_from_directory(path):
    for filename in ("model_index.json", "config.json"):
        config_path = os.path.join(path, filename)
        try:
            with open(config_path, "r", encoding="utf-8") as config_file:
                model_name = json.load(config_file).get("model_name", "")
        except (FileNotFoundError, OSError, ValueError, TypeError):
            continue
        if model_name:
            return model_name
    return ""


def normalize_model_source(model):
    value = model.rstrip("/")
    for prefix in ("huggingface://", "hf://"):
        if value.startswith(prefix):
            return value[len(prefix) :]
    parsed = urlparse(value)
    if parsed.scheme in {"http", "https"} and parsed.netloc.lower() == "huggingface.co":
        parts = [part for part in parsed.path.split("/") if part]
        if len(parts) >= 2:
            return "/".join(parts[:2])
    return value


def classify_model(model):
    if not model:
        return None
    normalized = normalize_model_source(model)
    if os.path.isdir(normalized):
        name = _model_name_from_directory(normalized).lower()
        if name == "longcat-video":
            return MODEL_KIND_BASE
        if name == "longcat-video-avatar-1.5":
            return MODEL_KIND_AVATAR
        return None

    normalized = normalized.lower()
    if normalized == BASE_MODEL_ID.lower():
        return MODEL_KIND_BASE
    if normalized == AVATAR_MODEL_ID.lower():
        return MODEL_KIND_AVATAR
    return None


def normalize_num_frames(value, default=93):
    frames = default if not value or value < 1 else value
    return max(1, ((frames - 1) // 4) * 4 + 1)


def avatar_segments_for_frames(frames):
    if not frames or frames <= 93:
        return 1
    return 1 + math.ceil((frames - 93) / 80)


def avatar_segments_for_duration(duration_seconds, fps=25):
    if duration_seconds <= 0:
        return 1
    return avatar_segments_for_frames(math.ceil(duration_seconds * fps))


def validate_dimensions(width, height):
    width = width or 832
    height = height or 480
    if width < 256 or height < 256:
        raise ValueError("width and height must each be at least 256")
    if width > 1280 or height > 768:
        raise ValueError("width and height must not exceed 1280x768")
    if width % 16 != 0 or height % 16 != 0:
        raise ValueError("width and height must be divisible by 16")
    if width * height > 1280 * 768:
        raise ValueError("requested video dimensions exceed the 1280x768 pixel limit")
    return width, height
