# SPDX-License-Identifier: MIT


def single_file_load_kwargs(original_config_file: str, from_single_file: bool) -> dict:
    if from_single_file and original_config_file:
        return {"original_config_file": original_config_file}
    return {}
