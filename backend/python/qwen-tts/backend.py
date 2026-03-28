#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for Qwen3-TTS
"""

from concurrent import futures
import time
import argparse
import signal
import sys
import os
import copy
import traceback
from pathlib import Path
import backend_pb2
import backend_pb2_grpc
import torch
import soundfile as sf
from qwen_tts import Qwen3TTSModel

import json
import hashlib
import pickle

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
        # Detect ROCm environment FIRST - before device selection
        # ROCm environments can be detected by torch.version.hip or HIP_VISIBLE_DEVICES
        is_rocm = hasattr(torch.version, "hip") or os.environ.get("HIP_VISIBLE_DEVICES")
        if is_rocm:
            print("Detected ROCm environment, flash_attention_2 may not be available", file=sys.stderr)

        # Get device - considering ROCm environment
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

        # Normalize potential 'mpx' typo to 'mps'
        if device == "mpx":
            print("Note: device 'mpx' detected, treating it as 'mps'.", file=sys.stderr)
            device = "mps"

        # Validate mps availability if requested
        if device == "mps" and not torch.backends.mps.is_available():
            print("Warning: MPS not available. Falling back to CPU.", file=sys.stderr)
            device = "cpu"

        self.device = device
        self._torch_device = torch.device(device)
        self.is_rocm = is_rocm

        options = request.Options

        # empty dict
        self.options = {}

        # The options are a list of strings in this form optname:optvalue
        # We are storing all the options in a dict so we can use it later when
        # generating the audio
        for opt in options:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)  # Split only on first colon
            # if value is a number, convert it to the appropriate type
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

                # Validate and store voices
                for voice_entry in voices_list:
                    if not isinstance(voice_entry, dict):
                        print(
                            f"[WARNING] Invalid voice entry (not a dict): {voice_entry}",
                            file=sys.stderr,
                        )
                        continue

                    name = voice_entry.get("name")
                    audio = voice_entry.get("audio")
                    ref_text = voice_entry.get("ref_text")

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
                    if ref_text is None or not isinstance(ref_text, str):
                        print(
                            f"[WARNING] Voice entry missing required 'ref_text' field: {voice_entry}",
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

        # Get model path from request
        model_path = request.Model
        if not model_path:
            model_path = "Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice"

        # Determine model type from model path or options
        self.model_type = self.options.get("model_type", None)
        if not self.model_type:
            if "CustomVoice" in model_path:
                self.model_type = "CustomVoice"
            elif "VoiceDesign" in model_path:
                self.model_type = "VoiceDesign"
            elif "Base" in model_path or "0.6B" in model_path or "1.7B" in model_path:
                self.model_type = "Base"  # VoiceClone model
            else:
                # Default to CustomVoice
                self.model_type = "CustomVoice"

        # Cache for voice clone prompts
        self._voice_clone_cache = {}

        # Pre-load cached voices if disk_cache is enabled
        self._preload_cached_voices()

        # Store AudioPath, ModelFile, and ModelPath from LoadModel request
        # These are used later in TTS for VoiceClone mode
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

        # Decide dtype & attention implementation
        # Check ROCm environment properly at the point of decision, not relying on stored flag
        if self.device == "mps":
            load_dtype = torch.float32  # MPS requires float32
            device_map = None
            attn_impl_primary = "sdpa"  # flash_attention_2 not supported on MPS
        elif self.device == "cuda":
            load_dtype = torch.bfloat16
            device_map = "cuda"
            # Check for ROCm environment directly - use sdpa for ROCm, flash_attention_2 for CUDA
            # ROCm is detected by torch.version.hip or HIP_VISIBLE_DEVICES environment variable
            has_rocm = hasattr(torch.version, "hip") or os.environ.get("HIP_VISIBLE_DEVICES")
            attn_impl_primary = "sdpa" if has_rocm else "flash_attention_2"
        else:  # cpu
            load_dtype = torch.float32
            device_map = "cpu"
            attn_impl_primary = "sdpa"

        print(
            f"Using device: {self.device}, torch_dtype: {load_dtype}, attn_implementation: {attn_impl_primary}, model_type: {self.model_type}",
            file=sys.stderr,
        )
        print(f"Loading model from: {model_path}", file=sys.stderr)

        # Load model with device-specific logic
        # Common parameters for all devices
        load_kwargs = {
            "dtype": load_dtype,
            "attn_implementation": attn_impl_primary,
            "trust_remote_code": True,  # Required for qwen-tts models
        }

        try:
            if self.device == "mps":
                load_kwargs["device_map"] = None  # load then move
                self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
                self.model.to("mps")
            elif self.device == "cuda":
                load_kwargs["device_map"] = device_map
                self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
            else:  # cpu
                load_kwargs["device_map"] = device_map
                self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
        except Exception as e:
            error_msg = str(e)
            print(
                f"[ERROR] Loading model: {type(e).__name__}: {error_msg}",
                file=sys.stderr,
            )
            print(traceback.format_exc(), file=sys.stderr)

            # Check if it's a missing feature extractor/tokenizer error
            if (
                "speech_tokenizer" in error_msg
                or "preprocessor_config.json" in error_msg
                or "feature extractor" in error_msg.lower()
            ):
                print(
                    "\n[ERROR] Model files appear to be incomplete. This usually means:",
                    file=sys.stderr,
                )
                print(
                    "  1. The model download was interrupted or incomplete",
                    file=sys.stderr,
                )
                print("  2. The model cache is corrupted", file=sys.stderr)
                print("\nTo fix this, try:", file=sys.stderr)
                print(
                    f"  rm -rf ~/.cache/huggingface/hub/models--Qwen--Qwen3-TTS-*",
                    file=sys.stderr,
                )
                print("  Then re-run to trigger a fresh download.", file=sys.stderr)
                print(
                    "\nAlternatively, try using a different model variant:",
                    file=sys.stderr,
                )
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice", file=sys.stderr)
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign", file=sys.stderr)
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-Base", file=sys.stderr)

            if attn_impl_primary == "flash_attention_2":
                print(
                    "\nTrying to use SDPA instead of flash_attention_2...",
                    file=sys.stderr,
                )
                load_kwargs["attn_implementation"] = "sdpa"
                try:
                    if self.device == "mps":
                        self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
                        self.model.to("mps")
                    else:
                        self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
                    print(
                        "[SUCCESS] Model loaded successfully with SDPA",
                        file=sys.stderr,
                    )
                except Exception as e2:
                    print(
                        f"[ERROR] Failed to load model with SDPA: {e2}",
                        file=sys.stderr,
                    )
                    return backend_pb2.Result(
                        success=False,
                        message=f"Failed to load model: {error_msg}",
                    )

        # Store the attention implementation used
        self.attn_implementation = attn_impl_primary

        print(
            f"[SUCCESS] Model loaded successfully on {self.device} with {attn_impl_primary}",
            file=sys.stderr,
        )

        return backend_pb2.Result(success=True)

    def _preload_cached_voices(self):
        """Pre-load cached voice clones if disk_cache is enabled."""
        if not self.options.get("disk_cache", False):
            return

        cache_dir = self.options.get("cache_dir", "/tmp/qwen_tts_cache")
        cache_file = os.path.join(cache_dir, "voice_cache.pkl")

        if os.path.exists(cache_file):
            try:
                with open(cache_file, "rb") as f:
                    self._voice_clone_cache = pickle.load(f)
                print(
                    f"[INFO] Loaded {len(self._voice_clone_cache)} cached voice(s)",
                    file=sys.stderr,
                )
            except Exception as e:
                print(
                    f"[WARNING] Failed to load voice cache: {e}",
                    file=sys.stderr,
                )
                self._voice_clone_cache = {}

    def _save_voice_cache(self):
        """Save voice clone cache to disk if disk_cache is enabled."""
        if not self.options.get("disk_cache", False):
            return

        cache_dir = self.options.get("cache_dir", "/tmp/qwen_tts_cache")
        cache_file = os.path.join(cache_dir, "voice_cache.pkl")

        os.makedirs(cache_dir, exist_ok=True)

        try:
            with open(cache_file, "wb") as f:
                pickle.dump(self._voice_clone_cache, f)
            print(
                f"[INFO] Saved {len(self._voice_clone_cache)} cached voice(s)",
                file=sys.stderr,
            )
        except Exception as e:
            print(
                f"[WARNING] Failed to save voice cache: {e}",
                file=sys.stderr,
            )

    def TTS(self, request, context):
        """Text-to-Speech generation."""
        try:
            text = request.Text
            if not text:
                return backend_pb2.Result(
                    success=False, message="Text is required for TTS"
                )

            voice_name = request.VoiceName
            output_file = request.OutputFile

            # Get voice audio and ref_text
            if voice_name and voice_name in self.voices:
                voice_audio = self.voices[voice_name]["audio"]
                ref_text = self.voices[voice_name]["ref_text"]
            else:
                # Use default voice
                voice_audio = None
                ref_text = None

            # Generate audio
            print(f"[INFO] Generating TTS: '{text[:50]}...' using voice: {voice_name}", file=sys.stderr)

            # Prepare generation kwargs
            gen_kwargs = {
                "text": text,
            }

            if voice_audio and ref_text:
                gen_kwargs["audio"] = voice_audio
                gen_kwargs["ref_text"] = ref_text
                print(f"[INFO] Using voice clone: {voice_name}", file=sys.stderr)

            # Generate
            with torch.inference_mode():
                output = self.model.generate(**gen_kwargs)

            # Convert to audio array
            if hasattr(output, "audio"):
                audio_array = output.audio
            else:
                audio_array = output

            # Save to file
            if output_file:
                sf.write(output_file, audio_array, 24000)
                print(f"[INFO] Saved audio to: {output_file}", file=sys.stderr)

            return backend_pb2.Result(
                success=True,
                message=f"Generated audio: {output_file}",
            )

        except Exception as e:
            print(f"[ERROR] TTS generation failed: {e}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.Result(
                success=False,
                message=f"TTS generation failed: {str(e)}",
            )


def serve():
    server = grpc.server(futures(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    listen_addr = "[::]:50051"
    server.add_insecure_port(listen_addr)
    server.start()
    print(f"Server started, listening on {listen_addr}", file=sys.stderr)
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
