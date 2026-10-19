# SPDX-License-Identifier: MIT
"""Test the Sound request adapter without importing model dependencies."""
import ast
from pathlib import Path
from types import SimpleNamespace
import unittest


class SoundExpansionTests(unittest.TestCase):
    def setUp(self):
        source = ast.parse(Path(__file__).with_name("backend.py").read_text())
        service = next(node for node in source.body
                       if isinstance(node, ast.ClassDef) and node.name == "BackendServicer")
        method = next(node for node in service.body
                      if isinstance(node, ast.FunctionDef) and node.name == "SoundGeneration")
        self.calls = []
        namespace = {
            "backend_pb2": SimpleNamespace(Result=lambda **fields: SimpleNamespace(**fields)),
            "_generate_audio_sync": lambda owner, payload, dst: self.calls.append(payload),
        }
        exec(compile(ast.Module(body=[method], type_ignores=[]), "backend.py", "exec"), namespace)
        self.generate = namespace["SoundGeneration"]

    def request(self, **fields):
        values = dict(dst="output.wav", text="original description", expanded=False,
                      language="it", instrumental=True, caption="piano", lyrics="",
                      think=False, src="", bpm=120, keyscale="", timesignature="", duration=0)
        values.update(fields)
        values["HasField"] = lambda name: name in {"instrumental", "bpm"}
        return SimpleNamespace(**values)

    def test_omission_preserves_native_sample_query(self):
        self.generate(None, self.request(), None)
        self.assertEqual(self.calls[0]["sample_query"], "original description")
        self.assertTrue(self.calls[0]["sample_mode"])

    def test_expanded_uses_structured_caption_and_empty_lyrics(self):
        self.generate(None, self.request(expanded=True, text=""), None)
        self.assertEqual(self.calls[0]["prompt"], "piano")
        self.assertEqual(self.calls[0]["lyrics"], "")
        self.assertEqual(self.calls[0]["bpm"], 120)
        self.assertNotIn("sample_query", self.calls[0])

    def test_marker_prevents_native_expansion_even_with_stale_text(self):
        self.generate(None, self.request(expanded=True), None)
        self.assertEqual(self.calls[0]["prompt"], "piano")
        self.assertNotIn("sample_query", self.calls[0])


if __name__ == "__main__":
    unittest.main()
