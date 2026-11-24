# SPDX-License-Identifier: MIT


def single_file_load_kwargs(original_config_file: str, from_single_file: bool) -> dict:
    if from_single_file and original_config_file:
        return {"original_config_file": original_config_file}
    return {}


def drop_negative_prompt_if_embedded(kwargs: dict) -> dict:
    """Remove negative_prompt from pipeline kwargs when negative_prompt_embeds is set.

    Diffusers rejects both being passed together. Pipelines without negative
    embeddings keep the user's negative prompt.
    """
    if "negative_prompt_embeds" in kwargs:
        kwargs.pop("negative_prompt", None)
    return kwargs
