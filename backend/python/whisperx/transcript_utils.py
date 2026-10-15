"""Helpers for WhisperX transcript responses."""


def require_diarization_token(diarize, token):
    """Reject diarization when WhisperX cannot load its gated pipeline."""
    if diarize and not token:
        raise ValueError("HF_TOKEN is required for WhisperX diarization")


def seconds_to_nanoseconds(seconds):
    """Convert WhisperX timestamps to the duration unit used by LocalAI."""
    return int(seconds * 1_000_000_000)


def diarize_or_keep(transcript, diarize, log):
    """Run diarization; if it fails, keep the transcript without speakers.

    Diarization is an add-on to a finished transcript. A refused download of
    the gated pyannote pipeline (403) or any other diarization error must not
    throw the transcript away.
    """
    try:
        return diarize(transcript)
    except Exception as err:  # noqa: BLE001 - any diarization failure degrades
        log(f"Diarization failed, returning transcript without speakers: {err!r}")
        return transcript
