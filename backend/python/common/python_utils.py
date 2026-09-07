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


def attach_media_parts(messages_dicts, n_images=0, n_videos=0):
    """Rebuild the last user message as content *parts* carrying media markers.

    Backends that let the tokenizer do the templating hand plain string content
    to ``apply_chat_template``, but a chat template only emits the model's own
    media tokens (``<|vision_start|><|image_pad|><|vision_end|>`` for the
    Qwen-VL family, and the equivalents elsewhere) when the content is a list
    of parts. Without those markers the engine's multimodal processor finds
    nothing to substitute and silently discards the pixels, even though they
    were forwarded correctly out of band.

    Returns a new list whose last user message has
    ``[{"type": "image"} * n_images, {"type": "video"} * n_videos, text]`` as
    its content, or ``None`` when there is nothing to attach - no media, no
    user turn, or content that is already a list of parts - so the caller can
    keep using the original string-content list.
    """
    if not n_images and not n_videos:
        return None
    idx = next(
        (
            i
            for i in reversed(range(len(messages_dicts)))
            if messages_dicts[i].get("role") == "user"
        ),
        None,
    )
    if idx is None:
        return None
    text = messages_dicts[idx].get("content") or ""
    if not isinstance(text, str):
        return None
    parts = [{"type": "image"}] * n_images + [{"type": "video"}] * n_videos
    if text:
        parts.append({"type": "text", "text": text})
    patched = list(messages_dicts)
    patched[idx] = dict(patched[idx], content=parts)
    return patched


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
                tool_calls = json.loads(msg.tool_calls)
                # Chat templates (e.g. Qwen) iterate function.arguments as a
                # mapping, but the OpenAI wire format carries it as a JSON
                # string — decode it back so the template's .items() works.
                for tc in tool_calls:
                    fn = tc.get("function") if isinstance(tc, dict) else None
                    if isinstance(fn, dict) and isinstance(fn.get("arguments"), str):
                        try:
                            fn["arguments"] = json.loads(fn["arguments"])
                        except json.JSONDecodeError:
                            pass
                d["tool_calls"] = tool_calls
            except json.JSONDecodeError:
                pass
        result.append(d)
    return result
