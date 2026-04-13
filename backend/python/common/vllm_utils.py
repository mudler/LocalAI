"""vLLM-specific helpers for the vllm and vllm-omni gRPC backends.

Generic helpers (``parse_options``, ``messages_to_dicts``) live in
``python_utils`` and are re-exported here for backwards compatibility with
existing imports in both backends.
"""
import sys

from python_utils import messages_to_dicts, parse_options

__all__ = ["parse_options", "messages_to_dicts", "setup_parsers"]


def setup_parsers(opts):
    """Return ``(tool_parser_cls, reasoning_parser_cls)`` from an opts dict.

    Uses vLLM's native ``ToolParserManager`` / ``ReasoningParserManager``.
    Returns ``(None, None)`` if vLLM isn't installed or the requested
    parser name can't be resolved.
    """
    tool_parser_cls = None
    reasoning_parser_cls = None

    tool_parser_name = opts.get("tool_parser")
    reasoning_parser_name = opts.get("reasoning_parser")

    if tool_parser_name:
        try:
            from vllm.tool_parsers import ToolParserManager
            tool_parser_cls = ToolParserManager.get_tool_parser(tool_parser_name)
            print(f"[vllm_utils] Loaded tool_parser: {tool_parser_name}", file=sys.stderr)
        except Exception as e:
            print(f"[vllm_utils] Failed to load tool_parser {tool_parser_name}: {e}", file=sys.stderr)

    if reasoning_parser_name:
        try:
            from vllm.reasoning import ReasoningParserManager
            reasoning_parser_cls = ReasoningParserManager.get_reasoning_parser(reasoning_parser_name)
            print(f"[vllm_utils] Loaded reasoning_parser: {reasoning_parser_name}", file=sys.stderr)
        except Exception as e:
            print(f"[vllm_utils] Failed to load reasoning_parser {reasoning_parser_name}: {e}", file=sys.stderr)

    return tool_parser_cls, reasoning_parser_cls
