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

from scipy.io import wavfile
from transformers import AutoProcessor, MusicgenForConditionalGeneration

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
            self.processor = AutoProcessor.from_pretrained(model_name)
            self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def SoundGeneration(self, request, context):
        model_name = request.model
        if model_name == "":
            return backend_pb2.Result(success=False, message="request.model is required")
        try:
            self.processor = AutoProcessor.from_pretrained(model_name)
            self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
            inputs = None
            if request.text == "":
                inputs = self.model.get_unconditional_inputs(num_samples=1)
            elif request.HasField('src'):
                # TODO SECURITY CODE GOES HERE LOL
                # WHO KNOWS IF THIS WORKS???
                sample_rate, wsamples = wavfile.read('path_to_your_file.wav')
                
                if request.HasField('src_divisor'):
                    wsamples = wsamples[: len(wsamples) // request.src_divisor]
                
                inputs = self.processor(
                    audio=wsamples,
                    sampling_rate=sample_rate,
                    text=[request.text],
                    padding=True,
                    return_tensors="pt",
                )
            else:
                inputs = self.processor(
                    text=[request.text],
                    padding=True,
                    return_tensors="pt",
                )
            
            tokens = 256
            if request.HasField('duration'):
                tokens = int(request.duration * 51.2) # 256 tokens = 5 seconds, therefore 51.2 tokens is one second
            guidance = 3.0
            if request.HasField('temperature'):
                guidance = request.temperature
            dosample = True
            if request.HasField('sample'):
                dosample = request.sample
            audio_values = self.model.generate(**inputs, do_sample=dosample, guidance_scale=guidance, max_new_tokens=tokens)
            print("[transformers-musicgen] SoundGeneration generated!", file=sys.stderr)
            sampling_rate = self.model.config.audio_encoder.sampling_rate
            wavfile.write(request.dst, rate=sampling_rate, data=audio_values[0, 0].numpy())
            print("[transformers-musicgen] SoundGeneration saved to", request.dst, file=sys.stderr)
            print("[transformers-musicgen] SoundGeneration for", file=sys.stderr)
            print("[transformers-musicgen] SoundGeneration requested tokens", tokens, file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)


# The TTS endpoint is older, and provides fewer features, but exists for compatibility reasons
    def TTS(self, request, context):
        model_name = request.model
        if model_name == "":
            return backend_pb2.Result(success=False, message="request.model is required")
        try:
            self.processor = AutoProcessor.from_pretrained(model_name)
            self.model = MusicgenForConditionalGeneration.from_pretrained(model_name)
            inputs = self.processor(
                text=[request.text],
                padding=True,
                return_tensors="pt",
            )
            tokens = 512 # No good place to set the "length" in TTS, so use 10s as a sane default
            audio_values = self.model.generate(**inputs, max_new_tokens=tokens)
            print("[transformers-musicgen] TTS generated!", file=sys.stderr)
            sampling_rate = self.model.config.audio_encoder.sampling_rate
            write_wav(request.dst, rate=sampling_rate, data=audio_values[0, 0].numpy())
            print("[transformers-musicgen] TTS saved to", request.dst, file=sys.stderr)
            print("[transformers-musicgen] TTS for", file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)


def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("[transformers-musicgen] Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("[transformers-musicgen] Received termination signal. Shutting down...")
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
    print(f"[transformers-musicgen] startup: {args}", file=sys.stderr)
    serve(args.addr)
