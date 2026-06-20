#pragma once

#include "common.cuh"

// W4A16 Marlin-style BF16 GEMM for NVIDIA Blackwell consumer GPUs (sm_120/121).
// Dense (non-MoE) 4-bit-weight matmul run on BF16 tensor cores, the path that
// reaches the GB10 BF16 ceiling where MMQ (int8, Ampere-tuned) and cuBLAS (sm_80
// fallback) both plateau at ~22% of it. Returns true if it handled the op; false
// to fall back to MMQ. Gated behind GGML_CUDA_W4A16 until correct + faster.
bool ggml_cuda_w4a16_mul_mat(
        ggml_backend_cuda_context & ctx,
        const ggml_tensor * src0,   // 4-bit weights (Q4_0/Q4_K)
        const ggml_tensor * src1,   // F32 activations
        ggml_tensor       * dst);   // F32 output
