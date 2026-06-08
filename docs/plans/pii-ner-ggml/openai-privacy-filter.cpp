// DRAFT / SKELETON — not wired into the build yet.
//
// Target: llama.cpp `src/models/openai-privacy-filter.cpp` (+ class decl in src/models/models.h,
// + add_subdirectory/source entry in src/CMakeLists.txt, + arch in src/llama-arch.{h,cpp},
// + loader in src/llama-model.cpp). See INTEGRATION.md for the full touch-point list.
//
// This is `src/models/openai-moe.cpp` (gpt-oss) with exactly three changes, each marked
// "CHANGE vs gpt-oss". Everything else is copied so the shared, hardened kernels
// (build_qkv, ggml_rope_ext/YaRN, build_attn + attn_sinks, build_moe_ffn SWIGLU_OAI_MOE) are
// reused verbatim. Verified against openai-moe.cpp @ commit 22d66b56.
//
// Reuse note: the symmetric banded NON-CAUSAL mask is NOT new code — it falls out of the
// no-cache attention path (build_attn_inp_no_cache -> fill_mask) once load_hparams sets
//   hparams.causal_attn = false; hparams.swa_type = LLAMA_SWA_TYPE_SYMMETRIC; hparams.n_swa = 256;
// (SYMMETRIC masks |p1-p0| > n_swa/2, so n_swa = 2*sliding_window = 256 gives |q-kv| <= 128.)

#include "models.h"

void llama_model_openai_privacy_filter::load_arch_hparams(llama_model_loader & ml) {
    ml.get_key(LLM_KV_ATTENTION_LAYERNORM_RMS_EPS, hparams.f_norm_rms_eps);
    ml.get_key(LLM_KV_EXPERT_FEED_FORWARD_LENGTH,  hparams.n_ff_exp);

    uint32_t sliding_window = 0;
    ml.get_key(LLM_KV_ATTENTION_SLIDING_WINDOW, sliding_window);   // = 128 (HALF-window)

    // CHANGE vs gpt-oss: bidirectional, symmetric band, no causal/global alternation.
    hparams.causal_attn = false;                       // encoder; whole seq in one ubatch
    hparams.swa_type    = LLAMA_SWA_TYPE_SYMMETRIC;    // |p1-p0| > n_swa/2 -> masked
    hparams.n_swa       = 2 * sliding_window;          // 256 -> half-window 128 == HF band
    // NB(verify): SYMMETRIC half = n_swa/2. The ×2 here is the most likely off-by-one source —
    // assert against an HF reference attention map on a >257-token input (see INTEGRATION.md §verify).

    // n_cls_out / pooling_type / classifier labels are read by the generic loader from GGUF
    // (already present for the reranker; TOKEN_CLS pooling value comes from PR #19725).

    type = LLM_TYPE_UNKNOWN;  // ~1.5B, single published size; n_layer == 8
}

void llama_model_openai_privacy_filter::load_arch_tensors(llama_model_loader &) {
    LLAMA_LOAD_LOCALS;
    const int64_t n_ff_exp = hparams.n_ff_exp;

    tok_embd    = create_tensor(tn(LLM_TENSOR_TOKEN_EMBD,  "weight"), {n_embd, n_vocab}, 0);
    output_norm = create_tensor(tn(LLM_TENSOR_OUTPUT_NORM, "weight"), {n_embd}, 0);

    // CHANGE vs gpt-oss: NO lm_head (`output`). Instead the token-classification head.
    // cls_out/cls_out_b are model-level (like bert.cpp), loaded into model.cls_out / cls_out_b.
    cls_out   = create_tensor(tn(LLM_TENSOR_CLS_OUT, "weight"), {n_embd, hparams.n_cls_out}, 0);
    cls_out_b = create_tensor(tn(LLM_TENSOR_CLS_OUT, "bias"),   {hparams.n_cls_out},         0);

    for (int i = 0; i < n_layer; ++i) {
        auto & layer = layers[i];

        layer.attn_norm      = create_tensor(tn(LLM_TENSOR_ATTN_NORM,      "weight", i), {n_embd}, 0);
        layer.attn_post_norm = create_tensor(tn(LLM_TENSOR_ATTN_POST_NORM, "weight", i), {n_embd}, 0);

        create_tensor_qkv(layer, i, n_embd, n_head * n_rot, n_head_kv * n_rot, n_head_kv * n_rot, 0);
        layer.wo   = create_tensor(tn(LLM_TENSOR_ATTN_OUT,  "weight", i), {n_head * n_rot, n_embd}, 0);
        layer.wo_b = create_tensor(tn(LLM_TENSOR_ATTN_OUT,  "bias",   i), {n_embd}, 0);

        layer.attn_sinks = create_tensor(tn(LLM_TENSOR_ATTN_SINKS, "weight", i), {n_head}, 0);

        layer.ffn_gate_inp  = create_tensor(tn(LLM_TENSOR_FFN_GATE_INP,  "weight", i), {n_embd, n_expert}, 0);
        layer.ffn_gate_exps = create_tensor(tn(LLM_TENSOR_FFN_GATE_EXPS, "weight", i), {n_embd, n_ff_exp, n_expert}, 0);
        layer.ffn_down_exps = create_tensor(tn(LLM_TENSOR_FFN_DOWN_EXPS, "weight", i), {n_ff_exp, n_embd, n_expert}, 0);
        layer.ffn_up_exps   = create_tensor(tn(LLM_TENSOR_FFN_UP_EXPS,   "weight", i), {n_embd, n_ff_exp, n_expert}, 0);

        layer.ffn_gate_inp_b  = create_tensor(tn(LLM_TENSOR_FFN_GATE_INP,  "bias", i), {n_expert}, 0);
        layer.ffn_gate_exps_b = create_tensor(tn(LLM_TENSOR_FFN_GATE_EXPS, "bias", i), {n_ff_exp, n_expert}, 0);
        layer.ffn_down_exps_b = create_tensor(tn(LLM_TENSOR_FFN_DOWN_EXPS, "bias", i), {n_embd, n_expert}, 0);
        layer.ffn_up_exps_b   = create_tensor(tn(LLM_TENSOR_FFN_UP_EXPS,   "bias", i), {n_ff_exp, n_expert}, 0);
    }
}

