# SPDX-License-Identifier: MIT

import os
import sys


def parse_options(options):
    parsed = {}
    for option in options:
        key, separator, value = option.partition(":")
        parsed[key] = value if separator else "true"
    return parsed


def classify_model(model):
    normalized = model.rstrip("/").lower()
    name = os.path.basename(normalized)
    if "ltx-2" in normalized or "ltx2" in normalized:
        return "ltx2"
    if "wan2" in name or "wan_2" in name:
        return "wan"
    return None


def _append(command, flag, value):
    if value is not None and value != "":
        command.extend([flag, str(value)])


def build_generation_command(
    *,
    family,
    model,
    prompt,
    destination,
    negative_prompt="",
    start_image="",
    end_image="",
    audio="",
    width=0,
    height=0,
    num_frames=0,
    fps=0,
    seed=0,
    steps=0,
    cfg_scale=0,
    params=None,
):
    params = params or {}
    if audio:
        raise ValueError("audio conditioning is not supported by mlx-video")

    if family == "ltx2":
        command = [sys.executable, "-m", "mlx_video.models.ltx_2.generate"]
        _append(command, "--model-repo", model)
        _append(command, "--prompt", prompt)
        _append(command, "--output-path", destination)
        _append(command, "--negative-prompt", negative_prompt)
        _append(command, "--image", start_image)
        _append(command, "--end-image", end_image)
        _append(command, "--width", width or None)
        _append(command, "--height", height or None)
        _append(command, "--num-frames", num_frames or None)
        _append(command, "--fps", fps or None)
        _append(command, "--seed", seed if seed > 0 else None)
        _append(command, "--steps", steps or None)
        _append(command, "--cfg-scale", cfg_scale or None)
        for key, flag in {
            "pipeline": "--pipeline",
            "tiling": "--tiling",
            "image_strength": "--image-strength",
            "end_image_strength": "--end-image-strength",
            "text_encoder_repo": "--text-encoder-repo",
        }.items():
            _append(command, flag, params.get(key))
        return command

    if family == "wan":
        if end_image:
            raise ValueError("Wan models do not support end_image conditioning")
        command = [sys.executable, "-m", "mlx_video.models.wan_2.generate"]
        _append(command, "--model-dir", model)
        _append(command, "--prompt", prompt)
        _append(command, "--output-path", destination)
        _append(command, "--negative-prompt", negative_prompt)
        _append(command, "--image", start_image)
        _append(command, "--width", width or None)
        _append(command, "--height", height or None)
        _append(command, "--num-frames", num_frames or None)
        _append(command, "--seed", seed if seed > 0 else None)
        _append(command, "--steps", steps or None)
        _append(command, "--guide-scale", cfg_scale or None)
        for key, flag in {
            "scheduler": "--scheduler",
            "shift": "--shift",
            "tiling": "--tiling",
            "trim_first_frames": "--trim-first-frames",
        }.items():
            _append(command, flag, params.get(key))
        return command

    raise ValueError(f"unsupported mlx-video model family: {family}")
