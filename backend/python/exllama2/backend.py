#!/usr/bin/env python3
import grpc
from concurrent import futures
import time
import backend_pb2
import backend_pb2_grpc
import argparse
import signal
import sys
import os
import glob

from pathlib import Path
import torch
import torch.nn.functional as F
from torch import version as torch_version


from exllamav2.generator import (
    ExLlamaV2BaseGenerator,
    ExLlamaV2Sampler
)


from exllamav2 import (
    ExLlamaV2,
    ExLlamaV2Config,
    ExLlamaV2Cache,
    ExLlamaV2Cache_8bit,
    ExLlamaV2Tokenizer,
    model_init,
)


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        try:
            model_directory = request.ModelFile

            config = ExLlamaV2Config()
            config.model_dir = model_directory
            config.prepare()

            model = ExLlamaV2(config)

            cache = ExLlamaV2Cache(model, lazy=True)
            model.load_autosplit(cache)

            tokenizer = ExLlamaV2Tokenizer(config)

            # Initialize generator

            generator = ExLlamaV2BaseGenerator(model, cache, tokenizer)

            self.generator = generator

            generator.warmup()
            self.model = model
            self.tokenizer = tokenizer
            self.cache = cache
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Predict(self, request, context):

        penalty = 1.15
        if request.Penalty != 0.0:
            penalty = request.Penalty

        settings = ExLlamaV2Sampler.Settings()
        settings.temperature = request.Temperature
        settings.top_k = request.TopK
        settings.top_p = request.TopP
        settings.token_repetition_penalty = penalty
        settings.disallow_tokens(self.tokenizer, [self.tokenizer.eos_token_id])
        tokens = 512

        if request.Tokens != 0:
            tokens = request.Tokens
        output = self.generator.generate_simple(
            request.Prompt, settings, tokens)

        # Remove prompt from response if present
        if request.Prompt in output:
            output = output.replace(request.Prompt, "")

        return backend_pb2.Result(message=bytes(output, encoding='utf-8'))

    def PredictStream(self, request, context):
        # Implement PredictStream RPC
        # for reply in some_data_generator():
        #    yield reply
        # Not implemented yet
        return self.Predict(request, context)


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
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