std::unique_ptr<llm_graph_context> llama_model_openai_privacy_filter::build_arch_graph(const llm_graph_params & params) const {
    return std::make_unique<graph>(*this, params);
}

llama_model_openai_privacy_filter::graph::graph(const llama_model & model, const llm_graph_params & params) : llm_graph_context(params) {
    ggml_tensor * cur;
    ggml_tensor * inpL;

    inpL = build_inp_embd(model.tok_embd);
    ggml_tensor * inp_pos = build_inp_pos();

    // CHANGE vs gpt-oss (1/2): no-cache, non-causal attention input. The symmetric band is
    // applied by fill_mask from hparams.{swa_type,n_swa}. (gpt-oss used build_attn_inp_kv_iswa.)
    auto * inp_attn = build_attn_inp_no_cache();

    for (int il = 0; il < n_layer; ++il) {
        const float freq_base_l  = model.get_rope_freq_base (cparams, il);
        const float freq_scale_l = model.get_rope_freq_scale(cparams, il);

        ggml_tensor * inpSA = inpL;

        cur = build_norm(inpL, model.layers[il].attn_norm, nullptr, LLM_NORM_RMS, il);
        cb(cur, "attn_norm", il);

        // self-attention (identical to gpt-oss: RoPE/YaRN + sinks + 1/sqrt(d) kq_scale)
        {
            auto [Qcur, Kcur, Vcur] = build_qkv(model.layers[il], cur, n_rot, n_head, n_head_kv, il);

            Qcur = ggml_rope_ext(ctx0, Qcur, inp_pos, nullptr, n_rot, rope_type, n_ctx_orig,
                                 freq_base_l, freq_scale_l, ext_factor, attn_factor, beta_fast, beta_slow);
            Kcur = ggml_rope_ext(ctx0, Kcur, inp_pos, nullptr, n_rot, rope_type, n_ctx_orig,
                                 freq_base_l, freq_scale_l, ext_factor, attn_factor, beta_fast, beta_slow);
            cb(Qcur, "Qcur", il); cb(Kcur, "Kcur", il); cb(Vcur, "Vcur", il);

            // kq_scale 1/sqrt(n_rot) == HF's head_dim**-0.25 applied to q and k (verify in fp16).
            cur = build_attn(inp_attn, model.layers[il].wo, model.layers[il].wo_b, model.layers[il].wo_s,
                             Qcur, Kcur, Vcur, nullptr, model.layers[il].attn_sinks, nullptr,
                             1.0f/sqrtf(float(n_rot)), il);
            cb(cur, "attn_out", il);
        }
        // NOTE: gpt-oss does build_inp_out_ids() + ggml_get_rows on the last layer to drop
        // unused tokens. For token classification we need ALL token outputs, so we do NOT
        // prune here (n_outputs == n_tokens under token-level pooling).

        ggml_tensor * ffn_inp = ggml_add(ctx0, cur, inpSA);
        cb(ffn_inp, "ffn_inp", il);

        cur = build_norm(ffn_inp, model.layers[il].attn_post_norm, nullptr, LLM_NORM_RMS, il);
        cb(cur, "attn_post_norm", il);

        cur = build_moe_ffn(cur,
                model.layers[il].ffn_gate_inp,  model.layers[il].ffn_gate_inp_b,
                model.layers[il].ffn_up_exps,   model.layers[il].ffn_up_exps_b,
                model.layers[il].ffn_gate_exps, model.layers[il].ffn_gate_exps_b,
                model.layers[il].ffn_down_exps, model.layers[il].ffn_down_exps_b,
                nullptr, n_expert, n_expert_used,
                LLM_FFN_SWIGLU_OAI_MOE, false, hparams.expert_weights_scale,
                LLAMA_EXPERT_GATING_FUNC_TYPE_SOFTMAX_WEIGHT, il);
        cb(cur, "ffn_moe_out", il);

        cur = ggml_add(ctx0, cur, ffn_inp);
        cur = build_cvec(cur, il);
        cb(cur, "l_out", il);
        inpL = cur;
    }

    cur = build_norm(inpL, model.output_norm, NULL, LLM_NORM_RMS, -1);
    cb(cur, "result_norm", -1);
    res->t_embd = cur;

    // CHANGE vs gpt-oss (2/2): no lm_head. build_pooling applies cls_out per token under
    // LLAMA_POOLING_TYPE_TOKEN_CLS (PR #19725) -> result is [n_cls_out, n_tokens].
    // Mirrors the encoder graphs (bert.cpp ends at res->t_embd and lets the framework pool).
    ggml_build_forward_expand(gf, res->t_embd);
}
