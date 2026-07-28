#!/usr/bin/env python3
"""
gRPC server of LocalAI for Qwen3-ASR (transformers backend, non-vLLM).
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import backend_pb2
import backend_pb2_grpc
import torch
from qwen_asr import Qwen3ASRModel

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors
from model_utils import resolve_model_reference



def is_float(s):
    try:
        float(s)
        return True
    except ValueError:
        return False


def is_int(s):
    try:
        int(s)
        return True
    except ValueError:
        return False


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Languages Qwen3-ASR understands, spelled the way its validate_language()
# expects (full English names).  Used to accept case-insensitive names and to
# leave anything already valid untouched.
_QWEN_ASR_SUPPORTED_LANGUAGES = (
    "Chinese", "English", "Cantonese", "Arabic", "German", "French", "Spanish",
    "Portuguese", "Indonesian", "Italian", "Korean", "Russian", "Thai",
    "Vietnamese", "Japanese", "Turkish", "Hindi", "Malay", "Dutch", "Swedish",
    "Danish", "Finnish", "Polish", "Czech", "Filipino", "Persian", "Greek",
    "Romanian", "Hungarian", "Macedonian",
)

# OpenAI-compatible clients (Home Assistant / wyoming_openai, Whisper tooling,
# ...) send ISO 639-1 codes such as "de", but qwen_asr only accepts the full
# names above.  Map the codes for every supported language to its name.
_LANGUAGE_CODE_TO_NAME = {
    "zh": "Chinese", "en": "English", "yue": "Cantonese", "ar": "Arabic",
    "de": "German", "fr": "French", "es": "Spanish", "pt": "Portuguese",
    "id": "Indonesian", "it": "Italian", "ko": "Korean", "ru": "Russian",
    "th": "Thai", "vi": "Vietnamese", "ja": "Japanese", "tr": "Turkish",
    "hi": "Hindi", "ms": "Malay", "nl": "Dutch", "sv": "Swedish",
    "da": "Danish", "fi": "Finnish", "pl": "Polish", "cs": "Czech",
    "fil": "Filipino", "tl": "Filipino", "fa": "Persian", "el": "Greek",
    "ro": "Romanian", "hu": "Hungarian", "mk": "Macedonian",
}


def _normalize_language(language):
    """Translate an ISO language code into the name Qwen3-ASR expects.

    ``request.language`` typically carries an ISO 639-1 code ("de", "de-DE"),
    but ``qwen_asr.validate_language`` only accepts full English names
    ("German").  Names that are already valid (in any case) are returned with
    their canonical spelling, and anything unrecognised is passed through
    unchanged so qwen_asr keeps raising its own descriptive error.
    """
    if not language:
        return language
    key = language.strip()
    if not key:
        return language
    lowered = key.lower()
    # Already a full language name (compare case-insensitively).
    for name in _QWEN_ASR_SUPPORTED_LANGUAGES:
        if lowered == name.lower():
            return name
    # ISO code, optionally with a region/script suffix like "de-DE" / "zh_CN".
    code = lowered.replace("_", "-").split("-", 1)[0]
    return _LANGUAGE_CODE_TO_NAME.get(code, key)


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        if torch.cuda.is_available():
            device = "cuda"
        else:
            device = "cpu"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device = "mps"
        if not torch.cuda.is_available() and request.CUDA:
            return backend_pb2.Result(success=False, message="CUDA is not available")

        self.device = device
        self.options = {}

        for opt in request.Options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            if is_float(value):
                value = float(value)
            elif is_int(value):
                value = int(value)
            elif value.lower() in ["true", "false"]:
                value = value.lower() == "true"
            self.options[key] = value

        model_path, local_only = resolve_model_reference(
            request, "Qwen/Qwen3-ASR-1.7B"
        )
        default_dtype = torch.bfloat16 if self.device == "cuda" else torch.float32
        load_dtype = default_dtype
        if "torch_dtype" in self.options:
            d = str(self.options["torch_dtype"]).lower()
            if d == "fp16":
                load_dtype = torch.float16
            elif d == "bf16":
                load_dtype = torch.bfloat16
            elif d == "fp32":
                load_dtype = torch.float32
            del self.options["torch_dtype"]

        self.max_inference_batch_size = self.options.get("max_inference_batch_size", 32)
        self.max_new_tokens = self.options.get("max_new_tokens", 256)

        forced_aligner = self.options.get("forced_aligner")
        if forced_aligner is not None and isinstance(forced_aligner, str):
            forced_aligner = forced_aligner.strip() or None
        attn_implementation = self.options.get("attn_implementation")
        if attn_implementation is not None and isinstance(attn_implementation, str):
            attn_implementation = attn_implementation.strip() or None

        if self.device == "mps":
            device_map = None
        elif self.device == "cuda":
            device_map = "cuda:0"
        else:
            device_map = "cpu"

        load_kwargs = dict(
            dtype=load_dtype,
            device_map=device_map,
            max_inference_batch_size=self.max_inference_batch_size,
            max_new_tokens=self.max_new_tokens,
        )
        if attn_implementation:
            load_kwargs["attn_implementation"] = attn_implementation
        if forced_aligner:
            load_kwargs["forced_aligner"] = forced_aligner
            forced_aligner_kwargs = dict(
                dtype=load_dtype,
                device_map=device_map,
            )
            if attn_implementation:
                forced_aligner_kwargs["attn_implementation"] = attn_implementation
            load_kwargs["forced_aligner_kwargs"] = forced_aligner_kwargs
        load_kwargs["local_files_only"] = local_only

        try:
            print(f"Loading Qwen3-ASR from {model_path}", file=sys.stderr)
            if attn_implementation:
                print(f"Using attn_implementation: {attn_implementation}", file=sys.stderr)
            if forced_aligner:
                print(f"Loading with forced_aligner: {forced_aligner}", file=sys.stderr)
            self.model = Qwen3ASRModel.from_pretrained(model_path, **load_kwargs)
            print("Qwen3-ASR model loaded successfully", file=sys.stderr)
        except Exception as err:
            print(f"[ERROR] LoadModel failed: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    @staticmethod
    def _is_cjk(ch):
        """Check if a character is CJK (Chinese/Japanese/Korean)."""
        cp = ord(ch)
        return (
            0x4E00 <= cp <= 0x9FFF      # CJK Unified Ideographs
            or 0x3400 <= cp <= 0x4DBF   # Extension A
            or 0x20000 <= cp <= 0x2A6DF # Extension B
            or 0xF900 <= cp <= 0xFAFF   # Compatibility Ideographs
            or 0x3040 <= cp <= 0x309F   # Hiragana
            or 0x30A0 <= cp <= 0x30FF   # Katakana
            or 0xAC00 <= cp <= 0xD7AF   # Hangul Syllables
        )

    @staticmethod
    def _is_punct(ch):
        """Check if a character is punctuation (no space before it)."""
        import unicodedata
        cat = unicodedata.category(ch)
        return cat.startswith('P')

    @staticmethod
    def _smart_join(tokens):
        """Join tokens with spaces for non-CJK text, without spaces for CJK.

        Rules:
          - Between two CJK chars: no space
          - Between two non-CJK tokens: space
          - Before punctuation: no space
          - CJK adjacent to non-CJK: no space (smooth mixed-text transition)
        """
        if not tokens:
            return ""
        result = [tokens[0]]
        for token in tokens[1:]:
            if not token:
                continue
            prev_ch = result[-1][-1] if result[-1] else ''
            curr_ch = token[0]
            # Punctuation never gets a space before it
            if BackendServicer._is_punct(curr_ch):
                result.append(token)
            # CJK to CJK: no space
            elif prev_ch and BackendServicer._is_cjk(prev_ch) and BackendServicer._is_cjk(curr_ch):
                result.append(token)
            # CJK adjacent to non-CJK or vice versa: no space
            elif prev_ch and (BackendServicer._is_cjk(prev_ch) or BackendServicer._is_cjk(curr_ch)):
                result.append(token)
            # Both non-CJK (Latin, Cyrillic, etc.): add space
            else:
                result.append(' ' + token)
        return "".join(result)

    @staticmethod
    def _extract_word_info(ts):
        """Return (start_sec, end_sec, text) from a ForcedAlignItem or tuple."""
        if hasattr(ts, 'start_time') and hasattr(ts, 'end_time') and hasattr(ts, 'text'):
            return (
                float(ts.start_time) if ts.start_time is not None else 0.0,
                float(ts.end_time) if ts.end_time is not None else 0.0,
                str(ts.text) if ts.text else "",
            )
        elif isinstance(ts, (list, tuple)) and len(ts) >= 3:
            return (
                float(ts[0]) if ts[0] is not None else 0.0,
                float(ts[1]) if ts[1] is not None else 0.0,
                ts[2] if len(ts) > 2 and ts[2] is not None else "",
            )
        return (0.0, 0.0, "")

    @staticmethod
    def _compute_gap_threshold(time_stamps):
        """Compute a gap threshold for sentence boundary detection.

        Uses the median inter-item gap multiplied by a factor, with a
        minimum floor of 0.3s.  Returns 0 if there are too few items.
        """
        if len(time_stamps) < 2:
            return 0.0
        gaps = []
        for i in range(1, len(time_stamps)):
            prev_s, prev_e, _ = BackendServicer._extract_word_info(time_stamps[i - 1])
            curr_s, _, _ = BackendServicer._extract_word_info(time_stamps[i])
            gaps.append(curr_s - prev_e)
        if not gaps:
            return 0.0
        gaps.sort()
        median = gaps[len(gaps) // 2]
        # threshold = max(median * 4, 0.3s)
        return max(median * 4, 0.3)

    def _build_segments(self, time_stamps, granularity):
        """Build TranscriptSegment list from forced-aligner output.

        granularity:
          - "word": one segment per aligned item (character / word)
          - "segment" (default): merge consecutive items, splitting at
            time gaps that exceed a dynamic threshold (sentence boundaries).
        """
        if granularity == "word":
            result = []
            for idx, ts in enumerate(time_stamps):
                s, e, t = self._extract_word_info(ts)
                result.append(backend_pb2.TranscriptSegment(
                    id=idx,
                    start=int(s * 1_000_000_000),
                    end=int(e * 1_000_000_000),
                    text=t,
                ))
            return result

        # segment mode — merge at time-gap boundaries
        threshold = self._compute_gap_threshold(time_stamps)
        result = []
        buf_text = []
        buf_start = None
        buf_end = 0.0
        prev_end = None

        for ts in time_stamps:
            s, e, t = self._extract_word_info(ts)

            # Detect sentence boundary via time gap
            if prev_end is not None and (s - prev_end) >= threshold and buf_text:
                result.append(backend_pb2.TranscriptSegment(
                    id=len(result),
                    start=int(buf_start * 1_000_000_000),
                    end=int(buf_end * 1_000_000_000),
                    text=self._smart_join(buf_text),
                ))
                buf_text = []
                buf_start = None

            if buf_start is None:
                buf_start = s
            buf_text.append(t)
            buf_end = e
            prev_end = e

        # flush remaining
        if buf_text and buf_start is not None:
            result.append(backend_pb2.TranscriptSegment(
                id=len(result),
                start=int(buf_start * 1_000_000_000),
                end=int(buf_end * 1_000_000_000),
                text=self._smart_join(buf_text),
            ))

        return result

    def AudioTranscription(self, request, context):
        result_segments = []
        text = ""
        try:
            audio_path = request.dst
            if not audio_path or not os.path.exists(audio_path):
                print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
                return backend_pb2.TranscriptResult(segments=[], text="")

            language = None
            if request.language and request.language.strip():
                language = _normalize_language(request.language.strip())

            ctx = ""
            if request.prompt and request.prompt.strip():
                ctx = request.prompt.strip()

            # Determine requested granularity (default: segment)
            granularities = list(request.timestamp_granularities) if request.timestamp_granularities else []
            granularity = "word" if "word" in granularities else "segment"

            has_aligner = getattr(self.model, 'forced_aligner', None) is not None
            try:
                results = self.model.transcribe(
                    audio=audio_path, language=language, context=ctx,
                    return_time_stamps=has_aligner,
                )
            except TypeError:
                results = self.model.transcribe(audio=audio_path, language=language, context=ctx)

            if not results:
                return backend_pb2.TranscriptResult(segments=[], text="")

            r = results[0]
            text = r.text or ""

            if getattr(r, 'time_stamps', None) and len(r.time_stamps) > 0:
                result_segments = self._build_segments(r.time_stamps, granularity)
            else:
                if text:
                    result_segments.append(backend_pb2.TranscriptSegment(
                        id=0, start=0, end=0, text=text
                    ))
        except Exception as err:
            print(f"Error in AudioTranscription: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

        return backend_pb2.TranscriptResult(segments=result_segments, text=text)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    args = parser.parse_args()
    serve(args.addr)