# SPDX-License-Identifier: MIT
"""Offline Drama 3 gallery regression. Requires PyYAML; downloads no weights."""
import pathlib
import unittest

import yaml


ROOT = pathlib.Path(__file__).resolve().parents[1]
NAME = "audio-cpp-fish-drama3"
FILE = "drama-3-backbone-8bit-experts-Q4_K_S.gguf"
REVISION = "17a5b40c5dc2c2be986f5371a7e650108b828e29"
SHA256 = "05af716baf78e7c20863b9ccebb3ebdeca9607dd3ea7d0b72c35079e675b212b"


class Drama3GalleryTest(unittest.TestCase):
    def test_gallery_entry(self):
        entries = yaml.safe_load((ROOT / "gallery/index.yaml").read_text())
        matches = [entry for entry in entries if entry["name"] == NAME]
        self.assertEqual(len(matches), 1, "expected exactly one Drama 3 entry")
        entry = matches[0]
        self.assertEqual(entry["url"], "github:mudler/LocalAI/gallery/virtual.yaml@master")
        yaml.safe_load((ROOT / "gallery/virtual.yaml").read_text())
        self.assertEqual(entry["license"], "other")
        self.assertIn("https://huggingface.co/Jmica/Drama-3-GGUF/blob/" + REVISION + "/LICENSE", entry["urls"])
        self.assertIn("plain TTS can run without a reference", entry["description"])
        self.assertTrue({"tts", "voice-cloning", "experimental", "gguf"} <= set(entry["tags"]))
        overrides = entry["overrides"]
        self.assertEqual(overrides["name"], NAME)
        self.assertEqual(overrides["backend"], "audio-cpp")
        self.assertEqual(overrides["known_usecases"], ["tts"])
        # Do not pin task:tts: the router must still select voice cloning.
        self.assertEqual(set(overrides["options"]), {"family:fish_drama3", "backend:best"})
        # LLM top_k is not forwarded by the TTS RPC. The existing audio.cpp
        # c6abb443 pin defaults to 4097 in fish_drama3/session.cpp:138.
        self.assertEqual(overrides["parameters"], {"model": "audio-cpp/" + FILE})
        self.assertNotIn("voices", overrides.get("tts", {}))
        self.assertEqual(entry["files"], [{
            "filename": "audio-cpp/" + FILE,
            "sha256": SHA256,
            "uri": "https://huggingface.co/Jmica/Drama-3-GGUF/resolve/" + REVISION + "/" + FILE,
        }])
        for text in ("experimental", "community port", "reference", "transcript",
                     "24.6 GB", "memory", "Fish Audio Community License"):
            self.assertIn(text, entry["description"])


if __name__ == "__main__":
    unittest.main()
