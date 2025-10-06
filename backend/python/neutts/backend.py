#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for NeuTTSAir
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
from neuttsair.neutts import NeuTTSAir
import soundfile as sf

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
        # device = "cuda" if request.CUDA else "cpu"
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


        options = request.Options

        # empty dict
        self.options = {}
        self.ref_text = None

        # The options are a list of strings in this form optname:optvalue
        # We are storing all the options in a dict so we can use it later when
        # generating the images
        for opt in options:
            if ":" not in opt:
                continue
            key, value = opt.split(":")
            # if value is a number, convert it to the appropriate type
            if is_float(value):
                value = float(value)
            elif is_int(value):
                value = int(value)
            elif value.lower() in ["true", "false"]:
                value = value.lower() == "true"
            self.options[key] = value

        codec_repo = "neuphonic/neucodec"
        if "codec_repo" in self.options:
            codec_repo = self.options["codec_repo"]
            del self.options["codec_repo"]
        if "ref_text" in self.options:
            self.ref_text = self.options["ref_text"]
            del self.options["ref_text"]

        self.AudioPath = None

        if os.path.isabs(request.AudioPath):
            self.AudioPath = request.AudioPath
        elif request.AudioPath and request.ModelFile != "" and not os.path.isabs(request.AudioPath):
            # get base path of modelFile
            modelFileBase = os.path.dirname(request.ModelFile)
            # modify LoraAdapter to be relative to modelFileBase
            self.AudioPath = os.path.join(modelFileBase, request.AudioPath)
        try:
            print("Preparing models, please wait", file=sys.stderr)
            self.model = NeuTTSAir(backbone_repo=request.Model, backbone_device=device, codec_repo=codec_repo, codec_device=device)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        try:
            kwargs = {}

            # add options to kwargs
            kwargs.update(self.options)

            ref_codes = self.model.encode_reference(self.AudioPath)

            wav = self.model.infer(request.text, ref_codes, self.ref_text)

            sf.write(request.dst, wav, 24000)            
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
