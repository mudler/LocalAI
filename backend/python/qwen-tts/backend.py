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

        # Store AudioPath, ModelFile, and ModelPath from LoadModel request
        # These are used later in TTS for VoiceClone mode
        self.audio_path = request.AudioPath if hasattr(request, 'AudioPath') and request.AudioPath else None
        self.model_file = request.ModelFile if hasattr(request, 'ModelFile') and request.ModelFile else None
        self.model_path = request.ModelPath if hasattr(request, 'ModelPath') and request.ModelPath else None

        # Decide dtype & attention implementation
        if self.device == "mps":
            load_dtype = torch.float32  # MPS requires float32
            device_map = None
            attn_impl_primary = "sdpa"  # flash_attention_2 not supported on MPS
        elif self.device == "cuda":
            load_dtype = torch.bfloat16
            device_map = "cuda"
            attn_impl_primary = "flash_attention_2"
        else:  # cpu
            load_dtype = torch.float32
            device_map = "cpu"
            attn_impl_primary = "sdpa"

        print(f"Using device: {self.device}, torch_dtype: {load_dtype}, attn_implementation: {attn_impl_primary}, model_type: {self.model_type}", file=sys.stderr)
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
            print(f"[ERROR] Loading model: {type(e).__name__}: {error_msg}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            
            # Check if it's a missing feature extractor/tokenizer error
            if "speech_tokenizer" in error_msg or "preprocessor_config.json" in error_msg or "feature extractor" in error_msg.lower():
                print("\n[ERROR] Model files appear to be incomplete. This usually means:", file=sys.stderr)
                print("  1. The model download was interrupted or incomplete", file=sys.stderr)
                print("  2. The model cache is corrupted", file=sys.stderr)
                print("\nTo fix this, try:", file=sys.stderr)
                print(f"  rm -rf ~/.cache/huggingface/hub/models--Qwen--Qwen3-TTS-*", file=sys.stderr)
                print("  Then re-run to trigger a fresh download.", file=sys.stderr)
                print("\nAlternatively, try using a different model variant:", file=sys.stderr)
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice", file=sys.stderr)
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign", file=sys.stderr)
                print("  - Qwen/Qwen3-TTS-12Hz-1.7B-Base", file=sys.stderr)
            
            if attn_impl_primary == 'flash_attention_2':
                print("\nTrying to use SDPA instead of flash_attention_2...", file=sys.stderr)
                load_kwargs["attn_implementation"] = 'sdpa'
                try:
                    if self.device == "mps":
                        load_kwargs["device_map"] = None
                        self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
                        self.model.to("mps")
                    else:
                        load_kwargs["device_map"] = (self.device if self.device in ("cuda", "cpu") else None)
                        self.model = Qwen3TTSModel.from_pretrained(model_path, **load_kwargs)
                except Exception as e2:
                    print(f"[ERROR] Failed to load with SDPA: {type(e2).__name__}: {e2}", file=sys.stderr)
                    print(traceback.format_exc(), file=sys.stderr)
                    raise e2
            else:
                raise e

        print(f"Model loaded successfully: {model_path}", file=sys.stderr)

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _detect_mode(self, request):
        """Detect which mode to use based on request parameters."""
        # Priority: VoiceClone > VoiceDesign > CustomVoice

        # model_type explicitly set
        if self.model_type == "CustomVoice":
            return "CustomVoice"
        if self.model_type == "VoiceClone":
            return "VoiceClone"
        if self.model_type == "VoiceDesign":
            return "VoiceDesign"

        # VoiceClone: AudioPath is provided (from LoadModel, stored in self.audio_path)
        if self.audio_path:
            return "VoiceClone"
        
        # VoiceDesign: instruct option is provided
        if "instruct" in self.options and self.options["instruct"]:
            return "VoiceDesign"
        
        # Default to CustomVoice
        return "CustomVoice"

    def _get_ref_audio_path(self, request):
        """Get reference audio path from stored AudioPath (from LoadModel)."""
        if not self.audio_path:
            return None
        
        # If absolute path, use as-is
        if os.path.isabs(self.audio_path):
            return self.audio_path
        
        # Try relative to ModelFile
        if self.model_file:
            model_file_base = os.path.dirname(self.model_file)
            ref_path = os.path.join(model_file_base, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path
        
        # Try relative to ModelPath
        if self.model_path:
            ref_path = os.path.join(self.model_path, self.audio_path)
            if os.path.exists(ref_path):
                return ref_path
        
        # Return as-is (might be URL or base64)
        return self.audio_path

    def _get_voice_clone_prompt(self, request, ref_audio, ref_text):
        """Get or create voice clone prompt, with caching."""
        cache_key = f"{ref_audio}:{ref_text}"
        
        if cache_key not in self._voice_clone_cache:
            print(f"Creating voice clone prompt from {ref_audio}", file=sys.stderr)
            try:
                prompt_items = self.model.create_voice_clone_prompt(
                    ref_audio=ref_audio,
                    ref_text=ref_text,
                    x_vector_only_mode=self.options.get("x_vector_only_mode", False),
                )
                self._voice_clone_cache[cache_key] = prompt_items
            except Exception as e:
                print(f"Error creating voice clone prompt: {e}", file=sys.stderr)
                print(traceback.format_exc(), file=sys.stderr)
                return None
        
        return self._voice_clone_cache[cache_key]

    def TTS(self, request, context):
        try:
            # Check if dst is provided
            if not request.dst:
                return backend_pb2.Result(
                    success=False,
                    message="dst (output path) is required"
                )
            
            # Prepare text
            text = request.text.strip()
            if not text:
                return backend_pb2.Result(
                    success=False,
                    message="Text is empty"
                )

            # Get language (auto-detect if not provided)
            language = request.language if hasattr(request, 'language') and request.language else None
            if not language or language == "":
                language = "Auto"  # Auto-detect language

            # Detect mode
            mode = self._detect_mode(request)
            print(f"Detected mode: {mode}", file=sys.stderr)

            # Get generation parameters from options
            max_new_tokens = self.options.get("max_new_tokens", None)
            top_p = self.options.get("top_p", None)
            temperature = self.options.get("temperature", None)
            do_sample = self.options.get("do_sample", None)

            # Prepare generation kwargs
            generation_kwargs = {}
            if max_new_tokens is not None:
                generation_kwargs["max_new_tokens"] = max_new_tokens
            if top_p is not None:
                generation_kwargs["top_p"] = top_p
            if temperature is not None:
                generation_kwargs["temperature"] = temperature
            if do_sample is not None:
                generation_kwargs["do_sample"] = do_sample

            instruct = self.options.get("instruct", "")
            if instruct is not None and instruct != "":
                generation_kwargs["instruct"] = instruct

            # Generate audio based on mode
            if mode == "VoiceClone":
                # VoiceClone mode
                ref_audio = self._get_ref_audio_path(request)
                if not ref_audio:
                    return backend_pb2.Result(
                        success=False,
                        message="AudioPath is required for VoiceClone mode"
                    )
                
                ref_text = self.options.get("ref_text", None)
                if not ref_text:
                    # Try to get from request if available
                    if hasattr(request, 'ref_text') and request.ref_text:
                        ref_text = request.ref_text
                    else:
                        # x_vector_only_mode doesn't require ref_text
                        if not self.options.get("x_vector_only_mode", False):
                            return backend_pb2.Result(
                                success=False,
                                message="ref_text is required for VoiceClone mode (or set x_vector_only_mode=true)"
                            )

                # Check if we should use cached prompt
                use_cached_prompt = self.options.get("use_cached_prompt", True)
                voice_clone_prompt = None
                
                if use_cached_prompt:
                    voice_clone_prompt = self._get_voice_clone_prompt(request, ref_audio, ref_text)
                    if voice_clone_prompt is None:
                        return backend_pb2.Result(
                            success=False,
                            message="Failed to create voice clone prompt"
                        )

                if voice_clone_prompt:
                    # Use cached prompt
                    wavs, sr = self.model.generate_voice_clone(
                        text=text,
                        language=language,
                        voice_clone_prompt=voice_clone_prompt,
                        **generation_kwargs
                    )
                else:
                    # Create prompt on-the-fly
                    wavs, sr = self.model.generate_voice_clone(
                        text=text,
                        language=language,
                        ref_audio=ref_audio,
                        ref_text=ref_text,
                        x_vector_only_mode=self.options.get("x_vector_only_mode", False),
                        **generation_kwargs
                    )

            elif mode == "VoiceDesign":
                # VoiceDesign mode
                if not instruct:
                    return backend_pb2.Result(
                        success=False,
                        message="instruct option is required for VoiceDesign mode"
                    )

                wavs, sr = self.model.generate_voice_design(
                    text=text,
                    language=language,
                    instruct=instruct,
                    **generation_kwargs
                )

            else:
                # CustomVoice mode (default)
                speaker = request.voice if request.voice else None
                if not speaker:
                    # Try to get from options
                    speaker = self.options.get("speaker", None)
                    if not speaker:
                        # Use default speaker
                        speaker = "Vivian"
                        print(f"No speaker specified, using default: {speaker}", file=sys.stderr)

                # Validate speaker if model supports it
                if hasattr(self.model, 'get_supported_speakers'):
                    try:
                        supported_speakers = self.model.get_supported_speakers()
                        if speaker not in supported_speakers:
                            print(f"Warning: Speaker '{speaker}' not in supported list. Available: {supported_speakers}", file=sys.stderr)
                            # Try to find a close match (case-insensitive)
                            speaker_lower = speaker.lower()
                            for sup_speaker in supported_speakers:
                                if sup_speaker.lower() == speaker_lower:
                                    speaker = sup_speaker
                                    print(f"Using matched speaker: {speaker}", file=sys.stderr)
                                    break
                    except Exception as e:
                        print(f"Warning: Could not get supported speakers: {e}", file=sys.stderr)

                wavs, sr = self.model.generate_custom_voice(
                    text=text,
                    language=language,
                    speaker=speaker,
                    **generation_kwargs
                )

            # Save output
            if wavs is not None and len(wavs) > 0:
                # wavs is a list, take first element
                audio_data = wavs[0] if isinstance(wavs, list) else wavs
                sf.write(request.dst, audio_data, sr)
                print(f"Saved output to {request.dst}", file=sys.stderr)
            else:
                return backend_pb2.Result(
                    success=False,
                    message="No audio output generated"
                )

        except Exception as err:
            print(f"Error in TTS: {err}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
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
