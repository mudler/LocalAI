"""
This is the extra gRPC server of LocalAI
"""

from __future__ import annotations
from typing import List
from concurrent import futures
import time
import argparse
import signal
import sys
import os

import grpc
import backend_pb2
import backend_pb2_grpc

from ctransformers import AutoModelForCausalLM

# Adapted from https://github.com/marella/ctransformers/tree/main#supported-models
# License: MIT
# Adapted by AIsuko
class ModelType:
    GPT = "gpt2"
    GPT_J_GPT4_ALL_J= "gptj"
    GPT_NEOX_STABLE_LM = "gpt_neox"
    FALCON= "falcon"
    LLaMA_LLaMA2 = "llama"
    MPT="mpt"
    STAR_CODER_CHAT="gpt_bigcode"
    DOLLY_V2="dolly-v2"
    REPLIT="replit"

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    
    def LoadModel(self, request, context):
        try:
            model_path = request.Model
            if not os.path.exists(model_path):
                return backend_pb2.Result(success=False, message=f"Model path {model_path} does not exist")
            model_type = request.ModelType
            if model_type not in ModelType.__dict__.values():
                return backend_pb2.Result(success=False, message=f"Model type {model_type} not supported")
            
            llm = AutoModelForCausalLM.from_pretrained(model_file=model_path, model_type=model_type)
            self.model=llm
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Predict(self, request, context):
        return super().Predict(request, context)

    def PredictStream(self, request, context):
        return super().PredictStream(request, context)

    def TokenizeString(self, request, context):
        try:
            tokens: List[int]=self.model.tokenize(request.prompt, add_bos_token=False)
            l=len(tokens)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.TokenizationResponse(length=l, tokens=tokens)

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