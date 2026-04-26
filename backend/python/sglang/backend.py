#!/usr/bin/env python3
"""LocalAI gRPC backend for sglang.

Wraps sglang's async Engine API behind the Backend gRPC contract defined
in backend.proto. Mirrors the structure of backend/python/vllm/backend.py
so that the two backends stay behavior-equivalent at the protocol level.

The streaming path applies sglang's per-request FunctionCallParser and
ReasoningParser so tool_calls and reasoning_content are emitted
incrementally inside ChatDelta, which is a capability sglang exposes
natively and vLLM does not.
"""
import asyncio
from concurrent import futures
import argparse
import signal
import sys
import os
import json
import gc
import uuid
import base64
import io
from typing import Dict, List, Optional, Tuple

from PIL import Image

import backend_pb2
import backend_pb2_grpc

import grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors

# sglang imports. Engine is the stable public entry point; parser modules
# are wrapped in try/except so older / leaner installs that omit them
# still load the backend for plain text generation.
from sglang.srt.entrypoints.engine import Engine

try:
    from sglang.srt.function_call.function_call_parser import FunctionCallParser
    # sglang's FunctionCallParser expects a list of pydantic Tool objects
    # (protocol.Tool with .function.name), not plain dicts. Wrap at the
    # request boundary to match.
    from sglang.srt.entrypoints.openai.protocol import Tool as SglTool
    HAS_TOOL_PARSERS = True
except Exception:
    FunctionCallParser = None  # type: ignore
    SglTool = None  # type: ignore
    HAS_TOOL_PARSERS = False

try:
    from sglang.srt.parser.reasoning_parser import ReasoningParser
    HAS_REASONING_PARSERS = True
except Exception:
    ReasoningParser = None  # type: ignore
    HAS_REASONING_PARSERS = False

try:
    from transformers import AutoTokenizer
    HAS_TRANSFORMERS = True
except Exception:
    AutoTokenizer = None  # type: ignore
    HAS_TRANSFORMERS = False


