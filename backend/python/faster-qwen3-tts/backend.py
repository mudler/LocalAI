#!/usr/bin/env python3
"""
gRPC server of LocalAI for Faster Qwen3-TTS (CUDA graph capture, voice clone only).
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import traceback
import backend_pb2
import backend_pb2_grpc
import torch
import soundfile as sf

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
        if not torch.cuda.is_available():
            return backend_pb2.Result(
                success=False,
                message="faster-qwen3-tts requires NVIDIA GPU with CUDA"
            )

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

        model_path = request.Model or "Qwen/Qwen3-TTS-12Hz-0.6B-Base"
        self.audio_path = request.AudioPath if hasattr(request, 'AudioPath') and request.AudioPath else None
        self.model_file = request.ModelFile if hasattr(request, 'ModelFile') and request.ModelFile else None
        self.model_path = request.ModelPath if hasattr(request, 'ModelPath') and request.ModelPath else None

        from faster_qwen3_tts import FasterQwen3TTS
        print(f"Loading model from: {model_path}", file=sys.stderr)
        try:
            self.model = FasterQwen3TTS.from_pretrained(model_path)
        except Exception as e:
            print(f"[ERROR] Loading model: {type(e).__name__}: {e}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(e))

        print(f"Model loaded successfully: {model_path}", file=sys.stderr)
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _get_ref_audio_path(self, request):
        if not self.audio_path:
            return None
        if os.path.isabs(self.audio_path):
            return self.audio_path
        if self.model_file:
            model_file_base = os.path.dirname(self.model_file)
            ref_path = os.path.join(model_file_base, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path
        if self.model_path:
            ref_path = os.path.join(self.model_path, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path
        return self.audio_path

    def TTS(self, request, context):
        try:
            if not request.dst:
                return backend_pb2.Result(
                    success=False,
                    message="dst (output path) is required"
                )
            text = request.text.strip()
            if not text:
                return backend_pb2.Result(
                    success=False,
                    message="Text is empty"
                )

            language = request.language if hasattr(request, 'language') and request.language else None
            if not language or language == "":
                language = "English"

            ref_audio = self._get_ref_audio_path(request)
            if not ref_audio:
                return backend_pb2.Result(
                    success=False,
                    message="AudioPath is required for voice clone (set in LoadModel)"
                )
            ref_text = self.options.get("ref_text")
            if not ref_text and hasattr(request, 'ref_text') and request.ref_text:
                ref_text = request.ref_text
            if not ref_text:
                return backend_pb2.Result(
                    success=False,
                    message="ref_text is required for voice clone (set via LoadModel Options, e.g. ref_text:Your reference transcript)"
                )

            chunk_size = self.options.get("chunk_size")
            generation_kwargs = {}
            if chunk_size is not None:
                generation_kwargs["chunk_size"] = int(chunk_size)

            audio_list, sr = self.model.generate_voice_clone(
                text=text,
                language=language,
                ref_audio=ref_audio,
                ref_text=ref_text,
                **generation_kwargs
            )

            if audio_list is None or (isinstance(audio_list, list) and len(audio_list) == 0):
                return backend_pb2.Result(
                    success=False,
                    message="No audio output generated"
                )
            audio_data = audio_list[0] if isinstance(audio_list, list) else audio_list
            sf.write(request.dst, audio_data, sr)
            print(f"Saved output to {request.dst}", file=sys.stderr)

        except Exception as err:
            print(f"Error in TTS: {err}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        return backend_pb2.Result(success=True)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ]
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
