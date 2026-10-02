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


    def test_failed_diarization_keeps_the_transcript(self):
        transcript = {"segments": [{"text": "Die Rechnung"}]}
        logged = []

        def refused(_):
            raise RuntimeError("403 Client Error: gated repo")

        result = transcript_utils.diarize_or_keep(transcript, refused, logged.append)
        self.assertIs(result, transcript)
        self.assertIn("403", logged[0])

    def test_successful_diarization_is_returned(self):
        transcript = {"segments": [{"text": "Die Rechnung"}]}
        with_speakers = {"segments": [{"text": "Die Rechnung", "speaker": "SPEAKER_00"}]}
        result = transcript_utils.diarize_or_keep(transcript, lambda _: with_speakers, lambda _: None)
        self.assertIs(result, with_speakers)


if __name__ == "__main__":
    unittest.main()
