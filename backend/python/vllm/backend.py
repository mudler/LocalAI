#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import signal
import sys
import os
import json
import time
import gc
from typing import List
from PIL import Image

import backend_pb2
import backend_pb2_grpc

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors

from vllm.engine.arg_utils import AsyncEngineArgs
from vllm.engine.async_llm_engine import AsyncLLMEngine
from vllm.sampling_params import SamplingParams
from vllm.utils import random_uuid
from vllm.transformers_utils.tokenizer import get_tokenizer
from vllm.multimodal.utils import fetch_image
from vllm.assets.video import VideoAsset
import base64
import io

# Version-compat imports — wrap in try/except for older vLLM versions
try:
    from vllm.tool_parsers import ToolParserManager
    HAS_TOOL_PARSERS = True
except ImportError:
    HAS_TOOL_PARSERS = False

try:
    from vllm.reasoning import ReasoningParserManager
    HAS_REASONING_PARSERS = True
except ImportError:
    HAS_REASONING_PARSERS = False

try:
    from vllm.sampling_params import GuidedDecodingParams
    HAS_GUIDED_DECODING = True
except ImportError:
    HAS_GUIDED_DECODING = False

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

    def _parse_options(self, options_list):
        """Parse Options[] key:value string list into a dict."""
        opts = {}
        for opt in options_list:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            opts[key.strip()] = value.strip()
        return opts

    def _messages_to_dicts(self, messages):
        """Convert proto Messages to list of dicts suitable for apply_chat_template()."""
        result = []
        for msg in messages:
            d = {"role": msg.role, "content": msg.content or ""}
            if msg.name:
                d["name"] = msg.name
            if msg.tool_call_id:
                d["tool_call_id"] = msg.tool_call_id
            if msg.reasoning_content:
                d["reasoning_content"] = msg.reasoning_content
            if msg.tool_calls:
                try:
                    d["tool_calls"] = json.loads(msg.tool_calls)
                except json.JSONDecodeError:
                    pass
            result.append(d)
        return result

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
        if request.DisableLogStatus:
            engine_args.disable_log_status = request.DisableLogStatus
        if request.DType != "":
            engine_args.dtype = request.DType
        if request.LimitImagePerPrompt != 0 or request.LimitVideoPerPrompt != 0 or request.LimitAudioPerPrompt != 0:
            # limit-mm-per-prompt defaults to 1 per modality, based on vLLM docs
            engine_args.limit_mm_per_prompt = {
                "image": max(request.LimitImagePerPrompt, 1),
                "video": max(request.LimitVideoPerPrompt, 1),
                "audio": max(request.LimitAudioPerPrompt, 1)
            }

        try:
            self.llm = AsyncLLMEngine.from_engine_args(engine_args)
        except Exception as err:
            print(f"Unexpected {err=}, {type(err)=}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        try:
            # vLLM >= 0.14 removed get_model_config() on AsyncLLM; the tokenizer
            # is either already loaded on the engine or can be built from the
            # Model name directly.
            tokenizer = None
            if hasattr(self.llm, "get_tokenizer"):
                try:
                    tokenizer = await self.llm.get_tokenizer()
                except TypeError:
                    tokenizer = self.llm.get_tokenizer()
                except Exception:
                    tokenizer = None
            if tokenizer is None and hasattr(self.llm, "tokenizer"):
                tokenizer = self.llm.tokenizer
            if tokenizer is None:
                tokenizer = get_tokenizer(
                    request.Model,
                    trust_remote_code=bool(request.TrustRemoteCode),
                    truncation_side="left",
                )
            self.tokenizer = tokenizer
        except Exception as err:
            return backend_pb2.Result(success=False, message=f"Unexpected {err=}, {type(err)=}")

        # Parse options for parser selection
        opts = self._parse_options(request.Options)

        # Instantiate tool/reasoning parser classes (they'll be instantiated per-request with tokenizer)
        self.tool_parser_cls = None
        self.reasoning_parser_cls = None
        if HAS_TOOL_PARSERS and opts.get("tool_parser"):
            try:
                self.tool_parser_cls = ToolParserManager.get_tool_parser(opts["tool_parser"])
                print(f"Loaded tool_parser: {opts['tool_parser']}", file=sys.stderr)
            except Exception as e:
                print(f"Failed to load tool_parser {opts.get('tool_parser')}: {e}", file=sys.stderr)

        if HAS_REASONING_PARSERS and opts.get("reasoning_parser"):
            try:
                self.reasoning_parser_cls = ReasoningParserManager.get_reasoning_parser(opts["reasoning_parser"])
                print(f"Loaded reasoning_parser: {opts['reasoning_parser']}", file=sys.stderr)
            except Exception as e:
                print(f"Failed to load reasoning_parser {opts.get('reasoning_parser')}: {e}", file=sys.stderr)

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

    async def TokenizeString(self, request, context):
        if not hasattr(self, 'tokenizer') or self.tokenizer is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("Model/tokenizer not loaded")
            return backend_pb2.TokenizationResponse()
        try:
            tokens = self.tokenizer.encode(request.Prompt)
            return backend_pb2.TokenizationResponse(length=len(tokens), tokens=tokens)
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return backend_pb2.TokenizationResponse()

    async def Free(self, request, context):
        try:
            if hasattr(self, 'llm'):
                del self.llm
            if hasattr(self, 'tokenizer'):
                del self.tokenizer
            self.tool_parser_cls = None
            self.reasoning_parser_cls = None
            gc.collect()
            try:
                import torch
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()
            except ImportError:
                pass
            return backend_pb2.Result(success=True, message="Model freed")
        except Exception as e:
            return backend_pb2.Result(success=False, message=str(e))

    async def _predict(self, request, context, streaming=False):
        # Build the sampling parameters
        # NOTE: this must stay in sync with the vllm backend
        request_to_sampling_params = {
            "N": "n",
            "PresencePenalty": "presence_penalty",
            "FrequencyPenalty": "frequency_penalty",
            "RepetitionPenalty": "repetition_penalty",
            "Temperature": "temperature",
            "TopP": "top_p",
            "TopK": "top_k",
            "MinP": "min_p",
            "Seed": "seed",
            "StopPrompts": "stop",
            "StopTokenIds": "stop_token_ids",
            "BadWords": "bad_words",
            "IncludeStopStrInOutput": "include_stop_str_in_output",
            "IgnoreEOS": "ignore_eos",
            "Tokens": "max_tokens",
            "MinTokens": "min_tokens",
            "Logprobs": "logprobs",
            "PromptLogprobs": "prompt_logprobs",
            "SkipSpecialTokens": "skip_special_tokens",
            "SpacesBetweenSpecialTokens": "spaces_between_special_tokens",
            "TruncatePromptTokens": "truncate_prompt_tokens",
        }

        sampling_params = SamplingParams(top_p=0.9, max_tokens=200)

        for request_field, param_field in request_to_sampling_params.items():
            if hasattr(request, request_field):
                value = getattr(request, request_field)
                if value not in (None, 0, [], False):
                    setattr(sampling_params, param_field, value)

        # Guided decoding: use Grammar field to pass JSON schema or BNF
        if HAS_GUIDED_DECODING and request.Grammar:
            try:
                json.loads(request.Grammar)  # valid JSON = JSON schema
                sampling_params.guided_decoding = GuidedDecodingParams(json=request.Grammar)
            except json.JSONDecodeError:
                sampling_params.guided_decoding = GuidedDecodingParams(grammar=request.Grammar)

        # Extract image paths and process images
        prompt = request.Prompt

        image_paths = request.Images
        image_data = [self.load_image(img_path) for img_path in image_paths]

        videos_path = request.Videos
        video_data = [self.load_video(video_path) for video_path in videos_path]

        # If tokenizer template is enabled and messages are provided instead of prompt, apply the tokenizer template
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            messages_dicts = self._messages_to_dicts(request.Messages)
            template_kwargs = {"tokenize": False, "add_generation_prompt": True}

            # Pass tools for tool calling
            if request.Tools:
                try:
                    template_kwargs["tools"] = json.loads(request.Tools)
                except json.JSONDecodeError:
                    pass

            # Enable thinking mode if requested
            if request.Metadata.get("enable_thinking", "").lower() == "true":
                template_kwargs["enable_thinking"] = True

            try:
                prompt = self.tokenizer.apply_chat_template(messages_dicts, **template_kwargs)
            except TypeError:
                # Some tokenizers don't support tools/enable_thinking kwargs — retry without them
                prompt = self.tokenizer.apply_chat_template(
                    messages_dicts, tokenize=False, add_generation_prompt=True
                )

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
        last_output = None
        try:
            async for request_output in outputs:
                iteration_text = request_output.outputs[0].text
                last_output = request_output

                if streaming:
                    # Remove text already sent as vllm concatenates the text from previous yields
                    delta_iteration_text = iteration_text.removeprefix(generated_text)
                    # Send the partial result
                    yield backend_pb2.Reply(
                        message=bytes(delta_iteration_text, encoding='utf-8'),
                        chat_deltas=[backend_pb2.ChatDelta(content=delta_iteration_text)],
                    )

                # Keep track of text generated
                generated_text = iteration_text
        finally:
            await outputs.aclose()

        # Remove the image files from /tmp folder
        for img_path in image_paths:
            try:
                os.remove(img_path)
            except Exception as e:
                print(f"Error removing image file: {img_path}, {e}", file=sys.stderr)

        # Parse reasoning and tool calls from final text using vLLM's native parsers
        content = generated_text
        reasoning_content = ""
        tool_calls_proto = []

        if self.reasoning_parser_cls:
            try:
                rp = self.reasoning_parser_cls(self.tokenizer)
                r, c = rp.extract_reasoning(generated_text, request=None)
                reasoning_content = r or ""
                content = c if c is not None else generated_text
            except Exception as e:
                print(f"Reasoning parser error: {e}", file=sys.stderr)

        if self.tool_parser_cls and request.Tools:
            try:
                tools = json.loads(request.Tools)
                # Some concrete parsers only accept the tokenizer; only the
                # abstract base declares the tools kwarg. Try with tools first,
                # fall back to tokenizer-only.
                try:
                    tp = self.tool_parser_cls(self.tokenizer, tools=tools)
                except TypeError:
                    tp = self.tool_parser_cls(self.tokenizer)
                info = tp.extract_tool_calls(content, request=None)
                if info.tools_called:
                    content = info.content or ""
                    for i, tc in enumerate(info.tool_calls):
                        tool_calls_proto.append(backend_pb2.ToolCallDelta(
                            index=i,
                            id=tc.id,
                            name=tc.function.name,
                            arguments=tc.function.arguments,
                        ))
            except Exception as e:
                print(f"Tool parser error: {e}", file=sys.stderr)

        # Extract token counts
        prompt_tokens = 0
        completion_tokens = 0
        if last_output is not None:
            try:
                prompt_tokens = len(last_output.prompt_token_ids or [])
            except Exception:
                pass
            try:
                completion_tokens = len(last_output.outputs[0].token_ids or [])
            except Exception:
                pass

        # Extract logprobs if requested
        logprobs_bytes = b""
        if last_output is not None and request.Logprobs > 0:
            try:
                lp = last_output.outputs[0].logprobs
                if lp:
                    logprobs_data = {"content": []}
                    for token_lp_dict in lp:
                        if token_lp_dict:
                            first_tok_id, first_lp = next(iter(token_lp_dict.items()))
                            logprobs_data["content"].append({
                                "token": getattr(first_lp, "decoded_token", str(first_tok_id)),
                                "logprob": first_lp.logprob,
                            })
                    logprobs_bytes = json.dumps(logprobs_data).encode("utf-8")
            except Exception as e:
                print(f"Logprobs extraction error: {e}", file=sys.stderr)

        chat_delta = backend_pb2.ChatDelta(
            content=content,
            reasoning_content=reasoning_content,
            tool_calls=tool_calls_proto,
        )

        if streaming:
            # Final chunk with structured data
            yield backend_pb2.Reply(
                message=b"",
                prompt_tokens=prompt_tokens,
                tokens=completion_tokens,
                chat_deltas=[chat_delta],
                logprobs=logprobs_bytes,
            )
            return

        # Non-streaming: single Reply with everything
        yield backend_pb2.Reply(
            message=bytes(content, encoding='utf-8'),
            prompt_tokens=prompt_tokens,
            tokens=completion_tokens,
            chat_deltas=[chat_delta],
            logprobs=logprobs_bytes,
        )

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
