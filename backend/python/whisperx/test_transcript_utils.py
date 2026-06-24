import unittest

import transcript_utils


class TestTranscriptUtils(unittest.TestCase):
    def test_diarization_requires_hugging_face_token(self):
        with self.assertRaisesRegex(
            ValueError,
            "HF_TOKEN is required for WhisperX diarization",
        ):
            transcript_utils.require_diarization_token(True, None)

    def test_diarization_does_not_require_token_when_disabled(self):
        transcript_utils.require_diarization_token(False, None)

    def test_seconds_are_serialized_as_nanoseconds(self):
        self.assertEqual(
            transcript_utils.seconds_to_nanoseconds(3.25),
            3_250_000_000,
        )


if __name__ == "__main__":
    unittest.main()
