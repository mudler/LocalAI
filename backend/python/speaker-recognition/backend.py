#!/usr/bin/env python3
"""gRPC server for the LocalAI speaker-recognition backend.

Implements Health / LoadModel / Status plus the voice-specific methods:
VoiceVerify, VoiceAnalyze, VoiceEmbed. The heavy lifting lives in
engines.py — this file is just the gRPC plumbing, mirroring the
insightface backend's two-engine split (SpeechBrain + OnnxDirect).
"""
from __future__ import annotations

import argparse
import os
import signal
import sys
import time
from concurrent import futures

import backend_pb2
import backend_pb2_grpc
import grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
from grpc_auth import get_auth_interceptors  # noqa: E402

from engines import SpeakerEngine, build_engine  # noqa: E402

_ONE_DAY = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))

# ECAPA-TDNN on VoxCeleb is the reference. Threshold is tuned for
# cosine distance (1 - cosine_similarity). Clients may override.
DEFAULT_VERIFY_THRESHOLD = 0.25


def _parse_options(raw: list[str]) -> dict[str, str]:
    out: dict[str, str] = {}
    for entry in raw:
        if ":" not in entry:
            continue
        k, v = entry.split(":", 1)
        out[k.strip()] = v.strip()
    return out


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self) -> None:
        self.engine: SpeakerEngine | None = None
        self.engine_name: str = ""
        self.model_name: str = ""
        self.verify_threshold: float = DEFAULT_VERIFY_THRESHOLD

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", "utf-8"))

    def LoadModel(self, request, context):
        options = _parse_options(list(request.Options))
        # Surface LocalAI's models directory (ModelPath) so engines can
        # anchor relative paths and auto-download into a writable spot
        # alongside every other gallery-managed asset.
        options["_model_path"] = request.ModelPath or ""
        try:
            engine, engine_name = build_engine(request.Model, options)
        except Exception as exc:  # noqa: BLE001
            return backend_pb2.Result(success=False, message=f"engine init failed: {exc}")

        self.engine = engine
        self.engine_name = engine_name
        self.model_name = request.Model

        threshold_opt = options.get("verify_threshold")
        if threshold_opt:
            try:
                self.verify_threshold = float(threshold_opt)
            except ValueError:
                pass
        return backend_pb2.Result(success=True, message=f"loaded {engine_name}")

    def Status(self, request, context):
        state = backend_pb2.StatusResponse.State.READY if self.engine else backend_pb2.StatusResponse.State.UNINITIALIZED
        return backend_pb2.StatusResponse(state=state)

    def _require_engine(self, context) -> SpeakerEngine | None:
        if self.engine is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("no speaker-recognition model loaded")
            return None
        return self.engine

    def VoiceVerify(self, request, context):
        engine = self._require_engine(context)
        if engine is None:
            return backend_pb2.VoiceVerifyResponse()
        if not request.audio1 or not request.audio2:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("audio1 and audio2 are required")
            return backend_pb2.VoiceVerifyResponse()

        threshold = request.threshold if request.threshold > 0 else self.verify_threshold
        started = time.time()
        try:
            distance = engine.compare(request.audio1, request.audio2)
        except Exception as exc:  # noqa: BLE001
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"voice verify failed: {exc}")
            return backend_pb2.VoiceVerifyResponse()

        elapsed_ms = (time.time() - started) * 1000.0
        # Confidence goes linearly from 100 at distance=0 to 0 at distance=threshold.
        confidence = max(0.0, min(100.0, (1.0 - distance / threshold) * 100.0))
        return backend_pb2.VoiceVerifyResponse(
            verified=distance <= threshold,
            distance=distance,
            threshold=threshold,
            confidence=confidence,
            model=self.model_name,
            processing_time_ms=elapsed_ms,
        )

    def VoiceEmbed(self, request, context):
        engine = self._require_engine(context)
        if engine is None:
            return backend_pb2.VoiceEmbedResponse()
        if not request.audio:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("audio is required")
            return backend_pb2.VoiceEmbedResponse()
        try:
            vec = engine.embed(request.audio)
        except Exception as exc:  # noqa: BLE001
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"voice embed failed: {exc}")
            return backend_pb2.VoiceEmbedResponse()
        return backend_pb2.VoiceEmbedResponse(embedding=list(vec), model=self.model_name)

    def VoiceAnalyze(self, request, context):
        engine = self._require_engine(context)
        if engine is None:
            return backend_pb2.VoiceAnalyzeResponse()
        if not request.audio:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("audio is required")
            return backend_pb2.VoiceAnalyzeResponse()

        actions = list(request.actions) or ["age", "gender", "emotion"]
        try:
            segments = engine.analyze(request.audio, actions)
        except NotImplementedError:
            context.set_code(grpc.StatusCode.UNIMPLEMENTED)
            context.set_details(f"analyze not supported by {self.engine_name}")
            return backend_pb2.VoiceAnalyzeResponse()
        except Exception as exc:  # noqa: BLE001
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"voice analyze failed: {exc}")
            return backend_pb2.VoiceAnalyzeResponse()

        proto_segments = []
        for seg in segments:
            proto_segments.append(
                backend_pb2.VoiceAnalysis(
                    start=seg.get("start", 0.0),
                    end=seg.get("end", 0.0),
                    age=seg.get("age", 0.0),
                    dominant_gender=seg.get("dominant_gender", ""),
                    gender=seg.get("gender", {}),
                    dominant_emotion=seg.get("dominant_emotion", ""),
                    emotion=seg.get("emotion", {}),
                )
            )
        return backend_pb2.VoiceAnalyzeResponse(segments=proto_segments)


def serve(address: str) -> None:
    interceptors = get_auth_interceptors()
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        interceptors=interceptors,
        options=[
            ("grpc.max_send_message_length", 128 * 1024 * 1024),
            ("grpc.max_receive_message_length", 128 * 1024 * 1024),
        ],
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("speaker-recognition backend listening on", address, flush=True)

    def _stop(*_):
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)
    try:
        while True:
            time.sleep(_ONE_DAY)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--addr", default="localhost:50051")
    args = parser.parse_args()
    serve(args.addr)
