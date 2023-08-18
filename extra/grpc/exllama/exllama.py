#!/usr/bin/env python3
import grpc
from concurrent import futures
import time
import backend_pb2
import backend_pb2_grpc
import argparse
import signal
import sys
import os, glob

from pathlib import Path
import torch
import torch.nn.functional as F
from torch import version as torch_version
from exllama.generator import ExLlamaGenerator
from exllama.model import ExLlama, ExLlamaCache, ExLlamaConfig
from exllama.tokenizer import ExLlamaTokenizer

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    def generate(self,prompt, max_new_tokens):
        self.generator.end_beam_search()

        # Tokenizing the input
        ids = self.generator.tokenizer.encode(prompt)

        self.generator.gen_begin_reuse(ids)
        initial_len = self.generator.sequence[0].shape[0]
        has_leading_space = False
        decoded_text = ''
        for i in range(max_new_tokens):
            token = self.generator.gen_single_token()
            if i == 0 and self.generator.tokenizer.tokenizer.IdToPiece(int(token)).startswith('▁'):
                has_leading_space = True

            decoded_text = self.generator.tokenizer.decode(self.generator.sequence[0][initial_len:])
            if has_leading_space:
                decoded_text = ' ' + decoded_text

            if token.item() == self.generator.tokenizer.eos_token_id:
                break
        return decoded_text
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):
        try:
            # https://github.com/turboderp/exllama/blob/master/example_cfg.py
            model_directory = request.ModelFile

            # Locate files we need within that directory
            tokenizer_path = os.path.join(model_directory, "tokenizer.model")
            model_config_path = os.path.join(model_directory, "config.json")
            st_pattern = os.path.join(model_directory, "*.safetensors")
            model_path = glob.glob(st_pattern)[0]

            # Create config, model, tokenizer and generator

            config = ExLlamaConfig(model_config_path)               # create config from config.json
            config.model_path = model_path                          # supply path to model weights file

            model = ExLlama(config)                                 # create ExLlama instance and load the weights
            tokenizer = ExLlamaTokenizer(tokenizer_path)            # create tokenizer from tokenizer model file

            cache = ExLlamaCache(model, batch_size = 2)             # create cache for inference
            generator = ExLlamaGenerator(model, tokenizer, cache)   # create generator

            self.generator= generator
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
        self.generator.settings.token_repetition_penalty_max = penalty
        self.generator.settings.temperature = request.Temperature
        self.generator.settings.top_k = request.TopK
        self.generator.settings.top_p = request.TopP

        tokens = 512
        if request.Tokens != 0:
            tokens = request.Tokens

        if self.cache.batch_size == 1:
            del self.cache
            self.cache = ExLlamaCache(self.model, batch_size=2)
            self.generator = ExLlamaGenerator(self.model, self.tokenizer, self.cache)

        t = self.generate(request.Prompt, tokens)

        # Remove prompt from response if present
        if request.Prompt in t:
            t = t.replace(request.Prompt, "")

        return backend_pb2.Result(message=bytes(t, encoding='utf-8'))

    def PredictStream(self, request, context):
        # Implement PredictStream RPC
        #for reply in some_data_generator():
        #    yield reply
        # Not implemented yet
        return self.Predict(request, context)


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=1))
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