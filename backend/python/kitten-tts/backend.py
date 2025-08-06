#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Kitten TTS
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import backend_pb2
import backend_pb2_grpc

import torch
from kittentts import KittenTTS
import soundfile as sf

import grpc


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))
KITTEN_LANGUAGE = os.environ.get('KITTEN_LANGUAGE', None)

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):

        # Get device
        # device = "cuda" if request.CUDA else "cpu"
        if torch.cuda.is_available():
            print("CUDA is available", file=sys.stderr)
            device = "cuda"
        else:
            print("CUDA is not available", file=sys.stderr)
            device = "cpu"

        if not torch.cuda.is_available() and request.CUDA:
            return backend_pb2.Result(success=False, message="CUDA is not available")

        self.AudioPath = None
        # List available KittenTTS models
        print("Available KittenTTS voices: expr-voice-2-m, expr-voice-2-f, expr-voice-3-m, expr-voice-3-f, expr-voice-4-m, expr-voice-4-f, expr-voice-5-m, expr-voice-5-f")
        if os.path.isabs(request.AudioPath):
            self.AudioPath = request.AudioPath
        elif request.AudioPath and request.ModelFile != "" and not os.path.isabs(request.AudioPath):
            # get base path of modelFile
            modelFileBase = os.path.dirname(request.ModelFile)
            # modify LoraAdapter to be relative to modelFileBase
            self.AudioPath = os.path.join(modelFileBase, request.AudioPath)

        try:
            print("Preparing KittenTTS model, please wait", file=sys.stderr)
            # Use the model name from request.Model, defaulting to "KittenML/kitten-tts-nano-0.1" if not specified
            model_name = request.Model if request.Model else "KittenML/kitten-tts-nano-0.1"
            self.tts = KittenTTS(model_name)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        try:
            # KittenTTS doesn't use language parameter like TTS, so we ignore it
            # For multi-speaker models, use voice parameter
            voice = request.voice if request.voice else "expr-voice-2-f"
            
            # Generate audio using KittenTTS
            audio = self.tts.generate(request.text, voice=voice)
            
            # Save the audio using soundfile
            sf.write(request.dst, audio, 24000)
            
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
