"""vLLM-specific helpers for the vllm and vllm-omni gRPC backends.

Generic helpers (``parse_options``, ``messages_to_dicts``) live in
``python_utils`` and are re-exported here for backwards compatibility with
existing imports in both backends.
"""
import dataclasses
import difflib
import json
import sys

from python_utils import messages_to_dicts, parse_options

__all__ = [
    "parse_options",
    "messages_to_dicts",
    "setup_parsers",
    "apply_options_to_engine_args",
    "normalize_option_key",
]

# Options[] entries LocalAI itself acts on, in their normalized spelling.
_BACKEND_LEVEL_OPTIONS = {"tool_parser", "reasoning_parser"}

_TRUTHY = {"true", "1", "yes", "on", "t", "y"}
_FALSY = {"false", "0", "no", "off", "f", "n"}


def normalize_option_key(key):
    """Normalize an Options[] key to its engine/field spelling.

    Users copy flags straight out of vLLM's CLI docs (``--reasoning-parser``),
    so accept that spelling everywhere LocalAI looks options up by name.
    """
    return key.strip().lstrip("-").replace("-", "_")


_BASE_HINTS = {
    "bool": "bool",
    "int": "int",
    "float": "float",
    "str": "str",
    "dict": "dict",
    "mapping": "dict",
}


def _hint_from_annotation(annotation):
    """Target type name for a field annotation, or None when it is not a scalar.

    ``dataclasses.fields()`` hands back either a real type, a ``typing``
    construct or - under PEP 563, which vLLM's arg_utils uses - the annotation
    as a plain string, so work on the textual form. Match the *base* of the
    annotation rather than searching it: ``Literal["auto", "float16"]`` (vLLM's
    dtype) contains "float" but is not a float.
    """
    if annotation is None:
        return None
    if isinstance(annotation, type):
        text = annotation.__name__
    elif isinstance(annotation, str):
        text = annotation
    else:
        text = str(annotation)
    text = text.replace("typing.", "").strip()
    while text.lower().startswith("optional[") and text.endswith("]"):
        text = text[len("optional["):-1].strip()
    if "|" in text:
        parts = [p.strip() for p in text.split("|") if p.strip().lower() not in ("none", "nonetype")]
        if len(parts) != 1:
            return None
        text = parts[0]
    base = text.split("[", 1)[0].strip().lower()
    return _BASE_HINTS.get(base)


def _hint_from_value(current):
    """Target type name inferred from a field's current value."""
    if isinstance(current, bool):
        return "bool"
    if isinstance(current, dict):
        return "dict"
    if isinstance(current, float):
        return "float"
    if isinstance(current, int):
        return "int"
    if isinstance(current, str):
        return "str"
    return None


def _type_hint(annotation, current):
    """Best-effort target type name for a dataclass field."""
    hint = _hint_from_annotation(annotation)
    if hint is None:
        hint = _hint_from_value(current)
    return hint


def _coerce_option(raw, hint):
    """Coerce a CLI-supplied string to the field's type. Raises ValueError."""
    if hint == "bool":
        low = raw.lower()
        if low in _TRUTHY:
            return True
        if low in _FALSY:
            return False
        raise ValueError(f"{raw!r} is not a boolean")
    if hint == "int":
        return int(raw)
    if hint == "float":
        return float(raw)
    if hint == "dict":
        return json.loads(raw)
    if hint == "str":
        return raw

    # Untyped (or union-typed) field: infer from the literal itself.
    low = raw.lower()
    if low in _TRUTHY:
        return True
    if low in _FALSY:
        return False
    for cast in (int, float):
        try:
            return cast(raw)
        except ValueError:
            pass
    if raw.startswith("{") or raw.startswith("["):
        return json.loads(raw)
    return raw


def apply_options_to_engine_args(engine_args, options):
    """Apply CLI-style ``--flag[:value]`` entries from Options[] to engine args.

    ``options:`` is a loose bag shared with backend-level settings
    (``tool_parser:``, ``reasoning_parser:``, ``vad_only``, …), so only
    ``--`` prefixed entries are treated as engine flags. Names are normalized
    the way vLLM's own CLI does (``--enable-prefix-caching`` →
    ``enable_prefix_caching``) and values are coerced to the target field's
    type. Both ``--flag:value`` (LocalAI convention) and ``--flag=value``
    (vLLM CLI convention) are accepted; a bare ``--flag`` sets a boolean field.

    Unknown or uncoercible flags warn on stderr and are skipped instead of
    failing the load - unlike ``engine_args:``, which is engine-only and
    therefore strict, Options[] carries entries this function knows nothing
    about.

    Returns a new dataclass instance via ``dataclasses.replace`` so the
    engine's ``__post_init__`` re-runs, or the original when nothing applied.
    """
    if not options:
        return engine_args

    fields = {f.name: f for f in dataclasses.fields(type(engine_args))}
    updates = {}
    for opt in options:
        opt = opt.strip()
        if not opt.startswith("--"):
            continue
        body = opt[2:]
        seps = [i for i in (body.find(":"), body.find("=")) if i != -1]
        if seps:
            cut = min(seps)
            name, raw = body[:cut], body[cut + 1:].strip()
        else:
            name, raw = body, None
        name = normalize_option_key(name)

        if name not in fields:
            # LocalAI reads these itself; whether the engine dataclass carries
            # them too varies by vLLM version, so never flag them as unknown.
            if name in _BACKEND_LEVEL_OPTIONS:
                continue
            suggestion = difflib.get_close_matches(name, fields, n=1)
            hint = f" did you mean {suggestion[0]!r}?" if suggestion else ""
            print(
                f"[vllm_utils] unknown engine option {opt!r} (field {name!r}), skipping.{hint}",
                file=sys.stderr,
            )
            continue

        target = _type_hint(fields[name].type, getattr(engine_args, name, None))
        if raw is None:
            if target != "bool":
                print(
                    f"[vllm_utils] engine option {opt!r} needs a value "
                    f"(field {name!r} is not a flag), skipping",
                    file=sys.stderr,
                )
                continue
            updates[name] = True
            continue

        try:
            updates[name] = _coerce_option(raw, target)
        except (ValueError, TypeError) as err:
            print(
                f"[vllm_utils] cannot apply engine option {opt!r} to field "
                f"{name!r}: {err}, skipping",
                file=sys.stderr,
            )

    if not updates:
        return engine_args
    print(f"[vllm_utils] engine options from Options[]: {updates}", file=sys.stderr)
    return dataclasses.replace(engine_args, **updates)


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
