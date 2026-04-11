#include "goqwen3ttscpp.h"
#include "ggml-backend.h"
#include "qwen3_tts.h"

#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

using namespace qwen3_tts;

// Global engine (loaded once, reused across requests)
static Qwen3TTS *g_engine = nullptr;
static bool g_loaded = false;
static int g_threads = 4;

static void ggml_log_cb(enum ggml_log_level level, const char *log, void *data) {
    const char *level_str;
    if (!log)
        return;
    switch (level) {
    case GGML_LOG_LEVEL_DEBUG:
        level_str = "DEBUG";
        break;
    case GGML_LOG_LEVEL_INFO:
        level_str = "INFO";
        break;
    case GGML_LOG_LEVEL_WARN:
        level_str = "WARN";
        break;
    case GGML_LOG_LEVEL_ERROR:
        level_str = "ERROR";
        break;
    default:
        level_str = "?????";
        break;
    }
    fprintf(stderr, "[%-5s] ", level_str);
    fputs(log, stderr);
    fflush(stderr);
}

// Map language string to language_id token used by the model
static int language_to_id(const char *lang) {
    if (!lang || lang[0] == '\0')
        return 2050; // default: English
    std::string l(lang);
    if (l == "en")
        return 2050;
    if (l == "ru")
        return 2069;
    if (l == "zh")
        return 2055;
    if (l == "ja")
        return 2058;
    if (l == "ko")
        return 2064;
    if (l == "de")
        return 2053;
    if (l == "fr")
        return 2061;
    if (l == "es")
        return 2054;
    if (l == "it")
        return 2056;
    if (l == "pt")
        return 2057;
    fprintf(stderr, "[qwen3-tts-cpp] Unknown language '%s', defaulting to English\n",
            lang);
    return 2050;
}

int load_model(const char *model_dir, int n_threads) {
    ggml_log_set(ggml_log_cb, nullptr);
    ggml_backend_load_all();

    if (n_threads <= 0)
        n_threads = 4;
    g_threads = n_threads;

    fprintf(stderr, "[qwen3-tts-cpp] Loading models from %s (threads=%d)\n",
            model_dir, n_threads);

    g_engine = new Qwen3TTS();
    if (!g_engine->load_models(model_dir)) {
        fprintf(stderr, "[qwen3-tts-cpp] FATAL: failed to load models from %s\n",
                model_dir);
        delete g_engine;
        g_engine = nullptr;
        return 1;
    }

    g_loaded = true;
    fprintf(stderr, "[qwen3-tts-cpp] Models loaded successfully\n");
    return 0;
}

int synthesize(const char *text, const char *ref_audio_path, const char *dst,
               const char *language, float temperature, float top_p,
               int top_k, float repetition_penalty, int max_audio_tokens,
               int n_threads) {
    if (!g_loaded || !g_engine) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: models not loaded\n");
        return 1;
    }

    if (!text || !dst) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: text and dst are required\n");
        return 2;
    }

    tts_params params;
    params.max_audio_tokens = max_audio_tokens > 0 ? max_audio_tokens : 4096;
    params.temperature = temperature;
    params.top_p = top_p;
    params.top_k = top_k;
    params.repetition_penalty = repetition_penalty;
    params.n_threads = n_threads > 0 ? n_threads : g_threads;
    params.language_id = language_to_id(language);

    fprintf(stderr, "[qwen3-tts-cpp] Synthesizing: text='%.50s%s', lang_id=%d, "
                    "temp=%.2f, threads=%d\n",
            text, (strlen(text) > 50 ? "..." : ""), params.language_id,
            temperature, params.n_threads);

    tts_result result;
    bool has_ref = ref_audio_path && ref_audio_path[0] != '\0';

    if (has_ref) {
        fprintf(stderr, "[qwen3-tts-cpp] Voice cloning with ref: %s\n",
                ref_audio_path);
        result = g_engine->synthesize_with_voice(text, ref_audio_path, params);
    } else {
        result = g_engine->synthesize(text, params);
    }

    if (!result.success) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: synthesis failed: %s\n",
                result.error_msg.c_str());
        return 3;
    }

    int n_samples = (int)result.audio.size();
    if (n_samples == 0) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: synthesis produced no samples\n");
        return 4;
    }

    fprintf(stderr,
            "[qwen3-tts-cpp] Synthesis done: %d samples (%.2fs @ 24kHz)\n",
            n_samples, (float)n_samples / 24000.0f);

    if (!save_audio_file(dst, result.audio, result.sample_rate)) {
        fprintf(stderr, "[qwen3-tts-cpp] ERROR: failed to write %s\n", dst);
        return 5;
    }

    fprintf(stderr, "[qwen3-tts-cpp] Wrote %s\n", dst);
    return 0;
}
