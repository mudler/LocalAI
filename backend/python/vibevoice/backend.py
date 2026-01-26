#!/usr/bin/env python3
"""
This is an extra gRPC server of LocalAI for VibeVoice
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
from vibevoice.modular.modeling_vibevoice_streaming_inference import VibeVoiceStreamingForConditionalGenerationInference
from vibevoice.processor.vibevoice_streaming_processor import VibeVoiceStreamingProcessor
from vibevoice.modular.modeling_vibevoice_asr import VibeVoiceASRForConditionalGeneration
from vibevoice.processor.vibevoice_asr_processor import VibeVoiceASRProcessor

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

        # Check if ASR mode is enabled
        self.asr_mode = self.options.get("asr_mode", False)
        if not isinstance(self.asr_mode, bool):
            # Handle string "true"/"false" case
            self.asr_mode = str(self.asr_mode).lower() == "true"

        # Get model path from request
        model_path = request.Model
        if not model_path:
            if self.asr_mode:
                model_path = "microsoft/VibeVoice-ASR"  # Default ASR model
            else:
                model_path = "microsoft/VibeVoice-Realtime-0.5B"  # Default TTS model
        
        # Get inference steps from options, default to 5 (TTS only)
        self.inference_steps = self.options.get("inference_steps", 5)
        if not isinstance(self.inference_steps, int) or self.inference_steps <= 0:
            self.inference_steps = 5

        # Get cfg_scale from options, default to 1.5 (TTS only)
        self.cfg_scale = self.options.get("cfg_scale", 1.5)
        if not isinstance(self.cfg_scale, (int, float)) or self.cfg_scale <= 0:
            self.cfg_scale = 1.5

        # Get ASR generation parameters from options
        self.max_new_tokens = self.options.get("max_new_tokens", 512)
        if not isinstance(self.max_new_tokens, int) or self.max_new_tokens <= 0:
            self.max_new_tokens = 512

        self.temperature = self.options.get("temperature", 0.0)
        if not isinstance(self.temperature, (int, float)) or self.temperature < 0:
            self.temperature = 0.0

        self.top_p = self.options.get("top_p", 1.0)
        if not isinstance(self.top_p, (int, float)) or self.top_p <= 0:
            self.top_p = 1.0

        self.do_sample = self.options.get("do_sample", None)
        if self.do_sample is None:
            # Default: use sampling if temperature > 0
            self.do_sample = self.temperature > 0
        elif not isinstance(self.do_sample, bool):
            self.do_sample = str(self.do_sample).lower() == "true"

        self.num_beams = self.options.get("num_beams", 1)
        if not isinstance(self.num_beams, int) or self.num_beams < 1:
            self.num_beams = 1

        # Determine voices directory
        # Priority order:
        # 1. voices_dir option (explicitly set by user - highest priority)
        # 2. Relative to ModelFile if provided
        # 3. Relative to ModelPath (models directory) if provided
        # 4. Backend directory
        # 5. Absolute path from AudioPath if provided
        voices_dir = None
        
        # First check if voices_dir is explicitly set in options
        if "voices_dir" in self.options:
            voices_dir_option = self.options["voices_dir"]
            if isinstance(voices_dir_option, str) and voices_dir_option.strip():
                voices_dir = voices_dir_option.strip()
                # If relative path, try to resolve it relative to ModelPath or ModelFile
                if not os.path.isabs(voices_dir):
                    if hasattr(request, 'ModelPath') and request.ModelPath:
                        voices_dir = os.path.join(request.ModelPath, voices_dir)
                    elif request.ModelFile:
                        model_file_base = os.path.dirname(request.ModelFile)
                        voices_dir = os.path.join(model_file_base, voices_dir)
                    # If still relative, make it absolute from current working directory
                    if not os.path.isabs(voices_dir):
                        voices_dir = os.path.abspath(voices_dir)
                # Check if the directory exists
                if not os.path.exists(voices_dir):
                    print(f"Warning: voices_dir option specified but directory does not exist: {voices_dir}", file=sys.stderr)
                    voices_dir = None
        
        # If not set via option, try relative to ModelFile if provided
        if not voices_dir and request.ModelFile:
            model_file_base = os.path.dirname(request.ModelFile)
            voices_dir = os.path.join(model_file_base, "voices", "streaming_model")
            if not os.path.exists(voices_dir):
                voices_dir = None
        
        # If not found, try relative to ModelPath (models directory)
        if not voices_dir and hasattr(request, 'ModelPath') and request.ModelPath:
            voices_dir = os.path.join(request.ModelPath, "voices", "streaming_model")
            if not os.path.exists(voices_dir):
                voices_dir = None
        
        # If not found, try relative to backend directory
        if not voices_dir:
            backend_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
            voices_dir = os.path.join(backend_dir, "vibevoice", "voices", "streaming_model")
            if not os.path.exists(voices_dir):
                # Try absolute path from AudioPath if provided
                if request.AudioPath and os.path.isabs(request.AudioPath):
                    voices_dir = os.path.dirname(request.AudioPath)
                else:
                    voices_dir = None

        # Initialize voice-related attributes (TTS only)
        self.voices_dir = voices_dir
        self.voice_presets = {}
        self._voice_cache = {}
        self.default_voice_key = None
    
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

        try:
            if self.asr_mode:
                # Load ASR model and processor
                print(f"Loading ASR processor & model from {model_path}", file=sys.stderr)
                
                # Load ASR processor
                self.processor = VibeVoiceASRProcessor.from_pretrained(
                    model_path,
                    language_model_pretrained_name="Qwen/Qwen2.5-7B"
                )

                print(f"Using device: {self.device}, torch_dtype: {load_dtype}, attn_implementation: {attn_impl_primary}", file=sys.stderr)

                # Load ASR model with device-specific logic
                try:
                    if self.device == "mps":
                        self.model = VibeVoiceASRForConditionalGeneration.from_pretrained(
                            model_path,
                            dtype=load_dtype,
                            device_map=None,  # load then move
                            attn_implementation=attn_impl_primary,
                            trust_remote_code=True
                        )
                        self.model.to("mps")
                    else:  # cpu
                        self.model = VibeVoiceASRForConditionalGeneration.from_pretrained(
                            model_path,
                            dtype=load_dtype,
                            device_map=device_map,
                            attn_implementation=attn_impl_primary,
                            trust_remote_code=True
                        )
                except Exception as e:
                    if attn_impl_primary == 'flash_attention_2':
                        print(f"[ERROR] : {type(e).__name__}: {e}", file=sys.stderr)
                        print(traceback.format_exc(), file=sys.stderr)
                        print("Error loading the ASR model. Trying to use SDPA.", file=sys.stderr)
                        self.model = VibeVoiceASRForConditionalGeneration.from_pretrained(
                            model_path,
                            dtype=load_dtype,
                            device_map=(self.device if self.device in ("cuda", "cpu") else None),
                            attn_implementation='sdpa',
                            trust_remote_code=True
                        )
                        if self.device == "mps":
                            self.model.to("mps")
                    else:
                        raise e

                self.model.eval()
                print(f"ASR model loaded successfully", file=sys.stderr)
            else:
                # Load TTS model and processor (existing logic)
                # Load voice presets if directory exists
                if self.voices_dir and os.path.exists(self.voices_dir):
                    self._load_voice_presets()
                else:
                    print(f"Warning: Voices directory not found. Voice presets will not be available.", file=sys.stderr)

                print(f"Loading TTS processor & model from {model_path}", file=sys.stderr)
                self.processor = VibeVoiceStreamingProcessor.from_pretrained(model_path)


                print(f"Using device: {self.device}, torch_dtype: {load_dtype}, attn_implementation: {attn_impl_primary}", file=sys.stderr)

                # Load model with device-specific logic
                try:
                    if self.device == "mps":
                        self.model = VibeVoiceStreamingForConditionalGenerationInference.from_pretrained(
                            model_path,
                            torch_dtype=load_dtype,
                            attn_implementation=attn_impl_primary,
                            device_map=None,  # load then move
                        )
                        self.model.to("mps")
                    else:  # cpu
                        self.model = VibeVoiceStreamingForConditionalGenerationInference.from_pretrained(
                            model_path,
                            torch_dtype=load_dtype,
                            device_map=device_map,
                            attn_implementation=attn_impl_primary,
                        )
                except Exception as e:
                    if attn_impl_primary == 'flash_attention_2':
                        print(f"[ERROR] : {type(e).__name__}: {e}", file=sys.stderr)
                        print(traceback.format_exc(), file=sys.stderr)
                        print("Error loading the model. Trying to use SDPA. However, note that only flash_attention_2 has been fully tested, and using SDPA may result in lower audio quality.", file=sys.stderr)
                        self.model = VibeVoiceStreamingForConditionalGenerationInference.from_pretrained(
                            model_path,
                            torch_dtype=load_dtype,
                            device_map=(self.device if self.device in ("cuda", "cpu") else None),
                            attn_implementation='sdpa'
                        )
                        if self.device == "mps":
                            self.model.to("mps")
                    else:
                        raise e

                self.model.eval()
                self.model.set_ddpm_inference_steps(num_steps=self.inference_steps)

                # Set default voice key
                if self.voice_presets:
                    # Try to get default from environment or use first available
                    preset_name = os.environ.get("VOICE_PRESET")
                    self.default_voice_key = self._determine_voice_key(preset_name)
                    print(f"Default voice preset: {self.default_voice_key}", file=sys.stderr)
                else:
                    print("Warning: No voice presets available. Voice selection will not work.", file=sys.stderr)

        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    def _load_voice_presets(self):
        """Load voice presets from the voices directory."""
        if not self.voices_dir or not os.path.exists(self.voices_dir):
            self.voice_presets = {}
            return

        self.voice_presets = {}
        
        # Get all .pt files in the voices directory
        pt_files = [f for f in os.listdir(self.voices_dir) 
                    if f.lower().endswith('.pt') and os.path.isfile(os.path.join(self.voices_dir, f))]
        
        # Create dictionary with filename (without extension) as key
        for pt_file in pt_files:
            # Remove .pt extension to get the name
            name = os.path.splitext(pt_file)[0]
            # Create full path
            full_path = os.path.join(self.voices_dir, pt_file)
            self.voice_presets[name] = full_path
        
        # Sort the voice presets alphabetically by name
        self.voice_presets = dict(sorted(self.voice_presets.items()))
        
        print(f"Found {len(self.voice_presets)} voice files in {self.voices_dir}", file=sys.stderr)
        if self.voice_presets:
            print(f"Available voices: {', '.join(self.voice_presets.keys())}", file=sys.stderr)

    def _determine_voice_key(self, name):
        """Determine voice key from name or use default."""
        if name and name in self.voice_presets:
            return name
        
        # Try default key
        default_key = "en-WHTest_man"
        if default_key in self.voice_presets:
            return default_key
        
        # Use first available
        if self.voice_presets:
            first_key = next(iter(self.voice_presets))
            print(f"Using fallback voice preset: {first_key}", file=sys.stderr)
            return first_key
        
        return None

    def _get_voice_path(self, speaker_name):
        """Get voice file path for a given speaker name."""
        if not self.voice_presets:
            return None
        
        # First try exact match
        if speaker_name and speaker_name in self.voice_presets:
            return self.voice_presets[speaker_name]
        
        # Try partial matching (case insensitive)
        if speaker_name:
            speaker_lower = speaker_name.lower()
            for preset_name, path in self.voice_presets.items():
                if preset_name.lower() in speaker_lower or speaker_lower in preset_name.lower():
                    return path
        
        # Default to first voice if no match found
        if self.default_voice_key and self.default_voice_key in self.voice_presets:
            return self.voice_presets[self.default_voice_key]
        elif self.voice_presets:
            default_voice = list(self.voice_presets.values())[0]
            print(f"Warning: No voice preset found for '{speaker_name}', using default voice: {default_voice}", file=sys.stderr)
            return default_voice
        
        return None

    def _ensure_voice_cached(self, voice_path):
        """Load and cache voice preset."""
        if not voice_path or not os.path.exists(voice_path):
            return None
        
        # Use path as cache key
        if voice_path not in self._voice_cache:
            print(f"Loading prefilled prompt from {voice_path}", file=sys.stderr)
            prefilled_outputs = torch.load(
                voice_path,
                map_location=self._torch_device,
                weights_only=False,
            )
            self._voice_cache[voice_path] = prefilled_outputs
        
        return self._voice_cache[voice_path]

    def TTS(self, request, context):
        try:
            # Get voice selection
            # Priority: request.voice > AudioPath > default
            voice_path = None
            voice_key = None
            
            if request.voice:
                # Try to get voice by name
                voice_path = self._get_voice_path(request.voice)
                if voice_path:
                    voice_key = request.voice
            elif request.AudioPath:
                # Use AudioPath as voice file
                if os.path.isabs(request.AudioPath):
                    voice_path = request.AudioPath
                elif request.ModelFile:
                    model_file_base = os.path.dirname(request.ModelFile)
                    voice_path = os.path.join(model_file_base, request.AudioPath)
                elif hasattr(request, 'ModelPath') and request.ModelPath:
                    voice_path = os.path.join(request.ModelPath, request.AudioPath)
                else:
                    voice_path = request.AudioPath
            elif self.default_voice_key:
                voice_path = self._get_voice_path(self.default_voice_key)
                voice_key = self.default_voice_key

            if not voice_path or not os.path.exists(voice_path):
                return backend_pb2.Result(
                    success=False, 
                    message=f"Voice file not found: {voice_path}. Please provide a valid voice preset or AudioPath."
                )

            # Load voice preset
            prefilled_outputs = self._ensure_voice_cached(voice_path)
            if prefilled_outputs is None:
                return backend_pb2.Result(
                    success=False,
                    message=f"Failed to load voice preset from {voice_path}"
                )

            # Get generation parameters from options
            cfg_scale = self.options.get("cfg_scale", self.cfg_scale)
            inference_steps = self.options.get("inference_steps", self.inference_steps)
            do_sample = self.options.get("do_sample", False)
            temperature = self.options.get("temperature", 0.9)
            top_p = self.options.get("top_p", 0.9)

            # Update inference steps if needed
            if inference_steps != self.inference_steps:
                self.model.set_ddpm_inference_steps(num_steps=inference_steps)
                self.inference_steps = inference_steps

            # Prepare text
            text = request.text.strip().replace("'", "'").replace('"', '"').replace('"', '"')

            # Prepare inputs
            inputs = self.processor.process_input_with_cached_prompt(
                text=text,
                cached_prompt=prefilled_outputs,
                padding=True,
                return_tensors="pt",
                return_attention_mask=True,
            )

            # Move tensors to target device
            target_device = self._torch_device
            for k, v in inputs.items():
                if torch.is_tensor(v):
                    inputs[k] = v.to(target_device)

            print(f"Generating audio with cfg_scale: {cfg_scale}, inference_steps: {inference_steps}", file=sys.stderr)

            # Generate audio
            outputs = self.model.generate(
                **inputs,
                max_new_tokens=None,
                cfg_scale=cfg_scale,
                tokenizer=self.processor.tokenizer,
                generation_config={
                    'do_sample': do_sample,
                    'temperature': temperature if do_sample else 1.0,
                    'top_p': top_p if do_sample else 1.0,
                },
                verbose=False,
                all_prefilled_outputs=copy.deepcopy(prefilled_outputs) if prefilled_outputs is not None else None,
            )

            # Save output
            if outputs.speech_outputs and outputs.speech_outputs[0] is not None:
                self.processor.save_audio(
                    outputs.speech_outputs[0],  # First (and only) batch item
                    output_path=request.dst,
                )
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

    def AudioTranscription(self, request, context):
        """Transcribe audio file to text using ASR model."""
        try:
            # Validate ASR mode is active
            if not self.asr_mode:
                return backend_pb2.TranscriptResult(
                    segments=[],
                    text="",
                )
                # Note: We return empty result instead of error to match faster-whisper behavior
            
            # Get audio file path
            audio_path = request.dst
            if not audio_path or not os.path.exists(audio_path):
                print(f"Error: Audio file not found: {audio_path}", file=sys.stderr)
                return backend_pb2.TranscriptResult(
                    segments=[],
                    text="",
                )
            
            print(f"Transcribing audio file: {audio_path}", file=sys.stderr)
            
            # Process audio with ASR processor
            inputs = self.processor(
                audio=audio_path,
                sampling_rate=None,
                return_tensors="pt",
                padding=True,
                add_generation_prompt=True
            )
            
            # Move tensors to target device
            for k, v in inputs.items():
                if torch.is_tensor(v):
                    inputs[k] = v.to(self._torch_device)
            
            # Prepare generation config
            generation_config = {
                "max_new_tokens": self.max_new_tokens,
                "pad_token_id": self.processor.pad_id,
                "eos_token_id": self.processor.tokenizer.eos_token_id,
            }
            
            # Beam search vs sampling
            if self.num_beams > 1:
                generation_config["num_beams"] = self.num_beams
                generation_config["do_sample"] = False  # Beam search doesn't use sampling
            else:
                generation_config["do_sample"] = self.do_sample
                # Only set temperature and top_p when sampling is enabled
                if self.do_sample:
                    generation_config["temperature"] = self.temperature
                    generation_config["top_p"] = self.top_p
            
            print(f"Generating transcription with max_new_tokens: {self.max_new_tokens}, temperature: {self.temperature}, do_sample: {self.do_sample}, num_beams: {self.num_beams}", file=sys.stderr)
            
            # Generate transcription
            with torch.no_grad():
                output_ids = self.model.generate(
                    **inputs,
                    **generation_config
                )
            
            # Decode outputs
            input_length = inputs['input_ids'].shape[1]
            generated_ids = output_ids[0, input_length:]  # Get generated tokens (excluding input)
            
            # Remove padding tokens from the end
            # Find the first eos_token or pad_token
            eos_positions = (generated_ids == self.processor.tokenizer.eos_token_id).nonzero(as_tuple=True)[0]
            if len(eos_positions) > 0:
                generated_ids = generated_ids[:eos_positions[0] + 1]
            
            # Decode generated text
            generated_text = self.processor.decode(generated_ids, skip_special_tokens=True)
            
            # Parse structured output to get segments
            result_segments = []
            try:
                transcription_segments = self.processor.post_process_transcription(generated_text)
                
                if transcription_segments:
                    # Map segments to TranscriptSegment format
                    for idx, seg in enumerate(transcription_segments):
                        # Extract timing information (if available)
                        # Handle both dict and object with attributes
                        if isinstance(seg, dict):
                            start_time = seg.get('start_time', 0)
                            end_time = seg.get('end_time', 0)
                            text = seg.get('text', '')
                            speaker_id = seg.get('speaker_id', None)
                        else:
                            # Handle object with attributes
                            start_time = getattr(seg, 'start_time', 0)
                            end_time = getattr(seg, 'end_time', 0)
                            text = getattr(seg, 'text', '')
                            speaker_id = getattr(seg, 'speaker_id', None)
                        
                        # Convert time to milliseconds (assuming seconds)
                        start_ms = int(start_time * 1000) if isinstance(start_time, (int, float)) else 0
                        end_ms = int(end_time * 1000) if isinstance(end_time, (int, float)) else 0
                        
                        # Add speaker info to text if available
                        if speaker_id is not None:
                            text = f"[Speaker {speaker_id}] {text}"
                        
                        result_segments.append(backend_pb2.TranscriptSegment(
                            id=idx,
                            start=start_ms,
                            end=end_ms,
                            text=text,
                            tokens=[]  # Token IDs not extracted for now
                        ))
            except Exception as e:
                print(f"Warning: Failed to parse structured output: {e}", file=sys.stderr)
                print(traceback.format_exc(), file=sys.stderr)
                # Fallback: create a single segment with the full text
                if generated_text:
                    result_segments.append(backend_pb2.TranscriptSegment(
                        id=0,
                        start=0,
                        end=0,
                        text=generated_text,
                        tokens=[]
                    ))
            
            # Combine all segment texts into full transcription
            if result_segments:
                full_text = " ".join([seg.text for seg in result_segments])
            else:
                full_text = generated_text if generated_text else ""
            
            print(f"Transcription completed: {len(result_segments)} segments", file=sys.stderr)
            
            return backend_pb2.TranscriptResult(
                segments=result_segments,
                text=full_text
            )
            
        except Exception as err:
            print(f"Error in AudioTranscription: {err}", file=sys.stderr)
            print(traceback.format_exc(), file=sys.stderr)
            return backend_pb2.TranscriptResult(
                segments=[],
                text="",
            )

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
