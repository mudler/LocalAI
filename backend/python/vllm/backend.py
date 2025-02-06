#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import signal
import sys
import os
from typing import List
from PIL import Image

import backend_pb2
import backend_pb2_grpc

import grpc
from vllm.engine.arg_utils import AsyncEngineArgs
from vllm.engine.async_llm_engine import AsyncLLMEngine
from vllm.sampling_params import SamplingParams
from vllm.utils import random_uuid
from vllm.transformers_utils.tokenizer import get_tokenizer
from vllm.multimodal.utils import fetch_image
from vllm.assets.video import VideoAsset
import base64
import io

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

# If MAX_WORKERS are specified in the environment use it, otherwise default to 1
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))

# Implement the BackendServicer class with the service methods
class BackendServicer(backend_pb2_grpc.BackendServicer):
    """
    A gRPC servicer that implements the Backend service defined in backend.proto.
    """
    def generate(self,prompt, max_new_tokens):
        """
        Generates text based on the given prompt and maximum number of new tokens.

        Args:
            prompt (str): The prompt to generate text from.
            max_new_tokens (int): The maximum number of new tokens to generate.

        Returns:
            str: The generated text.
        """
        self.generator.end_beam_search()

        # Tokenizing the input
        ids = self.generator.tokenizer.encode(prompt)

        self.generator.gen_begin_reuse(ids)
        initial_len = self.generator.sequence[0].shape[0]
        has_leading_space = False
        decoded_text = ''
        for i in range(max_new_tokens):
            token = self.generator.gen_single_token()
            if i == 0 and self.generator.tokenizer.tokenizer.IdToPiece(int(token)).startswith('▁'):
                has_leading_space = True

            decoded_text = self.generator.tokenizer.decode(self.generator.sequence[0][initial_len:])
            if has_leading_space:
                decoded_text = ' ' + decoded_text

            if token.item() == self.generator.tokenizer.eos_token_id:
                break
        return decoded_text

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
        Loads a language model.

        Args:
            request: The load model request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The load model result.
        """
        engine_args = AsyncEngineArgs(
            model=request.Model,
        )

        if request.Quantization != "":
            engine_args.quantization = request.Quantization
        if request.LoadFormat != "":
            engine_args.load_format = request.LoadFormat
        if request.GPUMemoryUtilization != 0:
            engine_args.gpu_memory_utilization = request.GPUMemoryUtilization
        if request.TrustRemoteCode:
            engine_args.trust_remote_code = request.TrustRemoteCode
        if request.EnforceEager:
            engine_args.enforce_eager = request.EnforceEager
        if request.TensorParallelSize:
            engine_args.tensor_parallel_size = request.TensorParallelSize
        if request.SwapSpace != 0:
            engine_args.swap_space = request.SwapSpace
        if request.MaxModelLen != 0:
            engine_args.max_model_len = request.MaxModelLen

        try:
            self.llm = AsyncLLMEngine.from_engine_args(engine_args)
        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        try:
           engine_model_config = await self.llm.get_model_config()
           self.tokenizer = get_tokenizer(
               engine_model_config.tokenizer,
               tokenizer_mode=engine_model_config.tokenizer_mode,
               trust_remote_code=engine_model_config.trust_remote_code,
               truncation_side="left",
           )
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")
        print("Model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    async def Predict(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters.

        Args:
            request: The predict request.
            context: The gRPC context.

        Returns:
            backend_pb2.Reply: The predict result.
        """
        gen = self._predict(request, context, streaming=False)
        res = await gen.__anext__()
        return res

    def Embedding(self, request, context):
        """
        A gRPC method that calculates embeddings for a given sentence.

        Args:
            request: An EmbeddingRequest object that contains the request parameters.
            context: A grpc.ServicerContext object that provides information about the RPC.

        Returns:
            An EmbeddingResult object that contains the calculated embeddings.
        """
        print("Calculated embeddings for: " + request.Embeddings, file=sys.stderr)
        outputs = self.model.encode(request.Embeddings)
        # Check if we have one result at least
        if len(outputs) == 0:
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details("No embeddings were calculated.")
            return backend_pb2.EmbeddingResult()
        return backend_pb2.EmbeddingResult(embeddings=outputs[0].outputs.embedding)

    async def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Returns:
            backend_pb2.Result: The predict stream result.
        """
        iterations = self._predict(request, context, streaming=True)
        try:
            async for iteration in iterations:
                yield iteration
        finally:
            await iterations.aclose()

    async def _predict(self, request, context, streaming=False):

        # Build sampling parameters
        sampling_params = SamplingParams(top_p=0.9, max_tokens=200)
        if request.TopP != 0:
            sampling_params.top_p = request.TopP
        if request.Tokens > 0:
            sampling_params.max_tokens = request.Tokens
        if request.Temperature != 0:
            sampling_params.temperature = request.Temperature
        if request.TopK != 0:
            sampling_params.top_k = request.TopK
        if request.PresencePenalty != 0:
            sampling_params.presence_penalty = request.PresencePenalty
        if request.FrequencyPenalty != 0:
            sampling_params.frequency_penalty = request.FrequencyPenalty
        if request.StopPrompts:
            sampling_params.stop = request.StopPrompts
        if request.IgnoreEOS:
            sampling_params.ignore_eos = request.IgnoreEOS
        if request.Seed != 0:
            sampling_params.seed = request.Seed

        # Extract image paths and process images
        prompt = request.Prompt

        image_paths = request.Images
        image_data = [self.load_image(img_path) for img_path in image_paths]

        videos_path = request.Videos
        video_data = [self.load_video(video_path) for video_path in videos_path]

        # If tokenizer template is enabled and messages are provided instead of prompt, apply the tokenizer template
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            prompt = self.tokenizer.apply_chat_template(request.Messages, tokenize=False, add_generation_prompt=True)

        # Generate text using the LLM engine
        request_id = random_uuid()
        print(f"Generating text with request_id: {request_id}", file=sys.stderr)
        multi_modal_data = {}
        if image_data:
            multi_modal_data["image"] = image_data
        if video_data:
            multi_modal_data["video"] = video_data
        outputs = self.llm.generate(
            {
            "prompt": prompt,
            "multi_modal_data": multi_modal_data if multi_modal_data else None,
            },
            sampling_params=sampling_params,
            request_id=request_id,
        )

        # Stream the results
        generated_text = ""
        try:
            async for request_output in outputs:
                iteration_text = request_output.outputs[0].text

                if streaming:
                    # Remove text already sent as vllm concatenates the text from previous yields
                    delta_iteration_text = iteration_text.removeprefix(generated_text)
                    # Send the partial result
                    yield backend_pb2.Reply(message=bytes(delta_iteration_text, encoding='utf-8'))

                # Keep track of text generated
                generated_text = iteration_text
        finally:
            await outputs.aclose()

        # If streaming, we already sent everything
        if streaming:
            return

        # Remove the image files from /tmp folder
        for img_path in image_paths:
            try:
                os.remove(img_path)
            except Exception as e:
                print(f"Error removing image file: {img_path}, {e}", file=sys.stderr)

        # Sending the final generated text
        yield backend_pb2.Reply(message=bytes(generated_text, encoding='utf-8'))

    def load_image(self, image_path: str):
        """
        Load an image from the given file path or base64 encoded data.
        
        Args:
            image_path (str): The path to the image file or base64 encoded data.

        Returns:
            Image: The loaded image.
        """
        try:

            image_data = base64.b64decode(image_path)
            image = Image.open(io.BytesIO(image_data))
            return image
        except Exception as e:
            print(f"Error loading image {image_path}: {e}", file=sys.stderr)
            return None

    def load_video(self, video_path: str):
        """
        Load a video from the given file path.
        
        Args:
            video_path (str): The path to the image file.

        Returns:
            Video: The loaded video.
        """
        try:
            timestamp = str(int(time.time() * 1000))  # Generate timestamp
            p = f"/tmp/vl-{timestamp}.data"  # Use timestamp in filename
            with open(p, "wb") as f:
                f.write(base64.b64decode(video_path))
            video = VideoAsset(name=p).np_ndarrays
            os.remove(p)
            return video
        except Exception as e:
            print(f"Error loading video {video_path}: {e}", file=sys.stderr)
            return None

async def serve(address):
    # Start asyncio gRPC server
    server = grpc.aio.server(migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS))
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