_ONE_DAY_IN_SECONDS = 60 * 60 * 24
MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer implementing the Backend service for sglang."""

    def _parse_options(self, options_list) -> Dict[str, str]:
        opts: Dict[str, str] = {}
        for opt in options_list:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            opts[key.strip()] = value.strip()
        return opts

    def _messages_to_dicts(self, messages) -> List[dict]:
        result: List[dict] = []
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
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    async def LoadModel(self, request, context):
        engine_kwargs = {"model_path": request.Model}

        if request.Quantization:
            engine_kwargs["quantization"] = request.Quantization
        if request.LoadFormat:
            engine_kwargs["load_format"] = request.LoadFormat
        if request.GPUMemoryUtilization:
            engine_kwargs["mem_fraction_static"] = float(request.GPUMemoryUtilization)
        if request.TrustRemoteCode:
            engine_kwargs["trust_remote_code"] = True
        if request.EnforceEager:
            engine_kwargs["disable_cuda_graph"] = True
        if request.TensorParallelSize:
            engine_kwargs["tp_size"] = int(request.TensorParallelSize)
        if request.MaxModelLen:
            engine_kwargs["context_length"] = int(request.MaxModelLen)
        if request.DType:
            engine_kwargs["dtype"] = request.DType

        opts = self._parse_options(request.Options)

        # Cache parser names — actual parser instances are created per
        # request because sglang's parsers are stateful.
        self.tool_parser_name: Optional[str] = opts.get("tool_parser") or None
        self.reasoning_parser_name: Optional[str] = opts.get("reasoning_parser") or None

        # Also hand the parser names to sglang's engine so its HTTP/OAI
        # paths work identically if someone hits the engine directly.
        if self.tool_parser_name:
            engine_kwargs["tool_call_parser"] = self.tool_parser_name
        if self.reasoning_parser_name:
            engine_kwargs["reasoning_parser"] = self.reasoning_parser_name

        try:
            self.llm = Engine(**engine_kwargs)
        except Exception as err:
            print(f"sglang Engine init failed: {err!r}", file=sys.stderr)
            return backend_pb2.Result(success=False, message=f"{err!r}")

        # sglang does not expose a uniform get_tokenizer() off Engine.
        # Use transformers directly — same path sglang uses internally.
        self.tokenizer = None
        if HAS_TRANSFORMERS:
            try:
                self.tokenizer = AutoTokenizer.from_pretrained(
                    request.Model,
                    trust_remote_code=bool(request.TrustRemoteCode),
                )
            except Exception as err:
                print(f"AutoTokenizer load failed (non-fatal): {err!r}", file=sys.stderr)

        print("Model loaded successfully", file=sys.stderr)
        return backend_pb2.Result(message="Model loaded successfully", success=True)

    async def Predict(self, request, context):
        gen = self._predict(request, context, streaming=False)
        res = await gen.__anext__()
        return res

    async def PredictStream(self, request, context):
        iterations = self._predict(request, context, streaming=True)
        try:
            async for iteration in iterations:
                yield iteration
        finally:
            try:
                await iterations.aclose()
            except Exception:
                pass

    async def TokenizeString(self, request, context):
        if not getattr(self, "tokenizer", None):
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("tokenizer not loaded")
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
            if hasattr(self, "llm"):
                try:
                    self.llm.shutdown()
                except Exception:
                    pass
                del self.llm
            if hasattr(self, "tokenizer"):
                del self.tokenizer
            self.tool_parser_name = None
            self.reasoning_parser_name = None
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

    def _build_sampling_params(self, request) -> dict:
        sampling_params: dict = {"temperature": 0.7, "max_new_tokens": 200}
        mapping = {
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
            "IgnoreEOS": "ignore_eos",
            "Tokens": "max_new_tokens",
            "MinTokens": "min_new_tokens",
            "SkipSpecialTokens": "skip_special_tokens",
        }
        for proto_field, sgl_key in mapping.items():
            if not hasattr(request, proto_field):
                continue
            value = getattr(request, proto_field)
            if value in (None, 0, 0.0, [], False, ""):
                continue
            # repeated fields come back as RepeatedScalarContainer — convert
            if hasattr(value, "__iter__") and not isinstance(value, (str, bytes)):
                value = list(value)
                if not value:
                    continue
            sampling_params[sgl_key] = value

        # Grammar → JSON schema or EBNF structured decoding.
        if getattr(request, "Grammar", ""):
            grammar = request.Grammar
            try:
                json.loads(grammar)
                sampling_params["json_schema"] = grammar
            except json.JSONDecodeError:
                sampling_params["ebnf"] = grammar

        return sampling_params

    def _build_prompt(self, request) -> str:
        prompt = request.Prompt
        if prompt or not request.UseTokenizerTemplate or not request.Messages:
            return prompt

        if self.tokenizer is None:
            print(
                "UseTokenizerTemplate requested but tokenizer not loaded; "
                "falling back to naive concatenation",
                file=sys.stderr,
            )
            return "\n".join(m.content or "" for m in request.Messages)

        messages_dicts = self._messages_to_dicts(request.Messages)
        template_kwargs: dict = {"tokenize": False, "add_generation_prompt": True}
        if request.Tools:
            try:
                template_kwargs["tools"] = json.loads(request.Tools)
            except json.JSONDecodeError:
                pass
        if request.Metadata.get("enable_thinking", "").lower() == "true":
            template_kwargs["enable_thinking"] = True

        try:
            return self.tokenizer.apply_chat_template(messages_dicts, **template_kwargs)
        except TypeError:
            return self.tokenizer.apply_chat_template(
                messages_dicts, tokenize=False, add_generation_prompt=True,
            )

    def _make_parsers(self, request):
        """Construct fresh per-request parser instances (stateful)."""
        tool_parser = None
        reasoning_parser = None

        if HAS_TOOL_PARSERS and self.tool_parser_name and request.Tools:
            try:
                tools_raw = json.loads(request.Tools)
                tools = [SglTool.model_validate(t) for t in tools_raw] if SglTool else tools_raw
                tool_parser = FunctionCallParser(
                    tools=tools, tool_call_parser=self.tool_parser_name,
                )
            except Exception as e:
                print(f"FunctionCallParser init failed: {e!r}", file=sys.stderr)

        if HAS_REASONING_PARSERS and self.reasoning_parser_name:
            try:
                reasoning_parser = ReasoningParser(
                    model_type=self.reasoning_parser_name,
                    stream_reasoning=True,
                )
            except Exception as e:
                print(f"ReasoningParser init failed: {e!r}", file=sys.stderr)

        return tool_parser, reasoning_parser

    async def _predict(self, request, context, streaming: bool = False):
        sampling_params = self._build_sampling_params(request)
        prompt = self._build_prompt(request)

        tool_parser, reasoning_parser = self._make_parsers(request)

        image_data = list(request.Images) if request.Images else None
        video_data = list(request.Videos) if request.Videos else None

        # Kick off streaming generation. We always use stream=True so the
        # non-stream path still gets parser coverage on the final text.
        try:
            iterator = await self.llm.async_generate(
                prompt=prompt,
                sampling_params=sampling_params,
                image_data=image_data,
                video_data=video_data,
                stream=True,
            )
        except Exception as e:
            print(f"sglang async_generate failed: {e!r}", file=sys.stderr)
            yield backend_pb2.Reply(message=bytes(f"error: {e!r}", "utf-8"))
            return

        generated_text = ""
        last_chunk: Optional[dict] = None
        # Track tool call ids once per (request, tool_index) to match the
        # OpenAI streaming contract (id sent on first chunk for that tool).
        tool_ids_seen: Dict[int, str] = {}

        try:
            async for chunk in iterator:
                last_chunk = chunk
                cumulative = chunk.get("text", "") if isinstance(chunk, dict) else ""
                delta_text = cumulative[len(generated_text):] if cumulative.startswith(generated_text) else cumulative
                generated_text = cumulative
                if not delta_text:
                    continue

                reasoning_delta = ""
                content_delta = delta_text

                if reasoning_parser is not None:
                    try:
                        r, n = reasoning_parser.parse_stream_chunk(delta_text)
                        reasoning_delta = r or ""
                        content_delta = n or ""
                    except Exception as e:
                        print(f"reasoning_parser.parse_stream_chunk: {e!r}", file=sys.stderr)

                tool_call_deltas: List[backend_pb2.ToolCallDelta] = []
                if tool_parser is not None and content_delta:
                    try:
                        normal_text, calls = tool_parser.parse_stream_chunk(content_delta)
                        content_delta = normal_text or ""
                        for tc in calls:
                            idx = int(getattr(tc, "tool_index", 0) or 0)
                            tc_id = tool_ids_seen.get(idx)
                            if tc_id is None:
                                tc_id = f"call_{uuid.uuid4().hex[:24]}"
                                tool_ids_seen[idx] = tc_id
                            tool_call_deltas.append(backend_pb2.ToolCallDelta(
                                index=idx,
                                id=tc_id,
                                name=getattr(tc, "name", "") or "",
                                arguments=getattr(tc, "parameters", "") or "",
                            ))
                    except Exception as e:
                        print(f"tool_parser.parse_stream_chunk: {e!r}", file=sys.stderr)

                if streaming and (content_delta or reasoning_delta or tool_call_deltas):
                    yield backend_pb2.Reply(
                        message=bytes(content_delta, "utf-8"),
                        chat_deltas=[backend_pb2.ChatDelta(
                            content=content_delta,
                            reasoning_content=reasoning_delta,
                            tool_calls=tool_call_deltas,
                        )],
                    )
        finally:
            try:
                await iterator.aclose()
            except Exception:
                pass

        # Extract token counts from the final chunk's meta_info.
        meta = {}
        if isinstance(last_chunk, dict):
            meta = last_chunk.get("meta_info") or {}
        prompt_tokens = int(meta.get("prompt_tokens", 0) or 0)
        completion_tokens = int(meta.get("completion_tokens", 0) or 0)

        # Non-streaming path: re-parse the full text with fresh parsers
        # so we return a clean, complete ChatDelta. Streaming parsers
        # used above have accumulated state we don't want to reuse.
        final_content = generated_text
        final_reasoning = ""
        final_tool_calls: List[backend_pb2.ToolCallDelta] = []

        if not streaming:
            final_reasoning_parser = None
            if HAS_REASONING_PARSERS and self.reasoning_parser_name:
                try:
                    final_reasoning_parser = ReasoningParser(
                        model_type=self.reasoning_parser_name,
                        stream_reasoning=False,
                    )
                except Exception:
                    final_reasoning_parser = None

            if final_reasoning_parser is not None:
                try:
                    r, n = final_reasoning_parser.parse_non_stream(generated_text)
                    final_reasoning = r or ""
                    final_content = n if n is not None else generated_text
                except Exception as e:
                    print(f"reasoning_parser.parse_non_stream: {e!r}", file=sys.stderr)

            if HAS_TOOL_PARSERS and self.tool_parser_name and request.Tools:
                try:
                    tools_raw = json.loads(request.Tools)
                    tools = [SglTool.model_validate(t) for t in tools_raw] if SglTool else tools_raw
                    fresh_tool_parser = FunctionCallParser(
                        tools=tools, tool_call_parser=self.tool_parser_name,
                    )
                    normal, calls = fresh_tool_parser.parse_non_stream(final_content)
                    if calls:
                        final_content = normal
                    for tc in calls:
                        idx = int(getattr(tc, "tool_index", 0) or 0)
                        final_tool_calls.append(backend_pb2.ToolCallDelta(
                            index=idx,
                            id=f"call_{uuid.uuid4().hex[:24]}",
                            name=getattr(tc, "name", "") or "",
                            arguments=getattr(tc, "parameters", "") or "",
                        ))
                except Exception as e:
                    print(f"tool_parser.parse_non_stream: {e!r}", file=sys.stderr)

        chat_delta = backend_pb2.ChatDelta(
            content=final_content if not streaming else "",
            reasoning_content=final_reasoning,
            tool_calls=final_tool_calls,
        )

        if streaming:
            yield backend_pb2.Reply(
                message=b"",
                prompt_tokens=prompt_tokens,
                tokens=completion_tokens,
                chat_deltas=[chat_delta],
            )
            return

        yield backend_pb2.Reply(
            message=bytes(final_content or "", "utf-8"),
            prompt_tokens=prompt_tokens,
            tokens=completion_tokens,
            chat_deltas=[chat_delta],
        )


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
    print("Server started. Listening on: " + address, file=sys.stderr)
    await server.wait_for_termination()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the sglang gRPC server.")
    parser.add_argument(
        "--addr", default="localhost:50051", help="The address to bind the server to.",
    )
    args = parser.parse_args()
    asyncio.run(serve(args.addr))
