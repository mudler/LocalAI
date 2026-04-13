"""Generic utilities shared across Python gRPC backends.

These helpers don't depend on any specific inference framework and can be
imported by any backend that needs to parse LocalAI gRPC options or build a
chat-template-compatible message list from proto Message objects.
"""
import json


def parse_options(options_list):
    """Parse Options[] list of ``key:value`` strings into a dict.

    Supports type inference for common cases (bool, int, float). Unknown or
    mixed-case values are returned as strings.

    Used by LoadModel to extract backend-specific options passed via
    ``ModelOptions.Options`` in ``backend.proto``.
    """
    opts = {}
    for opt in options_list:
        if ":" not in opt:
            continue
        key, value = opt.split(":", 1)
        key = key.strip()
        value = value.strip()
        # Try type conversion
        if value.lower() in ("true", "false"):
            opts[key] = value.lower() == "true"
        else:
            try:
                opts[key] = int(value)
            except ValueError:
                try:
                    opts[key] = float(value)
                except ValueError:
                    opts[key] = value
    return opts


def messages_to_dicts(proto_messages):
    """Convert proto ``Message`` objects to dicts suitable for ``apply_chat_template``.

    Handles: ``role``, ``content``, ``name``, ``tool_call_id``,
    ``reasoning_content``, ``tool_calls`` (JSON string → Python list).

    HuggingFace chat templates (and their MLX/vLLM wrappers) expect a list of
    plain dicts — proto Message objects don't work directly with Jinja, so
    this conversion is needed before every ``apply_chat_template`` call.
    """
    result = []
    for msg in proto_messages:
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
