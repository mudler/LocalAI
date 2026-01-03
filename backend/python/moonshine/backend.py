#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Moonshine transcription
"""
from concurrent import futures
import time
import argparse
import signal
import sys
import os
import backend_pb2
import backend_pb2_grpc
import moonshine_onnx

import grpc


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
        try:
            print("Preparing models, please wait", file=sys.stderr)
            # Store the model name for use in transcription
            # Model name format: e.g., "moonshine/tiny"
            self.model_name = request.Model
            print(f"Model name set to: {self.model_name}", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        resultSegments = []
        text = ""
        try:
            # moonshine_onnx.transcribe returns a list of strings
            transcriptions = moonshine_onnx.transcribe(request.dst, self.model_name)
            
            # Combine all transcriptions into a single text
            if isinstance(transcriptions, list):
                text = " ".join(transcriptions)
                # Create segments for each transcription in the list
                for id, trans in enumerate(transcriptions):
                    # Since moonshine doesn't provide timing info, we'll create a single segment
                    # with id and text, using approximate timing
                    resultSegments.append(backend_pb2.TranscriptSegment(
                        id=id, 
                        start=0, 
                        end=0, 
                        text=trans
                    ))
            else:
                # Handle case where it's not a list (shouldn't happen, but be safe)
                text = str(transcriptions)
                resultSegments.append(backend_pb2.TranscriptSegment(
                    id=0,
                    start=0,
                    end=0,
                    text=text
                ))
        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

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

