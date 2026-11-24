#include "parallel_params.h"

#include <cstdio>

int main() {
    // parallel:1 in the model options must not be replaced by the environment.
    if (llama_grpc::resolve_n_parallel(1, "4") != 1) {
        std::fprintf(stderr, "explicit parallel:1 was overwritten by LLAMACPP_PARALLEL\n");
        return 1;
    }
    if (llama_grpc::resolve_n_parallel(8, "4") != 8) {
        std::fprintf(stderr, "explicit parallel was overwritten by LLAMACPP_PARALLEL\n");
        return 1;
    }
    // Without an option the environment applies, otherwise the default.
    if (llama_grpc::resolve_n_parallel(std::nullopt, "4") != 4) {
        std::fprintf(stderr, "LLAMACPP_PARALLEL was not used as fallback\n");
        return 1;
    }
    if (llama_grpc::resolve_n_parallel(std::nullopt, nullptr) != 1) {
        std::fprintf(stderr, "default slot count is not 1\n");
        return 1;
    }
    if (llama_grpc::resolve_n_parallel(std::nullopt, "many") != 1) {
        std::fprintf(stderr, "unparsable LLAMACPP_PARALLEL was not ignored\n");
        return 1;
    }
    return 0;
}
