#include "thread_params.h"

#include <cstdio>

int main() {
    if (llama_grpc::resolve_batch_threads(-1, 4) != 4) {
        std::fprintf(stderr, "default batch threads did not inherit inference threads\n");
        return 1;
    }
    if (llama_grpc::resolve_batch_threads(2, 4) != 2) {
        std::fprintf(stderr, "explicit batch threads were overwritten\n");
        return 1;
    }
    return 0;
}
