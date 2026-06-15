"""
Built-in reward functions and inline function compiler for GRPO training.

All reward functions follow TRL's signature: (completions, **kwargs) -> list[float]
"""

import json
import re
import math
import string
import functools


# ---------------------------------------------------------------------------
# Built-in reward functions
# ---------------------------------------------------------------------------

def format_reward(completions, **kwargs):
    """Checks for <think>...</think> followed by an answer. Returns 1.0 or 0.0."""
    pattern = re.compile(r"<think>.*?</think>\s*\S", re.DOTALL)
    return [1.0 if pattern.search(c) else 0.0 for c in completions]


def reasoning_accuracy_reward(completions, **kwargs):
    """Extracts <answer>...</answer> content and compares to the expected answer."""
    answers = kwargs.get("answer", [])
    if not answers:
        return [0.0] * len(completions)

    pattern = re.compile(r"<answer>(.*?)</answer>", re.DOTALL)
    scores = []
    for i, c in enumerate(completions):
        expected = answers[i] if i < len(answers) else ""
        match = pattern.search(c)
        if match:
            extracted = match.group(1).strip()
            scores.append(1.0 if extracted.lower() == str(expected).strip().lower() else 0.0)
        else:
            scores.append(0.0)
    return scores


def length_reward(completions, target_length=200, **kwargs):
    """Score based on proximity to target_length. Returns [0, 1]."""
    scores = []
    for c in completions:
        length = len(c)
        if target_length <= 0:
            scores.append(0.0)
        else:
            diff = abs(length - target_length) / target_length
            scores.append(max(0.0, 1.0 - diff))
    return scores


def xml_tag_reward(completions, **kwargs):
    """Scores properly opened/closed XML tags (<think>, <answer>)."""
    tags = ["think", "answer"]
    scores = []
    for c in completions:
        tag_score = 0.0
        for tag in tags:
            if f"<{tag}>" in c and f"</{tag}>" in c:
                tag_score += 0.5
        scores.append(min(tag_score, 1.0))
    return scores


def no_repetition_reward(completions, n=4, **kwargs):
    """Penalizes n-gram repetition. Returns [0, 1]."""
    scores = []
    for c in completions:
        words = c.split()
        if len(words) < n:
            scores.append(1.0)
            continue
        ngrams = [tuple(words[i:i+n]) for i in range(len(words) - n + 1)]
        unique = len(set(ngrams))
        total = len(ngrams)
        scores.append(unique / total if total > 0 else 1.0)
    return scores


def code_execution_reward(completions, **kwargs):
    """Checks Python code block syntax validity via compile(). Returns 1.0 or 0.0."""
    pattern = re.compile(r"```python\s*\n(.*?)```", re.DOTALL)
    scores = []
    for c in completions:
        match = pattern.search(c)
        if not match:
            scores.append(0.0)
            continue
        code = match.group(1)
        try:
            compile(code, "<inline>", "exec")
            scores.append(1.0)
        except SyntaxError:
            scores.append(0.0)
    return scores


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------

BUILTIN_REGISTRY = {
    "format_reward": format_reward,
    "reasoning_accuracy_reward": reasoning_accuracy_reward,
    "length_reward": length_reward,
    "xml_tag_reward": xml_tag_reward,
    "no_repetition_reward": no_repetition_reward,
    "code_execution_reward": code_execution_reward,
}


# ---------------------------------------------------------------------------
# Inline function compiler
# ---------------------------------------------------------------------------

_SAFE_BUILTINS = {
    "len": len, "int": int, "float": float, "str": str, "bool": bool,
    "list": list, "dict": dict, "tuple": tuple, "set": set,
    "range": range, "enumerate": enumerate, "zip": zip,
    "map": map, "filter": filter, "sorted": sorted,
    "min": min, "max": max, "sum": sum, "abs": abs, "round": round,
    "any": any, "all": all, "isinstance": isinstance, "type": type,
    "print": print, "True": True, "False": False, "None": None,
    "ValueError": ValueError, "TypeError": TypeError,
    "KeyError": KeyError, "IndexError": IndexError,
}


def compile_inline_reward(name, code):
    """Compile user-provided code into a reward function.

    The code should be the body of a function that receives
    `completions` (list[str]) and `**kwargs`, and returns list[float].

    Available modules: re, math, json, string.
    """
    func_source = (
        f"def _user_reward_{name}(completions, **kwargs):\n"
        + "\n".join(f"    {line}" for line in code.splitlines())
    )

    restricted_globals = {
        "__builtins__": _SAFE_BUILTINS,
        "re": re,
        "math": math,
        "json": json,
        "string": string,
    }

    try:
        compiled = compile(func_source, f"<inline-reward-{name}>", "exec")
    except SyntaxError as e:
        raise ValueError(f"Syntax error in inline reward function '{name}': {e}")

    exec(compiled, restricted_globals)
    func = restricted_globals[f"_user_reward_{name}"]

    # Validate with a quick smoke test
    try:
        result = func(["test"], answer=["test"])
        if not isinstance(result, list):
            raise ValueError(
                f"Inline reward function '{name}' must return a list, got {type(result).__name__}"
            )
    except Exception as e:
        if "must return a list" in str(e):
            raise
        # Other errors during smoke test are acceptable (e.g. missing kwargs)
        pass

    return func


# ---------------------------------------------------------------------------
# Dispatcher
# ---------------------------------------------------------------------------

def build_reward_functions(specs_json):
    """Parse a JSON list of reward function specs and return a list of callables.

    Each spec is a dict with:
      - type: "builtin" or "inline"
      - name: function name
      - code: (inline only) Python function body
      - params: (optional) dict of string params applied via functools.partial
    """
    if isinstance(specs_json, str):
        specs = json.loads(specs_json)
    else:
        specs = specs_json

    if not isinstance(specs, list):
        raise ValueError("reward_funcs must be a JSON array of reward function specs")

    reward_funcs = []
    for spec in specs:
        spec_type = spec.get("type", "builtin")
        name = spec.get("name", "")
        params = spec.get("params", {})

        if spec_type == "builtin":
            if name not in BUILTIN_REGISTRY:
                available = ", ".join(sorted(BUILTIN_REGISTRY.keys()))
                raise ValueError(
                    f"Unknown builtin reward function '{name}'. Available: {available}"
                )
            func = BUILTIN_REGISTRY[name]
            if params:
                # Convert string params to appropriate types
                typed_params = {}
                for k, v in params.items():
                    try:
                        typed_params[k] = int(v)
                    except (ValueError, TypeError):
                        try:
                            typed_params[k] = float(v)
                        except (ValueError, TypeError):
                            typed_params[k] = v
                func = functools.partial(func, **typed_params)
            reward_funcs.append(func)

        elif spec_type == "inline":
            code = spec.get("code", "")
            if not code.strip():
                raise ValueError(f"Inline reward function '{name}' has no code")
            func = compile_inline_reward(name, code)
            reward_funcs.append(func)

        else:
            raise ValueError(f"Unknown reward function type '{spec_type}'. Use 'builtin' or 'inline'")

    return reward_funcs
