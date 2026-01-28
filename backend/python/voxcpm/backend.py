#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for VoxCPM
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import traceback
import numpy as np
import soundfile as sf
from voxcpm import VoxCPM

import backend_pb2
import backend_pb2_grpc
import torch

import grpc

def is_float(s):
    """Check if a string can be converted to float."""
    try:
        float(s)
        return True
    except ValueError:
        return False

def is_int(s):
    """Check if a string can be converted to int."""
    try:
        int(s)
        return True
    except ValueError:
        return False

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    
    def LoadModel(self, request, context):
        # Get device
        if torch.cuda.is_available():
            print("CUDA is available", file=sys.stderr)
            device = "cuda"
        else:
            print("CUDA is not available", file=sys.stderr)
            device = "cpu"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device = "mps"
        if not torch.cuda.is_available() and request.CUDA:
            return backend_pb2.Result(success=False, message="CUDA is not available")

        # Normalize potential 'mpx' typo to 'mps'
        if device == "mpx":
            print("Note: device 'mpx' detected, treating it as 'mps'.", file=sys.stderr)
            device = "mps"
        
        # Validate mps availability if requested
        if device == "mps" and not torch.backends.mps.is_available():
            print("Warning: MPS not available. Falling back to CPU.", file=sys.stderr)
            device = "cpu"

        self.device = device

        options = request.Options

        # empty dict
        self.options = {}

        # The options are a list of strings in this form optname:optvalue
        # We are storing all the options in a dict so we can use it later when
        # generating the audio
        for opt in options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)  # Split only on first colon
            # if value is a number, convert it to the appropriate type
            if is_float(value):
                value = float(value)
            elif is_int(value):
                value = int(value)
            elif value.lower() in ["true", "false"]:
                value = value.lower() == "true"
            self.options[key] = value

        # Get model path from request
        model_path = request.Model
        if not model_path:
            model_path = "openbmb/VoxCPM1.5"
        
        try:
            print(f"Loading model from {model_path}", file=sys.stderr)
            self.model = VoxCPM.from_pretrained(model_path)
            print(f"Model loaded successfully on device: {self.device}", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        try:
            # Get generation parameters from options with defaults
            cfg_value = self.options.get("cfg_value", 2.0)
            inference_timesteps = self.options.get("inference_timesteps", 10)
            normalize = self.options.get("normalize", False)
            denoise = self.options.get("denoise", False)
            retry_badcase = self.options.get("retry_badcase", True)
            retry_badcase_max_times = self.options.get("retry_badcase_max_times", 3)
            retry_badcase_ratio_threshold = self.options.get("retry_badcase_ratio_threshold", 6.0)
            use_streaming = self.options.get("streaming", False)

            # Handle voice cloning via prompt_wav_path and prompt_text
            prompt_wav_path = None
            prompt_text = None

            # Priority: request.voice > AudioPath > options
            if hasattr(request, 'voice') and request.voice:
                # If voice is provided, try to use it as a path
                if os.path.exists(request.voice):
                    prompt_wav_path = request.voice
                elif hasattr(request, 'ModelFile') and request.ModelFile:
                    model_file_base = os.path.dirname(request.ModelFile)
                    potential_path = os.path.join(model_file_base, request.voice)
                    if os.path.exists(potential_path):
                        prompt_wav_path = potential_path
                elif hasattr(request, 'ModelPath') and request.ModelPath:
                    potential_path = os.path.join(request.ModelPath, request.voice)
                    if os.path.exists(potential_path):
                        prompt_wav_path = potential_path

            if hasattr(request, 'AudioPath') and request.AudioPath:
                if os.path.isabs(request.AudioPath):
                    prompt_wav_path = request.AudioPath
                elif hasattr(request, 'ModelFile') and request.ModelFile:
                    model_file_base = os.path.dirname(request.ModelFile)
                    prompt_wav_path = os.path.join(model_file_base, request.AudioPath)
                elif hasattr(request, 'ModelPath') and request.ModelPath:
                    prompt_wav_path = os.path.join(request.ModelPath, request.AudioPath)
                else:
                    prompt_wav_path = request.AudioPath

            # Get prompt_text from options if available
            if "prompt_text" in self.options:
                prompt_text = self.options["prompt_text"]

            # Prepare text
            text = request.text.strip()

            print(f"Generating audio with cfg_value: {cfg_value}, inference_timesteps: {inference_timesteps}, streaming: {use_streaming}", file=sys.stderr)

            # Generate audio
            if use_streaming:
                # Streaming generation
                chunks = []
                for chunk in self.model.generate_streaming(
                    text=text,
                    prompt_wav_path=prompt_wav_path,
                    prompt_text=prompt_text,
                    cfg_value=cfg_value,
                    inference_timesteps=inference_timesteps,
                    normalize=normalize,
                    denoise=denoise,
                    retry_badcase=retry_badcase,
                    retry_badcase_max_times=retry_badcase_max_times,
                    retry_badcase_ratio_threshold=retry_badcase_ratio_threshold,
                ):
                    chunks.append(chunk)
                wav = np.concatenate(chunks)
            else:
                # Non-streaming generation
                wav = self.model.generate(
                    text=text,
                    prompt_wav_path=prompt_wav_path,
                    prompt_text=prompt_text,
                    cfg_value=cfg_value,
                    inference_timesteps=inference_timesteps,
                    normalize=normalize,
                    denoise=denoise,
                    retry_badcase=retry_badcase,
                    retry_badcase_max_times=retry_badcase_max_times,
                    retry_badcase_ratio_threshold=retry_badcase_ratio_threshold,
                )

            # Get sample rate from model
            sample_rate = self.model.tts_model.sample_rate

            # Save output
            sf.write(request.dst, wav, sample_rate)
            print(f"Saved output to {request.dst}", file=sys.stderr)

        except Exception as err:
            print(f"Error in TTS: {err}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
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
