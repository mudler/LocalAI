#!/usr/bin/env python3

from concurrent import futures
import argparse
import signal
import sys
import os
import time
import backend_pb2
import backend_pb2_grpc

import grpc

from utils.generation import SAMPLE_RATE, generate_audio, preload_models
from scipy.io.wavfile import write as write_wav
from utils.prompt_making import make_prompt

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    gRPC servicer for backend services.
    """
    def Health(self, request, context):
        """
        Health check service.

        Args:
            request: A backend_pb2.HealthRequest instance.
            context: A grpc.ServicerContext instance.

        Returns:
            A backend_pb2.Reply instance with message "OK".
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        """
        Load model service.

        Args:
            request: A backend_pb2.LoadModelRequest instance.
            context: A grpc.ServicerContext instance.

        Returns:
            A backend_pb2.Result instance with message "Model loaded successfully" and success=True if successful.
            A backend_pb2.Result instance with success=False and error message if unsuccessful.
        """
        model_name = request.Model
        try:
            print("Preparing models, please wait", file=sys.stderr)
            # download and load all models
            preload_models()
            self.clonedVoice = False
            # Assume directory from request.ModelFile.
            # Only if request.LoraAdapter it's not an absolute path
            if request.AudioPath and request.ModelFile != "" and not os.path.isabs(request.AudioPath):
                # get base path of modelFile
                modelFileBase = os.path.dirname(request.ModelFile)
                # modify LoraAdapter to be relative to modelFileBase
                request.AudioPath = os.path.join(modelFileBase, request.AudioPath)
            if request.AudioPath != "":
                print("Generating model", file=sys.stderr)
                make_prompt(name=model_name, audio_prompt_path=request.AudioPath)
                self.clonedVoice = True
                ### Use given transcript
                ##make_prompt(name=model_name, audio_prompt_path="paimon_prompt.wav",
                ##                transcript="Just, what was that? Paimon thought we were gonna get eaten.")
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        """
        Text-to-speech service.

        Args:
            request: A backend_pb2.TTSRequest instance.
            context: A grpc.ServicerContext instance.

        Returns:
            A backend_pb2.Result instance with success=True if successful.
            A backend_pb2.Result instance with success=False and error message if unsuccessful.
        """
        model = request.model
        print(request, file=sys.stderr)
        try:
            audio_array = None
            if model != "":
                if self.clonedVoice:
                    model = os.path.basename(request.model)
                audio_array = generate_audio(request.text, prompt=model)
            else:
                audio_array = generate_audio(request.text)
            print("saving to", request.dst, file=sys.stderr)
            # save audio to disk
            write_wav(request.dst, SAMPLE_RATE, audio_array)
            print("saved to", request.dst, file=sys.stderr)
            print("tts for", file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
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
