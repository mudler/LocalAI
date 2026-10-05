#!/usr/bin/env python3
# SPDX-License-Identifier: MIT

import argparse
import os
import platform
import signal
import subprocess
import sys
import traceback
from concurrent import futures

import grpc

import backend_pb2
import backend_pb2_grpc
from mlx_video_utils import build_generation_command, classify_model, parse_options

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
from grpc_auth import get_auth_interceptors  # noqa: E402


MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self):
        self.family = None
        self.model = None
        self.options = {}

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        if platform.system() != "Darwin" or platform.machine() != "arm64":
            return self._fail(
                context,
                grpc.StatusCode.FAILED_PRECONDITION,
                "mlx-video requires macOS on Apple Silicon",
            )

        model = (
            request.ModelFile
            if request.ModelFile and os.path.isdir(request.ModelFile)
            else request.Model
        )
        family = classify_model(model)
        if family is None:
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "mlx-video supports LTX-2 and converted Wan2.1/Wan2.2 MLX checkpoints",
            )
        if family == "wan" and not os.path.isdir(model):
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "Wan models must be converted to MLX and configured as a local model directory",
            )

        self.family = family
        self.model = model
        self.options = parse_options(request.Options)
        return backend_pb2.Result(message="mlx-video model configured", success=True)

    def Free(self, request, context):
        self.family = None
        self.model = None
        self.options = {}
        return backend_pb2.Result(message="Model released", success=True)

    def GenerateVideo(self, request, context):
        if not self.family or not self.model:
            return self._fail(
                context, grpc.StatusCode.FAILED_PRECONDITION, "model is not loaded"
            )
        if not request.prompt.strip():
            return self._fail(context, grpc.StatusCode.INVALID_ARGUMENT, "prompt is required")
        if not request.dst:
            return self._fail(
                context,
                grpc.StatusCode.INVALID_ARGUMENT,
                "output destination is required",
            )

        process = None
        try:
            command = build_generation_command(
                family=self.family,
                model=self.model,
                prompt=request.prompt,
                destination=request.dst,
                negative_prompt=request.negative_prompt,
                start_image=request.start_image,
                end_image=request.end_image,
                audio=request.audio,
                width=request.width,
                height=request.height,
                num_frames=request.num_frames,
                fps=request.fps,
                seed=request.seed,
                steps=request.step,
                cfg_scale=request.cfg_scale,
                params={**self.options, **dict(request.params)},
            )
            os.makedirs(
                os.path.dirname(request.dst) or ".", mode=0o750, exist_ok=True
            )
            process = subprocess.Popen(command)
            if hasattr(context, "add_callback"):
                context.add_callback(
                    lambda: process.poll() is None and process.terminate()
                )
            return_code = process.wait()
            if return_code != 0:
                raise RuntimeError(
                    f"upstream generator exited with status {return_code}"
                )
            if not os.path.isfile(request.dst) or os.path.getsize(request.dst) == 0:
                raise RuntimeError("upstream generator did not create the requested video")
            return backend_pb2.Result(message="Video generated successfully", success=True)
        except ValueError as err:
            return self._fail(context, grpc.StatusCode.INVALID_ARGUMENT, str(err))
        except Exception as err:
            print(f"mlx-video generation failed: {err}", file=sys.stderr)
            traceback.print_exc()
            return self._fail(
                context,
                grpc.StatusCode.INTERNAL,
                f"mlx-video generation failed: {err}",
            )

    @staticmethod
    def _fail(context, code, message):
        context.set_code(code)
        context.set_details(message)
        return backend_pb2.Result(message=message, success=False)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 64 * 1024 * 1024),
            ("grpc.max_send_message_length", 64 * 1024 * 1024),
            ("grpc.max_receive_message_length", 64 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print(f"MLX Video backend listening on {address}", file=sys.stderr)

    def stop_server(signum, frame):
        del signum, frame
        server.stop(0)

    signal.signal(signal.SIGINT, stop_server)
    signal.signal(signal.SIGTERM, stop_server)
    server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the MLX Video gRPC backend")
    parser.add_argument("--addr", default="localhost:50051")
    serve(parser.parse_args().addr)
