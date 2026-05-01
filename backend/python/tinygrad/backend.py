#!/usr/bin/env python3
"""
LocalAI gRPC backend for tinygrad.

LLM execution is delegated to `tinygrad.apps.llm.Transformer` — we keep
only a thin HF → GGUF-name adapter (vendor/appsllm_adapter.py) for the
safetensors path; GGUF models load through `Transformer.from_gguf()`
with native Q4/Q6/Q8 support.

Scope:
  - LLM text generation via apps.llm (Qwen3 / Qwen3.5 / Llama 3.x /
    GLM-4 / OLMoE / Kimi-K2 / Moonlight — anything apps.llm supports).
  - Native tool-call extraction via pluggable parsers (hermes,
    llama3_json, qwen3_xml, mistral).
  - Embeddings — mean-pooled last-hidden-state over the block stack.
  - Stable Diffusion 1.x, Whisper — handled by the vendored paths.

Sampling is greedy-only because `apps.llm.Transformer.generate` (in the
tinygrad 0.12.0 PyPI release) ends with `.argmax(-1)` and takes no
temperature / top-k / top-p / repetition-penalty arguments. These
request fields are accepted and ignored.

The heavy imports (tinygrad, tokenizers, tinygrad.apps.llm) are deferred
until `LoadModel`, because tinygrad binds its compute device at import
time from env vars. `_select_tinygrad_device()` maps LocalAI's BUILD_TYPE
onto the corresponding tinygrad env flag before any import happens.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import signal
import sys
import tempfile
import time
from concurrent import futures
from pathlib import Path
from typing import Any, Optional

import grpc

import backend_pb2
import backend_pb2_grpc

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'common'))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), 'common'))
from grpc_auth import get_auth_interceptors  # noqa: E402

from tool_parsers import resolve_parser  # noqa: E402
from tool_parsers.base import ToolCall  # noqa: E402

MAX_WORKERS = int(os.environ.get('PYTHON_GRPC_MAX_WORKERS', '1'))


# ---------------------------------------------------------------------------
# Device selection — must run BEFORE `import tinygrad` anywhere.
#
# In production this is set by run.sh based on which driver libraries the
# host has injected into the container (libcuda.so.1 → CUDA, libamdhip64
# → HIP, otherwise CLANG). This helper is only a fallback for direct
# invocations like the unit tests.
# ---------------------------------------------------------------------------

def _select_tinygrad_device() -> None:
    if any(os.environ.get(k) == "1" for k in ("CUDA", "HIP", "METAL", "CLANG", "AMD", "NV")):
        return
    os.environ["CLANG"] = "1"


# ---------------------------------------------------------------------------
# Model asset discovery
# ---------------------------------------------------------------------------

def _resolve_model_assets(model_ref: str) -> Path:
    """
    Accept either a local path or a HuggingFace repo id (e.g.
    "unsloth/Qwen3.5-0.8B-GGUF") and return the local directory / file.
    HF ids are materialized via `huggingface_hub.snapshot_download` — we
    pull both safetensors (for fp16 HF repos) and GGUF (for quantized
    repos) so the same code path handles either.
    """
    p = Path(model_ref)
    if p.exists():
        return p
    if "/" in model_ref and not model_ref.startswith(("/", ".")):
        from huggingface_hub import snapshot_download
        local = snapshot_download(
            repo_id=model_ref,
            allow_patterns=[
                "config.json",
                "tokenizer.json",
                "tokenizer_config.json",
                "special_tokens_map.json",
                "generation_config.json",
                "*.safetensors",
                "*.safetensors.index.json",
                "*.gguf",
            ],
        )
        return Path(local)
    raise FileNotFoundError(f"Model not found: {model_ref}")


def _gguf_path(model_ref: Path) -> Optional[Path]:
    """Return the GGUF file to load from a path that may be a file or dir."""
    if model_ref.is_file() and str(model_ref).endswith(".gguf"):
        return model_ref
    if model_ref.is_dir():
        ggufs = sorted(model_ref.glob("*.gguf"))
        if ggufs:
            return ggufs[0]
    return None


def _load_hf_safetensors(model_dir: Path) -> dict[str, Any]:
    """Load sharded or single-file HF safetensors from a directory."""
    from tinygrad.nn.state import safe_load

    index = model_dir / "model.safetensors.index.json"
    if index.exists():
        with open(index) as fp:
            weight_map = json.load(fp)["weight_map"]
        shards: dict[str, Any] = {}
        for shard_name in set(weight_map.values()):
            shards[shard_name] = safe_load(str(model_dir / shard_name))
        return {k: shards[n][k] for k, n in weight_map.items()}

    single = model_dir / "model.safetensors"
    if single.exists():
        return safe_load(str(single))

    raise FileNotFoundError(f"No safetensors weights found under {model_dir}")


def _auto_tool_parser(model_ref: Optional[str], config: dict) -> Optional[str]:
    """Pick a tool parser automatically from model family heuristics.

    Order of precedence: architecture name from config.json, then model ref
    string. Returns None to fall through to the passthrough parser.
    """
    arches = " ".join(a.lower() for a in config.get("architectures", []))
    ref = (model_ref or "").lower()
    blob = f"{arches} {ref}"

    if "qwen3" in blob:
        return "qwen3_xml"
    if "hermes" in blob or "qwen2" in blob or "qwen" in blob:
        return "hermes"
    if "llama-3" in blob or "llama_3" in blob or "llama3" in blob:
        return "llama3_json"
    if "mistral" in blob or "mixtral" in blob:
        return "mistral"
    return None


# ---------------------------------------------------------------------------
# Servicer
# ---------------------------------------------------------------------------

class BackendServicer(backend_pb2_grpc.BackendServicer):
    """gRPC servicer for the tinygrad backend."""

    def __init__(self) -> None:
        self._reset_state()

    def _reset_state(self) -> None:
        self.model_ref: Optional[str] = None
        self.model_type: str = "llm"
        self.options: dict[str, str] = {}
        # LLM state
        self.llm_model = None
        self.llm_config: dict = {}
        self.llm_tokenizer = None
        self.llm_eos_ids: list[int] = []
        self.chat_template: Optional[str] = None
        self.tool_parser = resolve_parser(None)
        self.max_context = 4096
        # Stable Diffusion state
        self.sd_model = None
        # Whisper state
        self.whisper_model = None
        self.whisper_tokenizer = None

    # --------------------- helpers --------------------------------------

    @staticmethod
    def _parse_options(options_list) -> dict[str, str]:
        opts: dict[str, str] = {}
        for opt in options_list:
            if ":" not in opt:
                continue
            key, value = opt.split(":", 1)
            opts[key.strip()] = value.strip()
        return opts

    @staticmethod
    def _detect_model_type(model_ref: str, explicit: Optional[str]) -> str:
        if explicit:
            return explicit
        name = (model_ref or "").lower()
        if "whisper" in name:
            return "whisper"
        if "sdxl" in name:
            return "sdxl"
        if "sd-v1" in name or "v1-5" in name or "stable-diffusion" in name:
            return "sd15"
        if any(tag in name for tag in ("bge", "e5", "minilm", "bert")):
            return "bert"
        return "llm"

    def _messages_to_dicts(self, messages) -> list[dict]:
        result = []
        for msg in messages:
            d: dict = {"role": msg.role, "content": msg.content or ""}
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

    def _render_prompt(self, request) -> str:
        """Render messages + tools into the model's chat template, or fall
        back to the raw Prompt field for models without a template."""
        if not request.Messages and request.Prompt:
            return request.Prompt

        if not self.chat_template:
            # No template known — concatenate role/content lines.
            lines = []
            for msg in request.Messages:
                lines.append(f"{msg.role}: {msg.content or ''}")
            return "\n".join(lines) + "\nassistant:"

        from jinja2 import Environment

        env = Environment(trim_blocks=True, lstrip_blocks=True)
        template = env.from_string(self.chat_template)

        tools = None
        if request.Tools:
            try:
                tools = json.loads(request.Tools)
            except json.JSONDecodeError:
                tools = None

        return template.render(
            messages=self._messages_to_dicts(request.Messages),
            tools=tools,
            add_generation_prompt=True,
            # Qwen3's chat template enables <think>...</think> reasoning
            # by default. On small models (0.6B) that reasoning preamble
            # eats the whole token budget before a tool call emerges, so
            # we disable it. Templates that don't know this var ignore it.
            enable_thinking=False,
        )

    # --------------------- LLM path -------------------------------------

    def _load_llm(self, model_path: Path) -> None:
        """Load an LLM through `tinygrad.apps.llm.Transformer`.

        Two paths:
          - GGUF file (anywhere in the tree) → `Transformer.from_gguf()`
            handles config, weight conversion (incl. Q4/Q6/Q8 quantization)
            and RoPE permute natively.
          - HF safetensors directory → build `TransformerConfig` from
            config.json and load weights via a small HF→GGUF-name adapter.
        """
        from tinygrad import Device, Tensor, dtypes
        from tinygrad.apps.llm import Transformer
        from tinygrad.nn.state import load_state_dict

        from vendor.appsllm_adapter import (
            _hf_to_appsllm_state_dict,
            _hf_to_transformer_kwargs,
        )

        max_context_cap = 8192

        gguf_file = _gguf_path(model_path)
        if gguf_file is not None:
            # GGUF path: apps.llm handles everything — config, quant, RoPE.
            gguf_tensor = Tensor.empty(
                os.stat(gguf_file).st_size, dtype=dtypes.uint8,
                device=f"disk:{gguf_file}",
            ).to(Device.DEFAULT)
            model, kv = Transformer.from_gguf(gguf_tensor, max_context=max_context_cap)
            self.llm_model = model
            self.max_context = model.max_context
            # Preserve a config-shaped dict for tool-parser heuristics and
            # the "loaded" message.
            arch = kv.get("general.architecture", "")
            self.llm_config = {
                "architectures": [kv.get("general.name", arch) or arch],
                "gguf_kv": kv,
            }

            # Tokenizer: prefer sidecar tokenizer.json (richer HF Jinja2
            # templates), fall back to apps.llm's SimpleTokenizer built
            # from GGUF metadata.
            self._load_tokenizer_for_dir(model_path if model_path.is_dir() else gguf_file.parent, gguf_kv=kv)
        else:
            # HF safetensors path.
            if not model_path.is_dir():
                raise FileNotFoundError(f"Expected HF model directory, got file: {model_path}")
            config_path = model_path / "config.json"
            if not config_path.exists():
                raise FileNotFoundError(f"config.json not found under {model_path}")
            with open(config_path) as fp:
                hf_config = json.load(fp)
            self.llm_config = hf_config

            raw_weights = _load_hf_safetensors(model_path)
            n_layers = hf_config["num_hidden_layers"]
            state_dict = _hf_to_appsllm_state_dict(raw_weights, n_layers)

            kwargs = _hf_to_transformer_kwargs(hf_config, state_dict, max_context_cap)
            self.max_context = kwargs["max_context"]

            model = Transformer(**kwargs)
            load_state_dict(model, state_dict, strict=False, consume=True)
            self.llm_model = model

            self._load_tokenizer_for_dir(model_path, gguf_kv=None)

        # Auto-pick tool parser from options or model family.
        parser_name = self.options.get("tool_parser") or _auto_tool_parser(self.model_ref, self.llm_config)
        self.tool_parser = resolve_parser(parser_name)

    def _load_tokenizer_for_dir(self, model_dir: Path, gguf_kv: Optional[dict]) -> None:
        """Load HF tokenizer + chat template + EOS ids from a model directory.

        Falls back to apps.llm's `SimpleTokenizer.from_gguf_kv` when there
        is no `tokenizer.json` sidecar (single-file GGUF, no HF repo).
        """
        tokenizer_json = model_dir / "tokenizer.json"
        if tokenizer_json.exists():
            from tokenizers import Tokenizer as HFTokenizer
            self.llm_tokenizer = HFTokenizer.from_file(str(tokenizer_json))
        elif gguf_kv is not None:
            from tinygrad.apps.llm import SimpleTokenizer
            self.llm_tokenizer = SimpleTokenizer.from_gguf_kv(gguf_kv)
        else:
            raise FileNotFoundError(f"tokenizer.json not found under {model_dir}")

        tok_cfg_path = model_dir / "tokenizer_config.json"
        if tok_cfg_path.exists():
            with open(tok_cfg_path) as fp:
                tok_cfg = json.load(fp)
            self.chat_template = tok_cfg.get("chat_template")

        self.llm_eos_ids = []
        for cfg_name in ("generation_config.json", "config.json"):
            cfg_path = model_dir / cfg_name
            if not cfg_path.exists():
                continue
            with open(cfg_path) as fp:
                cfg = json.load(fp)
            eos = cfg.get("eos_token_id")
            if isinstance(eos, list):
                self.llm_eos_ids.extend(int(x) for x in eos)
            elif isinstance(eos, int):
                self.llm_eos_ids.append(eos)
            if self.llm_eos_ids:
                break
        if not self.llm_eos_ids and gguf_kv is not None:
            eos = gguf_kv.get("tokenizer.ggml.eos_token_id")
            if isinstance(eos, int):
                self.llm_eos_ids.append(eos)

    # --------------------- Stable Diffusion path ------------------------

    def _load_sd(self, model_ref: str) -> None:
        """Load a Stable Diffusion 1.x checkpoint (CompVis `.ckpt` format)."""
        from huggingface_hub import hf_hub_download
        from tinygrad.nn.state import load_state_dict, torch_load

        from vendor.stable_diffusion import StableDiffusion

        ckpt_path = Path(model_ref)
        if not ckpt_path.exists():
            # Accept an HF repo id — fetch the canonical v1-5-pruned-emaonly.ckpt
            # from the requested repo. Common case is runwayml/stable-diffusion-v1-5.
            repo_id = model_ref if "/" in model_ref else "runwayml/stable-diffusion-v1-5"
            ckpt_file = self.options.get("sd_ckpt_filename", "v1-5-pruned-emaonly.ckpt")
            ckpt_path = Path(hf_hub_download(repo_id=repo_id, filename=ckpt_file))

        model = StableDiffusion()
        state_dict = torch_load(str(ckpt_path))
        if isinstance(state_dict, dict) and "state_dict" in state_dict:
            state_dict = state_dict["state_dict"]
        load_state_dict(model, state_dict, strict=False, verbose=False, realize=False)
        self.sd_model = model

    # --------------------- Whisper path ---------------------------------

    def _load_whisper(self, model_ref: str) -> None:
        """Load a Whisper checkpoint (OpenAI `.pt` format).

        Accepts a model-size alias (tiny / tiny.en / base / base.en / small /
        small.en) OR an explicit `.pt` file path OR the HF repo id naming
        convention `openai/whisper-*` (mapped to the matching OpenAI alias).
        """
        from vendor.whisper import init_whisper, MODEL_URLS

        alias = model_ref
        if "/" in alias and alias.startswith("openai/whisper-"):
            alias = alias.removeprefix("openai/whisper-")
        if alias not in MODEL_URLS:
            # Explicit path to a .pt checkpoint — fall back to size heuristic
            # via filename.
            basename = Path(alias).name.lower()
            for name in MODEL_URLS:
                if name in basename:
                    alias = name
                    break
            else:
                raise ValueError(
                    f"Unknown Whisper model_ref={model_ref!r}; expected one of {list(MODEL_URLS)} "
                    f"or an openai/whisper-* HF id"
                )

        model, enc = init_whisper(alias, batch_size=1)
        self.whisper_model = model
        self.whisper_tokenizer = enc

    # --------------------- LLM generation -------------------------------

    def _encode_prompt(self, prompt: str) -> list[int]:
        """Normalize tokenizer output: HF `tokenizers.Tokenizer.encode()`
        returns an `Encoding` with `.ids`; apps.llm's `SimpleTokenizer.encode()`
        returns `list[int]` directly."""
        encoded = self.llm_tokenizer.encode(prompt)
        return list(getattr(encoded, "ids", encoded))

    def _decode_tokens(self, ids: list[int]) -> str:
        return self.llm_tokenizer.decode(ids)

    def _generate_tokens(self, prompt: str, max_new_tokens: int, temperature: float):
        """Yield (token_id, token_text) pairs using `apps.llm.Transformer.generate()`.

        tinygrad 0.12.0's `generate()` is greedy-only (its `forward` ends
        with `.argmax(-1)` and it takes no temperature / top-k / top-p
        knobs). We accept `temperature` in the signature for API
        compatibility but it is ignored.
        """
        del temperature  # tinygrad.apps.llm.Transformer.generate is greedy-only
        ids = self._encode_prompt(prompt)
        if not ids:
            return

        count = 0
        for next_tok in self.llm_model.generate(list(ids)):
            if next_tok in self.llm_eos_ids:
                break
            yield next_tok, self._decode_tokens([next_tok])
            count += 1
            if count >= max_new_tokens:
                break

    # --------------------- gRPC methods ---------------------------------

    def Health(self, request, context):
        return backend_pb2.Reply(message=bytes("OK", 'utf-8'))

    async def LoadModel(self, request, context):
        try:
            _select_tinygrad_device()
            self._reset_state()
            self.options = self._parse_options(list(request.Options))
            self.model_ref = request.ModelFile or request.Model
            self.model_type = self._detect_model_type(self.model_ref, self.options.get("model_type"))

            if self.model_type in ("sd15", "sd", "stable-diffusion"):
                self._load_sd(self.model_ref)
                return backend_pb2.Result(
                    success=True, message="tinygrad Stable Diffusion 1.x loaded",
                )

            if self.model_type == "whisper":
                self._load_whisper(self.model_ref)
                return backend_pb2.Result(
                    success=True, message="tinygrad Whisper loaded",
                )

            if self.model_type != "llm":
                return backend_pb2.Result(
                    success=False,
                    message=f"tinygrad: model_type={self.model_type} not yet implemented",
                )

            model_path = _resolve_model_assets(self.model_ref)
            self._load_llm(model_path)

            return backend_pb2.Result(
                success=True,
                message=f"tinygrad LLM loaded (arch={self.llm_config.get('architectures', ['?'])[0]}, "
                        f"parser={self.tool_parser.name})",
            )
        except Exception as exc:
            import traceback
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"LoadModel failed: {exc}")

    async def Predict(self, request, context):
        if self.llm_model is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("LLM not loaded")
            return backend_pb2.Reply()

        try:
            prompt = self._render_prompt(request)
            max_new = request.Tokens if request.Tokens > 0 else 256
            temperature = request.Temperature if request.Temperature > 0 else 0.7

            t0 = time.monotonic()
            pieces: list[str] = []
            ntok = 0
            for _, text in self._generate_tokens(prompt, max_new, temperature):
                pieces.append(text)
                ntok += 1
            elapsed = time.monotonic() - t0

            full = "".join(pieces)
            from tool_parsers.hermes import HermesToolParser
            if isinstance(self.tool_parser, HermesToolParser):
                result = self.tool_parser.parse_full(full)
                content, calls, reasoning = result.content, result.tool_calls, result.reasoning
            else:
                content, calls = self.tool_parser.parse(full)
                reasoning = ""

            delta = backend_pb2.ChatDelta(
                content=content,
                reasoning_content=reasoning,
                tool_calls=[
                    backend_pb2.ToolCallDelta(index=c.index, id=c.id, name=c.name, arguments=c.arguments)
                    for c in calls
                ],
            )
            return backend_pb2.Reply(
                message=content.encode("utf-8"),
                tokens=ntok,
                timing_token_generation=elapsed,
                chat_deltas=[delta],
            )
        except Exception as exc:
            import traceback
            traceback.print_exc()
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Predict failed: {exc}")
            return backend_pb2.Reply()

    async def PredictStream(self, request, context):
        if self.llm_model is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("LLM not loaded")
            return

        try:
            prompt = self._render_prompt(request)
            max_new = request.Tokens if request.Tokens > 0 else 256
            temperature = request.Temperature if request.Temperature > 0 else 0.7

            buffer = ""
            for _, text in self._generate_tokens(prompt, max_new, temperature):
                buffer += text
                yield backend_pb2.Reply(
                    message=text.encode("utf-8"),
                    chat_deltas=[backend_pb2.ChatDelta(content=text)],
                )

            # Final emission carries the extracted tool calls (vLLM semantics).
            from tool_parsers.hermes import HermesToolParser
            if isinstance(self.tool_parser, HermesToolParser):
                result = self.tool_parser.parse_full(buffer)
                calls = result.tool_calls
                reasoning = result.reasoning
            else:
                _, calls = self.tool_parser.parse(buffer)
                reasoning = ""

            if calls or reasoning:
                yield backend_pb2.Reply(
                    chat_deltas=[backend_pb2.ChatDelta(
                        reasoning_content=reasoning,
                        tool_calls=[
                            backend_pb2.ToolCallDelta(index=c.index, id=c.id, name=c.name, arguments=c.arguments)
                            for c in calls
                        ],
                    )],
                )
        except Exception as exc:
            import traceback
            traceback.print_exc()
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"PredictStream failed: {exc}")

    async def Embedding(self, request, context):
        if self.llm_model is None or self.llm_tokenizer is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("No model loaded")
            return backend_pb2.EmbeddingResult()

        try:
            text = request.Embeddings
            if not text:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("Embeddings field is empty")
                return backend_pb2.EmbeddingResult()

            from tinygrad import Tensor, dtypes
            from vendor.appsllm_adapter import _embed_hidden

            ids = self._encode_prompt(text)
            if not ids:
                return backend_pb2.EmbeddingResult(embeddings=[])

            # Clamp to context window — truncate long inputs rather than blow up.
            ids = ids[: self.max_context]
            tokens = Tensor([ids])

            hidden = _embed_hidden(self.llm_model, tokens)  # (1, seqlen, dim)
            # Mean pool over sequence dim
            pooled = hidden.mean(axis=1).squeeze(0)  # (dim,)
            # L2 normalize
            norm = pooled.square().sum().sqrt()
            normalized = (pooled / (norm + 1e-12))
            vec = normalized.cast(dtypes.float32).tolist()

            return backend_pb2.EmbeddingResult(embeddings=[float(x) for x in vec])
        except Exception as exc:
            import traceback
            traceback.print_exc()
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"Embedding failed: {exc}")
            return backend_pb2.EmbeddingResult()

    async def GenerateImage(self, request, context):
        if self.sd_model is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("No Stable Diffusion model loaded")
            return backend_pb2.Result(success=False, message="not loaded")

        try:
            from PIL import Image
            from vendor.stable_diffusion import run_sd15

            steps = request.step if request.step > 0 else 20
            guidance = 7.5
            seed = request.seed if request.seed != 0 else None
            img_tensor = run_sd15(
                model=self.sd_model,
                prompt=request.positive_prompt or "",
                negative_prompt=request.negative_prompt or "",
                steps=steps,
                guidance=guidance,
                seed=seed,
            )
            arr = img_tensor.numpy()
            image = Image.fromarray(arr)
            dst = request.dst or os.path.join(tempfile.gettempdir(), "tinygrad_image.png")
            image.save(dst)
            return backend_pb2.Result(success=True, message=dst)
        except Exception as exc:
            import traceback
            traceback.print_exc()
            return backend_pb2.Result(success=False, message=f"GenerateImage failed: {exc}")

    def _transcribe(self, audio_path: str, language: Optional[str]) -> tuple[str, float]:
        from vendor.whisper import load_file_waveform, transcribe_waveform

        waveform = load_file_waveform(audio_path)
        text = transcribe_waveform(
            self.whisper_model,
            self.whisper_tokenizer,
            [waveform],
            language=language or None,
        )
        duration = float(len(waveform)) / 16000.0
        return text, duration

    async def AudioTranscription(self, request, context):
        if self.whisper_model is None or self.whisper_tokenizer is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("No Whisper model loaded")
            return backend_pb2.TranscriptResult()

        try:
            if not request.dst:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("TranscriptRequest.dst (audio file path) is required")
                return backend_pb2.TranscriptResult()

            text, duration = self._transcribe(request.dst, request.language)
            segments = [backend_pb2.TranscriptSegment(id=0, start=0, end=0, text=text)]
            return backend_pb2.TranscriptResult(
                text=text,
                language=request.language or "en",
                duration=duration,
                segments=segments,
            )
        except Exception as exc:
            import traceback
            traceback.print_exc()
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"AudioTranscription failed: {exc}")
            return backend_pb2.TranscriptResult()

    async def AudioTranscriptionStream(self, request, context):
        if self.whisper_model is None or self.whisper_tokenizer is None:
            context.set_code(grpc.StatusCode.FAILED_PRECONDITION)
            context.set_details("No Whisper model loaded")
            return

        try:
            if not request.dst:
                context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
                context.set_details("TranscriptRequest.dst (audio file path) is required")
                return

            # The vendored tinygrad whisper loop is chunked at the file level
            # (one inference pass per 30s segment), not token-level. To still
            # produce a streaming response we run the full transcription and
            # emit it as a single delta + a final-result envelope so the client
            # gets both code paths exercised.
            text, duration = self._transcribe(request.dst, request.language)
            yield backend_pb2.TranscriptStreamResponse(delta=text)
            final = backend_pb2.TranscriptResult(
                text=text,
                language=request.language or "en",
                duration=duration,
                segments=[backend_pb2.TranscriptSegment(id=0, start=0, end=0, text=text)],
            )
            yield backend_pb2.TranscriptStreamResponse(final_result=final)
        except Exception as exc:
            import traceback
            traceback.print_exc()
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details(f"AudioTranscriptionStream failed: {exc}")

    async def Status(self, request, context):
        return backend_pb2.StatusResponse(state=backend_pb2.StatusResponse.READY)

    async def Free(self, request, context):
        self._reset_state()
        return backend_pb2.Result(success=True, message="freed")


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
    parser = argparse.ArgumentParser(description="Run the tinygrad gRPC backend.")
    parser.add_argument("--addr", default="localhost:50051", help="Bind address")
    args = parser.parse_args()
    asyncio.run(serve(args.addr))
