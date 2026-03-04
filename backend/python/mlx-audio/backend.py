#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import signal
import sys
import os
import shutil
import glob
from typing import List
import time
import tempfile

import backend_pb2
import backend_pb2_grpc

import grpc
from mlx_audio.tts.utils import load_model
import soundfile as sf
import numpy as np
import uuid

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
    A gRPC servicer that implements the Backend service defined in backend.proto.
    This backend provides TTS (Text-to-Speech) functionality using MLX-Audio.
    """

    def Health(self, request, context):
        """
        Returns a health check message.

        Args:
            request: The health check request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The health check reply.
        """
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    async def LoadModel(self, request, context):
        """
        Loads a TTS model using MLX-Audio.

        Args:
            request: The load model request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The load model result.
        """
        try:
            print(f"Loading MLX-Audio TTS model: {request.Model}", file=sys.stderr)
            print(f"Request: {request}", file=sys.stderr)
            
            # Parse options like in the kokoro backend
            options = request.Options
            self.options = {}
            
            # The options are a list of strings in this form optname:optvalue
            # We store all the options in a dict for later use
            for opt in options:
                if ":" not in opt:
                    continue
                key, value = opt.split(":", 1)  # Split only on first colon to handle values with colons
                
                # Convert numeric values to appropriate types
                if is_float(value):
                    value = float(value)
                elif is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                    
                self.options[key] = value
            
            print(f"Options: {self.options}", file=sys.stderr)
            
            # Load the model using MLX-Audio's load_model function
            try:
                self.tts_model = load_model(request.Model)
                self.model_path = request.Model
                print(f"TTS model loaded successfully from {request.Model}", file=sys.stderr)
            except Exception as model_err:
                print(f"Error loading TTS model: {model_err}", file=sys.stderr)
                return backend_pb2.Result(success=False, message=f"Failed to load model: {model_err}")
                
        except Exception as err:
            print(f"Error loading MLX-Audio TTS model {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading MLX-Audio TTS model: {err}")

        print("MLX-Audio TTS model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="MLX-Audio TTS model loaded successfully", success=True)

    def TTS(self, request, context):
        """
        Generates TTS audio from text using MLX-Audio.

        Args:
            request: A TTSRequest object containing text, model, destination, voice, and language.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            A Result object indicating success or failure.
        """
        try:
            # Check if model is loaded
            if not hasattr(self, 'tts_model') or self.tts_model is None:
                return backend_pb2.Result(success=False, message="TTS model not loaded. Please call LoadModel first.")
            
            print(f"Generating TTS with MLX-Audio - text: {request.text[:50]}..., voice: {request.voice}, language: {request.language}", file=sys.stderr)
            
            # Handle speed parameter based on model type
            speed_value = self._handle_speed_parameter(request, self.model_path)
            
            # Map language names to codes if needed
            lang_code = self._map_language_code(request.language, request.voice)
            
            # Prepare generation parameters
            gen_params = {
                "text": request.text,
                "speed": speed_value,
                "verbose": False,
            }
            
            # Add model-specific parameters
            if request.voice and request.voice.strip():
                gen_params["voice"] = request.voice
            
            # Check if model supports language codes (primarily Kokoro)
            if "kokoro" in self.model_path.lower():
                gen_params["lang_code"] = lang_code
            
            # Add pitch and gender for Spark models
            if "spark" in self.model_path.lower():
                gen_params["pitch"] = 1.0  # Default to moderate
                gen_params["gender"] = "female"  # Default to female
            
            print(f"Generation parameters: {gen_params}", file=sys.stderr)
            
            # Generate audio using the loaded model
            try:
                results = self.tts_model.generate(**gen_params)
            except Exception as gen_err:
                print(f"Error during TTS generation: {gen_err}", file=sys.stderr)
                return backend_pb2.Result(success=False, message=f"TTS generation failed: {gen_err}")
            
            # Process the generated audio segments
            audio_arrays = []
            for segment in results:
                audio_arrays.append(segment.audio)
            
            # If no segments, return error
            if not audio_arrays:
                print("No audio segments generated", file=sys.stderr)
                return backend_pb2.Result(success=False, message="No audio generated")
            
            # Concatenate all segments
            cat_audio = np.concatenate(audio_arrays, axis=0)
            
            # Generate output filename and path
            if request.dst:
                output_path = request.dst
            else:
                unique_id = str(uuid.uuid4())
                filename = f"tts_{unique_id}.wav"
                output_path = filename
            
            # Write the audio as a WAV
            try:
                sf.write(output_path, cat_audio, 24000)
                print(f"Successfully wrote audio file to {output_path}", file=sys.stderr)
                
                # Verify the file exists and has content
                if not os.path.exists(output_path):
                    print(f"File was not created at {output_path}", file=sys.stderr)
                    return backend_pb2.Result(success=False, message="Failed to create audio file")
                
                file_size = os.path.getsize(output_path)
                if file_size == 0:
                    print("File was created but is empty", file=sys.stderr)
                    return backend_pb2.Result(success=False, message="Generated audio file is empty")
                
                print(f"Audio file size: {file_size} bytes", file=sys.stderr)
                
            except Exception as write_err:
                print(f"Error writing audio file: {write_err}", file=sys.stderr)
                return backend_pb2.Result(success=False, message=f"Failed to save audio: {write_err}")
            
            return backend_pb2.Result(success=True, message=f"TTS audio generated successfully: {output_path}")
                
        except Exception as e:
            print(f"Error in MLX-Audio TTS: {e}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"TTS generation failed: {str(e)}")

    async def Predict(self, request, context):
        """
        Generates TTS audio based on the given prompt using MLX-Audio TTS.
        This is a fallback method for compatibility with the Predict endpoint.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        try:
            # Check if model is loaded
            if not hasattr(self, 'tts_model') or self.tts_model is None:
                context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
                context.set_details("TTS model not loaded. Please call LoadModel first.")
                return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
            
            # For TTS, we expect the prompt to contain the text to synthesize
            if not request.Prompt:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Prompt is required for TTS generation")
                return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
            
            # Handle speed parameter based on model type
            speed_value = self._handle_speed_parameter(request, self.model_path)
            
            # Map language names to codes if needed
            lang_code = self._map_language_code(None, None)  # Use defaults for Predict
            
            # Prepare generation parameters
            gen_params = {
                "text": request.Prompt,
                "speed": speed_value,
                "verbose": False,
            }
            
            # Add model-specific parameters
            if hasattr(self, 'options') and 'voice' in self.options:
                gen_params["voice"] = self.options['voice']
            
            # Check if model supports language codes (primarily Kokoro)
            if "kokoro" in self.model_path.lower():
                gen_params["lang_code"] = lang_code
            
            print(f"Generating TTS with MLX-Audio - text: {request.Prompt[:50]}..., params: {gen_params}", file=sys.stderr)
            
            # Generate audio using the loaded model
            try:
                results = self.tts_model.generate(**gen_params)
            except Exception as gen_err:
                print(f"Error during TTS generation: {gen_err}", file=sys.stderr)
                context.set_code(grpc.StatusCode.INTERNAL)
                context.set_details(f"TTS generation failed: {gen_err}")
                return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
            
            # Process the generated audio segments
            audio_arrays = []
            for segment in results:
                audio_arrays.append(segment.audio)
            
            # If no segments, return error
            if not audio_arrays:
                print("No audio segments generated", file=sys.stderr)
                return backend_pb2.Reply(message=bytes("No audio generated", encoding='utf-8'))
            
            # Concatenate all segments
            cat_audio = np.concatenate(audio_arrays, axis=0)
            duration = len(cat_audio) / 24000  # Assuming 24kHz sample rate
            
            # Return success message with audio information
            response = f"TTS audio generated successfully. Duration: {duration:.2f}s, Sample rate: 24000Hz"
            return backend_pb2.Reply(message=bytes(response, encoding='utf-8'))
            
        except Exception as e:
            print(f"Error in MLX-Audio TTS Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"TTS generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    def _handle_speed_parameter(self, request, model_path):
        """
        Handle speed parameter based on model type.
        
        Args:
            request: The TTSRequest object.
            model_path: The model path to determine model type.
            
        Returns:
            float: The processed speed value.
        """
        # Get speed from options if available
        speed = 1.0
        if hasattr(self, 'options') and 'speed' in self.options:
            speed = self.options['speed']
        
        # Handle speed parameter based on model type
        if "spark" in model_path.lower():
            # Spark actually expects float values that map to speed descriptions
            speed_map = {
                "very_low": 0.0,
                "low": 0.5,
                "moderate": 1.0,
                "high": 1.5,
                "very_high": 2.0,
            }
            if isinstance(speed, str) and speed in speed_map:
                speed_value = speed_map[speed]
            else:
                # Try to use as float, default to 1.0 (moderate) if invalid
                try:
                    speed_value = float(speed)
                    if speed_value not in [0.0, 0.5, 1.0, 1.5, 2.0]:
                        speed_value = 1.0  # Default to moderate
                except:
                    speed_value = 1.0  # Default to moderate
        else:
            # Other models use float speed values
            try:
                speed_value = float(speed)
                if speed_value < 0.5 or speed_value > 2.0:
                    speed_value = 1.0  # Default to 1.0 if out of range
            except ValueError:
                speed_value = 1.0  # Default to 1.0 if invalid
        
        return speed_value

    def _map_language_code(self, language, voice):
        """
        Map language names to codes if needed.
        
        Args:
            language: The language parameter from the request.
            voice: The voice parameter from the request.
            
        Returns:
            str: The language code.
        """
        if not language:
            # Default to voice[0] if not found
            return voice[0] if voice else "a"
        
        # Map language names to codes if needed
        language_map = {
            "american_english": "a",
            "british_english": "b",
            "spanish": "e",
            "french": "f",
            "hindi": "h",
            "italian": "i",
            "portuguese": "p",
            "japanese": "j",
            "mandarin_chinese": "z",
            # Also accept direct language codes
            "a": "a", "b": "b", "e": "e", "f": "f", "h": "h", "i": "i", "p": "p", "j": "j", "z": "z",
        }
        
        return language_map.get(language.lower(), language)

    def _build_generation_params(self, request, default_speed=1.0):
        """
        Build generation parameters from request attributes and options for MLX-Audio TTS.

        Args:
            request: The gRPC request.
            default_speed: Default speed if not specified.

        Returns:
            dict: Generation parameters for MLX-Audio
        """
        # Initialize generation parameters for MLX-Audio TTS
        generation_params = {
            'speed': default_speed,
            'voice': 'af_heart',  # Default voice
            'lang_code': 'a',     # Default language code
        }
        
        # Extract parameters from request attributes
        if hasattr(request, 'Temperature') and request.Temperature > 0:
            # Temperature could be mapped to speed variation
            generation_params['speed'] = 1.0 + (request.Temperature - 0.5) * 0.5
        
        # Override with options if available
        if hasattr(self, 'options'):
            # Speed from options
            if 'speed' in self.options:
                generation_params['speed'] = self.options['speed']
            
            # Voice from options
            if 'voice' in self.options:
                generation_params['voice'] = self.options['voice']
            
            # Language code from options
            if 'lang_code' in self.options:
                generation_params['lang_code'] = self.options['lang_code']
            
            # Model-specific parameters
            param_option_mapping = {
                'temp': 'speed',
                'temperature': 'speed',
                'top_p': 'speed',  # Map top_p to speed variation
            }
            
            for option_key, param_key in param_option_mapping.items():
                if option_key in self.options:
                    if param_key == 'speed':
                        # Ensure speed is within reasonable bounds
                        speed_val = float(self.options[option_key])
                        if 0.5 <= speed_val <= 2.0:
                            generation_params[param_key] = speed_val
        
        return generation_params

async def serve(address):
    # Start asyncio gRPC server
    server = grpc.aio.server(migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),  # 50MB
        ])
    # Add the servicer to the server
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    # Bind the server to the address
    server.add_insecure_port(address)

    # Gracefully shutdown the server on SIGTERM or SIGINT
    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(
            sig, lambda: asyncio.ensure_future(server.stop(5))
        )

    # Start the server
    await server.start()
    print("MLX-Audio TTS Server started. Listening on: " + address, file=sys.stderr)
    # Wait for the server to be terminated
    await server.wait_for_termination()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the MLX-Audio TTS gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    asyncio.run(serve(args.addr))
