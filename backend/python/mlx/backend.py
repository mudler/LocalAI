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
from mlx_lm.models.cache import make_prompt_cache, can_trim_prompt_cache, trim_prompt_cache
import mlx.core as mx
import base64
import io

from mlx_cache import ThreadSafeLRUPromptCache

# Check for jaccl cluster mode
JACCL_CLUSTER_MODE = os.environ.get("JACCL_CLUSTER", "false").lower() == "true"
JACCL_HOSTFILE = os.environ.get("JACCL_HOSTFILE", "hostfiles/hosts.json")
JACCL_MODEL_DIR = os.environ.get("JACCL_MODEL_DIR", "")
JACCL_HTTP_HOST = os.environ.get("JACCL_HTTP_HOST", "0.0.0.0")
JACCL_HTTP_PORT = os.environ.get("JACCL_HTTP_PORT", "8080")
JACCL_MODEL_ID = os.environ.get("JACCL_MODEL_ID", "")

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
            
            # Check if jaccl cluster mode is enabled
            if JACCL_CLUSTER_MODE:
                print(f"JACCL Cluster mode enabled", file=sys.stderr)
                print(f"Hostfile: {JACCL_HOSTFILE}", file=sys.stderr)
                print(f"Model Dir: {JACCL_MODEL_DIR}", file=sys.stderr)
                
                # For cluster mode, we initialize the distributed backend
                # The actual model loading happens through mlx.launch with jaccl backend
                self.cluster_mode = True
                self.model_id = JACCL_MODEL_ID or os.path.basename(JACCL_MODEL_DIR) if JACCL_MODEL_DIR else request.Model
                
                # Set MLX environment variables for optimal performance
                os.environ["MLX_METAL_FAST_SYNCH"] = "1"
                os.environ["HF_HUB_OFFLINE"] = "1"
                os.environ["TRANSFORMERS_OFFLINE"] = "1"
                
                print(f"MLX distributed cluster initialized with model: {self.model_id}", file=sys.stderr)
            else:
                # Standard single-machine MLX loading
                self.cluster_mode = False
                
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

                # Initialize thread-safe LRU prompt cache for efficient generation
                max_cache_entries = self.options.get("max_cache_entries", 10)
                self.max_kv_size = self.options.get("max_kv_size", None)
                self.model_key = request.Model
                self.lru_cache = ThreadSafeLRUPromptCache(
                    max_size=max_cache_entries,
                    can_trim_fn=can_trim_prompt_cache,
                    trim_fn=trim_prompt_cache,
                )
                
        except Exception as err:
            print(f"Error loading MLX model {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading MLX model: {err}")

        print("MLX model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="MLX model loaded successfully", success=True)

    async def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters using MLX.

        Uses thread-safe LRU prompt cache for efficient prefix reuse across requests.
        In cluster mode, distributes inference across multiple Apple Silicon devices.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        prompt_cache = None
        cache_key = None

        try:
            # Prepare the prompt and tokenize for cache key
            prompt_text = self._prepare_prompt(request)
            
            if self.cluster_mode:
                # For cluster mode, use the distributed generate through HTTP API
                # This connects to the rank0 server that coordinates the cluster
                return await self._cluster_predict(request, prompt_text)
            
            cache_key = self._get_tokens_from_prompt(prompt_text)

            # Fetch nearest cache (exact, shorter prefix, or create new)
            prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(
                self.model_key, cache_key
            )
            if prompt_cache is None:
                prompt_cache = make_prompt_cache(self.model, self.max_kv_size)
                remaining_tokens = cache_key

            # Build generation parameters using request attributes and options
            max_tokens, sampler_params = self._build_generation_params(request)

            print(f"Generating text with MLX - max_tokens: {max_tokens}, cache_hit: {len(remaining_tokens) < len(cache_key)}", file=sys.stderr)

            # Create sampler with parameters
            sampler = make_sampler(**sampler_params)

            # Use stream_generate to track generated tokens for cache key
            generated_text = []
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=remaining_tokens if remaining_tokens else cache_key,
                max_tokens=max_tokens,
                sampler=sampler,
                prompt_cache=prompt_cache,
            ):
                generated_text.append(response.text)
                cache_key.append(response.token)

            # Insert completed cache
            self.lru_cache.insert_cache(self.model_key, cache_key, prompt_cache)

            return backend_pb2.Reply(message=bytes(''.join(generated_text), encoding='utf-8'))

        except Exception as e:
            print(f"Error in MLX Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    async def _cluster_predict(self, request, prompt_text):
        """
        Handle prediction in cluster mode by calling the distributed HTTP API.
        """
        import httpx
        import json
        
        try:
            # Build the API endpoint URL
            http_url = f"http://{JACCL_HTTP_HOST}:{JACCL_HTTP_PORT}/v1/chat/completions"
            
            print(f"Sending request to cluster HTTP API: {http_url}", file=sys.stderr)
            
            # Build the request payload for the OpenAI-compatible API
            max_tokens = int(self.options.get("max_tokens", request.MaxTokens)) if request.MaxTokens > 0 else 256
            
            payload = {
                "model": self.model_id,
                "messages": [
                    {"role": "user", "content": prompt_text}
                ],
                "max_tokens": max_tokens,
                "temperature": float(self.options.get("temperature", 0.7)),
                "stream": False
            }
            
            # Make HTTP request to the cluster server
            async with httpx.AsyncClient(timeout=120.0) as client:
                response = await client.post(
                    http_url,
                    json=payload,
                    headers={"Content-Type": "application/json"}
                )
                
                if response.status_code != 200:
                    print(f"Cluster API error: {response.status_code} - {response.text}", file=sys.stderr)
                    return backend_pb2.Reply(
                        message=bytes(f"Cluster API error: {response.status_code}", encoding='utf-8')
                    )
                
                result = response.json()
                generated_text = result.get("choices", [{}])[0].get("message", {}).get("content", "")
                
                print(f"Received {len(generated_text)} characters from cluster", file=sys.stderr)
                return backend_pb2.Reply(message=bytes(generated_text, encoding='utf-8'))
                
        except Exception as e:
            print(f"Error in cluster prediction: {e}", file=sys.stderr)
            return backend_pb2.Reply(
                message=bytes(f"Cluster prediction failed: {str(e)}", encoding='utf-8')
            )

    def _prepare_prompt(self, request):
        """Prepare the prompt from the request."""
        if request.Prompt:
            return request.Prompt
        return ""

    def _get_tokens_from_prompt(self, prompt_text):
        """Tokenize the prompt text."""
        if self.cluster_mode:
            # For cluster mode, return empty as tokenization happens on the server
            return []
        tokens = self.tokenizer.encode(prompt_text)
        return tokens

    def _build_generation_params(self, request):
        """Build generation parameters from request and options."""
        # Get max tokens
        max_tokens = request.MaxTokens
        if max_tokens <= 0:
            max_tokens = int(self.options.get("max_tokens", 256))
        
        # Build sampler parameters
        sampler_params = {
            "temperature": float(self.options.get("temperature", 0.7)),
            "top_p": float(self.options.get("top_p", 1.0)),
            "top_k": int(self.options.get("top_k", 0)),
            "repetition_penalty": float(self.options.get("repetition_penalty", 1.0)),
            "repetition_context_size": int(self.options.get("repetition_context_size", 20)),
        }
        
        return max_tokens, sampler_params


def serve():
    """Start the gRPC server."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    
    # Get the port from environment or default to 50051
    port = os.environ.get("GRPC_PORT", "50051")
    server.add_insecure_port(f"[::]:{port}")
    
    print(f"Starting MLX backend server on port {port}", file=sys.stderr)
    if JACCL_CLUSTER_MODE:
        print(f"Running in JACCL Cluster mode with hostfile: {JACCL_HOSTFILE}", file=sys.stderr)
    
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
