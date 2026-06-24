#include "gomnivoicecpp.h"
#include "ggml-backend.h"
#include "omnivoice.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>

static ov_context *g_ctx = nullptr;

static void ggml_log_cb(enum ggml_log_level level, const char *log,
                        void * /*data*/) {
    if (!log)
        return;
    const char *lvl = "?????";
    switch (level) {
    case GGML_LOG_LEVEL_DEBUG: lvl = "DEBUG"; break;
    case GGML_LOG_LEVEL_INFO:  lvl = "INFO";  break;
    case GGML_LOG_LEVEL_WARN:  lvl = "WARN";  break;
    case GGML_LOG_LEVEL_ERROR: lvl = "ERROR"; break;
    default: break;
    }
    fprintf(stderr, "[%-5s] %s", lvl, log);
    fflush(stderr);
}

int omni_load(const char *model_path, const char *codec_path, int use_fa,
              int clamp_fp16) {
    ggml_log_set(ggml_log_cb, nullptr);
    ggml_backend_load_all();

    if (!model_path || model_path[0] == '\0') {
        fprintf(stderr, "[omnivoice-cpp] ERROR: model_path is required\n");
        return 1;
    }
    if (!codec_path || codec_path[0] == '\0') {
        fprintf(stderr, "[omnivoice-cpp] ERROR: codec_path is required\n");
        return 2;
    }

    ov_init_params p;
    ov_init_default_params(&p);
    p.model_path = model_path;
    p.codec_path = codec_path;
    p.use_fa = use_fa != 0;
    p.clamp_fp16 = clamp_fp16 != 0;

    fprintf(stderr, "[omnivoice-cpp] Loading model=%s codec=%s\n", model_path,
            codec_path);

    g_ctx = ov_init(&p);
    if (!g_ctx) {
        fprintf(stderr, "[omnivoice-cpp] FATAL: ov_init failed: %s\n",
                ov_last_error());
        return 3;
    }
    fprintf(stderr, "[omnivoice-cpp] Model loaded (%s)\n", ov_version());
    return 0;
}

// Fill an ov_tts_params from the flat wrapper arguments.
static void fill_params(ov_tts_params *tp, const char *text, const char *lang,
                        const char *instruct, const float *ref_samples,
                        int ref_n, const char *ref_text, long long seed,
                        int denoise) {
    ov_tts_default_params(tp);
    tp->text = text ? text : "";
    tp->lang = lang ? lang : "";
    if (instruct && instruct[0] != '\0')
        tp->instruct = instruct;
    if (ref_samples && ref_n > 0) {
        tp->ref_audio_24k = ref_samples;
        tp->ref_n_samples = ref_n;
        if (ref_text && ref_text[0] != '\0')
            tp->ref_text = ref_text;
        tp->denoise = denoise != 0;
    }
    if (seed >= 0)
        tp->mg_seed = (uint64_t)seed;
}

float *omni_tts(const char *text, const char *lang, const char *instruct,
                const float *ref_samples, int ref_n, const char *ref_text,
                long long seed, int denoise, int *out_n) {
    if (out_n)
        *out_n = 0;
    if (!g_ctx) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: model not loaded\n");
        return nullptr;
    }
    if (!text || text[0] == '\0') {
        fprintf(stderr, "[omnivoice-cpp] ERROR: text is required\n");
        return nullptr; // omni_tts: out_n already 0
    }
    ov_tts_params tp;
    fill_params(&tp, text, lang, instruct, ref_samples, ref_n, ref_text, seed,
                denoise);

    ov_audio out = {0};
    enum ov_status rc = ov_synthesize(g_ctx, &tp, &out);
    if (rc != OV_STATUS_OK || out.n_samples <= 0 || !out.samples) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: synthesize failed (rc=%d): %s\n",
                (int)rc, ov_last_error());
        ov_audio_free(&out);
        return nullptr;
    }

    // Copy into a plain malloc buffer the Go side can free symmetrically via
    // omni_pcm_free; then release the ov_audio-owned buffer.
    size_t bytes = (size_t)out.n_samples * sizeof(float);
    float *buf = (float *)malloc(bytes);
    if (!buf) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: malloc(%zu) failed\n", bytes);
        ov_audio_free(&out);
        return nullptr;
    }
    memcpy(buf, out.samples, bytes);
    if (out_n)
        *out_n = out.n_samples;
    ov_audio_free(&out);
    return buf;
}

int omni_tts_stream(const char *text, const char *lang, const char *instruct,
                    const float *ref_samples, int ref_n, const char *ref_text,
                    long long seed, int denoise, omni_pcm_chunk_cb cb,
                    void *user_data) {
    if (!g_ctx) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: model not loaded\n");
        return 1;
    }
    if (!cb) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: stream callback is null\n");
        return 2;
    }
    if (!text || text[0] == '\0') {
        fprintf(stderr, "[omnivoice-cpp] ERROR: text is required\n");
        return 4;
    }
    ov_tts_params tp;
    fill_params(&tp, text, lang, instruct, ref_samples, ref_n, ref_text, seed,
                denoise);
    // ov_audio_chunk_cb has the identical signature to omni_pcm_chunk_cb
    // (bool vs int return are ABI-compatible; non-zero == true).
    tp.on_chunk = (ov_audio_chunk_cb)cb;
    tp.on_chunk_user_data = user_data;

    ov_audio out = {0}; // stays empty in streaming mode
    enum ov_status rc = ov_synthesize(g_ctx, &tp, &out);
    ov_audio_free(&out);
    if (rc != OV_STATUS_OK && rc != OV_STATUS_CANCELLED) {
        fprintf(stderr, "[omnivoice-cpp] ERROR: stream synth failed (rc=%d): %s\n",
                (int)rc, ov_last_error());
        return 3;
    }
    return 0;
}

void omni_pcm_free(float *p) { free(p); }

void omni_unload(void) {
    if (g_ctx) {
        ov_free(g_ctx);
        g_ctx = nullptr;
    }
}
