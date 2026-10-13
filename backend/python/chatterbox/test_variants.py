# SPDX-License-Identifier: MIT
"""CPU-only backend contract tests; no model packages or weights required."""
import importlib.util
from pathlib import Path
import sys
from types import ModuleType, SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch


def load_backend(turbo_available=True):
    modules = {}
    for name in ("torch", "torchaudio", "chatterbox", "chatterbox.tts",
                 "chatterbox.mtl_tts", "backend_pb2", "backend_pb2_grpc",
                 "grpc", "grpc_auth"):
        modules[name] = ModuleType(name)
    modules["chatterbox"].__path__ = []
    modules["torch"].cuda = SimpleNamespace(is_available=lambda: False)
    modules["torch"].backends = SimpleNamespace()
    modules["torchaudio"].save = MagicMock()
    modules["backend_pb2"].Result = SimpleNamespace
    modules["backend_pb2_grpc"].BackendServicer = object
    modules["grpc_auth"].get_auth_interceptors = MagicMock()
    classes = {}
    for variant, module, cls in (
        ("original", "chatterbox.tts", "ChatterboxTTS"),
        ("multilingual", "chatterbox.mtl_tts", "ChatterboxMultilingualTTS"),
        ("turbo", "chatterbox.tts_turbo", "ChatterboxTurboTTS"),
    ):
        classes[variant] = MagicMock()
        classes[variant].from_pretrained.return_value.sr = 24000
        if variant != "turbo" or turbo_available:
            modules.setdefault(module, ModuleType(module))
            setattr(modules[module], cls, classes[variant])
    if not turbo_available:
        modules["chatterbox.tts_turbo"] = None
    spec = importlib.util.spec_from_file_location(
        "chatterbox_backend_test", Path(__file__).with_name("backend.py"))
    backend = importlib.util.module_from_spec(spec)
    with patch.dict(sys.modules, modules), patch.object(sys, "path", sys.path.copy()):
        spec.loader.exec_module(backend)
    return backend, modules, classes


