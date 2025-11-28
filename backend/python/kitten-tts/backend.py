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
import pathlib
import logging
from collections import defaultdict
from datetime import datetime, timedelta

import grpc

# Configure structured logging for security audit trails
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    handlers=[
        logging.StreamHandler(sys.stderr)
    ]
)
logger = logging.getLogger('KittenTTS-Backend')


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))
KITTEN_LANGUAGE = os.environ.get('KITTEN_LANGUAGE', None)

# Rate limiting configuration
REQUESTS_PER_MINUTE = int(os.environ.get('TTS_REQUESTS_PER_MINUTE', '10'))
REQUEST_TIMEOUT_SECONDS = int(os.environ.get('TTS_REQUEST_TIMEOUT_SECONDS', '300'))

class RateLimiter:
    """Simple rate limiter to prevent DoS attacks via excessive requests."""
    def __init__(self, max_requests, window_seconds):
        self.max_requests = max_requests
        self.window_seconds = window_seconds
        self.requests = defaultdict(list)
    
    def is_allowed(self, client_id):
        """Check if a client is allowed to make a request."""
        now = time.time()
        # Clean old requests
        self.requests[client_id] = [
            req_time for req_time in self.requests[client_id]
            if now - req_time < self.window_seconds
        ]
        # Check if limit exceeded
        if len(self.requests[client_id]) >= self.max_requests:
            return False
        # Record this request
        self.requests[client_id].append(now)
        return True

# Global rate limiter instance
rate_limiter = RateLimiter(REQUESTS_PER_MINUTE, 60)

def validate_output_path(path):
    """Validate output path to prevent path traversal and shell injection attacks."""
    if not path:
        raise ValueError("Output path cannot be empty")
    
    # Check for null bytes which could be used in attacks
    if '\x00' in path:
        raise ValueError("Path contains null bytes")
    
    # Ensure it's a valid filename without shell metacharacters that could be used in command injection
    invalid_chars = ['$', '`', ';', '|', '&', '>', '<', '(', ')', '{', '}', '[', ']', '\n', '\r']
    for char in invalid_chars:
        if char in path:
            raise ValueError(f"Path contains invalid character: {char}")
    
    # Prevent path traversal attacks by checking for ".."
    if ".." in path:
        raise ValueError("Path traversal detected")
    
    # Convert to Path object for validation
    path_obj = pathlib.Path(path)
    
    return str(path_obj)

def validate_text_input(text):
    """Validate text input to prevent injection attacks."""
    if not text:
        raise ValueError("Text input cannot be empty")
    
    # Check for null bytes
    if '\x00' in text:
        raise ValueError("Text contains null bytes")
    
    return text

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):

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
            # Extract client identifier for rate limiting and logging
            client_id = "unknown"
            try:
                client_id = context.peer() if context else "unknown"
            except:
                pass
            
            # Check rate limit
            if not rate_limiter.is_allowed(client_id):
                logger.warning(f"Rate limit exceeded for client: {client_id}")
                return backend_pb2.Result(success=False, message="Rate limit exceeded")
            
            # Log TTS request for audit trail
            logger.info(f"TTS request from {client_id}: voice={request.voice}, text_length={len(request.text)}")
            
            # KittenTTS doesn't use language parameter like TTS, so we ignore it
            # For multi-speaker models, use voice parameter
            voice = request.voice if request.voice else "expr-voice-2-f"
            
            # Validate inputs to prevent security vulnerabilities
            safe_dst = validate_output_path(request.dst)
            safe_text = validate_text_input(request.text)
            
            # Generate audio using KittenTTS
            audio = self.tts.generate(safe_text, voice=voice)
            
            # Save the audio using soundfile
            sf.write(safe_dst, audio, 24000)
            
            # Log successful TTS generation
            logger.info(f"TTS generation successful for {client_id}: output={safe_dst}")
            
        except ValueError as err:
            logger.warning(f"Validation error for {client_id}: {err}")
            return backend_pb2.Result(success=False, message=f"Invalid input: {str(err)}")
        except Exception as err:
            logger.error(f"Unexpected error during TTS for {client_id}: {err=}, {type(err)=}")
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
