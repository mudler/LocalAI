"""Helpers for WhisperX transcript responses."""


def require_diarization_token(diarize, token):
    """Reject diarization when WhisperX cannot load its gated pipeline."""
    if diarize and not token:
        raise ValueError("HF_TOKEN is required for WhisperX diarization")


def seconds_to_nanoseconds(seconds):
    """Convert WhisperX timestamps to the duration unit used by LocalAI."""
    return int(seconds * 1_000_000_000)
