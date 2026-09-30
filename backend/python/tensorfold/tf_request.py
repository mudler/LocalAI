"""Translate a LocalAI PredictOptions message into an OpenAI-shaped body.

TensorFold's servers take the OpenAI request body, so both engines consume the
same dict and this module is the only place proto fields are read.
"""
from __future__ import annotations

import base64
import json
import os
import sys
from dataclasses import dataclass
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "common"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "common"))
from python_utils import messages_to_dicts  # noqa: E402


@dataclass(frozen=True)
class Built:
    body: dict[str, Any]
    chat: bool


def _sniff_mime(raw: bytes) -> str:
    if raw.startswith(b"\x89PNG"):
        return "image/png"
    if raw[:4] == b"RIFF" and raw[8:12] == b"WEBP":
        return "image/webp"
    return "image/jpeg"


def _image_url(value: str) -> str:
    if value.startswith(("data:", "http://", "https://")):
        return value
    if os.path.isfile(value):
        with open(value, "rb") as handle:
            raw = handle.read()
        return f"data:{_sniff_mime(raw)};base64,{base64.b64encode(raw).decode()}"
    raw = base64.b64decode(value, validate=False)
    return f"data:{_sniff_mime(raw)};base64,{value}"


def _attach_images(messages: list[dict[str, Any]], images: list[str]) -> list[dict[str, Any]]:
    index = next((i for i in reversed(range(len(messages))) if messages[i].get("role") == "user"), None)
    if index is None:
        raise ValueError("images were sent without a user message to attach them to")
    text = messages[index].get("content") or ""
    parts: list[dict[str, Any]] = [{"type": "text", "text": text}] if isinstance(text, str) and text else []
    parts += [{"type": "image_url", "image_url": {"url": _image_url(v)}} for v in images]
    patched = list(messages)
    patched[index] = dict(patched[index], content=parts)
    return patched


def build_body(request) -> Built:
    body: dict[str, Any] = {}
    if request.UseTokenizerTemplate and request.Messages:
        chat = True
        messages = messages_to_dicts(request.Messages)
        if request.Images:
            messages = _attach_images(messages, list(request.Images))
        body["messages"] = messages
    elif request.Prompt:
        # LocalAI already rendered the template: treat it as a raw completion,
        # which TensorFold serves without a template or thinking block.
        chat = False
        body["prompt"] = request.Prompt
    else:
        raise ValueError("the request has neither messages nor a prompt")

    # LocalAI sends zero for "not configured". Leaving these out lets each
    # model keep its own defaults (greedy on MLX, temperature 1.0 on CUDA).
    if request.Temperature > 0:
        body["temperature"] = request.Temperature
    if request.TopP > 0:
        body["top_p"] = request.TopP
    if request.TopK > 0:
        body["top_k"] = request.TopK
    if request.MinP > 0:
        body["min_p"] = request.MinP
    if request.Tokens > 0:
        body["max_tokens"] = request.Tokens
    if request.Seed > 0:
        body["seed"] = request.Seed
    if request.StopPrompts:
        body["stop"] = list(request.StopPrompts)
    if request.IgnoreEOS:
        body["ignore_eos"] = True

    if request.Tools:
        body["tools"] = json.loads(request.Tools)
        if request.ToolChoice:
            try:
                body["tool_choice"] = json.loads(request.ToolChoice)
            except json.JSONDecodeError:
                body["tool_choice"] = request.ToolChoice

    thinking = request.Metadata.get("enable_thinking", "").lower()
    if thinking in ("true", "false"):
        body["chat_template_kwargs"] = {"enable_thinking": thinking == "true"}
    if request.Metadata.get("reasoning_effort"):
        body["reasoning_effort"] = request.Metadata["reasoning_effort"]

    if request.Grammar:
        try:
            body["guided_json"] = json.loads(request.Grammar)
        except json.JSONDecodeError:
            body["guided_grammar"] = request.Grammar

    return Built(body=body, chat=chat)
