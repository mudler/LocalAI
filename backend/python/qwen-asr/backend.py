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

        model_path = request.Model or "Qwen/Qwen3-ASR-1.7B"
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
                language = request.language.strip()

            results = self.model.transcribe(audio=audio_path, language=language)

            if not results:
                return backend_pb2.TranscriptResult(segments=[], text="")

            r = results[0]
            text = r.text or ""

            if getattr(r, 'time_stamps', None) and len(r.time_stamps) > 0:
                for idx, ts in enumerate(r.time_stamps):
                    start_ms = 0
                    end_ms = 0
                    seg_text = text
                    if isinstance(ts, (list, tuple)) and len(ts) >= 3:
                        start_ms = int(float(ts[0]) * 1000) if ts[0] is not None else 0
                        end_ms = int(float(ts[1]) * 1000) if ts[1] is not None else 0
                        seg_text = ts[2] if len(ts) > 2 and ts[2] is not None else ""
                    result_segments.append(backend_pb2.TranscriptSegment(
                        id=idx, start=start_ms, end=end_ms, text=seg_text
                    ))
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
        ])
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
