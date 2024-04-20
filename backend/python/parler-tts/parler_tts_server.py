#!/usr/bin/env python3
"""
Extra gRPC server for MusicgenForConditionalGeneration models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os

import time
import backend_pb2
import backend_pb2_grpc

import grpc

from scipy.io.wavfile import write as write_wav

from parler_tts import ParlerTTSForConditionalGeneration
from transformers import AutoTokenizer
import soundfile as sf  
import torch

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
        device = "cuda:0" if torch.cuda.is_available() else "cpu"
        try:
            self.model = ParlerTTSForConditionalGeneration.from_pretrained(model_name).to(device)
            self.tokenizer = AutoTokenizer.from_pretrained(model_name)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        model_name = request.model
        voice = request.voice
        if voice == "":
            voice = "A female speaker with a slightly low-pitched voice delivers her words quite expressively, in a very confined sounding environment with clear audio quality. She speaks very fast."
        if model_name == "":
            return backend_pb2.Result(success=False, message="request.model is required")
        try:
            device = "cuda:0" if torch.cuda.is_available() else "cpu"
            input_ids = self.tokenizer(voice, return_tensors="pt").input_ids.to(device)
            prompt_input_ids = self.tokenizer(request.text, return_tensors="pt").input_ids.to(device)
           
            generation = self.model.generate(input_ids=input_ids, prompt_input_ids=prompt_input_ids)
            audio_arr = generation.cpu().numpy().squeeze()
            print("[parler-tts] TTS generated!", file=sys.stderr)
            sf.write(request.dst, audio_arr, self.model.config.sampling_rate)
            print("[parler-tts] TTS saved to", request.dst, file=sys.stderr)
            print("[parler-tts] TTS for", file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("[parler-tts] Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("[parler-tts] Received termination signal. Shutting down...")
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
    print(f"[parler-tts] startup: {args}", file=sys.stderr)
    serve(args.addr)
