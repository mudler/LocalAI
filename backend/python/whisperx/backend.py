#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for WhisperX transcription
with speaker diarization, word-level timestamps, and forced alignment.
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import backend_pb2
import backend_pb2_grpc

import grpc


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        import whisperx
        import torch

        device = "cpu"
        if request.CUDA:
            device = "cuda"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device = "mps"

        try:
            print("Preparing WhisperX model, please wait", file=sys.stderr)
            compute_type = "float16" if device != "cpu" else "int8"
            self.model = whisperx.load_model(
                request.Model,
                device,
                compute_type=compute_type,
            )
            self.device = device
            self.model_name = request.Model

            # Store HF token for diarization if available
            self.hf_token = os.environ.get("HF_TOKEN", None)
            self.diarize_pipeline = None

            # Cache for alignment models keyed by language code
            self.align_cache = {}

            print(f"WhisperX model loaded: {request.Model} on {device}", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _get_align_model(self, language_code):
        """Load or return cached alignment model for a given language."""
        import whisperx
        if language_code not in self.align_cache:
            model_a, metadata = whisperx.load_align_model(
                language_code=language_code,
                device=self.device,
            )
            self.align_cache[language_code] = (model_a, metadata)
        return self.align_cache[language_code]

    def AudioTranscription(self, request, context):
        import whisperx

        resultSegments = []
        text = ""
        try:
            audio = whisperx.load_audio(request.dst)

            # Transcribe
            transcript = self.model.transcribe(
                audio,
                batch_size=16,
                language=request.language if request.language else None,
            )

            # Align for word-level timestamps
            model_a, metadata = self._get_align_model(transcript["language"])
            transcript = whisperx.align(
                transcript["segments"],
                model_a,
                metadata,
                audio,
                self.device,
                return_char_alignments=False,
            )

            # Diarize if requested and HF token is available
            if request.diarize and self.hf_token:
                if self.diarize_pipeline is None:
                    self.diarize_pipeline = whisperx.DiarizationPipeline(
                        use_auth_token=self.hf_token,
                        device=self.device,
                    )
                diarize_segments = self.diarize_pipeline(audio)
                transcript = whisperx.assign_word_speakers(diarize_segments, transcript)

            # Build result segments
            for idx, seg in enumerate(transcript["segments"]):
                seg_text = seg.get("text", "")
                start = int(seg.get("start", 0))
                end = int(seg.get("end", 0))
                speaker = seg.get("speaker", "")

                resultSegments.append(backend_pb2.TranscriptSegment(
                    id=idx,
                    start=start,
                    end=end,
                    text=seg_text,
                    speaker=speaker,
                ))
                text += seg_text

        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

        return backend_pb2.TranscriptResult(segments=resultSegments, text=text)

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
        server.stop(0)
        sys.exit(0)

    # Set the signal handlers for SIGINT and SIGTERM
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    serve(args.addr)