class VariantTests(unittest.TestCase):
    def setUp(self):
        self.backend, self.modules, self.classes = load_backend()
        self.module_patch = patch.dict(sys.modules, self.modules)
        self.module_patch.start()
        self.addCleanup(self.module_patch.stop)
        self.service = self.backend.BackendServicer()

    def load(self, options=(), audio_path=""):
        return self.service.LoadModel(SimpleNamespace(
            CUDA=False, Options=options, AudioPath=audio_path,
            ModelFile="/models/voice.yaml"), None)

    def speak(self, params=None, voice="", text="Hello"):
        return self.service.TTS(SimpleNamespace(
            text=text, dst="/tmp/output.wav", voice=voice, params=params or {}), None)

    def test_model_selection(self):
        for options, variant, expected in (
            ([], "original", {"device": "cpu"}),
            (["multilingual:true"], "multilingual", {"device": "cpu"}),
            (["turbo:true"], "turbo", {"device": "cpu"}),
            (["nano:true"], "turbo", {"device": "cpu", "nano": True}),
            (["multilingual:false", "turbo:false", "nano:false"], "original", {"device": "cpu"}),
            (["multilingual:false", "turbo:TRUE", "nano:false"], "turbo", {"device": "cpu"}),
        ):
            with self.subTest(options=options):
                for cls in self.classes.values():
                    cls.reset_mock()
                result = self.load(options)
                self.assertTrue(result.success, result.message)
                self.classes[variant].from_pretrained.assert_called_once_with(**expected)
                for other, cls in self.classes.items():
                    if other != variant:
                        cls.from_pretrained.assert_not_called()

    def test_conflicting_variants_are_rejected(self):
        for selectors in (("turbo", "nano"), ("multilingual", "turbo"), ("multilingual", "nano")):
            with self.subTest(selectors=selectors):
                result = self.load([f"{selector}:true" for selector in selectors])
                self.assertFalse(result.success)
                self.assertIn("Only one", result.message)
        for cls in self.classes.values():
            cls.from_pretrained.assert_not_called()

    def test_invalid_selector_is_rejected(self):
        for selector in ("turbo", "nano", "multilingual"):
            for value in ("yes", "1", ""):
                with self.subTest(selector=selector, value=value):
                    result = self.load([f"{selector}:{value}"])
                    self.assertFalse(result.success)
                    self.assertIn("true or false", result.message)

    def test_turbo_import_is_lazy(self):
        backend, modules, classes = load_backend(turbo_available=False)
        with patch.dict(sys.modules, modules):
            self.service = backend.BackendServicer()
            self.assertTrue(self.load().success)
            self.assertFalse(self.load(["turbo:true"]).success)
            classes["original"].from_pretrained.assert_called_once()

    def test_load_error_is_returned(self):
        for variant in ("original", "multilingual", "turbo", "nano"):
            cls = self.classes["turbo" if variant == "nano" else variant]
            cls.from_pretrained.side_effect = RuntimeError("weights unavailable")
            result = self.load([] if variant == "original" else [f"{variant}:true"])
            self.assertFalse(result.success)
            self.assertIn("weights unavailable", result.message)
            cls.from_pretrained.side_effect = None

    def test_generation_filters_selectors_and_unsupported_knobs(self):
        for variant in ("original", "multilingual", "turbo", "nano"):
            with self.subTest(variant=variant):
                options = [] if variant == "original" else [f"{variant}:true"]
                self.assertTrue(self.load(options + ["cfg_weight:0.4", "exaggeration:0.6", "min_p:0.1", "temperature:0.7"], "reference.wav").success)
                result = self.speak({"turbo": "true", "nano": "true", "multilingual": "true", "cfg_weight": "0.8", "exaggeration": "0.9", "min_p": "0.2", "temperature": "0.5", "ref_text": "ignored"})
                self.assertTrue(result.success, getattr(result, "message", ""))
                expected = {"temperature": 0.5, "audio_prompt_path": "/models/reference.wav"}
                if variant in ("original", "multilingual"):
                    expected.update(cfg_weight=0.8, exaggeration=0.9, min_p=0.2)
                self.service.model.generate.assert_called_with("Hello", **expected)

    def test_top_k_remains_an_integer(self):
        for variant in ("turbo", "nano"):
            for params, expected in (({}, 100), ({"top_k": "50"}, 50)):
                with self.subTest(variant=variant, params=params):
                    self.assertTrue(self.load([f"{variant}:true", "top_k:100"]).success)
                    self.assertTrue(self.speak(params).success)
                    top_k = self.service.model.generate.call_args.kwargs["top_k"]
                    self.assertIs(type(top_k), int)
                    self.assertEqual(top_k, expected)

    def test_static_unsupported_knobs_are_filtered(self):
        self.assertTrue(self.load(["nano:true", "cfg_weight:0.4", "exaggeration:0.6", "min_p:0.1"]).success)
        self.assertTrue(self.speak().success)
        self.service.model.generate.assert_called_once_with("Hello")

    def test_voice_override_is_preserved(self):
        self.assertTrue(self.load(["turbo:true"], "reference.wav").success)
        with patch.object(self.backend.os.path, "isfile", return_value=True):
            self.assertTrue(self.speak(voice="/voices/alice.wav").success)
        self.service.model.generate.assert_called_once_with("Hello", audio_prompt_path="/voices/alice.wav")

    def test_long_text_still_uses_chunks(self):
        self.assertTrue(self.load(["nano:true"]).success)
        text = "A sentence with several words. " * 20
        with patch.object(self.backend, "merge_audio_files") as merge:
            self.assertTrue(self.speak(text=text).success)
            chunks = self.backend.split_text_at_word_boundary(text)
            self.assertEqual([call.args[0] for call in self.service.model.generate.call_args_list], chunks)
            merge.assert_called_once()


if __name__ == "__main__":
    unittest.main()
