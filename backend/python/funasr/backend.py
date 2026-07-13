#!/usr/bin/env python3
"""
gRPC backend for LocalAI wrapping FunASR (SenseVoice / Paraformer).
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

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        from funasr import AutoModel

        device = "cpu"
        if request.CUDA and torch.cuda.is_available():
            device = "cuda"
        xpu_available = hasattr(torch, "xpu") and torch.xpu.is_available()
        if xpu_available:
            device = "xpu"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device = "mps"

        model_id = request.Model or "iic/SenseVoiceSmall"

        try:
            print(f"Loading FunASR model: {model_id} on {device}", file=sys.stderr)
            self.model = AutoModel(
                model=model_id,
                vad_model="fsmn-vad",
                device=device,
                disable_update=True,
            )
            print("FunASR model loaded successfully", file=sys.stderr)
        except Exception as err:
            print(f"[ERROR] LoadModel failed: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        from funasr.utils.postprocess_utils import rich_transcription_postprocess

        result_segments = []
        text = ""
        try:
            audio_path = request.dst
            if not audio_path or not os.path.exists(audio_path):
                print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
                return backend_pb2.TranscriptResult(segments=[], text="")

            language = None
            if request.language and request.language.strip():
                language = request.language.strip()

            kwargs = {}
            if language:
                kwargs["language"] = language

            results = self.model.generate(input=audio_path, **kwargs)

            if not results:
                return backend_pb2.TranscriptResult(segments=[], text="")

            for idx, r in enumerate(results):
                seg_text = r.get("text", "") if isinstance(r, dict) else str(r)
                seg_text = rich_transcription_postprocess(seg_text)
                text += seg_text
                result_segments.append(backend_pb2.TranscriptSegment(
                    id=idx,
                    start=0,
                    end=0,
                    text=seg_text,
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
