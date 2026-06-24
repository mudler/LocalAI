#include "goqwen3ttscpp.h"
#include "ggml-backend.h"
#include "qwen.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>

static qt_context *g_ctx = nullptr;

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

int qt3_load(const char *talker_path, const char *codec_path, int use_fa,
             int clamp_fp16) {
    ggml_log_set(ggml_log_cb, nullptr);
    ggml_backend_load_all();

    if (!talker_path || talker_path[0] == '\0') {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: talker_path is required\n");
        return 1;
    }
    if (!codec_path || codec_path[0] == '\0') {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: codec_path is required\n");
        return 2;
    }

    qt_init_params p;
    qt_init_default_params(&p);
    p.talker_path = talker_path;
    p.codec_path = codec_path;
    p.use_fa = use_fa != 0;
    p.clamp_fp16 = clamp_fp16 != 0;

    fprintf(stderr, "[qwen3-tts-cpp] Loading talker=%s codec=%s\n", talker_path,
            codec_path);

    g_ctx = qt_init(&p);
    if (!g_ctx) {
        fprintf(stderr, "[qwen3-tts-cpp] FATAL: qt_init failed: %s\n",
                qt_last_error());
        return 3;
    }
    fprintf(stderr, "[qwen3-tts-cpp] Model loaded (%s)\n", qt_version());
    return 0;
}

// Fill a qt_tts_params from the flat wrapper arguments. Unset/zero scalars keep
// the qt defaults (temperature 0.9, top_k 50, top_p 1.0, rep 1.05, max 2048).
static void fill_params(qt_tts_params *tp, const char *text, const char *lang,
                        const char *instruct, const char *speaker,
                        const float *ref_samples, int ref_n,
                        const char *ref_text, long long seed, float temperature,
                        int top_k, float top_p, float repetition_penalty,
                        int max_new_tokens) {
    qt_tts_default_params(tp);
    tp->text = text ? text : "";
    if (lang && lang[0] != '\0')
        tp->lang = lang; // else keep default NULL -> auto
    if (instruct && instruct[0] != '\0')
        tp->instruct = instruct;
    if (speaker && speaker[0] != '\0')
        tp->speaker = speaker;
    if (ref_samples && ref_n > 0) {
        tp->ref_audio_24k = ref_samples;
        tp->ref_n_samples = ref_n;
        if (ref_text && ref_text[0] != '\0')
            tp->ref_text = ref_text;
    }
    if (seed >= 0)
        tp->seed = (int64_t)seed; // else default -1 (random)
    if (temperature > 0.0f)
        tp->temperature = temperature;
    if (top_k > 0)
        tp->top_k = top_k;
    if (top_p > 0.0f)
        tp->top_p = top_p;
    if (repetition_penalty > 0.0f)
        tp->repetition_penalty = repetition_penalty;
    if (max_new_tokens > 0)
        tp->max_new_tokens = max_new_tokens;
}

float *qt3_tts(const char *text, const char *lang, const char *instruct,
               const char *speaker, const float *ref_samples, int ref_n,
               const char *ref_text, long long seed, float temperature,
               int top_k, float top_p, float repetition_penalty,
               int max_new_tokens, int *out_n) {
    if (out_n)
        *out_n = 0;
    if (!g_ctx) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: model not loaded\n");
        return nullptr;
    }
    if (!text || text[0] == '\0') {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: text is required\n");
        return nullptr;
    }
    qt_tts_params tp;
    fill_params(&tp, text, lang, instruct, speaker, ref_samples, ref_n,
                ref_text, seed, temperature, top_k, top_p, repetition_penalty,
                max_new_tokens);

    qt_audio out = {0};
    enum qt_status rc = qt_synthesize(g_ctx, &tp, &out);
    if (rc != QT_STATUS_OK || out.n_samples <= 0 || !out.samples) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: synthesize failed (rc=%d): %s\n",
                (int)rc, qt_last_error());
        qt_audio_free(&out);
        return nullptr;
    }

    // Copy into a plain malloc buffer the Go side frees via qt3_pcm_free.
    size_t bytes = (size_t)out.n_samples * sizeof(float);
    float *buf = (float *)malloc(bytes);
    if (!buf) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: malloc(%zu) failed\n", bytes);
        qt_audio_free(&out);
        return nullptr;
    }
    memcpy(buf, out.samples, bytes);
    if (out_n)
        *out_n = out.n_samples;
    qt_audio_free(&out);
    return buf;
}

int qt3_tts_stream(const char *text, const char *lang, const char *instruct,
                   const char *speaker, const float *ref_samples, int ref_n,
                   const char *ref_text, long long seed, float temperature,
                   int top_k, float top_p, float repetition_penalty,
                   int max_new_tokens, qt3_chunk_cb cb, void *user_data) {
    if (!g_ctx) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: model not loaded\n");
        return 1;
    }
    if (!cb) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: stream callback is null\n");
        return 2;
    }
    if (!text || text[0] == '\0') {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: text is required\n");
        return 4;
    }
    qt_tts_params tp;
    fill_params(&tp, text, lang, instruct, speaker, ref_samples, ref_n,
                ref_text, seed, temperature, top_k, top_p, repetition_penalty,
                max_new_tokens);
    // qt_audio_chunk_cb has the identical signature to qt3_chunk_cb
    // (bool vs int return are ABI-compatible; non-zero == true).
    tp.on_chunk = (qt_audio_chunk_cb)cb;
    tp.on_chunk_user_data = user_data;

    qt_audio out = {0}; // stays empty in streaming mode
    enum qt_status rc = qt_synthesize(g_ctx, &tp, &out);
    qt_audio_free(&out);
    if (rc != QT_STATUS_OK && rc != QT_STATUS_CANCELLED) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: stream synth failed (rc=%d): %s\n",
                (int)rc, qt_last_error());
        return 3;
    }
    return 0;
}

void qt3_pcm_free(float *p) { free(p); }

void qt3_unload(void) {
    if (g_ctx) {
        qt_free(g_ctx);
        g_ctx = nullptr;
    }
}

int qt3_n_speakers(void) { return g_ctx ? qt_n_speakers(g_ctx) : 0; }

const char *qt3_speaker_name(int i) {
    return g_ctx ? qt_speaker_name(g_ctx, i) : nullptr;
}
