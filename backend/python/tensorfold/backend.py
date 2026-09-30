#!/usr/bin/env python3
"""LocalAI gRPC backend that runs TensorFold in-process."""
from __future__ import annotations

import argparse
import gc
import os
import queue
import signal
import sys
import threading
from concurrent import futures

import grpc

import backend_pb2
import backend_pb2_grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
from grpc_auth import get_auth_interceptors  # noqa: E402
from python_utils import parse_options  # noqa: E402

from tf_engine import Delta, GenerationCancelled, InvalidRequest, Result  # noqa: E402
from tf_loader import LoadError, load_engine  # noqa: E402
from tf_options import parse_load_config  # noqa: E402
from tf_reply import final_reply, stream_reply  # noqa: E402
from tf_request import build_body  # noqa: E402

# Requests are handled by TensorFold's own scheduler, so more workers than the
# largest --parallel lane count only queue in there. Eight matches its "auto".
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "8"))

_DONE = object()


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def __init__(self, loader=None):
        self._loader = loader or load_engine
        self._engine = None
        self._lock = threading.Lock()

    def Health(self, request, context):
        return backend_pb2.Reply(message=b"OK")

    def LoadModel(self, request, context):
        try:
            if request.ModelPath:
                # Keep Hugging Face downloads (targets and drafters) in LocalAI's
                # models directory rather than the user's home.
                os.environ.setdefault("HF_HOME", os.path.join(request.ModelPath, "huggingface"))
            cfg = parse_load_config(request.Model, request.ContextSize, parse_options(request.Options))
            engine = self._loader(cfg)
        except (ValueError, LoadError) as err:
            print(f"tensorfold load failed: {err}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))
        except Exception as err:  # noqa: BLE001
            print(f"tensorfold load crashed: {err!r}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"tensorfold failed to load {request.Model}: {err}")
        with self._lock:
            self._engine = engine
        return backend_pb2.Result(success=True, message="TensorFold model loaded")

    def _engine_or_fail(self, context):
        with self._lock:
            engine = self._engine
        if engine is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("no model is loaded")
        return engine

    @staticmethod
    def _fail(context, err):
        if isinstance(err, (InvalidRequest, ValueError)):
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
        else:
            context.set_code(grpc.StatusCode.INTERNAL)
        context.set_details(str(err) or type(err).__name__)

    @staticmethod
    def _cancel_flag(context) -> threading.Event:
        gone = threading.Event()
        # add_callback returns False when the RPC has already terminated; the
        # callback then never fires, so the request is treated as cancelled.
        if not context.add_callback(gone.set):
            gone.set()
        return gone

    def Predict(self, request, context):
        engine = self._engine_or_fail(context)
        if engine is None:
            return backend_pb2.Reply()
        gone = self._cancel_flag(context)
        try:
            built = build_body(request)
            result = engine.generate(built.body, built.chat, None, gone.is_set)
        except GenerationCancelled:
            return backend_pb2.Reply()
        except Exception as err:  # noqa: BLE001
            self._fail(context, err)
            return backend_pb2.Reply()
        return final_reply(result, with_text=True)

    def PredictStream(self, request, context):
        engine = self._engine_or_fail(context)
        if engine is None:
            return
        gone = self._cancel_flag(context)
        try:
            built = build_body(request)
        except Exception as err:  # noqa: BLE001
            self._fail(context, err)
            return

        # Generation blocks its thread and reports through a callback, so it
        # runs in a worker and this generator drains a queue.
        items: queue.Queue = queue.Queue()

        def work():
            try:
                items.put(engine.generate(built.body, built.chat, items.put, gone.is_set))
            except BaseException as err:  # noqa: BLE001
                items.put(err)
            finally:
                items.put(_DONE)

        threading.Thread(target=work, name="tensorfold-generate", daemon=True).start()
        while True:
            item = items.get()
            if item is _DONE:
                return
            if isinstance(item, Delta):
                yield stream_reply(item)
            elif isinstance(item, Result):
                yield final_reply(item, with_text=False)
            elif isinstance(item, GenerationCancelled):
                return
            elif isinstance(item, BaseException):
                self._fail(context, item)
                return

    def TokenizeString(self, request, context):
        engine = self._engine_or_fail(context)
        if engine is None:
            return backend_pb2.TokenizationResponse()
        try:
            tokens = engine.tokenize(request.Prompt)
        except Exception as err:  # noqa: BLE001
            self._fail(context, err)
            return backend_pb2.TokenizationResponse()
        return backend_pb2.TokenizationResponse(length=len(tokens), tokens=tokens)

    def Embedding(self, request, context):
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("TensorFold does not serve embeddings")
        return backend_pb2.EmbeddingResult()

    def Free(self, request, context):
        with self._lock:
            engine, self._engine = self._engine, None
        try:
            if engine is not None:
                engine.close()
            del engine
            gc.collect()
            self._release_device_memory()
        except Exception as err:  # noqa: BLE001
            return backend_pb2.Result(success=False, message=str(err))
        return backend_pb2.Result(success=True, message="TensorFold model freed")

    @staticmethod
    def _release_device_memory():
        try:
            import mlx.core as mx

            mx.clear_cache()
        except Exception:  # noqa: BLE001 - not installed on the CUDA profile
            pass
        try:
            import torch

            if torch.cuda.is_available():
                torch.cuda.empty_cache()
        except Exception:  # noqa: BLE001 - not installed on the MLX profile
            pass


def serve(address: str) -> None:
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 50 * 1024 * 1024),
            ("grpc.max_send_message_length", 50 * 1024 * 1024),
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),
        ],
        interceptors=get_auth_interceptors(),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    def stop(*_):
        server.stop(5)

    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, stop)
    server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    serve(parser.parse_args().addr)
