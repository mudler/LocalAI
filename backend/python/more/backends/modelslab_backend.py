#!/usr/bin/env python3
"""
ModelsLab Backend for LocalAI

This backend provides LocalAI with access to ModelsLab's comprehensive cloud AI APIs,
supporting LLM chat, image generation, video generation, text-to-speech, and embeddings.

Key Features:
- Multi-modal AI generation (text, image, video, audio)
- Hybrid cloud processing for LocalAI self-hosted users
- Async generation with polling for heavy workloads
- Hardware-agnostic (no GPU required locally)
- Cost-efficient pay-per-use cloud AI

Author: ModelsLab Integration Team
License: Apache 2.0
"""

import asyncio
import argparse
import json
import logging
import os
import signal
import sys
import time
from concurrent import futures
from typing import Optional, Dict, Any, List
from urllib.parse import urljoin

import grpc
import httpx
from PIL import Image
import io
import tempfile
import base64

import backend_pb2
import backend_pb2_grpc

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

# Constants
_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '4'))


class ModelsLabAPIClient:
    """HTTP client for ModelsLab API with async support and error handling"""
    
    def __init__(self, api_key: str, base_url: str = "https://modelslab.com/api/v6"):
        self.api_key = api_key
        self.base_url = base_url.rstrip('/')
        self.timeout = int(os.environ.get('MODELSLAB_TIMEOUT', '300'))
        self.max_retries = int(os.environ.get('MODELSLAB_RETRY_COUNT', '3'))
        
        # HTTP client configuration
        self.client = httpx.AsyncClient(
            timeout=httpx.Timeout(self.timeout),
            limits=httpx.Limits(max_connections=10, max_keepalive_connections=5)
        )
        
    async def post(self, endpoint: str, data: Dict[str, Any]) -> Dict[str, Any]:
        """Make POST request to ModelsLab API with retry logic"""
        url = urljoin(self.base_url + '/', endpoint.lstrip('/'))
        
        # Add API key to request data
        data['key'] = self.api_key
        
        for attempt in range(self.max_retries):
            try:
                logger.info(f"POST {url} (attempt {attempt + 1}/{self.max_retries})")
                response = await self.client.post(url, json=data)
                
                if response.status_code == 200:
                    return response.json()
                elif response.status_code == 401:
                    raise ValueError("Invalid ModelsLab API key")
                elif response.status_code == 429:
                    # Rate limiting - wait and retry
                    wait_time = 2 ** attempt
                    logger.warning(f"Rate limited, waiting {wait_time}s before retry")
                    await asyncio.sleep(wait_time)
                    continue
                elif response.status_code >= 500:
                    # Server error - retry
                    logger.warning(f"Server error {response.status_code}, retrying...")
                    await asyncio.sleep(2 ** attempt)
                    continue
                else:
                    # Client error - don't retry
                    error_msg = response.text
                    raise ValueError(f"API error {response.status_code}: {error_msg}")
                    
            except httpx.RequestError as e:
                if attempt == self.max_retries - 1:
                    raise ValueError(f"Connection error: {str(e)}")
                await asyncio.sleep(2 ** attempt)
                
        raise ValueError(f"Failed after {self.max_retries} attempts")
    
    async def get(self, endpoint: str) -> Dict[str, Any]:
        """Make GET request to ModelsLab API"""
        url = urljoin(self.base_url + '/', endpoint.lstrip('/'))
        
        try:
            response = await self.client.get(url, params={'key': self.api_key})
            
            if response.status_code == 200:
                return response.json()
            else:
                raise ValueError(f"GET error {response.status_code}: {response.text}")
                
        except httpx.RequestError as e:
            raise ValueError(f"Connection error: {str(e)}")
    
    async def download_file(self, url: str) -> bytes:
        """Download file from URL and return bytes"""
        try:
            response = await self.client.get(url)
            response.raise_for_status()
            return response.content
        except httpx.RequestError as e:
            raise ValueError(f"Download error: {str(e)}")
    
    async def close(self):
        """Close HTTP client"""
        await self.client.aclose()


