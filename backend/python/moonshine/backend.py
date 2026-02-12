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
from moonshine_voice import (
    Transcriber,
    get_model_for_language,
    load_wav_file,
)

import grpc


_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """
    def __init__(self):
        self.transcriber = None
        self.model_name = None

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    
    def LoadModel(self, request, context):
        try:
            print("Preparing models, please wait", file=sys.stderr)
            self.model_name = request.Model
            print(f"Model name set to: {self.model_name}", file=sys.stderr)
            
            # Default values
            language = "en"
            model_arch = None
            
            # Parse options from request
            options = request.Options
            self.options = {}
            
            # The options are a list of strings in this form optname:optvalue
            for opt in options:
                if ":" not in opt:
                    continue
                key, value = opt.split(":", 1)
                self.options[key] = value
            
            print(f"Options: {self.options}", file=sys.stderr)
            
            # Extract language and model_arch from options
            if "language" in self.options:
                language = self.options["language"]
            if "model_arch" in self.options:
                model_arch = self.options["model_arch"]
            
            # Get the model path and architecture
            model_path, model_arch = get_model_for_language(language, model_arch)
            print(f"Loading model: {model_path} with architecture: {model_arch} for language: {language}", file=sys.stderr)
            
            # Initialize the transcriber
            self.transcriber = Transcriber(model_path=model_path, model_arch=model_arch)
            print("Model loaded successfully", file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        resultSegments = []
        text = ""
        try:
            if self.transcriber is None:
                raise Exception("Model not loaded. Call LoadModel first.")
            
            # Load the audio file
            audio_data, sample_rate = load_wav_file(request.dst)
            print(f"Loaded audio file: {request.dst} with sample rate: {sample_rate}", file=sys.stderr)
            
            # Transcribe without streaming
            transcript = self.transcriber.transcribe_without_streaming(
                audio_data, sample_rate=sample_rate, flags=0
            )
            
            # Process transcript lines
            full_text_parts = []
            for idx, line in enumerate(transcript.lines):
                line_text = line.text.strip()
                full_text_parts.append(line_text)
                
                # Create segment with timing information
                start_ms = int(line.start_time * 1000)
                end_ms = int((line.start_time + line.duration) * 1000)
                
                resultSegments.append(backend_pb2.TranscriptSegment(
                    id=idx,
                    start=start_ms,
                    end=end_ms,
                    text=line_text
                ))
                
                print(f"Segment {idx}: [{line.start_time:.2f}s - {line.start_time + line.duration:.2f}s] {line_text}", file=sys.stderr)
            
            # Combine all transcriptions into a single text
            text = " ".join(full_text_parts)
            
        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            import traceback
            traceback.print_exc()
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

