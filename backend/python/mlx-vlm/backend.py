#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import gc
import json
import signal
import sys
import os
import tempfile
import types
from typing import List

import backend_pb2
import backend_pb2_grpc

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors
from python_utils import messages_to_dicts, parse_options
from mlx_utils import parse_tool_calls, split_reasoning

from mlx_vlm import load, stream_generate
from mlx_vlm.prompt_utils import apply_chat_template
from mlx_vlm.tool_parsers import _infer_tool_parser, load_tool_module
from mlx_vlm.utils import load_config
from mlx_lm.sample_utils import make_logits_processors, make_sampler
import mlx.core as mx
import base64
import io
from PIL import Image

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

            # Parse Options[] key:value strings into a typed dict
            self.options = parse_options(request.Options)
            print(f"Options: {self.options}", file=sys.stderr)

            # Load model and processor using MLX-VLM
            # mlx-vlm load function returns (model, processor) instead of (model, tokenizer)
            self.model, self.processor = load(request.Model)

            # Load model config for chat template support
            self.config = load_config(request.Model)

            # Auto-infer the tool parser from the chat template. mlx-vlm has
            # its own _infer_tool_parser that falls back to mlx-lm parsers.
            tokenizer = (
                self.processor.tokenizer if hasattr(self.processor, "tokenizer") else self.processor
            )
            self.tool_module = None
            if hasattr(tokenizer, "chat_template"):
                try:
                    parser_type = _infer_tool_parser(tokenizer.chat_template)
                    if parser_type is not None:
                        self.tool_module = load_tool_module(parser_type)
                        print(
                            f"[mlx-vlm] auto-detected tool parser: {parser_type}",
                            file=sys.stderr,
                        )
                    else:
                        print(
                            "[mlx-vlm] no tool parser matched the chat template",
                            file=sys.stderr,
                        )
                except Exception as e:
                    print(
                        f"[mlx-vlm] failed to load tool parser: {e}",
                        file=sys.stderr,
                    )

            # Reasoning tokens — check if the tokenizer advertises thinking
            # markers. Fall back to empty strings (split_reasoning no-ops).
            self.think_start = getattr(tokenizer, "think_start", "") or ""
            self.think_end = getattr(tokenizer, "think_end", "") or ""
            self.has_thinking = bool(
                getattr(tokenizer, "has_thinking", False) or self.think_start
            )

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
            image_paths, audio_paths = self._collect_media(request, temp_files)

            prompt = self._prepare_prompt(
                request,
                num_images=len(image_paths),
                num_audios=len(audio_paths),
            )

            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(request)
            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            print(
                f"Generating text with MLX-VLM - max_tokens: {max_tokens}, "
                f"images: {len(image_paths)}, audios: {len(audio_paths)}",
                file=sys.stderr,
            )

            accumulated = []
            last_response = None
            for response in stream_generate(
                model=self.model,
                processor=self.processor,
                prompt=prompt,
                image=image_paths if image_paths else None,
                audio=audio_paths if audio_paths else None,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
            ):
                accumulated.append(response.text)
                last_response = response
                if stop_words and any(s in "".join(accumulated) for s in stop_words):
                    break

            full_text = self._truncate_at_stop("".join(accumulated), stop_words)
            content, reasoning_content, tool_calls_proto, prompt_tokens, completion_tokens, logprobs_bytes = (
                self._finalize_output(request, full_text, last_response)
            )

            return backend_pb2.Reply(
                message=bytes(content, encoding='utf-8'),
                prompt_tokens=prompt_tokens,
                tokens=completion_tokens,
                logprobs=logprobs_bytes,
                chat_deltas=[
                    backend_pb2.ChatDelta(
                        content=content,
                        reasoning_content=reasoning_content,
                        tool_calls=tool_calls_proto,
                    )
                ],
            )

        except Exception as e:
            print(f"Error in MLX-VLM Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))
        finally:
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

    def _collect_media(self, request, temp_files):
        """Decode base64 Images and Audios into temp file paths.

        Appends every temp file to ``temp_files`` so the finally block can
        clean up even on mid-generation errors.
        """
        image_paths = []
        audio_paths = []
        if request.Images:
            for img_data in request.Images:
                img_path = self.load_image_from_base64(img_data)
                if img_path:
                    image_paths.append(img_path)
                    temp_files.append(img_path)
        if request.Audios:
            for audio_data in request.Audios:
                audio_path = self.load_audio_from_base64(audio_data)
                if audio_path:
                    audio_paths.append(audio_path)
                    temp_files.append(audio_path)
        return image_paths, audio_paths

    async def TokenizeString(self, request, context):
        """Tokenize ``request.Prompt`` via the processor's tokenizer."""
        if not hasattr(self, "processor") or self.processor is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("processor not loaded")
            return backend_pb2.TokenizationResponse()
        try:
            tokenizer = (
                self.processor.tokenizer
                if hasattr(self.processor, "tokenizer")
                else self.processor
            )
            tokens = tokenizer.encode(request.Prompt)
            if hasattr(tokens, "tolist"):
                tokens = tokens.tolist()
            tokens = list(tokens)
            return backend_pb2.TokenizationResponse(length=len(tokens), tokens=tokens)
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return backend_pb2.TokenizationResponse()

    async def Free(self, request, context):
        """Drop the loaded model, processor and tool module."""
        try:
            if hasattr(self, "model"):
                del self.model
            if hasattr(self, "processor"):
                del self.processor
            if hasattr(self, "config"):
                del self.config
            self.tool_module = None
            gc.collect()
            # mlx.clear_cache (mlx >= 0.30) supersedes mlx.metal.clear_cache.
            try:
                if hasattr(mx, "clear_cache"):
                    mx.clear_cache()
                elif hasattr(mx, "metal") and hasattr(mx.metal, "clear_cache"):
                    mx.metal.clear_cache()
            except Exception:
                pass
            try:
                import torch  # type: ignore
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()
            except Exception:
                pass
            return backend_pb2.Result(success=True, message="MLX-VLM model freed")
        except Exception as e:
            return backend_pb2.Result(success=False, message=str(e))

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
            image_paths, audio_paths = self._collect_media(request, temp_files)

            prompt = self._prepare_prompt(
                request,
                num_images=len(image_paths),
                num_audios=len(audio_paths),
            )

            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(
                request, default_max_tokens=512
            )
            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            print(
                f"Streaming text with MLX-VLM - max_tokens: {max_tokens}, "
                f"images: {len(image_paths)}, audios: {len(audio_paths)}",
                file=sys.stderr,
            )

            accumulated = []
            last_response = None
            for response in stream_generate(
                model=self.model,
                processor=self.processor,
                prompt=prompt,
                image=image_paths if image_paths else None,
                audio=audio_paths if audio_paths else None,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
            ):
                accumulated.append(response.text)
                last_response = response
                yield backend_pb2.Reply(
                    message=bytes(response.text, encoding='utf-8'),
                    chat_deltas=[backend_pb2.ChatDelta(content=response.text)],
                )
                if stop_words and any(s in "".join(accumulated) for s in stop_words):
                    break

            full_text = self._truncate_at_stop("".join(accumulated), stop_words)
            content, reasoning_content, tool_calls_proto, prompt_tokens, completion_tokens, logprobs_bytes = (
                self._finalize_output(request, full_text, last_response)
            )
            yield backend_pb2.Reply(
                message=b"",
                prompt_tokens=prompt_tokens,
                tokens=completion_tokens,
                logprobs=logprobs_bytes,
                chat_deltas=[
                    backend_pb2.ChatDelta(
                        content="",
                        reasoning_content=reasoning_content,
                        tool_calls=tool_calls_proto,
                    )
                ],
            )

        except Exception as e:
            print(f"Error in MLX-VLM PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming generation failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))
        finally:
            self.cleanup_temp_files(temp_files)

    def _build_template_kwargs(self, request, num_images, num_audios):
        """Collect kwargs for ``apply_chat_template`` that survive model variants."""
        kwargs = {"num_images": num_images, "num_audios": num_audios}
        if request.Tools:
            try:
                kwargs["tools"] = json.loads(request.Tools)
            except json.JSONDecodeError:
                pass
        if request.Metadata.get("enable_thinking", "").lower() == "true":
            kwargs["enable_thinking"] = True
        return kwargs

    def _apply_template(self, request, messages, num_images, num_audios):
        kwargs = self._build_template_kwargs(request, num_images, num_audios)
        try:
            return apply_chat_template(self.processor, self.config, messages, **kwargs)
        except TypeError:
            # Fallback for older mlx-vlm versions that reject tools=/enable_thinking=
            return apply_chat_template(
                self.processor,
                self.config,
                messages,
                num_images=num_images,
                num_audios=num_audios,
            )

    def _prepare_prompt(self, request, num_images=0, num_audios=0):
        """
        Prepare the prompt for MLX-VLM generation, handling chat templates and
        multimodal inputs. Forwards tool definitions and enable_thinking when
        present on the request.
        """
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            messages = messages_to_dicts(request.Messages)
            return self._apply_template(request, messages, num_images, num_audios)

        if request.Prompt:
            if num_images > 0 or num_audios > 0:
                messages = [{"role": "user", "content": request.Prompt}]
                return self._apply_template(request, messages, num_images, num_audios)
            return request.Prompt

        # Fallback to empty prompt with multimodal template if we have media
        if num_images > 0 or num_audios > 0:
            messages = [{"role": "user", "content": ""}]
            return self._apply_template(request, messages, num_images, num_audios)
        return ""





    def _build_generation_params(self, request, default_max_tokens=200):
        """
        Build generation parameters from request attributes and options.

        Returns:
            tuple: (max_tokens, sampler_params, logits_params, stop_words)
        """
        max_tokens = getattr(request, 'Tokens', default_max_tokens) or default_max_tokens

        temp = getattr(request, 'Temperature', 0.0) or 0.6
        top_p = getattr(request, 'TopP', 0.0) or 1.0
        min_p = getattr(request, 'MinP', 0.0) or 0.0
        top_k = getattr(request, 'TopK', 0) or 0

        sampler_params = {
            'temp': temp,
            'top_p': top_p,
            'min_p': min_p,
            'top_k': top_k,
        }

        logits_params = {}
        repetition_penalty = getattr(request, 'RepetitionPenalty', 0.0) or 0.0
        if repetition_penalty and repetition_penalty != 1.0:
            logits_params['repetition_penalty'] = repetition_penalty
        presence_penalty = getattr(request, 'PresencePenalty', 0.0) or 0.0
        if presence_penalty:
            logits_params['presence_penalty'] = presence_penalty
        frequency_penalty = getattr(request, 'FrequencyPenalty', 0.0) or 0.0
        if frequency_penalty:
            logits_params['frequency_penalty'] = frequency_penalty

        seed = getattr(request, 'Seed', 0)
        if seed != 0:
            mx.random.seed(seed)

        if hasattr(self, 'options'):
            if 'max_tokens' in self.options:
                max_tokens = self.options['max_tokens']
            option_mapping = {
                'temp': 'temp', 'temperature': 'temp',
                'top_p': 'top_p', 'min_p': 'min_p', 'top_k': 'top_k',
            }
            for option_key, param_key in option_mapping.items():
                if option_key in self.options:
                    sampler_params[param_key] = self.options[option_key]
            for option_key in ('repetition_penalty', 'presence_penalty', 'frequency_penalty'):
                if option_key in self.options:
                    logits_params[option_key] = self.options[option_key]
            if 'seed' in self.options:
                mx.random.seed(self.options['seed'])

        stop_words = list(getattr(request, 'StopPrompts', []) or [])
        return max_tokens, sampler_params, logits_params, stop_words

    def _finalize_output(self, request, generated_text, last_response):
        """Split reasoning + tool calls out of generated_text and return the
        tuple consumed by Reply-builders."""
        content = generated_text
        reasoning_content = ""

        if getattr(self, "has_thinking", False):
            reasoning_content, content = split_reasoning(content, self.think_start, self.think_end)

        tool_calls_proto: List[backend_pb2.ToolCallDelta] = []
        if self.tool_module is not None:
            parsed_tools = None
            if request.Tools:
                try:
                    parsed_tools = json.loads(request.Tools)
                except json.JSONDecodeError:
                    parsed_tools = None
            calls, content = parse_tool_calls(content, self.tool_module, parsed_tools)
            for c in calls:
                tool_calls_proto.append(
                    backend_pb2.ToolCallDelta(
                        index=c["index"],
                        id=c["id"],
                        name=c["name"],
                        arguments=c["arguments"],
                    )
                )

        prompt_tokens = int(getattr(last_response, "prompt_tokens", 0) or 0) if last_response else 0
        completion_tokens = int(getattr(last_response, "generation_tokens", 0) or 0) if last_response else 0

        logprobs_bytes = b""
        if last_response is not None and int(getattr(request, "Logprobs", 0) or 0) > 0:
            try:
                lp = getattr(last_response, "logprobs", None)
                if lp is not None:
                    token_id = int(getattr(last_response, "token", 0) or 0)
                    tokenizer = (
                        self.processor.tokenizer
                        if hasattr(self.processor, "tokenizer")
                        else self.processor
                    )
                    token_text = tokenizer.decode([token_id]) if token_id else ""
                    top_logprob = float(lp[token_id]) if hasattr(lp, "__getitem__") else 0.0
                    logprobs_bytes = json.dumps(
                        {"content": [{"token": token_text, "logprob": top_logprob}]}
                    ).encode("utf-8")
            except Exception as e:
                print(f"[mlx-vlm] Logprobs extraction failed: {e}", file=sys.stderr)

        return content, reasoning_content, tool_calls_proto, prompt_tokens, completion_tokens, logprobs_bytes

    def _truncate_at_stop(self, text, stop_words):
        if not stop_words:
            return text
        earliest = len(text)
        for stop in stop_words:
            if not stop:
                continue
            idx = text.find(stop)
            if idx >= 0 and idx < earliest:
                earliest = idx
        return text[:earliest] if earliest < len(text) else text

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
        ],
        interceptors=get_auth_interceptors(aio=True),
    )
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
