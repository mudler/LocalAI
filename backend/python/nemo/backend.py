#!/usr/bin/env python3
"""
gRPC server of LocalAI for NVIDIA NEMO Toolkit ASR.
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
import nemo.collections.asr as nemo_asr

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors



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

        model_name = request.Model or "nvidia/parakeet-tdt-0.6b-v3"

        try:
            print(f"Loading NEMO ASR model from {model_name}", file=sys.stderr)
            self.model = nemo_asr.models.ASRModel.from_pretrained(model_name=model_name)
            print("NEMO ASR model loaded successfully", file=sys.stderr)
        except Exception as err:
            print(f"[ERROR] LoadModel failed: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _get_stride_seconds(self):
        """Compute the seconds-per-frame stride for the loaded model.

        stride = preprocessor_window_stride * encoder_subsampling_factor
        """
        try:
            preprocessor = self.model.preprocessor
            window_stride = preprocessor._cfg.get('window_stride', 0.01)
            subsampling_factor = getattr(self.model.encoder, 'subsampling_factor', 8)
            return window_stride * subsampling_factor
        except (AttributeError, KeyError, TypeError) as err:
            print(
                f"Warning: could not compute stride from model config ({err}), "
                f"falling back to 0.08s/frame",
                file=sys.stderr,
            )
            return 0.08

    def _build_segments_with_words(self, hypothesis, stride, timestamp_granularities=None):
        """Build TranscriptSegment list from a NeMo Hypothesis with timestamps.

        Supports two granularity modes:
          - "word": one TranscriptSegment per word, each with a single TranscriptWord entry
          - "segment" (default): merge consecutive words into sentence-level segments,
            splitting at word-level time gaps that exceed a dynamic threshold.
        """
        if not hypothesis or not isinstance(hypothesis.timestamp, dict):
            return []

        word_offsets = hypothesis.timestamp.get('word', [])
        if not word_offsets:
            return []

        granularities = list(timestamp_granularities) if timestamp_granularities else []
        granularity = "word" if "word" in granularities else "segment"

        # Build a flat list of (text, start_ns, end_ns) from NeMo word offsets
        transcript_words = []
        for wo in word_offsets:
            word_text = wo.get('word', '')
            if not word_text:
                continue
            start_offset = wo.get('start_offset', 0)
            end_offset = wo.get('end_offset', start_offset)
            start_ns = int(start_offset * stride * 1_000_000_000)
            end_ns = int(end_offset * stride * 1_000_000_000)
            transcript_words.append({
                'text': word_text,
                'start': start_ns,
                'end': end_ns,
            })

        if not transcript_words:
            return []

        if granularity == "word":
            # One segment per word
            result = []
            for idx, tw in enumerate(transcript_words):
                word = backend_pb2.TranscriptWord(
                    start=tw['start'], end=tw['end'], text=tw['text']
                )
                result.append(backend_pb2.TranscriptSegment(
                    id=idx,
                    start=tw['start'],
                    end=tw['end'],
                    text=tw['text'],
                    words=[word],
                ))
            return result

        # segment mode — merge at word-level time-gap boundaries
        # Compute gap threshold: median inter-word gap * 3, clamped to [0.3, 2.0]s
        gaps = []
        for i in range(1, len(transcript_words)):
            gap = (transcript_words[i]['start'] - transcript_words[i - 1]['end']) / 1_000_000_000
            if gap > 0:
                gaps.append(gap)
        if gaps:
            gaps.sort()
            median_gap = gaps[len(gaps) // 2]
            threshold_ns = int(max(0.3, min(median_gap * 3, 2.0)) * 1_000_000_000)
        else:
            threshold_ns = int(0.5 * 1_000_000_000)

        result = []
        buf_words = []  # list of TranscriptWord protobuf
        buf_start = None
        buf_end = 0
        buf_text = []
        prev_end = None

        for tw in transcript_words:
            # Detect word-level time gap
            if prev_end is not None and (tw['start'] - prev_end) >= threshold_ns and buf_text:
                seg_text = ' '.join(buf_text)
                result.append(backend_pb2.TranscriptSegment(
                    id=len(result),
                    start=buf_start,
                    end=buf_end,
                    text=seg_text,
                    words=list(buf_words),
                ))
                buf_words = []
                buf_text = []
                buf_start = None

            if buf_start is None:
                buf_start = tw['start']
            buf_end = tw['end']
            buf_text.append(tw['text'])
            buf_words.append(backend_pb2.TranscriptWord(
                start=tw['start'], end=tw['end'], text=tw['text']
            ))
            prev_end = tw['end']

        # flush remaining
        if buf_text and buf_start is not None:
            seg_text = ' '.join(buf_text)
            result.append(backend_pb2.TranscriptSegment(
                id=len(result),
                start=buf_start,
                end=buf_end,
                text=seg_text,
                words=list(buf_words),
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

            # Determine requested timestamp granularity
            timestamp_granularities = list(request.timestamp_granularities) if request.timestamp_granularities else []
            want_timestamps = bool(timestamp_granularities)

            if want_timestamps:
                # Request timestamps from NeMo.
                # timestamps=True forces NeMo to return Hypothesis objects with
                # the timestamp dict populated, so we omit return_hypotheses to
                # let NeMo choose the correct return type.
                results = self.model.transcribe([audio_path], timestamps=True)

                if results and len(results) > 0:
                    hypotheses = results[0] if isinstance(results[0], list) else results
                    if hypotheses and len(hypotheses) > 0:
                        hypothesis = hypotheses[0]

                        # Hypothesis object should have .timestamp populated
                        if not hasattr(hypothesis, 'timestamp') or not isinstance(hypothesis.timestamp, dict):
                            print(
                                "Warning: timestamps were requested but NeMo did not return "
                                "Hypothesis objects; falling back to untimestamped output",
                                file=sys.stderr,
                            )

                        # Extract text
                        if hasattr(hypothesis, 'text'):
                            text = hypothesis.text or ""
                        elif isinstance(hypothesis, str):
                            text = hypothesis

                        # Build segments with word-level timestamps
                        stride = self._get_stride_seconds()
                        result_segments = self._build_segments_with_words(
                            hypothesis, stride, timestamp_granularities
                        )

                        # If no word offsets but we have text, fall back to single segment
                        if not result_segments and text:
                            result_segments.append(backend_pb2.TranscriptSegment(
                                id=0, start=0, end=0, text=text
                            ))
            else:
                # Simple transcription without timestamps
                # NEMO's transcribe method accepts a list of audio paths and returns a list of transcripts
                results = self.model.transcribe([audio_path])

                if results and len(results) > 0:
                    # Get the transcript text from the first result.
                    # CTC models return List[str], TDT/RNNT models return List[Hypothesis]
                    # where the actual text lives in Hypothesis.text.
                    result = results[0]
                    if isinstance(result, str):
                        text = result
                    else:
                        text = getattr(result, 'text', None) or ""

                    if text:
                        # Create a single segment with the full transcription
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
