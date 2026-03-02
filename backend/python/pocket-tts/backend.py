#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Pocket TTS
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import traceback
import scipy.io.wavfile
import backend_pb2
import backend_pb2_grpc
import torch
from pocket_tts import TTSModel

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

        # Default voice for caching
        self.default_voice_url = self.options.get("default_voice", None)
        self._voice_cache = {}

        try:
            print("Loading Pocket TTS model", file=sys.stderr)
            self.tts_model = TTSModel.load_model()
            print(f"Model loaded successfully. Sample rate: {self.tts_model.sample_rate}", file=sys.stderr)

            # Pre-load default voice if specified
            if self.default_voice_url:
                try:
                    print(f"Pre-loading default voice: {self.default_voice_url}", file=sys.stderr)
                    voice_state = self.tts_model.get_state_for_audio_prompt(self.default_voice_url)
                    self._voice_cache[self.default_voice_url] = voice_state
                    print("Default voice loaded successfully", file=sys.stderr)
                except Exception as e:
                    print(f"Warning: Failed to pre-load default voice: {e}", file=sys.stderr)

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _get_voice_state(self, voice_input):
        """
        Get voice state from cache or load it.
        voice_input can be:
        - HuggingFace URL (e.g., hf://kyutai/tts-voices/alba-mackenna/casual.wav)
        - Local file path
        - None (use default)
        """
        # Use default if no voice specified
        if not voice_input:
            voice_input = self.default_voice_url

        if not voice_input:
            return None

        # Check cache first
        if voice_input in self._voice_cache:
            return self._voice_cache[voice_input]

        # Load voice state
        try:
            print(f"Loading voice from: {voice_input}", file=sys.stderr)
            voice_state = self.tts_model.get_state_for_audio_prompt(voice_input)
            self._voice_cache[voice_input] = voice_state
            return voice_state
        except Exception as e:
            print(f"Error loading voice from {voice_input}: {e}", file=sys.stderr)
            return None

    def TTS(self, request, context):
        try:
            # Determine voice input
            # Priority: request.voice > AudioPath (from ModelOptions) > default
            voice_input = None
            
            if request.voice:
                voice_input = request.voice
            elif hasattr(request, 'AudioPath') and request.AudioPath:
                # Use AudioPath as voice file
                if os.path.isabs(request.AudioPath):
                    voice_input = request.AudioPath
                elif hasattr(request, 'ModelFile') and request.ModelFile:
                    model_file_base = os.path.dirname(request.ModelFile)
                    voice_input = os.path.join(model_file_base, request.AudioPath)
                elif hasattr(request, 'ModelPath') and request.ModelPath:
                    voice_input = os.path.join(request.ModelPath, request.AudioPath)
                else:
                    voice_input = request.AudioPath

            # Get voice state
            print(f"DEBUG: voice_input={voice_input}", file=sys.stderr)
            voice_state = self._get_voice_state(voice_input)
            print(f"DEBUG: voice_state={voice_state}", file=sys.stderr)
            if voice_state is None:
                return backend_pb2.Result(
                    success=False,
                    message=f"Voice not found or failed to load: {voice_input}. Please provide a valid voice URL or file path."
                )

            # Prepare text
            text = request.text.strip()

            if not text:
                return backend_pb2.Result(
                    success=False,
                    message="Text is empty"
                )

            print(f"Generating audio for text: {text[:50]}...", file=sys.stderr)

            # Generate audio
            audio = self.tts_model.generate_audio(voice_state, text)

            # Audio is a 1D torch tensor containing PCM data
            if audio is None or audio.numel() == 0:
                return backend_pb2.Result(
                    success=False,
                    message="No audio generated"
                )

            # Save audio to file
            output_path = request.dst
            if not output_path:
                output_path = "/tmp/pocket-tts-output.wav"

            # Ensure output directory exists
            output_dir = os.path.dirname(output_path)
            if output_dir and not os.path.exists(output_dir):
                os.makedirs(output_dir, exist_ok=True)

            # Convert torch tensor to numpy and save
            audio_numpy = audio.numpy()
            scipy.io.wavfile.write(output_path, self.tts_model.sample_rate, audio_numpy)
            print(f"Saved audio to {output_path}", file=sys.stderr)

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
