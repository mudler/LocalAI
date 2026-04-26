"""Tool-call parsers for the tinygrad backend.

Each parser takes raw model output and extracts OpenAI-style tool calls so
the backend can populate `ChatDelta.tool_calls[]` natively (matching vLLM's
behavior, which the Go core prefers over regex fallback parsing).
"""
from __future__ import annotations

from .base import ToolCall, ToolParser, resolve_parser

__all__ = ["ToolCall", "ToolParser", "resolve_parser"]
