#pragma once

#include <cstdint>

namespace llama_grpc {

inline int32_t resolve_batch_threads(int32_t batch_threads, int32_t inference_threads) {
    return batch_threads < 0 ? inference_threads : batch_threads;
}

} // namespace llama_grpc
