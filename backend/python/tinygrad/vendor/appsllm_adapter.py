"""Glue code between LocalAI's HF-shaped model assets and tinygrad.apps.llm.

apps.llm's `Transformer` uses GGUF-native weight names and consumes a
`TransformerConfig` dataclass. LocalAI resolves models from HuggingFace
snapshots (HF safetensors + config.json) so we translate both sides here.

This module does NOT subclass anything from apps.llm. With the Qwen3+
scope the backend targets, we can use `apps.llm.Transformer` unchanged
(no qkv_bias, no RoPE permute). Everything below is a thin adapter.
"""
from __future__ import annotations

from typing import Any


def _hf_to_appsllm_state_dict(hf_weights: dict[str, Any], n_layers: int) -> dict[str, Any]:
    """Rename a HuggingFace-style state dict to the GGUF-native keys that
    `tinygrad.apps.llm.Transformer` expects.

    HF and apps.llm both store RoPE weights in half-split layout, so no
    permute is required — only a direct key rename and a tied-embedding
    fallback for models like Llama 3.2 that drop `lm_head.weight`.
    """
    keymap: dict[str, str] = {
        "model.embed_tokens.weight": "token_embd.weight",
        "model.norm.weight": "output_norm.weight",
        "lm_head.weight": "output.weight",
    }
    for layer in range(n_layers):
        keymap[f"model.layers.{layer}.input_layernorm.weight"] = f"blk.{layer}.attn_norm.weight"
        keymap[f"model.layers.{layer}.post_attention_layernorm.weight"] = f"blk.{layer}.ffn_norm.weight"
        for hf_proj, gguf_proj in (("q", "q"), ("k", "k"), ("v", "v"), ("o", "output")):
            keymap[f"model.layers.{layer}.self_attn.{hf_proj}_proj.weight"] = f"blk.{layer}.attn_{gguf_proj}.weight"
        keymap[f"model.layers.{layer}.self_attn.q_norm.weight"] = f"blk.{layer}.attn_q_norm.weight"
        keymap[f"model.layers.{layer}.self_attn.k_norm.weight"] = f"blk.{layer}.attn_k_norm.weight"
        for hf_name, gguf_name in (("gate", "gate"), ("up", "up"), ("down", "down")):
            keymap[f"model.layers.{layer}.mlp.{hf_name}_proj.weight"] = f"blk.{layer}.ffn_{gguf_name}.weight"

    # Fail loudly if the model carries Q/K/V projection bias (Qwen2 / 2.5).
    # apps.llm's `TransformerBlock` hardcodes `bias=False`, so these weights
    # would be silently dropped by `load_state_dict(strict=False)` and the
    # model would produce garbage. Supported families (Qwen3, Qwen3.5,
    # Llama 3.x, GLM-4, Mistral) have no qkv bias.
    bias_keys = [k for k in hf_weights
                 if k.startswith("model.layers.") and
                 any(k.endswith(f".self_attn.{p}_proj.bias") for p in ("q", "k", "v"))]
    if bias_keys:
        raise ValueError(
            "tinygrad backend: model has Q/K/V projection bias ("
            f"{bias_keys[0]} etc). Supported families are Qwen3, Qwen3.5, "
            "Llama 3.x, GLM-4, Mistral. For Qwen2 / 2.5 please use a "
            "newer model or the vLLM / llama.cpp backends."
        )

    sd = {dst: hf_weights[src] for src, dst in keymap.items() if src in hf_weights}
    if "output.weight" not in sd and "token_embd.weight" in sd:
        sd["output.weight"] = sd["token_embd.weight"]
    return sd


def _hf_to_transformer_kwargs(hf_config: dict, state_dict: dict[str, Any], max_context: int) -> dict:
    """Build the kwargs dict for `tinygrad.apps.llm.Transformer(**kwargs)`.

    Supports dense Qwen3 / Qwen3.5 / Llama 3.x / GLM-4 / Mistral-shaped
    models. The tinygrad 0.12.0 `Transformer` takes keyword-only args (no
    `TransformerConfig` dataclass) — so we return a plain dict.
    """
    n_heads = hf_config["num_attention_heads"]
    head_dim = hf_config.get("head_dim") or (hf_config["hidden_size"] // n_heads)

    # Detect qk_norm presence from the GGUF-shaped state dict (matches
    # apps.llm's own heuristic in `from_gguf`).
    qk_norm = 0
    qn = state_dict.get("blk.0.attn_q_norm.weight")
    if qn is not None:
        qk_norm = int(qn.shape[0])

    max_pos = hf_config.get("max_position_embeddings", 4096)

    return dict(
        num_blocks=hf_config["num_hidden_layers"],
        dim=hf_config["hidden_size"],
        hidden_dim=hf_config["intermediate_size"],
        n_heads=n_heads,
        n_kv_heads=hf_config.get("num_key_value_heads", n_heads),
        norm_eps=hf_config.get("rms_norm_eps", 1e-5),
        vocab_size=hf_config["vocab_size"],
        head_dim=head_dim,
        rope_theta=float(hf_config.get("rope_theta", 10000.0)),
        max_context=min(max_pos, max_context),
        qk_norm=qk_norm,
    )


def _embed_hidden(model, tokens):
    """Return mean-poolable hidden states by running the block stack
    without going through the LM head + Gumbel-max sampler baked into
    `Transformer.forward`."""
    x = model.token_embd(tokens).float()
    for blk in model.blk:
        x = blk(x, 0)
    return model.output_norm(x)
