#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for fish-speech TTS
"""

from concurrent import futures
import time
import argparse
import signal
import sys
import os
import traceback
import backend_pb2
import backend_pb2_grpc
import torch
import soundfile as sf
import numpy as np

import json

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
MAX_WORKERS = int(os.environ.get("PYTHON_GRPC_MAX_WORKERS", "1"))


# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    BackendServicer is the class that implements the gRPC service
    """

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", "utf-8"))

    def LoadModel(self, request, context):
        try:
            # Get device
            if torch.cuda.is_available():
                print("CUDA is available", file=sys.stderr)
                device = "cuda"
            else:
                print("CUDA is not available", file=sys.stderr)
                device = "cpu"
            mps_available = (
                hasattr(torch.backends, "mps") and torch.backends.mps.is_available()
            )
            if mps_available:
                device = "mps"
            if not torch.cuda.is_available() and request.CUDA:
                return backend_pb2.Result(success=False, message="CUDA is not available")

            # Validate mps availability if requested
            if device == "mps" and not torch.backends.mps.is_available():
                print("Warning: MPS not available. Falling back to CPU.", file=sys.stderr)
                device = "cpu"

            self.device = device
            self._torch_device = torch.device(device)

            options = request.Options

            # empty dict
            self.options = {}

            # The options are a list of strings in this form optname:optvalue
            for opt in options:
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

            # Parse voices configuration from options
            self.voices = {}
            if "voices" in self.options:
                try:
                    voices_data = self.options["voices"]
                    if isinstance(voices_data, str):
                        voices_list = json.loads(voices_data)
                    else:
                        voices_list = voices_data

                    for voice_entry in voices_list:
                        if not isinstance(voice_entry, dict):
                            print(
                                f"[WARNING] Invalid voice entry (not a dict): {voice_entry}",
                                file=sys.stderr,
                            )
                            continue

                        name = voice_entry.get("name")
                        audio = voice_entry.get("audio")
                        ref_text = voice_entry.get("ref_text", "")

                        if not name or not isinstance(name, str):
                            print(
                                f"[WARNING] Voice entry missing required 'name' field: {voice_entry}",
                                file=sys.stderr,
                            )
                            continue
                        if not audio or not isinstance(audio, str):
                            print(
                                f"[WARNING] Voice entry missing required 'audio' field: {voice_entry}",
                                file=sys.stderr,
                            )
                            continue

                        self.voices[name] = {"audio": audio, "ref_text": ref_text}
                        print(
                            f"[INFO] Registered voice '{name}' with audio: {audio}",
                            file=sys.stderr,
                        )

                    print(f"[INFO] Loaded {len(self.voices)} voice(s)", file=sys.stderr)
                except json.JSONDecodeError as e:
                    print(f"[ERROR] Failed to parse voices JSON: {e}", file=sys.stderr)
                except Exception as e:
                    print(
                        f"[ERROR] Error processing voices configuration: {e}",
                        file=sys.stderr,
                    )
                    print(traceback.format_exc(), file=sys.stderr)

            # Store AudioPath, ModelFile, and ModelPath from LoadModel request
            self.audio_path = (
                request.AudioPath
                if hasattr(request, "AudioPath") and request.AudioPath
                else None
            )
            self.model_file = (
                request.ModelFile
                if hasattr(request, "ModelFile") and request.ModelFile
                else None
            )
            self.model_path = (
                request.ModelPath
                if hasattr(request, "ModelPath") and request.ModelPath
                else None
            )

            # Get model path from request
            model_path = request.Model
            if not model_path:
                model_path = "fishaudio/s2-pro"

            # If model_path looks like a HuggingFace repo ID (e.g. "fishaudio/fish-speech-1.5"),
            # download it locally first since fish-speech expects a local directory
            if "/" in model_path and not os.path.exists(model_path):
                from huggingface_hub import snapshot_download

                print(
                    f"Downloading model from HuggingFace: {model_path}",
                    file=sys.stderr,
                )
                model_path = snapshot_download(repo_id=model_path)
                print(f"Model downloaded to: {model_path}", file=sys.stderr)

            # Determine precision
            if device in ("mps", "cpu"):
                precision = torch.float32
            else:
                precision = torch.bfloat16

            # Whether to use torch.compile
            compile_model = self.options.get("compile", False)

            print(
                f"Using device: {device}, precision: {precision}, compile: {compile_model}",
                file=sys.stderr,
            )
            print(f"Loading model from: {model_path}", file=sys.stderr)

            # Import fish-speech modules
            from fish_speech.inference_engine import TTSInferenceEngine
            from fish_speech.models.dac.inference import load_model as load_decoder_model
            from fish_speech.models.text2semantic.inference import (
                launch_thread_safe_queue,
            )

            # Determine decoder checkpoint path
            # The codec model is typically at <checkpoint_path>/codec.pth
            decoder_checkpoint = self.options.get("decoder_checkpoint", None)
            if not decoder_checkpoint:
                # Try common locations
                if os.path.isdir(model_path):
                    candidate = os.path.join(model_path, "codec.pth")
                    if os.path.exists(candidate):
                        decoder_checkpoint = candidate

            # Launch LLaMA queue (runs in daemon thread)
            print("Launching LLaMA queue...", file=sys.stderr)
            llama_queue = launch_thread_safe_queue(
                checkpoint_path=model_path,
                device=device,
                precision=precision,
                compile=compile_model,
            )

            # Load DAC decoder
            decoder_config = self.options.get("decoder_config", "modded_dac_vq")
            if not decoder_checkpoint:
                return backend_pb2.Result(
                    success=False,
                    message="Decoder checkpoint (codec.pth) not found. "
                    "Ensure the model directory contains codec.pth or set "
                    "decoder_checkpoint option.",
                )
            print(
                f"Loading DAC decoder (config={decoder_config}, checkpoint={decoder_checkpoint})...",
                file=sys.stderr,
            )
            decoder_model = load_decoder_model(
                config_name=decoder_config,
                checkpoint_path=decoder_checkpoint,
                device=device,
            )

            # Create TTS inference engine
            self.engine = TTSInferenceEngine(
                llama_queue=llama_queue,
                decoder_model=decoder_model,
                precision=precision,
                compile=compile_model,
            )

            print(f"Model loaded successfully: {model_path}", file=sys.stderr)

            return backend_pb2.Result(message="Model loaded successfully", success=True)

        except Exception as e:
            print(f"[ERROR] Loading model: {type(e).__name__}: {e}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(
                success=False, message=f"Failed to load model: {e}"
            )

    def _get_ref_audio_path(self, voice_name=None):
        """Get reference audio path from voices dict or stored AudioPath."""
        if voice_name and voice_name in self.voices:
            audio_path = self.voices[voice_name]["audio"]

            if os.path.isabs(audio_path):
                return audio_path

            # Try relative to ModelFile
            if self.model_file:
                model_file_base = os.path.dirname(self.model_file)
                ref_path = os.path.join(model_file_base, audio_path)
                if os.path.exists(ref_path):
                    return ref_path

            # Try relative to ModelPath
            if self.model_path:
                ref_path = os.path.join(self.model_path, audio_path)
                if os.path.exists(ref_path):
                    return ref_path

            return audio_path

        # Fall back to legacy single-voice mode
        if not self.audio_path:
            return None

        if os.path.isabs(self.audio_path):
            return self.audio_path

        if self.model_file:
            model_file_base = os.path.dirname(self.model_file)
            ref_path = os.path.join(model_file_base, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path

        if self.model_path:
            ref_path = os.path.join(self.model_path, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path

        return self.audio_path

    def TTS(self, request, context):
        try:
            from fish_speech.utils.schema import ServeTTSRequest, ServeReferenceAudio

            if not request.dst:
                return backend_pb2.Result(
                    success=False, message="dst (output path) is required"
                )

            text = request.text.strip()
            if not text:
                return backend_pb2.Result(success=False, message="Text is empty")

            # Get generation parameters from options
            top_p = self.options.get("top_p", 0.8)
            temperature = self.options.get("temperature", 0.8)
            repetition_penalty = self.options.get("repetition_penalty", 1.1)
            max_new_tokens = self.options.get("max_new_tokens", 1024)
            chunk_length = self.options.get("chunk_length", 200)

            # Build references list for voice cloning
            references = []
            voice_name = request.voice if request.voice else None

            if voice_name and voice_name in self.voices:
                ref_audio_path = self._get_ref_audio_path(voice_name)
                if ref_audio_path and os.path.exists(ref_audio_path):
                    with open(ref_audio_path, "rb") as f:
                        audio_bytes = f.read()
                    ref_text = self.voices[voice_name].get("ref_text", "")
                    references.append(
                        ServeReferenceAudio(audio=audio_bytes, text=ref_text)
                    )
                    print(
                        f"[INFO] Using voice '{voice_name}' with reference audio: {ref_audio_path}",
                        file=sys.stderr,
                    )
            elif self.audio_path:
                ref_audio_path = self._get_ref_audio_path()
                if ref_audio_path and os.path.exists(ref_audio_path):
                    with open(ref_audio_path, "rb") as f:
                        audio_bytes = f.read()
                    ref_text = self.options.get("ref_text", "")
                    references.append(
                        ServeReferenceAudio(audio=audio_bytes, text=ref_text)
                    )
                    print(
                        f"[INFO] Using reference audio: {ref_audio_path}",
                        file=sys.stderr,
                    )

            # Build ServeTTSRequest
            tts_request = ServeTTSRequest(
                text=text,
                references=references,
                top_p=top_p,
                temperature=temperature,
                repetition_penalty=repetition_penalty,
                max_new_tokens=max_new_tokens,
                chunk_length=chunk_length,
            )

            # Run inference
            print(f"Generating speech for text: {text[:100]}...", file=sys.stderr)
            start_time = time.time()

            sample_rate = None
            audio_data = None

            for result in self.engine.inference(tts_request):
                if result.code == "final":
                    sample_rate, audio_data = result.audio
                elif result.code == "error":
                    error_msg = str(result.error) if result.error else "Unknown error"
                    print(f"[ERROR] TTS inference error: {error_msg}", file=sys.stderr)
                    return backend_pb2.Result(
                        success=False, message=f"TTS inference error: {error_msg}"
                    )

            generation_duration = time.time() - start_time

            if audio_data is None or sample_rate is None:
                return backend_pb2.Result(
                    success=False, message="No audio output generated"
                )

            # Ensure audio_data is a numpy array
            if not isinstance(audio_data, np.ndarray):
                audio_data = np.array(audio_data)

            audio_duration = len(audio_data) / sample_rate if sample_rate > 0 else 0
            print(
                f"[INFO] TTS generation completed: {generation_duration:.2f}s, "
                f"audio_duration={audio_duration:.2f}s, sample_rate={sample_rate}",
                file=sys.stderr,
                flush=True,
            )

            # Save output
            sf.write(request.dst, audio_data, sample_rate)
            print(f"Saved {audio_duration:.2f}s audio to {request.dst}", file=sys.stderr)

        except Exception as err:
            print(f"Error in TTS: {err}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(
                success=False, message=f"Unexpected {err=}, {type(err)=}"
            )

        return backend_pb2.Result(success=True)


def serve(address):
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ("grpc.max_message_length", 50 * 1024 * 1024),  # 50MB
            ("grpc.max_send_message_length", 50 * 1024 * 1024),  # 50MB
            ("grpc.max_receive_message_length", 50 * 1024 * 1024),  # 50MB
        ],
    )
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
