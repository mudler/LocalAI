#include "marlin-w4a16.cuh"
#include "mma.cuh"

#include <cstdio>
#include <cstdlib>
#include <cuda_bf16.h>

// W4A16 Marlin-style GEMM.
//
// In-kernel dequantize Q4 weights -> BF16, multiply against BF16-converted F32
// activations using mma.sync m16n8k16 BF16 tensor-core ops, accumulate in F32,
// write F32 output. Handles only the contiguous 2D GEMM (prefill) case for
// Q4_0 / Q4_K; everything else returns false and falls back to MMQ.
//
// ggml MUL_MAT convention: dst[m,n] = sum_k src0[k,m] * src1[k,n].
//   src0 (weights): ne0=K (contiguous), ne1=M  -> row m is K contiguous quants.
//   src1 (acts,f32): ne0=K (contiguous), ne1=N -> row n is K contiguous floats.
//   dst  (f32):      ne0=M (contiguous), ne1=N -> element (m,n) at m + n*M.
// Both operands are row-major [row][k]; m16n8k16 computes C[m,n] += sum_k A[m,k]*B[n,k].
//
// Thread layout: blockDim = (32, WM*WN). threadIdx.x is the warp lane (0..31,
// required by mma.cuh get_i/get_j), threadIdx.y is the warp index.
//
// P3b step 1 - conflict-free shared layout via SKEW PADDING:
//  - WM*WN warps compute a BM(=WM*FM*16) x BN(=WN*FN*8) output tile; each warp
//    owns an FM x FN grid of m16n8k16 mma fragments accumulated in F32.
//  - Per 16-deep k-step the warps cooperatively dequant the BM x 16 Q4 weight
//    strip + load the BN x 16 f32->bf16 activation strip into shared, then feed
//    the tensor cores with ldmatrix.x4 (A) / ldmatrix.x2 (B).
//  - The shared rows are PADDED to SPAD(=12) bf162 instead of the natural 8.
//    ldmatrix's per-lane address is row*stride; with the natural stride 8 (a
//    divisor of the 32-bank / 128-byte cycle) rows 0,4,8,12 collide -> 2-way
//    bank conflict on every fragment load (this is why P3 measured a plain
//    ldmatrix swap as neutral). Skewing the stride to 12 (4-byte aligned, so
//    ldmatrix's 16-byte alignment holds) makes {r*12 mod 32} hit 8 distinct
//    bank-quads for r in 0..7, so both halves of ldmatrix.x4 and ldmatrix.x2 are
//    conflict-free. The pad costs only +50% on the small (~4 KB) staged tile, so
//    unlike a 128-byte-row XOR swizzle it does NOT collapse occupancy on GB10
//    (a wide-row swizzle pushed shared to 16 KB and dropped this to ~2.8 TFLOPS).
//
// Dead-ends already proven (do not re-try): a double-buffered KSTAGE=64 cp.async
// pipeline collapsed occupancy (32 KB shared -> 2.7 TFLOPS); a plain ldmatrix on
// the UNpadded layout was neutral (bank conflicts); a wide-row (BK=64) XOR swizzle
// was conflict-free but occupancy-starved (16 KB shared -> 2.8 TFLOPS). Skew
// padding gets the conflict-free feed at near-zero occupancy cost.

using namespace ggml_cuda_mma;

typedef tile<16, 8, nv_bfloat162> tile_A; // 16(M) x 16(K)
typedef tile< 8, 8, nv_bfloat162> tile_B; //  8(N) x 16(K)
typedef tile<16, 8, float>        tile_C; // 16(M) x  8(N)

// bf162 columns actually live per shared row (16 k-values = 8 bf162) ...
#define W4A16_KP   8
// ... padded to this stride to bank-skew the ldmatrix row addresses.
#define W4A16_SPAD 12

static bool w4a16_enabled() {
    static const bool en = (std::getenv("GGML_CUDA_W4A16") != nullptr);
    return en;
}

