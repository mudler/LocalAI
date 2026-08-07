#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import asyncio
from concurrent import futures
import os
import signal
import sys

BACKEND_DIR = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, BACKEND_DIR)
sys.path.insert(0, os.path.join(BACKEND_DIR, "common"))
sys.path.insert(0, os.path.join(BACKEND_DIR, "..", "common"))

import backend_pb2
import backend_pb2_grpc
import grpc

from grpc_auth import get_auth_interceptors
from runtime import BackendFailure, MLXAudioRuntime, request_cancelled


MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """Serve exactly one explicitly configured MLX-Audio model role."""

    def __init__(self, runtime=None):
        self.runtime = runtime or MLXAudioRuntime()

    @staticmethod
    def _fail(context, failure):
        code = getattr(grpc.StatusCode, failure.code, grpc.StatusCode.INTERNAL)
        context.set_code(code)
        context.set_details(failure.message)

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    async def LoadModel(self, request, context):
        try:
            role = self.runtime.load(request.Model, request.Options, model_root=request.ModelPath)
        except BackendFailure as failure:
            self._fail(context, failure)
            return backend_pb2.Result(success=False, message=failure.message)
        return backend_pb2.Result(success=True, message=f"MLX-Audio {role} model loaded")

    async def Free(self, request, context):
        self.runtime.unload()
        return backend_pb2.Result(success=True, message="MLX-Audio model unloaded")

    def VAD(self, request, context):
        try:
            segments = self.runtime.vad(request.audio)
        except BackendFailure as failure:
            self._fail(context, failure)
            return backend_pb2.VADResponse()
        return backend_pb2.VADResponse(
            segments=[backend_pb2.VADSegment(start=start, end=end) for start, end in segments]
        )

    def AudioTranscription(self, request, context):
        try:
            text, language = self.runtime.transcribe(request.dst, request.language)
        except BackendFailure as failure:
            self._fail(context, failure)
            return backend_pb2.TranscriptResult()
        return backend_pb2.TranscriptResult(text=text, language=language)

    def TTS(self, request, context):
        try:
            destination = self.runtime.synthesize(
                request.text,
                request.voice,
                request.language,
                request.dst,
                params=request.params,
                cancelled=lambda: request_cancelled(context),
            )
        except BackendFailure as failure:
            self._fail(context, failure)
            return backend_pb2.Result(success=False, message=failure.message)
        return backend_pb2.Result(success=True, message=f"TTS audio written to {destination}")


async def serve(address):
    options = [
        ("grpc.max_message_length", 50 * 1024 * 1024),
        ("grpc.max_send_message_length", 50 * 1024 * 1024),
        ("grpc.max_receive_message_length", 50 * 1024 * 1024),
    ]
    server = grpc.aio.server(
        migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        interceptors=get_auth_interceptors(aio=True),
        options=options,
    )
    servicer = BackendServicer()
    backend_pb2_grpc.add_BackendServicer_to_server(servicer, server)
    server.add_insecure_port(address)
    await server.start()
    print(f"MLX-Audio backend listening on {address}", file=sys.stderr)

    async def shutdown():
        servicer.runtime.unload()
        await server.stop(5)

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, lambda: asyncio.ensure_future(shutdown()))
    try:
        await server.wait_for_termination()
    finally:
        servicer.runtime.unload()


def main():
    parser = argparse.ArgumentParser(description="LocalAI MLX-Audio backend")
    parser.add_argument("--addr", default="localhost:50051")
    args = parser.parse_args()
    asyncio.run(serve(args.addr))


if __name__ == "__main__":
    main()
