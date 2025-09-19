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
from mlx_lm import load, generate, stream_generate
from mlx_lm.sample_utils import make_sampler
from mlx_lm.models.cache import make_prompt_cache
import mlx.core as mx
import base64
import io

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

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
        Loads a language model using MLX.

        Args:
            request: The load model request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The load model result.
        """
        try:
            print(f"Loading MLX model: {request.Model}", file=sys.stderr)
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
                
                # Convert numeric values to appropriate types
                if is_float(value):
                    value = float(value)
                elif is_int(value):
                    value = int(value)
                elif value.lower() in ["true", "false"]:
                    value = value.lower() == "true"
                    
                self.options[key] = value
            
            print(f"Options: {self.options}", file=sys.stderr)
            
            # Build tokenizer config for MLX using options
            tokenizer_config = {}
            
            # Handle trust_remote_code from request or options
            if request.TrustRemoteCode or self.options.get("trust_remote_code", False):
                tokenizer_config["trust_remote_code"] = True
            
            # Handle EOS token from options
            if "eos_token" in self.options:
                tokenizer_config["eos_token"] = self.options["eos_token"]
            
            # Handle other tokenizer config options
            for key in ["pad_token", "bos_token", "unk_token", "sep_token", "cls_token", "mask_token"]:
                if key in self.options:
                    tokenizer_config[key] = self.options[key]
            
            # Load model and tokenizer using MLX
            if tokenizer_config:
                print(f"Loading with tokenizer_config: {tokenizer_config}", file=sys.stderr)
                self.model, self.tokenizer = load(request.Model, tokenizer_config=tokenizer_config)
            else:
                self.model, self.tokenizer = load(request.Model)
            
            # Initialize prompt cache for efficient generation
            max_kv_size = self.options.get("max_kv_size", None)
            self.prompt_cache = make_prompt_cache(self.model, max_kv_size)
                
        except Exception as err:
            print(f"Error loading MLX model {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading MLX model: {err}")

        print("MLX model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="MLX model loaded successfully", success=True)

    async def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters using MLX.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        try:
            # Prepare the prompt
            prompt = self._prepare_prompt(request)
            
            # Build generation parameters using request attributes and options
            max_tokens, sampler_params = self._build_generation_params(request)
            
            print(f"Generating text with MLX - max_tokens: {max_tokens}, sampler_params: {sampler_params}", file=sys.stderr)
            
            # Create sampler with parameters
            sampler = make_sampler(**sampler_params)
            
            # Generate text using MLX with proper parameters
            response = generate(
                self.model,
                self.tokenizer,
                prompt=prompt,
                max_tokens=max_tokens,
                sampler=sampler,
                prompt_cache=self.prompt_cache,
                verbose=False
            )
            
            return backend_pb2.Reply(message=bytes(response, encoding='utf-8'))
            
        except Exception as e:
            print(f"Error in MLX Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.
        
        Note: MLX-LM doesn't support embeddings directly. This method returns an error.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """
        print("Embeddings not supported in MLX backend", file=sys.stderr)
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("Embeddings are not supported in the MLX backend.")
        return backend_pb2.EmbeddingResult()

    async def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results using MLX.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Yields:
            backend_pb2.Reply: Streaming predict results.
        """
        try:
            # Prepare the prompt
            prompt = self._prepare_prompt(request)
            
            # Build generation parameters using request attributes and options
            max_tokens, sampler_params = self._build_generation_params(request, default_max_tokens=512)
            
            print(f"Streaming text with MLX - max_tokens: {max_tokens}, sampler_params: {sampler_params}", file=sys.stderr)
            
            # Create sampler with parameters
            sampler = make_sampler(**sampler_params)
            
            # Stream text generation using MLX with proper parameters
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=prompt,
                max_tokens=max_tokens,
                sampler=sampler,
                prompt_cache=self.prompt_cache,
            ):
                yield backend_pb2.Reply(message=bytes(response.text, encoding='utf-8'))
                
        except Exception as e:
            print(f"Error in MLX PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming generation failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    def _prepare_prompt(self, request):
        """
        Prepare the prompt for MLX generation, handling chat templates if needed.

        Args:
            request: The gRPC request containing prompt and message information.

        Returns:
            str: The prepared prompt.
        """
        # If tokenizer template is enabled and messages are provided instead of prompt, apply the tokenizer template
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            # Convert gRPC messages to the format expected by apply_chat_template
            messages = []
            for msg in request.Messages:
                messages.append({"role": msg.role, "content": msg.content})
            
            prompt = self.tokenizer.apply_chat_template(
                messages, 
                tokenize=False, 
                add_generation_prompt=True
            )
            return prompt
        else:
            return request.Prompt





    def _build_generation_params(self, request, default_max_tokens=200):
        """
        Build generation parameters from request attributes and options.

        Args:
            request: The gRPC request.
            default_max_tokens: Default max_tokens if not specified.

        Returns:
            tuple: (max_tokens, sampler_params dict)
        """
        # Extract max_tokens
        max_tokens = getattr(request, 'Tokens', default_max_tokens)
        if max_tokens == 0:
            max_tokens = default_max_tokens
        
        # Extract sampler parameters from request attributes
        temp = getattr(request, 'Temperature', 0.0)
        if temp == 0.0:
            temp = 0.6  # Default temperature
        
        top_p = getattr(request, 'TopP', 0.0)
        if top_p == 0.0:
            top_p = 1.0  # Default top_p
        
        # Initialize sampler parameters
        sampler_params = {
            'temp': temp,
            'top_p': top_p,
            'xtc_threshold': 0.0,
            'xtc_probability': 0.0,
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
            
            # Sampler parameters from options
            sampler_option_mapping = {
                'temp': 'temp',
                'temperature': 'temp',  # alias
                'top_p': 'top_p', 
                'xtc_threshold': 'xtc_threshold',
                'xtc_probability': 'xtc_probability',
            }
            
            for option_key, param_key in sampler_option_mapping.items():
                if option_key in self.options:
                    sampler_params[param_key] = self.options[option_key]
            
            # Handle seed from options
            if 'seed' in self.options:
                mx.random.seed(self.options['seed'])
        
        # Special tokens for XTC sampling (if tokenizer has eos_token_ids)
        xtc_special_tokens = []
        if hasattr(self.tokenizer, 'eos_token_ids') and self.tokenizer.eos_token_ids:
            xtc_special_tokens = list(self.tokenizer.eos_token_ids)
        elif hasattr(self.tokenizer, 'eos_token_id') and self.tokenizer.eos_token_id is not None:
            xtc_special_tokens = [self.tokenizer.eos_token_id]
        
        # Add newline token if available
        try:
            newline_tokens = self.tokenizer.encode("\n")
            xtc_special_tokens.extend(newline_tokens)
        except:
            pass  # Skip if encoding fails
        
        sampler_params['xtc_special_tokens'] = xtc_special_tokens
        
        return max_tokens, sampler_params

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
