"""Load-time options for the tensorfold backend.

ModelOptions.Options arrives as key:value strings. Each key maps to one
`tensorfold serve` flag, so upstream's own argparse validates the value and
supplies every default; nothing here restates an upstream default.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

# option key -> (flag, kind). "value" takes the option's value, "switch" is
# added when the option is true and omitted when it is false.
FLAGS: dict[str, tuple[str, str]] = {
    "parallel": ("--parallel", "value"),
    "max_tokens": ("--max-tokens", "value"),
    "temperature": ("--temperature", "value"),
    "top_p": ("--top-p", "value"),
    "top_k": ("--top-k", "value"),
    "min_p": ("--min-p", "value"),
    "reasoning_effort": ("--reasoning-effort", "value"),
    "thinking_budget": ("--thinking-budget", "value"),
    "drafter": ("--drafter", "value"),
    "mtp_drafts": ("--mtp-drafts", "value"),
    "mtp_confidence": ("--mtp-confidence", "value"),
    "kv_dtype": ("--kv-dtype", "value"),
    "prompt_cache_gib": ("--prompt-cache-gib", "value"),
    "checkpoint_slots": ("--checkpoint-slots", "value"),
    "spill_gib": ("--spill-gib", "value"),
    "snapshot_dir": ("--snapshot-dir", "value"),
    "max_snapshots": ("--max-snapshots", "value"),
    "decode_share": ("--decode-share", "value"),
    "mlx_cache_gib": ("--mlx-cache-gib", "value"),
    "no_drafts": ("--no-drafts", "switch"),
    "vision": ("--vision", "switch"),
}

# Handled outside FLAGS because they are not a plain flag.
_SPECIAL = frozenset({"thinking", "context", "memory_limit_gb"})

_KV_DTYPES = ("bf16", "int8", "int4")


@dataclass(frozen=True)
class LoadConfig:
    model: str
    argv: tuple[str, ...]
    env: dict[str, str] = field(default_factory=dict)


def parse_load_config(model: str, context_size: int, options: dict[str, Any]) -> LoadConfig:
    unknown = sorted(set(options) - set(FLAGS) - _SPECIAL)
    if unknown:
        known = ", ".join(sorted(set(FLAGS) | _SPECIAL))
        raise ValueError(f"unknown tensorfold option(s): {', '.join(unknown)}. Known options: {known}")

    argv: list[str] = ["serve", model, "--no-update-check"]
    env: dict[str, str] = {}

    context = options.get("context", context_size)
    if "context" in options or context > 0:
        argv += ["--context", str(int(context))]

    if "thinking" in options:
        if not isinstance(options["thinking"], bool):
            raise ValueError("thinking must be true or false")
        argv.append("--thinking" if options["thinking"] else "--no-thinking")

    if "memory_limit_gb" in options:
        env["TENSORFOLD_MEMORY_LIMIT_GB"] = str(options["memory_limit_gb"])

    for key, (flag, kind) in FLAGS.items():
        if key not in options:
            continue
        value = options[key]
        if kind == "switch":
            if not isinstance(value, bool):
                raise ValueError(f"{key} must be true or false")
            if value:
                argv.append(flag)
            continue
        if isinstance(value, bool):
            raise ValueError(f"{key} takes a value, not true or false")
        if key == "kv_dtype" and value not in _KV_DTYPES:
            raise ValueError(f"kv_dtype must be one of {', '.join(_KV_DTYPES)}")
        argv += [flag, str(value)]

    return LoadConfig(model=model, argv=tuple(argv), env=env)
