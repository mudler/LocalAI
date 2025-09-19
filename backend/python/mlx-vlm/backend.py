#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import signal
import sys
import os
from typing import List
import time

import backend_pb2
import backend_pb2_grpc

import grpc
from mlx_vlm import load, generate, stream_generate
from mlx_vlm.prompt_utils import apply_chat_template
from mlx_vlm.utils import load_config, load_image
import mlx.core as mx
import base64
import io
from PIL import Image
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

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer that implements the Backend service defined in backend.proto.
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
        Loads a multimodal vision-language model using MLX-VLM.

        Args:
            request: The load model request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The load model result.
        """
        try:
            print(f"Loading MLX-VLM model: {request.Model}", file=sys.stderr)
            print(f"Request: {request}", file=sys.stderr)
            
            # Parse options like in the diffusers backend
            options = request.Options
            self.options = {}
            
            # The options are a list of strings in this form optname:optvalue
            # We store all the options in a dict for later use
            for opt in options:
                if ":" not in opt:
                    continue
                key, value = opt.split(":", 1)  # Split only on first colon to handle values with colons
                
                if is_float(value):
                    value = float(value)
                elif is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                    
                self.options[key] = value
            
            print(f"Options: {self.options}", file=sys.stderr)
            
            # Load model and processor using MLX-VLM
            # mlx-vlm load function returns (model, processor) instead of (model, tokenizer)
            self.model, self.processor = load(request.Model)
            
            # Load model config for chat template support
            self.config = load_config(request.Model)
                
        except Exception as err:
            print(f"Error loading MLX-VLM model {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading MLX-VLM model: {err}")

        print("MLX-VLM model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="MLX-VLM model loaded successfully", success=True)

    async def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters using MLX-VLM with multimodal support.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        temp_files = []
        try:
            # Process images and audios from request
            image_paths = []
            audio_paths = []
            
            # Process images
            if request.Images:
                for img_data in request.Images:
                    img_path = self.load_image_from_base64(img_data)
                    if img_path:
                        image_paths.append(img_path)
                        temp_files.append(img_path)
            
            # Process audios
            if request.Audios:
                for audio_data in request.Audios:
                    audio_path = self.load_audio_from_base64(audio_data)
                    if audio_path:
                        audio_paths.append(audio_path)
                        temp_files.append(audio_path)
            
            # Prepare the prompt with multimodal information
            prompt = self._prepare_prompt(request, num_images=len(image_paths), num_audios=len(audio_paths))
            
            # Build generation parameters using request attributes and options
            max_tokens, generation_params = self._build_generation_params(request)
            
            print(f"Generating text with MLX-VLM - max_tokens: {max_tokens}, params: {generation_params}", file=sys.stderr)
            print(f"Images: {len(image_paths)}, Audios: {len(audio_paths)}", file=sys.stderr)
            
            # Generate text using MLX-VLM with multimodal inputs
            response = generate(
                model=self.model,
                processor=self.processor,
                prompt=prompt,
                image=image_paths if image_paths else None,
                audio=audio_paths if audio_paths else None,
                max_tokens=max_tokens,
                temperature=generation_params.get('temp', 0.6),
                top_p=generation_params.get('top_p', 1.0),
                verbose=False
            )
            
            return backend_pb2.Reply(message=bytes(response, encoding='utf-8'))
            
        except Exception as e:
            print(f"Error in MLX-VLM Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
        finally:
            # Clean up temporary files
            self.cleanup_temp_files(temp_files)

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.
        
        Note: MLX-VLM doesn't support embeddings directly. This method returns an error.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """
        print("Embeddings not supported in MLX-VLM backend", file=sys.stderr)
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("Embeddings are not supported in the MLX-VLM backend.")
        return backend_pb2.EmbeddingResult()

    async def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results using MLX-VLM with multimodal support.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Yields:
            backend_pb2.Reply: Streaming predict results.
        """
        temp_files = []
        try:
            # Process images and audios from request
            image_paths = []
            audio_paths = []
            
            # Process images
            if request.Images:
                for img_data in request.Images:
                    img_path = self.load_image_from_base64(img_data)
                    if img_path:
                        image_paths.append(img_path)
                        temp_files.append(img_path)
            
            # Process audios
            if request.Audios:
                for audio_data in request.Audios:
                    audio_path = self.load_audio_from_base64(audio_data)
                    if audio_path:
                        audio_paths.append(audio_path)
                        temp_files.append(audio_path)
            
            # Prepare the prompt with multimodal information
            prompt = self._prepare_prompt(request, num_images=len(image_paths), num_audios=len(audio_paths))
            
            # Build generation parameters using request attributes and options
            max_tokens, generation_params = self._build_generation_params(request, default_max_tokens=512)
            
            print(f"Streaming text with MLX-VLM - max_tokens: {max_tokens}, params: {generation_params}", file=sys.stderr)
            print(f"Images: {len(image_paths)}, Audios: {len(audio_paths)}", file=sys.stderr)
            
            # Stream text generation using MLX-VLM with multimodal inputs
            for response in stream_generate(
                model=self.model,
                processor=self.processor,
                prompt=prompt,
                image=image_paths if image_paths else None,
                audio=audio_paths if audio_paths else None,
                max_tokens=max_tokens,
                temperature=generation_params.get('temp', 0.6),
                top_p=generation_params.get('top_p', 1.0),
            ):
                yield backend_pb2.Reply(message=bytes(response.text, encoding='utf-8'))
                
        except Exception as e:
            print(f"Error in MLX-VLM PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming generation failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))
        finally:
            # Clean up temporary files
            self.cleanup_temp_files(temp_files)

    def _prepare_prompt(self, request, num_images=0, num_audios=0):
        """
        Prepare the prompt for MLX-VLM generation, handling chat templates and multimodal inputs.

        Args:
            request: The gRPC request containing prompt and message information.
            num_images: Number of images in the request.
            num_audios: Number of audio files in the request.

        Returns:
            str: The prepared prompt.
        """
        # If tokenizer template is enabled and messages are provided instead of prompt, apply the tokenizer template
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            # Convert gRPC messages to the format expected by apply_chat_template
            messages = []
            for msg in request.Messages:
                messages.append({"role": msg.role, "content": msg.content})
            
            # Use mlx-vlm's apply_chat_template which handles multimodal inputs
            prompt = apply_chat_template(
                self.processor,
                self.config, 
                messages,
                num_images=num_images,
                num_audios=num_audios
            )
            return prompt
        elif request.Prompt:
            # If we have a direct prompt but also have images/audio, we need to format it properly
            if num_images > 0 or num_audios > 0:
                # Create a simple message structure for multimodal prompt
                messages = [{"role": "user", "content": request.Prompt}]
                prompt = apply_chat_template(
                    self.processor,
                    self.config, 
                    messages,
                    num_images=num_images,
                    num_audios=num_audios
                )
                return prompt
            else:
                return request.Prompt
        else:
            # Fallback to empty prompt with multimodal template if we have media
            if num_images > 0 or num_audios > 0:
                messages = [{"role": "user", "content": ""}]
                prompt = apply_chat_template(
                    self.processor,
                    self.config, 
                    messages,
                    num_images=num_images,
                    num_audios=num_audios
                )
                return prompt
            else:
                return ""





    def _build_generation_params(self, request, default_max_tokens=200):
        """
        Build generation parameters from request attributes and options for MLX-VLM.

        Args:
            request: The gRPC request.
            default_max_tokens: Default max_tokens if not specified.

        Returns:
            tuple: (max_tokens, generation_params dict)
        """
        # Extract max_tokens
        max_tokens = getattr(request, 'Tokens', default_max_tokens)
        if max_tokens == 0:
            max_tokens = default_max_tokens
        
        # Extract generation parameters from request attributes
        temp = getattr(request, 'Temperature', 0.0)
        if temp == 0.0:
            temp = 0.6  # Default temperature
        
        top_p = getattr(request, 'TopP', 0.0)
        if top_p == 0.0:
            top_p = 1.0  # Default top_p
        
        # Initialize generation parameters for MLX-VLM
        generation_params = {
            'temp': temp,
            'top_p': top_p,
        }
        
        # Add seed if specified
        seed = getattr(request, 'Seed', 0)
        if seed != 0:
            mx.random.seed(seed)
        
        # Override with options if available
        if hasattr(self, 'options'):
            # Max tokens from options
            if 'max_tokens' in self.options:
                max_tokens = self.options['max_tokens']
            
            # Generation parameters from options
            param_option_mapping = {
                'temp': 'temp',
                'temperature': 'temp',  # alias
                'top_p': 'top_p', 
            }
            
            for option_key, param_key in param_option_mapping.items():
                if option_key in self.options:
                    generation_params[param_key] = self.options[option_key]
            
            # Handle seed from options
            if 'seed' in self.options:
                mx.random.seed(self.options['seed'])
        
        return max_tokens, generation_params

    def load_image_from_base64(self, image_data: str):
        """
        Load an image from base64 encoded data.

        Args:
            image_data (str): Base64 encoded image data.

        Returns:
            PIL.Image or str: The loaded image or path to the image.
        """
        try:
            decoded_data = base64.b64decode(image_data)
            image = Image.open(io.BytesIO(decoded_data))
            
            # Save to temporary file for mlx-vlm
            with tempfile.NamedTemporaryFile(delete=False, suffix='.jpg') as tmp_file:
                image.save(tmp_file.name, format='JPEG')
                return tmp_file.name
                
        except Exception as e:
            print(f"Error loading image from base64: {e}", file=sys.stderr)
            return None

    def load_audio_from_base64(self, audio_data: str):
        """
        Load audio from base64 encoded data.

        Args:
            audio_data (str): Base64 encoded audio data.

        Returns:
            str: Path to the loaded audio file.
        """
        try:
            decoded_data = base64.b64decode(audio_data)
            
            # Save to temporary file for mlx-vlm
            with tempfile.NamedTemporaryFile(delete=False, suffix='.wav') as tmp_file:
                tmp_file.write(decoded_data)
                return tmp_file.name
                
        except Exception as e:
            print(f"Error loading audio from base64: {e}", file=sys.stderr)
            return None

    def cleanup_temp_files(self, file_paths: List[str]):
        """
        Clean up temporary files.

        Args:
            file_paths (List[str]): List of file paths to clean up.
        """
        for file_path in file_paths:
            try:
                if file_path and os.path.exists(file_path):
                    os.remove(file_path)
            except Exception as e:
                print(f"Error removing temporary file {file_path}: {e}", file=sys.stderr)

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
    print("Server started. Listening on: " + address, file=sys.stderr)
    # Wait for the server to be terminated
    await server.wait_for_termination()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to."
    )
    args = parser.parse_args()

    asyncio.run(serve(args.addr))
