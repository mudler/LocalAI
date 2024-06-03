#!/usr/bin/env python3
from concurrent import futures
import time
import argparse
import signal
import sys
import os

import backend_pb2
import backend_pb2_grpc

import grpc
import torch
from transformers import AutoTokenizer
from petals import AutoDistributedModelForCausalLM

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer that implements the Backend service defined in backend.proto.
    """
    def Health(self, request, context):
        """
        Returns a health check message.

        Args:
            request: The health check request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The health check reply.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        Loads a language model.

        Args:
            request: The load model request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The load model result.
        """
        try:
            self.tokenizer = AutoTokenizer.from_pretrained(request.Model, use_fast=False, add_bos_token=False)
            self.model = AutoDistributedModelForCausalLM.from_pretrained(request.Model)
            self.cuda = False
            if request.CUDA:
                self.model = self.model.cuda()
                self.cuda = True

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The predict result.
        """

        inputs = self.tokenizer(request.Prompt, return_tensors="pt")["input_ids"]
        if self.cuda:
            inputs = inputs.cuda()
 
        if request.Tokens == 0:
            # Max to max value if tokens are not specified
            request.Tokens = 8192

        # TODO: kwargs and map all parameters
        outputs = self.model.generate(inputs, max_new_tokens=request.Tokens)

        generated_text = self.tokenizer.decode(outputs[0])
        # Remove prompt from response if present
        if request.Prompt in generated_text:
            generated_text = generated_text.replace(request.Prompt, "")

        return backend_pb2.Result(message=bytes(generated_text, encoding='utf-8'))

    def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The predict stream result.
        """
        # Implement PredictStream RPC
        #for reply in some_data_generator():
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
