"""Map a LocalAI TranscriptRequest onto faster-whisper's transcribe() options."""


def transcribe_kwargs(request):
    """Pass on the language, task, prompt and temperature the request asked for.

    An empty language (or "auto") lets faster-whisper detect it. A temperature
    of 0 is the proto default, so it keeps faster-whisper's own fallback
    schedule; only a positive value is passed on.
    """
    language = (request.language or "").strip()
    kwargs = {
        "beam_size": 5,
        "condition_on_previous_text": False,
        "word_timestamps": "word" in request.timestamp_granularities,
        "language": None if language.lower() in ("", "auto") else language,
        "task": "translate" if request.translate else "transcribe",
    }
    if request.prompt:
        kwargs["initial_prompt"] = request.prompt
    if request.temperature > 0:
        kwargs["temperature"] = request.temperature
    return kwargs
