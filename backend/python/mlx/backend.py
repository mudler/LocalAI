#!/usr/bin/env python3
import asyncio
from concurrent import futures
import argparse
import gc
import json
import signal
import sys
import os
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

from mlx_lm import load, stream_generate
from mlx_lm.sample_utils import make_logits_processors, make_sampler
from mlx_lm.models.cache import make_prompt_cache, can_trim_prompt_cache, trim_prompt_cache
import mlx.core as mx

from mlx_cache import ThreadSafeLRUPromptCache

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

            # Parse Options[] key:value strings into a typed dict (shared helper)
            self.options = parse_options(request.Options)
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

            # mlx_lm.load() returns a TokenizerWrapper that detects tool
            # calling and thinking markers from the chat template / vocab.
            # mlx-lm >= 0.30 also exposes a parser callable on the wrapper;
            # earlier versions don't (we fall back to json.loads inside
            # _tool_module_from_tokenizer below).
            has_tools = bool(getattr(self.tokenizer, "has_tool_calling", False))
            has_thinking = bool(getattr(self.tokenizer, "has_thinking", False))
            tcs = getattr(self.tokenizer, "tool_call_start", None)
            tce = getattr(self.tokenizer, "tool_call_end", None)
            print(
                f"MLX tokenizer capabilities: has_tool_calling={has_tools} "
                f"has_thinking={has_thinking} tool_call_start={tcs!r} tool_call_end={tce!r}",
                file=sys.stderr,
            )

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
            cache_key = self._get_tokens_from_prompt(prompt_text)

            # Fetch nearest cache (exact, shorter prefix, or create new)
            prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(
                self.model_key, cache_key
            )
            if prompt_cache is None:
                prompt_cache = make_prompt_cache(self.model, self.max_kv_size)
                remaining_tokens = cache_key

            # Build generation parameters using request attributes and options
            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(request)

            print(
                f"Generating text with MLX - max_tokens: {max_tokens}, "
                f"cache_hit: {len(remaining_tokens) < len(cache_key)}",
                file=sys.stderr,
            )

            # Create sampler and optional logits processors (penalties)
            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            # Use stream_generate to collect text + track tokens for cache key
            generated_text = []
            last_response = None
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=remaining_tokens if remaining_tokens else cache_key,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
                prompt_cache=prompt_cache,
            ):
                generated_text.append(response.text)
                cache_key.append(response.token)
                last_response = response
                # Early stop on user-provided stop sequences
                if stop_words and any(s in "".join(generated_text) for s in stop_words):
                    break

            # Insert completed cache
            self.lru_cache.insert_cache(self.model_key, cache_key, prompt_cache)

            full_text = self._truncate_at_stop("".join(generated_text), stop_words)
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

    async def TokenizeString(self, request, context):
        """Tokenize ``request.Prompt`` using the loaded model's tokenizer."""
        if not hasattr(self, "tokenizer") or self.tokenizer is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("tokenizer not loaded")
            return backend_pb2.TokenizationResponse()
        try:
            tokens = self.tokenizer.encode(request.Prompt)
            if hasattr(tokens, "tolist"):
                tokens = tokens.tolist()
            tokens = list(tokens)
            return backend_pb2.TokenizationResponse(length=len(tokens), tokens=tokens)
        except Exception as e:
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(str(e))
            return backend_pb2.TokenizationResponse()

    async def Free(self, request, context):
        """Drop the loaded model, tokenizer and prompt cache.

        Metal / CUDA memory is released via ``gc.collect()`` + the
        platform-specific cache clear hooks when available.
        """
        try:
            if hasattr(self, "model"):
                del self.model
            if hasattr(self, "tokenizer"):
                del self.tokenizer
            if hasattr(self, "lru_cache") and self.lru_cache is not None:
                try:
                    self.lru_cache.clear()
                except Exception:
                    pass
                self.lru_cache = None
            gc.collect()
            # Metal: drop the cached allocator. mlx.clear_cache (mlx >= 0.30)
            # supersedes the now-deprecated mlx.metal.clear_cache.
            try:
                if hasattr(mx, "clear_cache"):
                    mx.clear_cache()
                elif hasattr(mx, "metal") and hasattr(mx.metal, "clear_cache"):
                    mx.metal.clear_cache()
            except Exception:
                pass
            # CUDA: release the torch cache if a CUDA-backed mlx variant
            # happens to be installed alongside torch (best-effort).
            try:
                import torch  # type: ignore
                if torch.cuda.is_available():
                    torch.cuda.empty_cache()
            except Exception:
                pass
            return backend_pb2.Result(success=True, message="MLX model freed")
        except Exception as e:
            return backend_pb2.Result(success=False, message=str(e))

    async def PredictStream(self, request, context):
        """
        Generates text based on the given prompt and sampling parameters, and streams the results using MLX.

        Uses thread-safe LRU prompt cache for efficient prefix reuse across requests.

        Args:
            request: The predict stream request.
            context: The gRPC context.

        Yields:
            backend_pb2.Reply: Streaming predict results.
        """
        prompt_cache = None
        cache_key = None

        try:
            # Prepare the prompt and tokenize for cache key
            prompt_text = self._prepare_prompt(request)
            cache_key = self._get_tokens_from_prompt(prompt_text)

            # Fetch nearest cache (exact, shorter prefix, or create new)
            prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(
                self.model_key, cache_key
            )
            if prompt_cache is None:
                prompt_cache = make_prompt_cache(self.model, self.max_kv_size)
                remaining_tokens = cache_key

            # Build generation parameters using request attributes and options
            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(
                request, default_max_tokens=512
            )

            print(
                f"Streaming text with MLX - max_tokens: {max_tokens}, "
                f"cache_hit: {len(remaining_tokens) < len(cache_key)}",
                file=sys.stderr,
            )

            # Create sampler and optional logits processors (penalties)
            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            accumulated = []
            last_response = None
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=remaining_tokens if remaining_tokens else cache_key,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
                prompt_cache=prompt_cache,
            ):
                cache_key.append(response.token)
                accumulated.append(response.text)
                last_response = response
                # Emit a content delta. Structured reasoning / tool parsing
                # happens on the final chunk so we don't fragment the state
                # machine in v1.
                yield backend_pb2.Reply(
                    message=bytes(response.text, encoding='utf-8'),
                    chat_deltas=[backend_pb2.ChatDelta(content=response.text)],
                )
                # Early stop on user-provided stop sequences
                if stop_words and any(s in "".join(accumulated) for s in stop_words):
                    break

            # Final chunk: run reasoning + tool parsing on accumulated text
            # and emit the structured ChatDelta with token counts + logprobs.
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
            print(f"Error in MLX PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming generation failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))

        finally:
            # Always insert cache, even on interruption
            if prompt_cache is not None and cache_key is not None:
                try:
                    self.lru_cache.insert_cache(self.model_key, cache_key, prompt_cache)
                except Exception as e:
                    print(f"Error inserting cache: {e}", file=sys.stderr)

    def _prepare_prompt(self, request):
        """
        Prepare the prompt for MLX generation, handling chat templates if needed.

        Args:
            request: The gRPC request containing prompt and message information.

        Returns:
            str: The prepared prompt.
        """
        # If tokenizer template is enabled and messages are provided instead
        # of prompt, apply the tokenizer template (forwards tool definitions
        # and enable_thinking when the model supports them).
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            messages = messages_to_dicts(request.Messages)

            kwargs = {"tokenize": False, "add_generation_prompt": True}
            if request.Tools:
                try:
                    kwargs["tools"] = json.loads(request.Tools)
                except json.JSONDecodeError:
                    pass
            enable_thinking = request.Metadata.get("enable_thinking", "").lower()
            if enable_thinking == "true":
                kwargs["enable_thinking"] = True

            try:
                return self.tokenizer.apply_chat_template(messages, **kwargs)
            except TypeError:
                # Fallback for tokenizers whose template doesn't accept
                # tools= or enable_thinking=.
                return self.tokenizer.apply_chat_template(
                    messages,
                    tokenize=False,
                    add_generation_prompt=True,
                )
        return request.Prompt

    def _get_tokens_from_prompt(self, prompt_text: str) -> List[int]:
        """
        Tokenize prompt text for cache key generation.

        Args:
            prompt_text: The prompt string to tokenize.

        Returns:
            List[int]: List of token IDs.
        """
        tokens = self.tokenizer.encode(prompt_text)
        if hasattr(tokens, 'tolist'):
            return tokens.tolist()
        return list(tokens)





    def _build_generation_params(self, request, default_max_tokens=200):
        """
        Build generation parameters from request attributes and options.

        Args:
            request: The gRPC request.
            default_max_tokens: Default max_tokens if not specified.

        Returns:
            tuple: (max_tokens, sampler_params dict, logits_processor_params dict,
                    stop_words list)
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

        min_p = getattr(request, 'MinP', 0.0)
        # min_p default of 0.0 means disabled (no filtering)

        top_k = getattr(request, 'TopK', 0)
        # top_k default of 0 means disabled (no filtering)

        # Initialize sampler parameters
        sampler_params = {
            'temp': temp,
            'top_p': top_p,
            'min_p': min_p,
            'top_k': top_k,
            'xtc_threshold': 0.0,
            'xtc_probability': 0.0,
        }

        # Logits processor parameters — only set fields the request actually
        # provides so we can feed them unconditionally to make_logits_processors.
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
                'min_p': 'min_p',
                'top_k': 'top_k',
                'xtc_threshold': 'xtc_threshold',
                'xtc_probability': 'xtc_probability',
            }

            for option_key, param_key in sampler_option_mapping.items():
                if option_key in self.options:
                    sampler_params[param_key] = self.options[option_key]

            # Logits processor overrides
            for option_key in ('repetition_penalty', 'presence_penalty', 'frequency_penalty'):
                if option_key in self.options:
                    logits_params[option_key] = self.options[option_key]

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
        except Exception:
            pass  # Skip if encoding fails

        sampler_params['xtc_special_tokens'] = xtc_special_tokens

        # Stop sequences are applied post-decode (mlx-lm doesn't have a
        # built-in stop-sequence sampler param). Preserve the list here.
        stop_words = list(getattr(request, 'StopPrompts', []) or [])

        return max_tokens, sampler_params, logits_params, stop_words

    def _tool_module_from_tokenizer(self):
        """Build a duck-typed tool module from the TokenizerWrapper.

        On mlx-lm >= 0.30 the wrapper exposes a ``tool_parser`` callable
        that's been resolved from the model's chat template. On older
        releases (e.g. 0.29.x) the wrapper only carries the start/end
        markers — fall back to ``json.loads`` of the body, which matches
        what ``mlx_lm.tool_parsers.json_tools.parse_tool_call`` does on
        HEAD and covers the only format 0.29 detects (``<tool_call>``).
        """
        start = getattr(self.tokenizer, "tool_call_start", None)
        end = getattr(self.tokenizer, "tool_call_end", None)
        if not start:
            return None
        parse_fn = getattr(self.tokenizer, "tool_parser", None)
        if parse_fn is None:
            def parse_fn(body, tools):  # noqa: E306 — local fallback
                return json.loads(body.strip())
        return types.SimpleNamespace(
            tool_call_start=start,
            tool_call_end=end or "",
            parse_tool_call=parse_fn,
        )

    def _finalize_output(self, request, generated_text, last_response):
        """Build a ChatDelta + token counts + logprobs from accumulated output.

        Returns ``(content, reasoning_content, tool_calls_proto,
        prompt_token_count, completion_token_count, logprobs_bytes)``.
        """
        content = generated_text
        reasoning_content = ""

        if getattr(self.tokenizer, "has_thinking", False):
            think_start = getattr(self.tokenizer, "think_start", "") or ""
            think_end = getattr(self.tokenizer, "think_end", "") or ""
            reasoning_content, content = split_reasoning(content, think_start, think_end)

        tool_calls_proto: List[backend_pb2.ToolCallDelta] = []
        tool_module = None
        if getattr(self.tokenizer, "has_tool_calling", False):
            tool_module = self._tool_module_from_tokenizer()
        if tool_module is not None:
            parsed_tools = None
            if request.Tools:
                try:
                    parsed_tools = json.loads(request.Tools)
                except json.JSONDecodeError:
                    parsed_tools = None
            calls, content = parse_tool_calls(content, tool_module, parsed_tools)
            for c in calls:
                tool_calls_proto.append(
                    backend_pb2.ToolCallDelta(
                        index=c["index"],
                        id=c["id"],
                        name=c["name"],
                        arguments=c["arguments"],
                    )
                )

        prompt_token_count = int(getattr(last_response, "prompt_tokens", 0) or 0) if last_response else 0
        completion_token_count = int(getattr(last_response, "generation_tokens", 0) or 0) if last_response else 0

        logprobs_bytes = b""
        # Logprobs extraction — only when the request asked for them.
        if last_response is not None and int(getattr(request, "Logprobs", 0) or 0) > 0:
            try:
                lp = getattr(last_response, "logprobs", None)
                if lp is not None:
                    # GenerationResponse.logprobs on the last chunk is the
                    # logprob distribution of the final token. Without a
                    # per-token history we at minimum surface the last token's
                    # top-1 logprob so clients get a non-empty field.
                    token_id = int(getattr(last_response, "token", 0) or 0)
                    token_text = self.tokenizer.decode([token_id]) if token_id else ""
                    top_logprob = float(lp[token_id]) if hasattr(lp, "__getitem__") else 0.0
                    logprobs_bytes = json.dumps(
                        {
                            "content": [
                                {"token": token_text, "logprob": top_logprob}
                            ]
                        }
                    ).encode("utf-8")
            except Exception as e:
                print(f"[mlx] Logprobs extraction failed: {e}", file=sys.stderr)

        return content, reasoning_content, tool_calls_proto, prompt_token_count, completion_token_count, logprobs_bytes

    def _truncate_at_stop(self, text, stop_words):
        """Truncate ``text`` at the first occurrence of any stop sequence."""
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