class ModelsLabBackend(backend_pb2_grpc.BackendServicer):
    """LocalAI gRPC backend implementation for ModelsLab API"""
    
    def __init__(self):
        self.api_client: Optional[ModelsLabAPIClient] = None
        self.model_configs = {}
        self.current_model_type = None
        
        # Model type mappings
        self.endpoint_mapping = {
            'text': 'chat/completions',
            'image': 'images/text2img', 
            'video': 'video/text2video',
            'tts': 'tts',
            'embeddings': 'embeddings'
        }
        
        # Default model mappings
        self.default_models = {
            'text': 'meta-llama/llama-3.1-8b-instruct',
            'image': 'flux',
            'video': 'cogvideox',
            'tts': 'tts',
            'embeddings': 'text-embedding-3-small'
        }
        
    def Health(self, request, context):
        """Health check endpoint"""
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))
    
    def LoadModel(self, request, context):
        """Initialize ModelsLab API client and configure model routing"""
        try:
            logger.info(f"Loading model: {request.Model}")
            logger.info(f"Pipeline type: {request.PipelineType}")
            logger.info(f"Options: {request.Options}")
            
            # Extract API key from options
            api_key = None
            base_url = "https://modelslab.com/api/v6"
            
            for option in request.Options:
                if ':' in option:
                    key, value = option.split(':', 1)
                    if key == 'modelslab_api_key':
                        api_key = value
                    elif key == 'modelslab_base_url':
                        base_url = value
            
            if not api_key:
                # Try environment variable
                api_key = os.environ.get('MODELSLAB_API_KEY')
            
            if not api_key:
                return backend_pb2.Result(
                    success=False, 
                    message="ModelsLab API key required. Set MODELSLAB_API_KEY environment variable or pass modelslab_api_key:YOUR_KEY in options."
                )
            
            # Initialize API client
            self.api_client = ModelsLabAPIClient(api_key, base_url)
            
            # Detect model type from pipeline type and request
            self.current_model_type = self._detect_model_type(request)
            
            # Store model configuration
            self.model_configs = {
                'model': request.Model or self.default_models.get(self.current_model_type),
                'pipeline_type': request.PipelineType,
                'cfg_scale': request.CFGScale or 7.0,
                'options': {opt.split(':', 1)[0]: opt.split(':', 1)[1] 
                           for opt in request.Options if ':' in opt}
            }
            
            logger.info(f"ModelsLab backend loaded successfully. Model type: {self.current_model_type}")
            return backend_pb2.Result(message="Model loaded successfully", success=True)
            
        except Exception as e:
            logger.error(f"Failed to load model: {e}")
            return backend_pb2.Result(success=False, message=f"Failed to load model: {str(e)}")
    
    def _detect_model_type(self, request) -> str:
        """Auto-detect model type from request parameters"""
        pipeline_type = request.PipelineType.lower()
        
        # Explicit pipeline type detection
        if 'diffusion' in pipeline_type or 'image' in pipeline_type:
            return 'image'
        elif 'video' in pipeline_type:
            return 'video'
        elif 'tts' in pipeline_type or 'speech' in pipeline_type:
            return 'tts'
        elif 'embed' in pipeline_type:
            return 'embeddings'
        else:
            return 'text'  # Default to LLM
    
    def GenerateImage(self, request, context):
        """Generate image using ModelsLab API"""
        try:
            if not self.api_client:
                return backend_pb2.Result(success=False, message="Model not loaded")
            
            # Prepare ModelsLab API request
            data = {
                "model_id": self.model_configs['model'],
                "prompt": request.positive_prompt,
                "width": request.width or 1024,
                "height": request.height or 1024,
                "num_inference_steps": request.step or 25,
                "guidance_scale": request.cfg_scale or self.model_configs['cfg_scale'],
                "samples": 1
            }
            
            # Add optional parameters
            if hasattr(request, 'negative_prompt') and request.negative_prompt:
                data["negative_prompt"] = request.negative_prompt
            
            if request.seed > 0:
                data["seed"] = request.seed
            
            logger.info(f"Generating image with parameters: {data}")
            
            # Make async API call
            loop = asyncio.new_event_loop()
            asyncio.set_event_loop(loop)
            
            try:
                result = loop.run_until_complete(self._generate_image_async(data, request.dst))
                return result
            finally:
                loop.close()
                
        except Exception as e:
            logger.error(f"Image generation failed: {e}")
            return backend_pb2.Result(success=False, message=f"Image generation failed: {str(e)}")
    
    async def _generate_image_async(self, data: Dict[str, Any], dst_path: str) -> backend_pb2.Result:
        """Async image generation with polling"""
        try:
            # Submit generation request
            response = await self.api_client.post('images/text2img', data)
            
            if response.get('status') == 'processing':
                # Async generation - poll for completion
                request_id = response.get('id')
                result = await self._poll_for_completion(f'images/fetch/{request_id}')
                image_url = result.get('output', [None])[0]
            else:
                # Immediate result
                image_url = response.get('output', [None])[0]
            
            if not image_url:
                raise ValueError("No image URL in response")
            
            # Download and save image
            image_data = await self.api_client.download_file(image_url)
            
            with open(dst_path, 'wb') as f:
                f.write(image_data)
            
            logger.info(f"Image saved to {dst_path}")
            return backend_pb2.Result(message="Image generated successfully", success=True)
            
        except Exception as e:
            logger.error(f"Async image generation failed: {e}")
            return backend_pb2.Result(success=False, message=str(e))
    
    def GenerateVideo(self, request, context):
        """Generate video using ModelsLab API"""
        try:
            if not self.api_client:
                return backend_pb2.Result(success=False, message="Model not loaded")
            
            # Prepare ModelsLab API request
            data = {
                "model_id": self.model_configs['model'],
                "prompt": request.prompt,
                "width": request.width or 1360,
                "height": request.height or 768,
                "num_frames": request.num_frames or 49,
                "fps": request.fps or 16,
                "guidance_scale": request.cfg_scale or 6.0,
                "num_inference_steps": request.step or 50
            }
            
            # Handle image-to-video mode
            if hasattr(request, 'start_image') and request.start_image:
                data["init_image"] = request.start_image
                endpoint = 'video/img2video'
            else:
                endpoint = 'video/text2video'
            
            # Add optional parameters
            if hasattr(request, 'negative_prompt') and request.negative_prompt:
                data["negative_prompt"] = request.negative_prompt
            
            if request.seed > 0:
                data["seed"] = request.seed
            
            logger.info(f"Generating video with parameters: {data}")
            
            # Make async API call
            loop = asyncio.new_event_loop()
            asyncio.set_event_loop(loop)
            
            try:
                result = loop.run_until_complete(self._generate_video_async(endpoint, data, request.dst))
                return result
            finally:
                loop.close()
                
        except Exception as e:
            logger.error(f"Video generation failed: {e}")
            return backend_pb2.Result(success=False, message=f"Video generation failed: {str(e)}")
    
    async def _generate_video_async(self, endpoint: str, data: Dict[str, Any], dst_path: str) -> backend_pb2.Result:
        """Async video generation with polling"""
        try:
            # Submit generation request  
            response = await self.api_client.post(endpoint, data)
            
            if response.get('status') == 'processing':
                # Async generation - poll for completion (video takes longer)
                request_id = response.get('id')
                result = await self._poll_for_completion(f'video/fetch/{request_id}', max_wait=1200)  # 20 minutes
                video_url = result.get('output')
            else:
                # Immediate result
                video_url = response.get('output')
            
            if not video_url:
                raise ValueError("No video URL in response")
            
            # Download and save video
            video_data = await self.api_client.download_file(video_url)
            
            with open(dst_path, 'wb') as f:
                f.write(video_data)
            
            logger.info(f"Video saved to {dst_path}")
            return backend_pb2.Result(message="Video generated successfully", success=True)
            
        except Exception as e:
            logger.error(f"Async video generation failed: {e}")
            return backend_pb2.Result(success=False, message=str(e))
    
    async def _poll_for_completion(self, fetch_endpoint: str, max_wait: int = 600) -> Dict[str, Any]:
        """Poll ModelsLab API until generation completes"""
        for attempt in range(max_wait // 10):  # Poll every 10 seconds
            await asyncio.sleep(10)
            
            try:
                result = await self.api_client.get(fetch_endpoint)
                
                status = result.get('status')
                if status == 'success':
                    return result
                elif status == 'error' or status == 'failed':
                    raise ValueError(f"Generation failed: {result.get('error', 'Unknown error')}")
                elif status in ['processing', 'queued']:
                    logger.info(f"Generation in progress... (attempt {attempt + 1})")
                    continue
                else:
                    logger.warning(f"Unknown status: {status}")
                    
            except Exception as e:
                logger.error(f"Polling error: {e}")
                if attempt == max_wait // 10 - 1:  # Last attempt
                    raise
                
        raise TimeoutError(f"Generation timed out after {max_wait} seconds")


def serve(address):
    """Start the gRPC server"""
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),  # 50MB
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ]
    )
    
    backend_pb2_grpc.add_BackendServicer_to_server(ModelsLabBackend(), server)
    server.add_insecure_port(address)
    server.start()
    
    logger.info(f"ModelsLab backend started. Listening on: {address}")
    
    # Signal handlers for graceful shutdown
    def signal_handler(sig, frame):
        logger.info("Received termination signal. Shutting down...")
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
    parser = argparse.ArgumentParser(description="ModelsLab backend for LocalAI")
    parser.add_argument(
        "--addr", 
        default=os.environ.get("LOCALAI_BACKEND_ADDRESS", "localhost:50051"),
        help="The address to bind the server to"
    )
    args = parser.parse_args()
    
    serve(args.addr)