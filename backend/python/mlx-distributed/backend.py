#!/usr/bin/env python3
"""
MLX Distributed Inference Backend for LocalAI.

Two startup modes:

1. Server mode (started by LocalAI automatically):
   run.sh --addr localhost:50051
   Distributed config comes from LoadModel options or env vars.

2. Worker mode (started by CLI for remote ranks):
   run.sh --worker --hostfile hosts.json --rank 1 --backend ring
   Enters a loop waiting for commands from rank 0.
"""
import asyncio
from concurrent import futures
import argparse
import gc
import json
import os
import signal
import sys
import tempfile
import types
from typing import List

import grpc
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors
from python_utils import messages_to_dicts, parse_options as _shared_parse_options
from mlx_utils import parse_tool_calls, split_reasoning


import backend_pb2
import backend_pb2_grpc

MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


def mlx_distributed_init(rank, hostfile, backend="ring", coordinator=None):
    """Initialize MLX distributed runtime.

    Ring: MLX_HOSTFILE points to a JSON array of "ip:port" strings. Each rank
    binds to its own entry (hostfile[rank]) and connects to neighbors for the
    ring pipeline.

    JACCL: MLX_IBV_DEVICES points to a JSON 2D matrix of RDMA device names.
    MLX_JACCL_COORDINATOR is rank 0's ip:port where it runs a TCP service that
    helps all ranks establish RDMA connections.
    """
    import mlx.core as mx

    if backend == "ring":
        os.environ["MLX_HOSTFILE"] = hostfile
        os.environ["MLX_RANK"] = str(rank)
        os.environ["MLX_RING_VERBOSE"] = "1"
        return mx.distributed.init(backend="ring", strict=True)
    elif backend == "jaccl":
        os.environ["MLX_IBV_DEVICES"] = hostfile
        os.environ["MLX_RANK"] = str(rank)
        if coordinator:
            os.environ["MLX_JACCL_COORDINATOR"] = coordinator
        return mx.distributed.init(backend="jaccl", strict=True)
    else:
        raise ValueError(f"Unknown backend: {backend}")