// 6-bit packed scale/min decode for Q4_K (mirrors convert.cu get_scale_min_k4).
static __device__ __forceinline__ void w4a16_scale_min_k4(int j, const uint8_t * q, uint8_t & d, uint8_t & m) {
    if (j < 4) {
        d = q[j] & 63; m = q[j + 4] & 63;
    } else {
        d = (q[j+4] & 0xF) | ((q[j-4] >> 6) << 4);
        m = (q[j+4] >>  4) | ((q[j-0] >> 6) << 4);
    }
}

// Dequantize a single Q4_0 weight at column k of a row.
static __device__ __forceinline__ float w4a16_dq_q4_0(const char * row, int k) {
    const block_q4_0 * blk = (const block_q4_0 *) row + (k / QK4_0);
    const int j = k % QK4_0;
    const float d = __half2float(blk->d);
    const int q = (j < QK4_0/2) ? (blk->qs[j] & 0xF) : (blk->qs[j - QK4_0/2] >> 4);
    return (q - 8) * d;
}

// Dequantize a single Q4_K weight at column k of a row.
static __device__ __forceinline__ float w4a16_dq_q4_K(const char * row, int k) {
    const block_q4_K * blk = (const block_q4_K *) row + (k / QK_K);
    const int e = k % QK_K;
    const int il     = e / 64;        // 0..3
    const int within = e % 64;
    const int half   = within / 32;   // 0..1
    const int pos    = within % 32;
    const int ir     = pos / 4;       // 0..7
    const int l      = pos % 4;       // 0..3
    const int is     = 2*il + half;
    const float dall = __low2half (blk->dm);
    const float dmin = __high2half(blk->dm);
    uint8_t sc, mn;
    w4a16_scale_min_k4(is, blk->scales, sc, mn);
    const float d = dall * sc;
    const float m = dmin * mn;
    const uint8_t qb = blk->qs[32*il + 4*ir + l];
    const int q = (half == 0) ? (qb & 0xF) : (qb >> 4);
    return d * q - m;
}

template <bool IS_Q4_K, int WM, int WN, int FM, int FN>
static __global__ void __launch_bounds__(WM*WN*32, 1)
w4a16_gemm_kernel(
        const char * __restrict__ src0,
        const char * __restrict__ src1,
        float      * __restrict__ dst,
        const int M, const int N, const int K,
        const int64_t nb01, const int64_t nb11, const int64_t dst_ne0) {
    constexpr int KP   = W4A16_KP;      // 8 bf162 = 16 k per row
    constexpr int SPAD = W4A16_SPAD;    // padded row stride (bank skew)
    constexpr int BM  = WM*FM*16;
    constexpr int BN  = WN*FN*8;
    constexpr int NTH = WM*WN*32;

    const int m0 = blockIdx.x * BM;
    const int n0 = blockIdx.y * BN;

    const int warp_id = threadIdx.y;        // 0 .. WM*WN-1
    const int warp_n  = warp_id % WN;
    const int warp_m  = warp_id / WN;
    const int tid     = threadIdx.y*32 + threadIdx.x;

    __shared__ nv_bfloat162 sW[BM*SPAD]; // [m][kpair], padded row stride SPAD
    __shared__ nv_bfloat162 sB[BN*SPAD]; // [n][kpair], padded row stride SPAD

    tile_C C[FM][FN]; // zero-initialized accumulators

    for (int k0 = 0; k0 < K; k0 += 16) {
        // Dequantize the BM x 16 weight strip once; reused across the block's BN span.
        #pragma unroll
        for (int idx = tid; idx < BM*KP; idx += NTH) {
            const int m  = idx / KP;
            const int kk = idx % KP;
            const int k  = k0 + 2*kk;
            float w0 = 0.0f, w1 = 0.0f;
            if (m0 + m < M) {
                const char * row = src0 + (int64_t)(m0 + m) * nb01;
                if (IS_Q4_K) { w0 = w4a16_dq_q4_K(row, k); w1 = w4a16_dq_q4_K(row, k + 1); }
                else         { w0 = w4a16_dq_q4_0(row, k); w1 = w4a16_dq_q4_0(row, k + 1); }
            }
            sW[m*SPAD + kk] = __floats2bfloat162_rn(w0, w1);
        }
        // Load the BN x 16 activation strip (f32 -> bf16).
        #pragma unroll
        for (int idx = tid; idx < BN*KP; idx += NTH) {
            const int n  = idx / KP;
            const int kk = idx % KP;
            const int k  = k0 + 2*kk;
            float a0 = 0.0f, a1 = 0.0f;
            if (n0 + n < N) {
                const float * arow = (const float *)(src1 + (int64_t)(n0 + n) * nb11);
                a0 = arow[k]; a1 = arow[k + 1];
            }
            sB[n*SPAD + kk] = __floats2bfloat162_rn(a0, a1);
        }
        __syncthreads();

        tile_A Af[FM];
        tile_B Bf[FN];
        #pragma unroll
        for (int fm = 0; fm < FM; ++fm) {
            const int mrow = (warp_m*FM + fm) * 16;
            load_ldmatrix(Af[fm], sW + mrow*SPAD, SPAD);
        }
        #pragma unroll
        for (int fn = 0; fn < FN; ++fn) {
            const int ncol = (warp_n*FN + fn) * 8;
            load_ldmatrix(Bf[fn], sB + ncol*SPAD, SPAD);
        }
        #pragma unroll
        for (int fm = 0; fm < FM; ++fm) {
            #pragma unroll
            for (int fn = 0; fn < FN; ++fn) {
                mma(C[fm][fn], Af[fm], Bf[fn]);
            }
        }
        __syncthreads();
    }

    #pragma unroll
    for (int fm = 0; fm < FM; ++fm) {
        #pragma unroll
        for (int fn = 0; fn < FN; ++fn) {
            const int mbase = m0 + (warp_m*FM + fm) * 16;
            const int nbase = n0 + (warp_n*FN + fn) * 8;
            #pragma unroll
            for (int l = 0; l < tile_C::ne; ++l) {
                const int m = mbase + tile_C::get_i(l);
                const int n = nbase + tile_C::get_j(l);
                if (m < M && n < N) {
                    dst[(int64_t)n * dst_ne0 + m] = C[fm][fn].x[l];
                }
            }
        }
    }
}

