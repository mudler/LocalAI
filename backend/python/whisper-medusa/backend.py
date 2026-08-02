#!/usr/bin/env python3
"""LocalAI gRPC backend for aiola Whisper-Medusa speech recognition."""

import argparse
from concurrent import futures
import os
import signal
import sys
import time

import backend_pb2
import backend_pb2_grpc
import grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
from grpc_auth import get_auth_interceptors
from model_utils import resolve_model_reference


SAMPLE_RATE = 16000
MAX_DURATION_SECONDS = 30
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))
_ONE_DAY_IN_SECONDS = 60 * 60 * 24


def _parse_options(raw_options):
    options = {}
    for option in raw_options:
        if ":" not in option:
            continue
        key, value = option.split(":", 1)
        try:
            value = int(value)
        except ValueError:
            try:
                value = float(value)
            except ValueError:
                pass
        options[key] = value
    return options


def _prepare_audio(path, torchaudio):
    waveform, sample_rate = torchaudio.load(path)
    if waveform.shape[0] > 1:
        waveform = waveform.mean(dim=0, keepdim=True)
    if sample_rate != SAMPLE_RATE:
        waveform = torchaudio.transforms.Resample(sample_rate, SAMPLE_RATE)(waveform)
        sample_rate = SAMPLE_RATE
    return waveform, sample_rate


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.model = None
        self.processor = None
        self.device = None
        self.options = {}

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        try:
            import torch
            from transformers import WhisperProcessor
            from whisper_medusa import WhisperMedusaModel

            if request.CUDA and not torch.cuda.is_available():
                return backend_pb2.Result(success=False, message="CUDA is not available")

            self.device = torch.device("cuda" if torch.cuda.is_available() else "cpu")
            if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
                self.device = torch.device("mps")

            self.options = _parse_options(request.Options)
            model_path, local_only = resolve_model_reference(
                request, "aiola/whisper-medusa-linear-libri"
            )
            self.model = WhisperMedusaModel.from_pretrained(
                model_path, local_files_only=local_only
            ).to(self.device)
            self.model.eval()
            self.processor = WhisperProcessor.from_pretrained(
                model_path, local_files_only=local_only
            )
        except Exception as err:
            print(f"Whisper-Medusa model load failed: {err}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(success=True, message="Model loaded successfully")

    def AudioTranscription(self, request, context):
        if self.model is None or self.processor is None:
            return backend_pb2.TranscriptResult(segments=[], text="")

        try:
            import torch
            import torchaudio

            waveform, sample_rate = _prepare_audio(request.dst, torchaudio)
            duration = waveform.shape[-1] / sample_rate
            if duration > MAX_DURATION_SECONDS:
                raise ValueError(
                    f"Whisper-Medusa supports audio clips up to {MAX_DURATION_SECONDS} seconds"
                )

            language = request.language or str(self.options.get("language", "en"))
            regulation_start = int(self.options.get("regulation_start", 140))
            regulation_factor = float(self.options.get("regulation_factor", 1.01))
            features = self.processor(
                waveform.squeeze(), return_tensors="pt", sampling_rate=sample_rate
            ).input_features.to(self.device)
            with torch.inference_mode():
                output = self.model.generate(
                    features,
                    language=language,
                    exponential_decay_length_penalty=(
                        regulation_start,
                        regulation_factor,
                    ),
                )
            text = self.processor.decode(output[0], skip_special_tokens=True).strip()
            segment = backend_pb2.TranscriptSegment(
                id=0,
                start=0,
                end=int(duration * 1_000_000_000),
                text=text,
            )
            return backend_pb2.TranscriptResult(segments=[segment], text=text)
        except Exception as err:
            print(f"Whisper-Medusa transcription failed: {err}", file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print(f"Server started. Listening on: {address}", file=sys.stderr)

    def stop_server(_signal, _frame):
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, stop_server)
    signal.signal(signal.SIGTERM, stop_server)
    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the Whisper-Medusa backend")
    parser.add_argument("--addr", default="localhost:50051")
    serve(parser.parse_args().addr)
