import unittest
from types import SimpleNamespace

from transcribe_options import transcribe_kwargs


def request(**fields):
    base = {"language": "", "translate": False, "prompt": "", "temperature": 0.0, "timestamp_granularities": []}
    base.update(fields)
    return SimpleNamespace(**base)


class TestTranscribeOptions(unittest.TestCase):
    def test_language_is_passed_on(self):
        self.assertEqual(transcribe_kwargs(request(language="it"))["language"], "it")

    def test_empty_or_auto_language_is_detected(self):
        for language in ("", "  ", "auto", "AUTO"):
            self.assertIsNone(transcribe_kwargs(request(language=language))["language"], language)

    def test_translate_selects_the_translate_task(self):
        self.assertEqual(transcribe_kwargs(request(translate=True))["task"], "translate")
        self.assertEqual(transcribe_kwargs(request())["task"], "transcribe")

    def test_prompt_becomes_the_initial_prompt(self):
        self.assertEqual(transcribe_kwargs(request(prompt="LocalAI, gRPC"))["initial_prompt"], "LocalAI, gRPC")
        self.assertNotIn("initial_prompt", transcribe_kwargs(request()))

    def test_only_a_positive_temperature_is_passed_on(self):
        self.assertEqual(transcribe_kwargs(request(temperature=0.4))["temperature"], 0.4)
        self.assertNotIn("temperature", transcribe_kwargs(request()))

    def test_word_timestamps_follow_the_granularities(self):
        self.assertTrue(transcribe_kwargs(request(timestamp_granularities=["word"]))["word_timestamps"])
        self.assertFalse(transcribe_kwargs(request(timestamp_granularities=["segment"]))["word_timestamps"])

    def test_existing_defaults_are_kept(self):
        kwargs = transcribe_kwargs(request())
        self.assertEqual(kwargs["beam_size"], 5)
        self.assertFalse(kwargs["condition_on_previous_text"])


if __name__ == "__main__":
    unittest.main()
