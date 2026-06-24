# SPDX-License-Identifier: MIT

import importlib.util
import sys
import tempfile
import types
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("prepare_upstream.py")


def load_prepare_upstream():
    if not MODULE_PATH.exists():
        raise AssertionError("prepare_upstream.py is missing")
    spec = importlib.util.spec_from_file_location("prepare_upstream", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakeAudioData:
    @property
    def T(self):
        return self

    def copy(self):
        return "channels-first"


class PrepareUpstreamTests(unittest.TestCase):
    def test_cuda13_dependencies_follow_available_pytorch_wheels(self):
        prepare_upstream = load_prepare_upstream()

        with tempfile.TemporaryDirectory() as tmp:
            pyproject = Path(tmp) / "pyproject.toml"
            pyproject.write_text(
                'dependencies = [\n    "torch==2.8.0",\n    "torchaudio==2.8.0",\n]\n'
                'stable = [\n    "torch==2.8.0",\n    "torchaudio",\n]\n'
            )

            prepare_upstream.patch_cuda13_dependencies(pyproject)

            self.assertEqual(
                pyproject.read_text(),
                'dependencies = [\n    "torch==2.9.1",\n    "torchaudio==2.9.1",\n]\n'
                'stable = [\n    "torch==2.9.1",\n    "torchaudio",\n]\n',
            )

    def test_reference_audio_uses_soundfile_without_torchcodec(self):
        prepare_upstream = load_prepare_upstream()

        with tempfile.TemporaryDirectory() as tmp:
            loader = Path(tmp) / "reference_loader.py"
            loader.write_text(
                "class ReferenceLoader:\n"
                "    def load_audio(self, reference_audio):\n"
                "        waveform, original_sr = torchaudio.load(reference_audio, backend=self.backend)\n"
                "        return waveform, original_sr\n"
            )
            prepare_upstream.patch_reference_loader(loader)

            calls = []
            fake_soundfile = types.SimpleNamespace(
                read=lambda source, **kwargs: (
                    calls.append((source, kwargs)) or FakeAudioData(),
                    24000,
                )
            )
            fake_torch = types.SimpleNamespace(
                from_numpy=lambda data: ("tensor", data),
            )
            previous_soundfile = sys.modules.get("soundfile")
            previous_torch = sys.modules.get("torch")
            sys.modules["soundfile"] = fake_soundfile
            sys.modules["torch"] = fake_torch
            try:
                namespace = {"torchaudio": None}
                exec(compile(loader.read_text(), str(loader), "exec"), namespace)
                instance = namespace["ReferenceLoader"]()
                instance.backend = "soundfile"

                waveform, sample_rate = instance.load_audio("voice.wav")
            finally:
                if previous_soundfile is None:
                    del sys.modules["soundfile"]
                else:
                    sys.modules["soundfile"] = previous_soundfile
                if previous_torch is None:
                    del sys.modules["torch"]
                else:
                    sys.modules["torch"] = previous_torch

            self.assertEqual(waveform, ("tensor", "channels-first"))
            self.assertEqual(sample_rate, 24000)
            self.assertEqual(
                calls,
                [("voice.wav", {"dtype": "float32", "always_2d": True})],
            )

    def test_reference_loader_drift_fails_the_build(self):
        prepare_upstream = load_prepare_upstream()

        with tempfile.TemporaryDirectory() as tmp:
            loader = Path(tmp) / "reference_loader.py"
            loader.write_text("def load_audio():\n    pass\n")

            with self.assertRaisesRegex(RuntimeError, "torchaudio.load call"):
                prepare_upstream.patch_reference_loader(loader)

    def test_preparation_can_be_repeated(self):
        prepare_upstream = load_prepare_upstream()

        with tempfile.TemporaryDirectory() as tmp:
            pyproject = Path(tmp) / "pyproject.toml"
            pyproject.write_text(
                'dependencies = ["torch==2.8.0", "torchaudio==2.8.0"]\n'
            )
            loader = Path(tmp) / "reference_loader.py"
            loader.write_text(
                "def load_audio(reference_audio):\n"
                "        waveform, original_sr = torchaudio.load(reference_audio, backend=self.backend)\n"
            )

            prepare_upstream.patch_cuda13_dependencies(pyproject)
            prepare_upstream.patch_reference_loader(loader)
            try:
                prepare_upstream.patch_cuda13_dependencies(pyproject)
                prepare_upstream.patch_reference_loader(loader)
            except RuntimeError as err:
                self.fail(f"preparation is not idempotent: {err}")


if __name__ == "__main__":
    unittest.main()
