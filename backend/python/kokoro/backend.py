#!/usr/bin/env python3
"""
Extra gRPC server for Kokoro models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
import time
import backend_pb2
import backend_pb2_grpc
import soundfile as sf
import grpc

from models import build_model
from kokoro import generate
import torch

SAMPLE_RATE = 22050
_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer for the backend service.

    This class implements the gRPC methods for the backend service, including Health, LoadModel, and Embedding.
    """
    def Health(self, request, context):
        """
        A gRPC method that returns the health status of the backend service.

        Args:
            request: A HealthRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Reply object that contains the health status of the backend service.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        A gRPC method that loads a model into memory.

        Args:
            request: A LoadModelRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Result object that contains the result of the LoadModel operation.
        """
        model_name = request.Model
        try:
            device = "cuda:0" if torch.cuda.is_available() else "cpu"
            self.MODEL = build_model(request.ModelFile, device)
            options = request.Options
            # Find the voice from the options, options are a list of strings in this form optname:optvalue:
            VOICE_NAME = None
            for opt in options:
                if opt.startswith("voice:"):
                    VOICE_NAME = opt.split(":")[1]
                    break
            if VOICE_NAME is None:
                return backend_pb2.Result(success=False, message=f"No voice specified in options")
            MODELPATH = request.ModelPath
            # If voice name contains a plus, split it and load the two models and combine them
            if "+" in VOICE_NAME:
                voice1, voice2 = VOICE_NAME.split("+")
                voice1 = torch.load(f'{MODELPATH}/{voice1}.pt', weights_only=True).to(device)
                voice2 = torch.load(f'{MODELPATH}/{voice2}.pt', weights_only=True).to(device)
                self.VOICEPACK = torch.mean(torch.stack([voice1, voice2]), dim=0)
            else:
                self.VOICEPACK = torch.load(f'{MODELPATH}/{VOICE_NAME}.pt', weights_only=True).to(device)

            self.VOICE_NAME = VOICE_NAME

            print(f'Loaded voice: {VOICE_NAME}')
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        model_name = request.model
        if model_name == "":
            return backend_pb2.Result(success=False, message="request.model is required")
        try:
            audio, out_ps = generate(self.MODEL, request.text, self.VOICEPACK, lang=self.VOICE_NAME)
            print(out_ps)
            sf.write(request.dst, audio, SAMPLE_RATE)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("[Kokoro] Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("[Kokoro] Received termination signal. Shutting down...")
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
    print(f"[Kokoro] startup: {args}", file=sys.stderr)
    serve(args.addr)
