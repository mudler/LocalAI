// SPDX-License-Identifier: MIT

#pragma once

namespace llama_grpc {

struct passthrough_gpu_layers_state {
    int main;
    int draft;
};

inline passthrough_gpu_layers_state prepare_passthrough_gpu_layers(
    int & main_gpu_layers,
    int & draft_gpu_layers) {
    const passthrough_gpu_layers_state saved{
        main_gpu_layers,
        draft_gpu_layers,
    };
    main_gpu_layers = -1;
    draft_gpu_layers = -1;
    return saved;
}

inline void restore_passthrough_gpu_layers(
    int & main_gpu_layers,
    int & draft_gpu_layers,
    passthrough_gpu_layers_state saved,
    bool main_overridden = false,
    bool draft_overridden = false) {
    if (!main_overridden && main_gpu_layers == -1) {
        main_gpu_layers = saved.main;
    }
    if (!draft_overridden && draft_gpu_layers == -1) {
        draft_gpu_layers = saved.draft;
    }
}

} // namespace llama_grpc
