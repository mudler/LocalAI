// Phase 2 (core numeric de-risk): attention over GATHERED paged KV must equal
// an independent host-computed reference.
//
// This answers the central risk in the design: feeding gather-to-scratch KV
// (a sequence whose blocks are non-contiguous in the shared pool) into ggml's
// standard attention ops (mul_mat -> soft_max_ext -> mul_mat) produces correct
// attention. If this holds, the paged read path is numerically sound; the
// remaining work is wiring it into llama-graph.cpp (Gate 0 in a real model).

#include "../paged_kv_manager.h"

#include "ggml.h"
#include "ggml-cpu.h"
#include "ggml-alloc.h"
#include "ggml-backend.h"

#include <cassert>
#include <cstdio>
#include <cmath>
#include <vector>

using namespace paged;

int main() {
    const int d          = 8;     // head dim
    const int n_kv       = 48;    // 3 blocks worth of KV tokens
    const int n_q        = 4;     // query tokens
    const int block_size = 16;
    const int num_blocks = 8;
    const int total_slots = block_size * num_blocks;
    const float scale = 1.0f / std::sqrt((float) d);

    // Non-contiguous physical layout for the KV sequence (blocks [2,1,5]).
    PagedKVManager m(num_blocks, block_size, /*enable_caching=*/false);
    assert(m.allocate(0, 2 * block_size));
    assert(m.allocate(1, 2 * block_size));
    m.free(0);
    assert(m.allocate(2, n_kv));
    std::vector<int> positions(n_kv);
    for (int i = 0; i < n_kv; ++i) positions[i] = i;
    auto slots64 = m.slot_mapping(2, positions);
    std::vector<int32_t> slots32(slots64.begin(), slots64.end());

    // Deterministic K, V, Q in logical [d, n] layout (column-major: col = token).
    std::vector<float> K(d * n_kv), V(d * n_kv), Q(d * n_q);
    for (int t = 0; t < n_kv; ++t)
        for (int e = 0; e < d; ++e) {
            K[t * d + e] = std::sin(0.1f * t + 0.3f * e);
            V[t * d + e] = std::cos(0.2f * t - 0.1f * e);
        }
    for (int q = 0; q < n_q; ++q)
        for (int e = 0; e < d; ++e) Q[q * d + e] = std::sin(0.05f * q + 0.7f * e);

    // ---- Independent host reference attention -------------------------------
    std::vector<float> ref(d * n_q, 0.0f);
    for (int q = 0; q < n_q; ++q) {
        std::vector<float> score(n_kv);
        float mx = -1e30f;
        for (int t = 0; t < n_kv; ++t) {
            float dot = 0.0f;
            for (int e = 0; e < d; ++e) dot += K[t * d + e] * Q[q * d + e];
            score[t] = dot * scale;
            mx = std::fmax(mx, score[t]);
        }
        float sum = 0.0f;
        for (int t = 0; t < n_kv; ++t) { score[t] = std::exp(score[t] - mx); sum += score[t]; }
        for (int t = 0; t < n_kv; ++t) {
            float p = score[t] / sum;
            for (int e = 0; e < d; ++e) ref[q * d + e] += p * V[t * d + e];
        }
    }

    // ---- ggml paged path ----------------------------------------------------
    ggml_backend_t backend = ggml_backend_cpu_init();
    struct ggml_init_params dp = { ggml_tensor_overhead() * 16, NULL, true };
    struct ggml_context * ctx_data = ggml_init(dp);

    struct ggml_tensor * poolK = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, d, total_slots);
    struct ggml_tensor * poolV = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, d, total_slots);
    struct ggml_tensor * kSrc  = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, d, n_kv);
    struct ggml_tensor * vSrc  = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, d, n_kv);
    struct ggml_tensor * qT    = ggml_new_tensor_2d(ctx_data, GGML_TYPE_F32, d, n_q);
    struct ggml_tensor * wIdx  = ggml_new_tensor_1d(ctx_data, GGML_TYPE_I64, n_kv);
    struct ggml_tensor * gIdx  = ggml_new_tensor_1d(ctx_data, GGML_TYPE_I32, n_kv);

    ggml_backend_buffer_t buf = ggml_backend_alloc_ctx_tensors(ctx_data, backend);
    std::vector<float> zeros(d * total_slots, 0.0f);
    ggml_backend_tensor_set(poolK, zeros.data(), 0, ggml_nbytes(poolK));
    ggml_backend_tensor_set(poolV, zeros.data(), 0, ggml_nbytes(poolV));
    ggml_backend_tensor_set(kSrc, K.data(), 0, ggml_nbytes(kSrc));
    ggml_backend_tensor_set(vSrc, V.data(), 0, ggml_nbytes(vSrc));
    ggml_backend_tensor_set(qT,   Q.data(), 0, ggml_nbytes(qT));
    ggml_backend_tensor_set(wIdx, slots64.data(), 0, ggml_nbytes(wIdx));
    ggml_backend_tensor_set(gIdx, slots32.data(), 0, ggml_nbytes(gIdx));

    struct ggml_init_params cp = { ggml_tensor_overhead() * 64 + ggml_graph_overhead(), NULL, true };
    struct ggml_context * ctx = ggml_init(cp);

    struct ggml_tensor * wroteK = ggml_set_rows(ctx, poolK, kSrc, wIdx);
    struct ggml_tensor * wroteV = ggml_set_rows(ctx, poolV, vSrc, wIdx);
    struct ggml_tensor * gK = ggml_get_rows(ctx, wroteK, gIdx);          // [d, n_kv]
    struct ggml_tensor * gV = ggml_get_rows(ctx, wroteV, gIdx);          // [d, n_kv]

    struct ggml_tensor * kq    = ggml_mul_mat(ctx, gK, qT);              // [n_kv, n_q]
    struct ggml_tensor * probs = ggml_soft_max_ext(ctx, kq, NULL, scale, 0.0f);
    struct ggml_tensor * vT    = ggml_cont(ctx, ggml_transpose(ctx, gV)); // [n_kv, d]
    struct ggml_tensor * out   = ggml_mul_mat(ctx, vT, probs);           // [d, n_q]
    ggml_set_output(out);

    struct ggml_cgraph * gf = ggml_new_graph(ctx);
    ggml_build_forward_expand(gf, out);
    ggml_gallocr_t galloc = ggml_gallocr_new(ggml_backend_cpu_buffer_type());
    assert(ggml_gallocr_alloc_graph(galloc, gf));
    assert(ggml_backend_graph_compute(backend, gf) == GGML_STATUS_SUCCESS);

    std::vector<float> got(d * n_q);
    ggml_backend_tensor_get(out, got.data(), 0, ggml_nbytes(out));

    // ---- compare ------------------------------------------------------------
    double max_err = 0.0;
    for (int i = 0; i < d * n_q; ++i) max_err = std::fmax(max_err, std::fabs(got[i] - ref[i]));
    printf("paged attention max abs err vs host reference: %.3e\n", max_err);
    assert(max_err < 1e-4 && "paged-gathered attention must match host reference");

    ggml_gallocr_free(galloc);
    ggml_free(ctx);
    ggml_free(ctx_data);
    ggml_backend_buffer_free(buf);
    ggml_backend_free(backend);

    printf("test_ggml_paged_attn: OK (attention over non-contiguous paged KV matches reference)\n");
    return 0;
}
