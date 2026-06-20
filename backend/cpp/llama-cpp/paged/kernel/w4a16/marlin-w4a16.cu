#include "marlin-w4a16.cuh"

#include <cstdio>
#include <cstdlib>

// P1: dispatch seam only. The BF16 Marlin kernel (dequant Q4->BF16 in shared mem,
// mma.sync m16n8k16, cp.async double-buffered pipeline, offline weight reshuffle)
// lands in P2/P3. For now this always falls back to MMQ, so the default build is
// byte-identical and the test-backend-ops MUL_MAT gate stays 1103/1103.

static bool w4a16_enabled() {
    static const bool en = (std::getenv("GGML_CUDA_W4A16") != nullptr);
    return en;
}

bool ggml_cuda_w4a16_mul_mat(
        ggml_backend_cuda_context & ctx,
        const ggml_tensor * src0,
        const ggml_tensor * src1,
        ggml_tensor       * dst) {
    GGML_UNUSED(ctx);

    if (!w4a16_enabled()) {
        return false;
    }
    if (src0->type != GGML_TYPE_Q4_0 && src0->type != GGML_TYPE_Q4_K) {
        return false;
    }
    if (src1->type != GGML_TYPE_F32 || dst->type != GGML_TYPE_F32) {
        return false;
    }
    const int cc = ggml_cuda_info().devices[ggml_cuda_get_device()].cc;
    if (!GGML_CUDA_CC_IS_NVIDIA(cc) || cc < GGML_CUDA_CC_BLACKWELL) {
        return false; // consumer Blackwell (sm_120/121) only
    }

    // TODO(P2/P3): launch the W4A16 BF16 Marlin kernel here; verify parity vs MMQ
    // (test-backend-ops) before returning true.
    static bool warned = false;
    if (!warned) {
        warned = true;
        fprintf(stderr, "[w4a16] GGML_CUDA_W4A16 set, kernel not yet implemented (P1 seam) - using MMQ\n");
    }
    return false;
}
