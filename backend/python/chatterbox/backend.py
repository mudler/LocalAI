#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Chatterbox TTS
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
import torchaudio as ta
from chatterbox.tts import ChatterboxTTS
from chatterbox.mtl_tts import ChatterboxMultilingualTTS
import grpc
import tempfile

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

def split_text_at_word_boundary(text, max_length=250):
    """
    Split text at word boundaries without truncating words.
    Returns a list of text chunks.
    """
    if not text or len(text) <= max_length:
        return [text]
    
    chunks = []
    words = text.split()
    current_chunk = ""
    
    for word in words:
        # Check if adding this word would exceed the limit
        if len(current_chunk) + len(word) + 1 <= max_length:
            if current_chunk:
                current_chunk += " " + word
            else:
                current_chunk = word
        else:
            # If current chunk is not empty, add it to chunks
            if current_chunk:
                chunks.append(current_chunk)
                current_chunk = word
            else:
                # If a single word is longer than max_length, we have to include it anyway
                chunks.append(word)
                current_chunk = ""
    
    # Add the last chunk if it's not empty
    if current_chunk:
        chunks.append(current_chunk)
    
    return chunks

def merge_audio_files(audio_files, output_path, sample_rate):
    """
    Merge multiple audio files into a single audio file.
    """
    if not audio_files:
        return
    
    if len(audio_files) == 1:
        # If only one file, just copy it
        import shutil
        shutil.copy2(audio_files[0], output_path)
        return
    
    # Load all audio files
    waveforms = []
    for audio_file in audio_files:
        waveform, sr = ta.load(audio_file)
        if sr != sample_rate:
            # Resample if necessary
            resampler = ta.transforms.Resample(sr, sample_rate)
            waveform = resampler(waveform)
        waveforms.append(waveform)
    
    # Concatenate all waveforms
    merged_waveform = torch.cat(waveforms, dim=1)
    
    # Save the merged audio
    ta.save(output_path, merged_waveform, sample_rate)
    
    # Clean up temporary files
    for audio_file in audio_files:
        if os.path.exists(audio_file):
            os.remove(audio_file)

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
            if "multilingual" in self.options:
                # remove key from options
                del self.options["multilingual"]
                self.model = ChatterboxMultilingualTTS.from_pretrained(device=device)
            else:
                self.model = ChatterboxTTS.from_pretrained(device=device)
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        # Implement your logic here for the LoadModel service
        # Replace this with your desired response
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def TTS(self, request, context):
        try:
            kwargs = {}

            if "language" in self.options:
                kwargs["language_id"] = self.options["language"]
            if self.AudioPath is not None:
                kwargs["audio_prompt_path"] = self.AudioPath

            # add options to kwargs
            kwargs.update(self.options)

            # Check if text exceeds 250 characters
            # (chatterbox does not support long text)
            # https://github.com/resemble-ai/chatterbox/issues/60
            # https://github.com/resemble-ai/chatterbox/issues/110
            if len(request.text) > 250:
                # Split text at word boundaries
                text_chunks = split_text_at_word_boundary(request.text, max_length=250)
                print(f"Splitting text into chunks of 250 characters: {len(text_chunks)}", file=sys.stderr)
                # Generate audio for each chunk
                temp_audio_files = []
                for i, chunk in enumerate(text_chunks):
                    # Generate audio for this chunk
                    wav = self.model.generate(chunk, **kwargs)
                    
                    # Create temporary file for this chunk
                    temp_file = tempfile.NamedTemporaryFile(delete=False, suffix='.wav')
                    temp_file.close()
                    ta.save(temp_file.name, wav, self.model.sr)
                    temp_audio_files.append(temp_file.name)
                
                # Merge all audio files
                merge_audio_files(temp_audio_files, request.dst, self.model.sr)
            else:
                # Generate audio using ChatterboxTTS for short text
                wav = self.model.generate(request.text, **kwargs)
                # Save the generated audio
                ta.save(request.dst, wav, self.model.sr)
            
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