# Re-export the shared helper under the local name for back-compat with
# any callers (and the existing distributed worker tests) that imported
# parse_options directly from this module.
parse_options = _shared_parse_options


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer for distributed MLX inference (runs on rank 0).

    When started by LocalAI (server mode), distributed init happens at
    LoadModel time using config from model options or environment variables.
    """

    def __init__(self):
        self.group = None
        self.dist_backend = None
        self.model = None
        self.tokenizer = None
        self.coordinator = None
        self.options = {}
        self.lru_cache = None
        self.model_key = None
        self.max_kv_size = None

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    async def LoadModel(self, request, context):
        try:
            import mlx.core as mx
            from mlx_lm import load
            from mlx_lm.models.cache import make_prompt_cache, can_trim_prompt_cache, trim_prompt_cache

            print(f"[Rank 0] Loading model: {request.Model}", file=sys.stderr)

            self.options = parse_options(request.Options)
            print(f"Options: {self.options}", file=sys.stderr)

            # Get distributed config from model options, falling back to env vars.
            # If neither is set, run as single-node (no distributed).
            hostfile = self.options.get("hostfile", os.environ.get("MLX_DISTRIBUTED_HOSTFILE", ""))
            dist_backend = str(self.options.get("distributed_backend",
                               os.environ.get("MLX_DISTRIBUTED_BACKEND", "ring")))
            # JACCL coordinator: rank 0 reads from env (set by CLI --coordinator).
            # Not in model options — rank 0 is the coordinator, workers get
            # the address via their own --coordinator CLI flag.
            jaccl_coordinator = os.environ.get("MLX_JACCL_COORDINATOR", "")

            if hostfile:
                from coordinator import DistributedCoordinator, CMD_LOAD_MODEL
                from sharding import pipeline_auto_parallel

                print(f"[Rank 0] Initializing distributed: backend={dist_backend}, hostfile={hostfile}", file=sys.stderr)
                self.dist_backend = dist_backend
                self.group = mlx_distributed_init(
                    rank=0,
                    hostfile=hostfile,
                    backend=dist_backend,
                    coordinator=jaccl_coordinator or None,
                )
                self.coordinator = DistributedCoordinator(self.group)
                self.coordinator.broadcast_command(CMD_LOAD_MODEL)
                self.coordinator.broadcast_model_name(request.Model)
            else:
                print("[Rank 0] No hostfile configured, running single-node", file=sys.stderr)

            # Build tokenizer config from request and options
            tokenizer_config = {}
            if request.TrustRemoteCode or self.options.get("trust_remote_code", False):
                tokenizer_config["trust_remote_code"] = True
            # Token overrides from options
            for key in ["eos_token", "pad_token", "bos_token", "unk_token",
                        "sep_token", "cls_token", "mask_token"]:
                if key in self.options:
                    tokenizer_config[key] = self.options[key]

            if tokenizer_config:
                print(f"Loading with tokenizer_config: {tokenizer_config}", file=sys.stderr)
                self.model, self.tokenizer = load(request.Model, tokenizer_config=tokenizer_config)
            else:
                self.model, self.tokenizer = load(request.Model)

            if self.group is not None:
                from sharding import pipeline_auto_parallel
                self.model = pipeline_auto_parallel(self.model, self.group)
                print(f"[Rank 0] Model loaded and sharded across {self.group.size()} ranks", file=sys.stderr)
            else:
                # Single-node: set up prompt cache for efficient generation
                from mlx_cache import ThreadSafeLRUPromptCache
                max_cache_entries = self.options.get("max_cache_entries", 10)
                self.max_kv_size = self.options.get("max_kv_size", None)
                self.model_key = request.Model
                self.lru_cache = ThreadSafeLRUPromptCache(
                    max_size=max_cache_entries,
                    can_trim_fn=can_trim_prompt_cache,
                    trim_fn=trim_prompt_cache,
                )
                print("[Rank 0] Model loaded (single-node with prompt cache)", file=sys.stderr)

            # Log auto-detected TokenizerWrapper capabilities. Same shape
            # as the mlx backend: has_tool_calling / has_thinking from
            # mlx_lm.tokenizer_utils + the start/end markers it sniffed
            # from the chat template / vocab.
            has_tools = bool(getattr(self.tokenizer, "has_tool_calling", False))
            has_thinking = bool(getattr(self.tokenizer, "has_thinking", False))
            tcs = getattr(self.tokenizer, "tool_call_start", None)
            tce = getattr(self.tokenizer, "tool_call_end", None)
            print(
                f"[Rank 0] Tokenizer capabilities: has_tool_calling={has_tools} "
                f"has_thinking={has_thinking} tool_call_start={tcs!r} tool_call_end={tce!r}",
                file=sys.stderr,
            )

        except Exception as err:
            print(f"[Rank 0] Error loading model: {err}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"Error loading model: {err}")

        return backend_pb2.Result(message="Model loaded successfully", success=True)

    async def Predict(self, request, context):
        prompt_cache = None
        cache_key = None

        try:
            import mlx.core as mx
            from mlx_lm import stream_generate
            from mlx_lm.sample_utils import make_logits_processors, make_sampler

            prompt_text = self._prepare_prompt(request)
            tokens = self._get_tokens_from_prompt(prompt_text)

            if self.coordinator:
                from coordinator import CMD_GENERATE
                self.coordinator.broadcast_command(CMD_GENERATE, len(tokens))
                self.coordinator.broadcast_tokens(tokens)

            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(request)

            if self.coordinator:
                gen_params = self.coordinator.broadcast_generation_params(
                    max_tokens=max_tokens,
                    temperature=sampler_params.get('temp', 0.6),
                    top_p=sampler_params.get('top_p', 1.0),
                )
                max_tokens = gen_params["max_tokens"]

            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            # Use prompt cache in single-node mode
            gen_kwargs = {}
            if self.lru_cache is not None:
                from mlx_lm.models.cache import make_prompt_cache
                cache_key = list(tokens)
                prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(
                    self.model_key, cache_key
                )
                if prompt_cache is None:
                    prompt_cache = make_prompt_cache(self.model, self.max_kv_size)
                    remaining_tokens = cache_key
                gen_kwargs['prompt_cache'] = prompt_cache
                tokens = remaining_tokens if remaining_tokens else cache_key

            generated = []
            last_response = None
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=tokens,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
                **gen_kwargs,
            ):
                generated.append(response.text)
                last_response = response
                if cache_key is not None:
                    cache_key.append(response.token)
                if stop_words and any(s in "".join(generated) for s in stop_words):
                    break

            if self.lru_cache is not None and cache_key is not None:
                self.lru_cache.insert_cache(self.model_key, cache_key, prompt_cache)

            full_text = self._truncate_at_stop("".join(generated), stop_words)
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
            print(f"[Rank 0] Error in Predict: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Generation failed: {str(e)}")
            return backend_pb2.Reply(message=bytes("", encoding='utf-8'))

    async def PredictStream(self, request, context):
        prompt_cache = None
        cache_key = None

        try:
            import mlx.core as mx
            from mlx_lm import stream_generate
            from mlx_lm.sample_utils import make_logits_processors, make_sampler

            prompt_text = self._prepare_prompt(request)
            tokens = self._get_tokens_from_prompt(prompt_text)

            if self.coordinator:
                from coordinator import CMD_GENERATE
                self.coordinator.broadcast_command(CMD_GENERATE, len(tokens))
                self.coordinator.broadcast_tokens(tokens)

            max_tokens, sampler_params, logits_params, stop_words = self._build_generation_params(
                request, default_max_tokens=512
            )

            if self.coordinator:
                gen_params = self.coordinator.broadcast_generation_params(
                    max_tokens=max_tokens,
                    temperature=sampler_params.get('temp', 0.6),
                    top_p=sampler_params.get('top_p', 1.0),
                )
                max_tokens = gen_params["max_tokens"]

            sampler = make_sampler(**sampler_params)
            logits_processors = make_logits_processors(**logits_params) if logits_params else None

            # Use prompt cache in single-node mode
            gen_kwargs = {}
            if self.lru_cache is not None:
                from mlx_lm.models.cache import make_prompt_cache
                cache_key = list(tokens)
                prompt_cache, remaining_tokens = self.lru_cache.fetch_nearest_cache(
                    self.model_key, cache_key
                )
                if prompt_cache is None:
                    prompt_cache = make_prompt_cache(self.model, self.max_kv_size)
                    remaining_tokens = cache_key
                gen_kwargs['prompt_cache'] = prompt_cache
                tokens = remaining_tokens if remaining_tokens else cache_key

            accumulated = []
            last_response = None
            for response in stream_generate(
                self.model,
                self.tokenizer,
                prompt=tokens,
                max_tokens=max_tokens,
                sampler=sampler,
                logits_processors=logits_processors,
                **gen_kwargs,
            ):
                if cache_key is not None:
                    cache_key.append(response.token)
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
            print(f"[Rank 0] Error in PredictStream: {e}", file=sys.stderr)
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Streaming failed: {str(e)}")
            yield backend_pb2.Reply(message=bytes("", encoding='utf-8'))

        finally:
            if self.lru_cache is not None and prompt_cache is not None and cache_key is not None:
                try:
                    self.lru_cache.insert_cache(self.model_key, cache_key, prompt_cache)
                except Exception as e:
                    print(f"Error inserting cache: {e}", file=sys.stderr)

    def Embedding(self, request, context):
        print("Embeddings not supported in MLX distributed backend", file=sys.stderr)
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("Embeddings are not supported in the MLX distributed backend.")
        return backend_pb2.EmbeddingResult()

    async def TokenizeString(self, request, context):
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
        try:
            # If we're rank 0 of a distributed run, tell workers to shut
            # down their per-request loops first so they release the model.
            if self.coordinator is not None:
                try:
                    from coordinator import CMD_SHUTDOWN
                    self.coordinator.broadcast_command(CMD_SHUTDOWN)
                except Exception as e:
                    print(f"[Rank 0] failed to broadcast shutdown: {e}", file=sys.stderr)
            if hasattr(self, "model"):
                del self.model
            if hasattr(self, "tokenizer"):
                del self.tokenizer
            if self.lru_cache is not None:
                try:
                    self.lru_cache.clear()
                except Exception:
                    pass
                self.lru_cache = None
            self.coordinator = None
            self.group = None
            gc.collect()
            try:
                import mlx.core as mx  # type: ignore
                if hasattr(mx, "clear_cache"):
                    mx.clear_cache()
                elif hasattr(mx, "metal") and hasattr(mx.metal, "clear_cache"):
                    mx.metal.clear_cache()
            except Exception:
                pass
            return backend_pb2.Result(success=True, message="MLX distributed model freed")
        except Exception as e:
            return backend_pb2.Result(success=False, message=str(e))

    def _prepare_prompt(self, request):
        if not request.Prompt and request.UseTokenizerTemplate and request.Messages:
            messages = messages_to_dicts(request.Messages)
            kwargs = {"tokenize": False, "add_generation_prompt": True}
            if request.Tools:
                try:
                    kwargs["tools"] = json.loads(request.Tools)
                except json.JSONDecodeError:
                    pass
            if request.Metadata.get("enable_thinking", "").lower() == "true":
                kwargs["enable_thinking"] = True
            try:
                return self.tokenizer.apply_chat_template(messages, **kwargs)
            except TypeError:
                return self.tokenizer.apply_chat_template(
                    messages, tokenize=False, add_generation_prompt=True
                )
        return request.Prompt

    def _get_tokens_from_prompt(self, prompt_text: str) -> List[int]:
        tokens = self.tokenizer.encode(prompt_text)
        if hasattr(tokens, 'tolist'):
            return tokens.tolist()
        return list(tokens)

    def _tool_module_from_tokenizer(self):
        """Same shim as the mlx backend: fall back to json.loads when the
        installed mlx-lm doesn't expose a tool_parser callable on the
        wrapper (true on 0.29.x — only HEAD ships parsers)."""
        start = getattr(self.tokenizer, "tool_call_start", None)
        end = getattr(self.tokenizer, "tool_call_end", None)
        if not start:
            return None
        parse_fn = getattr(self.tokenizer, "tool_parser", None)
        if parse_fn is None:
            def parse_fn(body, tools):  # noqa: E306
                return json.loads(body.strip())
        return types.SimpleNamespace(
            tool_call_start=start,
            tool_call_end=end or "",
            parse_tool_call=parse_fn,
        )

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

    def _finalize_output(self, request, generated_text, last_response):
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
                        index=c["index"], id=c["id"], name=c["name"], arguments=c["arguments"],
                    )
                )

        prompt_token_count = int(getattr(last_response, "prompt_tokens", 0) or 0) if last_response else 0
        completion_token_count = int(getattr(last_response, "generation_tokens", 0) or 0) if last_response else 0

        logprobs_bytes = b""
        if last_response is not None and int(getattr(request, "Logprobs", 0) or 0) > 0:
            try:
                lp = getattr(last_response, "logprobs", None)
                if lp is not None:
                    token_id = int(getattr(last_response, "token", 0) or 0)
                    token_text = self.tokenizer.decode([token_id]) if token_id else ""
                    top_logprob = float(lp[token_id]) if hasattr(lp, "__getitem__") else 0.0
                    logprobs_bytes = json.dumps(
                        {"content": [{"token": token_text, "logprob": top_logprob}]}
                    ).encode("utf-8")
            except Exception as e:
                print(f"[Rank 0] Logprobs extraction failed: {e}", file=sys.stderr)

        return content, reasoning_content, tool_calls_proto, prompt_token_count, completion_token_count, logprobs_bytes

    def _build_generation_params(self, request, default_max_tokens=200):
        import mlx.core as mx

        max_tokens = getattr(request, 'Tokens', default_max_tokens)
        if max_tokens == 0:
            max_tokens = default_max_tokens

        temp = getattr(request, 'Temperature', 0.0)
        if temp == 0.0:
            temp = 0.6

        top_p = getattr(request, 'TopP', 0.0)
        if top_p == 0.0:
            top_p = 1.0

        sampler_params = {
            'temp': temp,
            'top_p': top_p,
            'min_p': getattr(request, 'MinP', 0.0),
            'top_k': getattr(request, 'TopK', 0),
            'xtc_threshold': 0.0,
            'xtc_probability': 0.0,
        }

        # Logits processor parameters — pulled from the request and
        # forwarded to make_logits_processors. Rank 0 is the only rank
        # running the sampler so we don't need to broadcast these to
        # workers (workers participate in the pipeline-parallel forward
        # pass only).
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
                'temp': 'temp',
                'temperature': 'temp',
                'top_p': 'top_p',
                'min_p': 'min_p',
                'top_k': 'top_k',
                'xtc_threshold': 'xtc_threshold',
                'xtc_probability': 'xtc_probability',
            }
            for opt_key, param_key in option_mapping.items():
                if opt_key in self.options:
                    sampler_params[param_key] = self.options[opt_key]
            for opt_key in ('repetition_penalty', 'presence_penalty', 'frequency_penalty'):
                if opt_key in self.options:
                    logits_params[opt_key] = self.options[opt_key]
            if 'seed' in self.options:
                mx.random.seed(self.options['seed'])

        stop_words = list(getattr(request, 'StopPrompts', []) or [])
        return max_tokens, sampler_params, logits_params, stop_words

        # XTC special tokens
        xtc_special_tokens = []
        if hasattr(self.tokenizer, 'eos_token_ids') and self.tokenizer.eos_token_ids:
            xtc_special_tokens = list(self.tokenizer.eos_token_ids)
        elif hasattr(self.tokenizer, 'eos_token_id') and self.tokenizer.eos_token_id is not None:
            xtc_special_tokens = [self.tokenizer.eos_token_id]
        try:
            newline_tokens = self.tokenizer.encode("\n")
            xtc_special_tokens.extend(newline_tokens)
        except:
            pass
        sampler_params['xtc_special_tokens'] = xtc_special_tokens

        return max_tokens, sampler_params


def run_worker(group):
    """Worker loop for ranks > 0.  Waits for commands from rank 0."""
    from mlx_lm import load, stream_generate
    from mlx_lm.sample_utils import make_sampler
    from coordinator import DistributedCoordinator, CMD_LOAD_MODEL, CMD_GENERATE, CMD_SHUTDOWN
    from sharding import pipeline_auto_parallel
    import mlx.core as mx

    coordinator = DistributedCoordinator(group)
    model = None
    tokenizer = None

    print(f"[Rank {group.rank()}] Worker started, waiting for commands...", file=sys.stderr)

    while True:
        cmd, payload_size = coordinator.wait_for_command()

        if cmd == CMD_LOAD_MODEL:
            model_name = coordinator.broadcast_model_name()
            print(f"[Rank {group.rank()}] Loading model: {model_name}", file=sys.stderr)
            model, tokenizer = load(model_name)
            model = pipeline_auto_parallel(model, group)
            print(f"[Rank {group.rank()}] Model loaded and sharded", file=sys.stderr)

        elif cmd == CMD_GENERATE:
            if model is None:
                print(f"[Rank {group.rank()}] No model loaded, skipping generate", file=sys.stderr)
                continue

            token_count = coordinator.broadcast_token_count(payload_size)
            tokens_array = coordinator.broadcast_tokens([0] * token_count)
            tokens = tokens_array.tolist()

            gen_params = coordinator.broadcast_generation_params()

            sampler = make_sampler(
                temp=gen_params["temperature"],
                top_p=gen_params["top_p"],
            )

            for _ in stream_generate(
                model, tokenizer,
                prompt=tokens,
                max_tokens=gen_params["max_tokens"],
                sampler=sampler,
            ):
                pass

        elif cmd == CMD_SHUTDOWN:
            print(f"[Rank {group.rank()}] Shutting down", file=sys.stderr)
            break


async def serve(address):
    server = grpc.aio.server(
        migration_thread_pool=futures.ThreadPoolExecutor(max_workers=MAX_WORKERS),
        options=[
            ('grpc.max_message_length', 50 * 1024 * 1024),
            ('grpc.max_send_message_length', 50 * 1024 * 1024),
            ('grpc.max_receive_message_length', 50 * 1024 * 1024),
        ],
    
        interceptors=get_auth_interceptors(aio=True),
    )
    backend_pb2_grpc.add_BackendServicer_to_server(BackendServicer(), server)
    server.add_insecure_port(address)

    loop = asyncio.get_event_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, lambda: asyncio.ensure_future(server.stop(5)))

    await server.start()
    print(f"[Rank 0] gRPC server listening on {address}", file=sys.stderr)
    await server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="MLX Distributed Backend")
    parser.add_argument("--addr", default="localhost:50051",
                        help="gRPC listen address (used by LocalAI to send requests)")
    parser.add_argument("--worker", action="store_true",
                        help="Run in worker mode (for remote ranks started by CLI)")
    parser.add_argument("--backend", default="ring", choices=["ring", "jaccl"],
                        help="ring = TCP pipeline parallelism, jaccl = RDMA tensor parallelism")
    parser.add_argument("--hostfile", default=None,
                        help="Path to hostfile JSON (required for --worker mode)")
    parser.add_argument("--rank", type=int, default=0,
                        help="Rank of this process (0 = server, >0 = worker)")
    parser.add_argument("--coordinator", default=None,
                        help="JACCL coordinator ip:port (jaccl backend only)")
    args = parser.parse_args()

    if args.worker:
        if not args.hostfile:
            print("Error: --hostfile is required in worker mode", file=sys.stderr)
            sys.exit(1)
        group = mlx_distributed_init(args.rank, args.hostfile, args.backend, args.coordinator)
        run_worker(group)
    else:
        # Server mode: started by LocalAI with just --addr.
        # Distributed init deferred to LoadModel (reads config from model options/env vars).
        asyncio.run(serve(args.addr))
