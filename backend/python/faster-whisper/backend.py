#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Bark TTS
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import torch
import backend_pb2
import backend_pb2_grpc

from faster_whisper import WhisperModel

import grpc


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))
COQUI_LANGUAGE = os.environ.get('COQUI_LANGUAGE', None)

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    def LoadModel(self, request, context):
        device = "cpu"
        precision = "float32"
        # Get device
        # device = "cuda" if request.CUDA else "cpu"
        # Detecting CUDA availability using Torch.
        if (request.CUDA & torch.cuda.is_available()):
            device = "cuda"
            precision="float16"

        try:
            print("Preparing models, please wait", file=sys.stderr)
            self.model = WhisperModel(request.Model, device=device, compute_type=precision)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        resultSegments = []
        text = ""
        try:
            segments, info = self.model.transcribe(request.dst, beam_size=5, condition_on_previous_text=False)
            id = 0
            for segment in segments:
                print("[%.2fs -> %.2fs] %s" % (segment.start, segment.end, segment.text))
                resultSegments.append(backend_pb2.TranscriptSegment(id=id, start=segment.start, end=segment.end, text=segment.text))
                text += segment.text
                id += 1            
        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)

        return backend_pb2.TranscriptResult(segments=resultSegments, text=text)

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