bool ggml_cuda_w4a16_mul_mat(
        ggml_backend_cuda_context & ctx,
        const ggml_tensor * src0,
        const ggml_tensor * src1,
        ggml_tensor       * dst) {
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

    if (src0->ne[2] != 1 || src0->ne[3] != 1 ||
        src1->ne[2] != 1 || src1->ne[3] != 1 ||
        dst->ne[2]  != 1 || dst->ne[3]  != 1) {
        return false;
    }
    if (!ggml_is_contiguous(src0) || !ggml_is_contiguous(src1) || !ggml_is_contiguous(dst)) {
        return false;
    }

    const int64_t K = src0->ne[0];
    const int64_t M = src0->ne[1];
    const int64_t N = src1->ne[1];
    if (src1->ne[0] != K || dst->ne[0] != M || dst->ne[1] != N) {
        return false;
    }
    if (K % 16 != 0) {
        return false;
    }

    cudaStream_t stream = ctx.stream();

    // Block tile config: WM*WN warps compute BM(=WM*FM*16) x BN(=WN*FN*8).
    constexpr int WM = 4, WN = 2, FM = 2, FN = 4; // BM=128, BN=64, 8 warps
    constexpr int BM = WM*FM*16;
    constexpr int BN = WN*FN*8;
    const dim3 grid((unsigned)((M + BM - 1) / BM), (unsigned)((N + BN - 1) / BN), 1);
    const dim3 block(32, WM*WN, 1);

    if (src0->type == GGML_TYPE_Q4_K) {
        w4a16_gemm_kernel<true, WM, WN, FM, FN><<<grid, block, 0, stream>>>(
            (const char *) src0->data, (const char *) src1->data, (float *) dst->data,
            (int) M, (int) N, (int) K, src0->nb[1], src1->nb[1], dst->ne[0]);
    } else {
        w4a16_gemm_kernel<false, WM, WN, FM, FN><<<grid, block, 0, stream>>>(
            (const char *) src0->data, (const char *) src1->data, (float *) dst->data,
            (int) M, (int) N, (int) K, src0->nb[1], src1->nb[1], dst->ne[0]);
    }
    return true;
}
