#!/usr/bin/env python3
"""
Extra gRPC server for OpenVoice models.
"""
from concurrent import futures

import argparse
import signal
import sys
import os
import torch
from openvoice import se_extractor
from openvoice.api import ToneColorConverter
from melo.api import TTS

import time
import backend_pb2
import backend_pb2_grpc

import grpc


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

            self.clonedVoice = False
            # Assume directory from request.ModelFile.
            # Only if request.LoraAdapter it's not an absolute path
            if request.AudioPath and request.ModelFile != "" and not os.path.isabs(request.AudioPath):
                # get base path of modelFile
                modelFileBase = os.path.dirname(request.ModelFile)
                request.AudioPath = os.path.join(modelFileBase, request.AudioPath)
            if request.AudioPath != "":
                self.clonedVoice = True

            self.modelpath = request.ModelFile
            self.speaker = request.Type
            self.ClonedVoicePath = request.AudioPath
            
            ckpt_converter = request.Model+'/converter'
            device = "cuda:0" if torch.cuda.is_available() else "cpu"
            self.device = device
            self.tone_color_converter = None
            if self.clonedVoice:
                self.tone_color_converter = ToneColorConverter(f'{ckpt_converter}/config.json', device=device)
                self.tone_color_converter.load_ckpt(f'{ckpt_converter}/checkpoint.pth')
       
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        model_name = request.model
        if model_name == "":
            return backend_pb2.Result(success=False, message="request.model is required")
        try:
            # Speed is adjustable
            speed = 1.0
            voice = "EN"
            if request.voice:
                voice = request.voice
            model = TTS(language=voice, device=self.device)
            speaker_ids = model.hps.data.spk2id
            speaker_key = self.speaker
            modelpath = self.modelpath
            for s in speaker_ids.keys():
                print(f"Speaker: {s} - ID: {speaker_ids[s]}")
            speaker_id = speaker_ids[speaker_key]
            speaker_key = speaker_key.lower().replace('_', '-')
            source_se = torch.load(f'{modelpath}/base_speakers/ses/{speaker_key}.pth', map_location=self.device)
            model.tts_to_file(request.text, speaker_id, request.dst, speed=speed)
            if self.clonedVoice:
                reference_speaker = self.ClonedVoicePath
                target_se, audio_name = se_extractor.get_se(reference_speaker, self.tone_color_converter, vad=False)
                # Run the tone color converter
                encode_message = "@MyShell"
                self.tone_color_converter.convert(
                    audio_src_path=request.dst, 
                    src_se=source_se, 
                    tgt_se=target_se, 
                    output_path=request.dst,
                    message=encode_message)
           
            print("[OpenVoice] TTS generated!", file=sys.stderr)
            print("[OpenVoice] TTS saved to", request.dst, file=sys.stderr)
            print(request, file=sys.stderr)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        return backend_pb2.Result(success=True)

def serve(address):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("[OpenVoice] Server started. Listening on: " + address, file=sys.stderr)

    # Define the signal handler function
    def signal_handler(sig, frame):
        print("[OpenVoice] Received termination signal. Shutting down...")
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
    print(f"[OpenVoice] startup: {args}", file=sys.stderr)
    serve(args.addr)
