#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
from pathlib import Path


TORCH_28 = '"torch==2.8.0"'
TORCH_29 = '"torch==2.9.1"'
TORCHAUDIO_28 = '"torchaudio==2.8.0"'
TORCHAUDIO_29 = '"torchaudio==2.9.1"'
TORCHAUDIO_LOAD = (
    "        waveform, original_sr = "
    "torchaudio.load(reference_audio, backend=self.backend)"
)
SOUNDFILE_LOAD = "\n".join(
    (
        "        import soundfile as _sf",
        "        import torch as _torch",
        "",
        "        data, original_sr = _sf.read(",
        '            reference_audio, dtype="float32", always_2d=True',
        "        )",
        "        waveform = _torch.from_numpy(data.T.copy())",
    )
)


def patch_cuda13_dependencies(pyproject: Path) -> None:
    content = pyproject.read_text()
    if (
        TORCH_28 not in content
        and TORCHAUDIO_28 not in content
        and TORCH_29 in content
        and TORCHAUDIO_29 in content
    ):
        return
    if TORCH_28 not in content or TORCHAUDIO_28 not in content:
        raise RuntimeError("fish-speech's torch 2.8 dependency pins have changed")

    content = content.replace(TORCH_28, TORCH_29)
    content = content.replace(TORCHAUDIO_28, TORCHAUDIO_29)
    pyproject.write_text(content)


def patch_reference_loader(loader: Path) -> None:
    content = loader.read_text()
    if TORCHAUDIO_LOAD not in content and content.count(SOUNDFILE_LOAD) == 1:
        return
    if content.count(TORCHAUDIO_LOAD) != 1:
        raise RuntimeError("fish-speech's torchaudio.load call has changed")

    loader.write_text(content.replace(TORCHAUDIO_LOAD, SOUNDFILE_LOAD))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    parser.add_argument("--cuda-major")
    args = parser.parse_args()

    if args.cuda_major == "13":
        patch_cuda13_dependencies(args.source / "pyproject.toml")
    patch_reference_loader(
        args.source / "fish_speech/inference_engine/reference_loader.py"
    )


if __name__ == "__main__":
    main()
