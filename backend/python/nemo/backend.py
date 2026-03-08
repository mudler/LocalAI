#!/usr/bin/env python3
"""
GRPC server of LocalAI for NVIDIA NEMO Toolkit ASR.
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
import nemo.collections.asr as nemo_asr
import numpy as np

try:
    import torchaudio
    TORCHAUDIO_AVAILABLE = True
except ImportError:
    TORCHAUDIO_AVAILABLE = False
    print("[WARNING] torchaudio not available, will use fallback audio loading", file=sys.stderr)

import grpc


def is_float(s):
    try:
        float(s)
        return True
    except ValueError:
        return False


def is_int(s):
    try:
        int(s)
        return True
    except ValueError:
        return False


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def load_audio_np(audio_path, target_sample_rate=16000):
    """Load audio file as numpy array using available methods."""
    if TORCHAUDIO_AVAILABLE:
        try:
            waveform, sample_rate = torchaudio.load(audio_path)
            # Convert to mono if stereo
            if waveform.shape[0] > 1:
                waveform = waveform.mean(dim=0, keepdim=True)
            # Resample if needed
            if sample_rate != target_sample_rate:
                resampler = torchaudio.transforms.Resample(sample_rate, target_sample_rate)
                waveform = resampler(waveform)
            # Convert to numpy
            audio_np = waveform.squeeze().numpy()
            return audio_np, target_sample_rate
        except Exception as e:
            print(f"[WARNING] torchaudio loading failed: {e}, trying fallback", file=sys.stderr)
    
    # Fallback: try using scipy or soundfile
    try:
        import soundfile as sf
        audio_np, sample_rate = sf.read(audio_path)
        if audio_np.ndim > 1:
            audio_np = audio_np.mean(axis=1)
        if sample_rate != target_sample_rate:
            from scipy.signal import resample
            num_samples = int(len(audio_np) * target_sample_rate / sample_rate)
            audio_np = resample(audio_np, num_samples)
        return audio_np, target_sample_rate
    except ImportError:
        pass
    
    try:
        from scipy.io import wavfile
        sample_rate, audio_np = wavfile.read(audio_path)
        if audio_np.ndim > 1:
            audio_np = audio_np.mean(axis=1)
        return audio_np, sample_rate
    except ImportError:
        pass
    
    raise RuntimeError("No audio loading library available (torchaudio, soundfile, scipy)")


class BackendServicer(backend_pb2_grpc.BackendServicer):
    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    def LoadModel(self, request, context):
        if torch.cuda.is_available():
            device = "cuda"
        else:
            device = "cpu"
        mps_available = hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
        if mps_available:
            device = "mps"
        if not torch.cuda.is_available() and request.CUDA:
            return backend_pb2.Result(success=False, message="CUDA is not available")

        self.device = device
        self.options = {}

        for opt in request.Options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            if is_float(value):
                value = float(value)
            elif is_int(value):
                value = int(value)
            elif value.lower() in ["true", "false"]:
                value = value.lower() == "true"
            self.options[key] = value

        model_name = request.Model or "nvidia/parakeet-tdt-0.6b-v3"

        try:
            print(f"Loading NEMO ASR model from {model_name}", file=sys.stderr)
            self.model = nemo_asr.models.ASRModel.from_pretrained(model_name=model_name)
            print("NEMO ASR model loaded successfully", file=sys.stderr)
        except Exception as err:
            print(f"[ERROR] LoadModel failed: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.Result(success=False, message=str(err))

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def AudioTranscription(self, request, context):
        result_segments = []
        text = ""
        try:
            audio_path = request.dst
            if not audio_path or not os.path.exists(audio_path):
                print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
                return backend_pb2.TranscriptResult(segments=[], text="")

            # Load audio as numpy array to avoid lhotse dataloader issues
            audio_np, sample_rate = load_audio_np(audio_path, target_sample_rate=16000)
            
            # Convert to torch tensor
            audio_tensor = torch.from_numpy(audio_np).float()
            audio_tensor = audio_tensor.unsqueeze(0)  # Add batch dimension
            
            # Use the model's transcribe method with the tensor directly
            # Some NEMO models accept audio tensors directly
            try:
                # Try passing the waveform tensor directly
                results = self.model.transcribe(audio_tensor, return_char_alignments=False)
            except TypeError:
                # Fallback: try with dict format
                results = self.model.transcribe(
                    [{"audio_file": audio_path}],
                    return_char_alignments=False
                )
            
            if not results or len(results) == 0:
                print("[WARNING] No transcription results returned", file=sys.stderr)
                return backend_pb2.TranscriptResult(segments=[], text="")

            # Get the transcript text from the first result
            if isinstance(results, list) and len(results) > 0:
                text = results[0]
            elif isinstance(results, dict) and "text" in results:
                text = results["text"]
            else:
                text = str(results) if results else ""
            
            if text:
                # Create a single segment with the full transcription
                result_segments.append(backend_pb2.TranscriptSegment(
                    id=0, start=0, end=0, text=text
                ))

        except Exception as err:
            print(f"Error in AudioTranscription: {err}", file=sys.stderr)
            import traceback
            traceback.print_exc(file=sys.stderr)
            return backend_pb2.TranscriptResult(segments=[], text="")

        return backend_pb2.TranscriptResult(segments=result_segments, text=text)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ])
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)
    server.start()
    print("Server started. Listening on: " + address, file=sys.stderr)

    def signal_handler(sig, frame):
        print("Received termination signal. Shutting down...")
        server.stop(0)
        sys.exit(0)

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument("--addr", default="localhost:50051", help="The address to bind the server to.")
    args = parser.parse_args()
    serve(args.addr)
