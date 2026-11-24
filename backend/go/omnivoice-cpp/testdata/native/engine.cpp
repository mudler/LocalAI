// SPDX-License-Identifier: MIT
#include "omnivoice.h"
#include "ggml-backend.h"
#include <cstdlib>
#include <cstring>
#include <string>

struct ov_context {};
static ov_context context;
static ov_tts_params captured;
static std::string text, lang, instruct, ref_text;
static int calls, frees, defaults;
static float ref_first;
void ggml_log_set(void (*)(ggml_log_level, const char *, void *), void *) {}
void ggml_backend_load_all() {}

extern "C" {
const char *ov_version() { return "model-free fixture"; }
const char *ov_last_error() { return "fixture error"; }
void ov_init_default_params(ov_init_params *p) {
    *p = {};
    p->abi_version = OV_ABI_VERSION;
    p->use_fa = true;
}
ov_context *ov_init(const ov_init_params *) { return &context; }
void ov_free(ov_context *) {}
void ov_tts_default_params(ov_tts_params *p) {
    ++defaults;
    *p = {};
    p->abi_version = OV_ABI_VERSION;
    p->chunk_duration_sec = 15;
    p->chunk_threshold_sec = 30;
    p->denoise = p->preprocess_prompt = p->postproc = true;
    p->mg_num_step = 32;
    p->mg_guidance_scale = 2;
    p->mg_t_shift = 0.1f;
    p->mg_layer_penalty_factor = p->mg_position_temperature = 5;
    p->mg_seed = 42;
}
ov_status ov_synthesize(ov_context *, const ov_tts_params *p, ov_audio *out) {
    ++calls;
    captured = *p;
    text = p->text ? p->text : "";
    lang = p->lang ? p->lang : "";
    instruct = p->instruct ? p->instruct : "";
    ref_text = p->ref_text ? p->ref_text : "";
    ref_first = p->ref_n_samples ? p->ref_audio_24k[0] : 0;
    float pcm[] = {0.25f, -0.25f};
    if (p->on_chunk) {
        return p->on_chunk(pcm, 2, p->on_chunk_user_data) ? OV_STATUS_OK : OV_STATUS_CANCELLED;
    }
    out->samples = static_cast<float *>(malloc(sizeof(pcm)));
    memcpy(out->samples, pcm, sizeof(pcm));
    out->n_samples = 2;
    out->sample_rate = 24000;
    return OV_STATUS_OK;
}
void ov_audio_free(ov_audio *out) {
    ++frees;
    free(out->samples);
    *out = {};
}
void fixture_reset() {
    captured = {};
    calls = frees = defaults = 0;
    text.clear(); lang.clear(); instruct.clear(); ref_text.clear();
    ref_first = 0;
}
int fixture_calls() { return calls; }
int fixture_frees() { return frees; }
int fixture_defaults() { return defaults; }
int fixture_frames() { return captured.T_override; }
int fixture_postproc() { return captured.postproc; }
int fixture_denoise() { return captured.denoise; }
int fixture_ref_n() { return captured.ref_n_samples; }
float fixture_ref_first() { return ref_first; }
uint64_t fixture_seed() { return captured.mg_seed; }
const char *fixture_text() { return text.c_str(); }
const char *fixture_lang() { return lang.c_str(); }
const char *fixture_instruct() { return instruct.c_str(); }
const char *fixture_ref_text() { return ref_text.c_str(); }
int fixture_other_defaults() {
    return captured.abi_version == OV_ABI_VERSION &&
        captured.chunk_duration_sec == 15 && captured.chunk_threshold_sec == 30 &&
        captured.preprocess_prompt && captured.mg_num_step == 32 &&
        captured.mg_guidance_scale == 2 && captured.mg_t_shift == 0.1f &&
        captured.mg_layer_penalty_factor == 5 && captured.mg_position_temperature == 5 &&
        captured.mg_class_temperature == 0 && !captured.cancel && !captured.dump_dir;
}
}
