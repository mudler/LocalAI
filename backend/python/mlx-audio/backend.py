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
from mlx_audio.tts.generate import generate_audio
import soundfile as sf
import numpy as np

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer that implements the Backend service defined in backend.proto.
    This backend provides TTS (Text-to-Speech) functionality using MLX-Audio.
    """

    def _is_float(self, s):
        """Check if a string can be converted to float."""
        try:
            float(s)
            return True
        except ValueError:
            return False

    def _is_int(self, s):
        """Check if a string can be converted to int."""
        try:
            int(s)
            return True
        except ValueError:
            return False

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
                if self._is_float(value):
                    value = float(value)
                elif self._is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                    
                self.options[key] = value
            
            print(f"Options: {self.options}", file=sys.stderr)
            
            # Store the model path for later use
            self.model_path = request.Model
                
        except Exception as err:
            print(f"Error loading MLX-Audio TTS model {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading MLX-Audio TTS model: {err}")

        print("MLX-Audio TTS model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="MLX-Audio TTS model loaded successfully", success=True)

    async def Predict(self, request, context):
        """
        Generates text based on the given prompt using MLX-Audio TTS.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        try:
            # For TTS, we expect the prompt to contain the text to synthesize
            if not request.Prompt:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Prompt is required for TTS generation")
                return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
            
            # Get generation parameters
            generation_params = self._build_generation_params(request)
            
            print(f"Generating TTS with MLX-Audio - text: {request.Prompt[:50]}..., params: {generation_params}", file=sys.stderr)
            
            # Generate audio using MLX-Audio
            # Note: MLX-Audio generates files, so we'll create a temporary file and read it back
            with tempfile.NamedTemporaryFile(delete=False, suffix='.wav') as tmp_file:
                temp_output = tmp_file.name
            
            try:
                # Generate audio using MLX-Audio
                generate_audio(
                    text=request.Prompt,
                    model_path=self.model_path,
                    voice=generation_params.get('voice', 'af_heart'),
                    speed=generation_params.get('speed', 1.0),
                    lang_code=generation_params.get('lang_code', 'a'),
                    file_prefix="tts_output",
                    audio_format="wav",
                    sample_rate=24000,
                    join_audio=True,
                    verbose=False
                )
                
                # Read the generated audio file
                audio_data, sample_rate = sf.read(temp_output)
                
                # Convert to base64 for response (or handle as needed)
                # For now, we'll return a success message
                response = f"TTS audio generated successfully. Sample rate: {sample_rate}, Duration: {len(audio_data)/sample_rate:.2f}s"
                
                return backend_pb2.Reply(message=bytes(response, encoding='utf-8'))
                
            finally:
                # Clean up temporary file
                if os.path.exists(temp_output):
                    os.remove(temp_output)
            
        except Exception as e:
            print(f"Error in MLX-Audio TTS Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"TTS generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

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
            print(f"Generating TTS with MLX-Audio - text: {request.text[:50]}..., voice: {request.voice}, language: {request.language}", file=sys.stderr)
            
            # Get generation parameters
            generation_params = self._build_generation_params_from_tts(request)
            
            # Generate audio using MLX-Audio
            generate_audio(
                text=request.text,
                model_path=request.model if request.model else self.model_path,
                voice=generation_params.get('voice', 'af_heart'),
                speed=generation_params.get('speed', 1.0),
                lang_code=generation_params.get('lang_code', 'a'),
                file_prefix="tts_output",
                audio_format="wav",
                sample_rate=24000,
                join_audio=True,
                verbose=False
            )
            
            # The generate_audio function creates files with a specific naming pattern
            # We need to find the generated file and move it to the requested destination
            generated_files = glob.glob("tts_output_*.wav")
            
            if generated_files:
                # Sort by creation time to get the most recent
                generated_files.sort(key=lambda x: os.path.getctime(x), reverse=True)
                generated_file = generated_files[0]
                
                # Move to requested destination if specified
                if request.dst:
                    shutil.move(generated_file, request.dst)
                    print(f"TTS audio saved to: {request.dst}", file=sys.stderr)
                else:
                    print(f"TTS audio generated: {generated_file}", file=sys.stderr)
                
                # Clean up other generated files
                for file in generated_files[1:]:
                    try:
                        os.remove(file)
                    except:
                        pass
                
                return backend_pb2.Result(success=True, message=f"TTS audio generated successfully")
            else:
                return backend_pb2.Result(success=False, message="No audio file was generated")
                
        except Exception as e:
            print(f"Error in MLX-Audio TTS: {e}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"TTS generation failed: {str(e)}")

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.
        
        Note: MLX-Audio doesn't support embeddings directly. This method returns an error.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """
        print("Embeddings not supported in MLX-Audio backend", file=sys.stderr)
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("Embeddings are not supported in the MLX-Audio backend.")
        return backend_pb2.EmbeddingResult()

    async def PredictStream(self, request, context):
        """
        Generates TTS audio based on the given prompt and streams the results using MLX-Audio.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Yields:
            backend_pb2.Reply: Streaming TTS results.
        """
        try:
            # For TTS streaming, we expect the prompt to contain the text to synthesize
            if not request.Prompt:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Prompt is required for TTS generation")
                yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))
                return
            
            # Get generation parameters
            generation_params = self._build_generation_params(request)
            
            print(f"Streaming TTS with MLX-Audio - text: {request.Prompt[:50]}..., params: {generation_params}", file=sys.stderr)
            
            # Generate audio using MLX-Audio
            with tempfile.NamedTemporaryFile(delete=False, suffix='.wav') as tmp_file:
                temp_output = tmp_file.name
            
            try:
                # Generate audio using MLX-Audio
                generate_audio(
                    text=request.Prompt,
                    model_path=self.model_path,
                    voice=generation_params.get('voice', 'af_heart'),
                    speed=generation_params.get('speed', 1.0),
                    lang_code=generation_params.get('lang_code', 'a'),
                    file_prefix="tts_stream",
                    audio_format="wav",
                    sample_rate=24000,
                    join_audio=True,
                    verbose=False
                )
                
                # Read the generated audio file
                audio_data, sample_rate = sf.read(temp_output)
                
                # For streaming, we'll yield progress updates
                # In a real implementation, you might want to stream the audio data itself
                yield backend_pb2.Reply(message=bytes(f"TTS generation started. Text length: {len(request.Prompt)}", encoding='utf-8'))
                yield backend_pb2.Reply(message=bytes(f"Audio generated. Sample rate: {sample_rate}, Duration: {len(audio_data)/sample_rate:.2f}s", encoding='utf-8'))
                
            finally:
                # Clean up temporary file
                if os.path.exists(temp_output):
                    os.remove(temp_output)
                
        except Exception as e:
            print(f"Error in MLX-Audio TTS PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"TTS streaming generation failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    def _build_generation_params_from_tts(self, request):
        """
        Build generation parameters from TTSRequest for MLX-Audio TTS.

        Args:
            request: The TTSRequest object.

        Returns:
            dict: Generation parameters for MLX-Audio
        """
        # Initialize generation parameters for MLX-Audio TTS
        generation_params = {
            'speed': 1.0,
            'voice': 'af_heart',  # Default voice
            'lang_code': 'a',     # Default language code
        }
        
        # Set voice from request
        if request.voice:
            generation_params['voice'] = request.voice
        
        # Set language code from request
        if request.language:
            generation_params['language'] = request.language
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
            lang_code = language_map.get(request.language.lower(), request.language)
            generation_params['lang_code'] = lang_code
        
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
        
        return generation_params

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
