#include "mossttscpp.h"
#include "ggml-backend.h"
#include "moss_tts_capi.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>

// Single loaded Local pipeline. base.SingleThread serializes calls on the Go
// side, so a plain global is safe (mirrors qwen3-tts-cpp's g_ctx).
static moss_local *g_local = nullptr;

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

int mtl_load(const char *local_path, const char *codec_path,
             const char *tokenizer_path) {
    ggml_log_set(ggml_log_cb, nullptr);
    ggml_backend_load_all();

    if (!local_path || local_path[0] == '\0') {
        fprintf(stderr, "[moss-tts-cpp] ERROR: local_path is required\n");
        return 1;
    }
    if (!codec_path || codec_path[0] == '\0') {
        fprintf(stderr, "[moss-tts-cpp] ERROR: codec_path is required\n");
        return 2;
    }
    if (!tokenizer_path || tokenizer_path[0] == '\0') {
        fprintf(stderr, "[moss-tts-cpp] ERROR: tokenizer_path is required\n");
        return 3;
    }

    fprintf(stderr,
            "[moss-tts-cpp] Loading local=%s codec=%s tokenizer=%s\n",
            local_path, codec_path, tokenizer_path);

    g_local = moss_local_load(local_path, codec_path, tokenizer_path);
    if (!g_local) {
        fprintf(stderr, "[moss-tts-cpp] FATAL: moss_local_load failed\n");
        return 4;
    }
    fprintf(stderr, "[moss-tts-cpp] Model loaded (%s)\n", moss_tts_version());
    return 0;
}

float *mtl_tts(const char *text, const char *reference_wav, int seed,
               int *out_n, int *out_sr) {
    if (out_n)
        *out_n = 0;
    if (out_sr)
        *out_sr = 0;
    if (!g_local) {
        fprintf(stderr, "[moss-tts-cpp] ERROR: model not loaded\n");
        return nullptr;
    }
    if (!text || text[0] == '\0') {
        fprintf(stderr, "[moss-tts-cpp] ERROR: text is required\n");
        return nullptr;
    }

    // An empty reference path means "no cloning": pass NULL so the engine skips
    // the clone branch rather than trying to open "".
    const char *ref = (reference_wav && reference_wav[0] != '\0')
                          ? reference_wav
                          : nullptr;

    int n = 0, sr = 0;
    float *pcm = moss_local_tts(g_local, text, ref, seed, &n, &sr);
    if (!pcm || n <= 0) {
        fprintf(stderr, "[moss-tts-cpp] ERROR: moss_local_tts failed\n");
        if (pcm)
            moss_free(pcm);
        return nullptr;
    }

    // Copy into a plain malloc buffer the Go side frees via mtl_pcm_free, then
    // release the engine-owned buffer with moss_free (mirrors qwen3-tts-cpp,
    // keeping ownership on the C runtime's malloc/free).
    size_t bytes = (size_t)n * sizeof(float);
    float *buf = (float *)malloc(bytes);
    if (!buf) {
        fprintf(stderr, "[moss-tts-cpp] ERROR: malloc(%zu) failed\n", bytes);
        moss_free(pcm);
        return nullptr;
    }
    memcpy(buf, pcm, bytes);
    moss_free(pcm);

    if (out_n)
        *out_n = n;
    if (out_sr)
        *out_sr = sr;
    return buf;
}

void mtl_pcm_free(float *p) { free(p); }

void mtl_unload(void) {
    if (g_local) {
        moss_local_free(g_local);
        g_local = nullptr;
    }
}

const char *mtl_version(void) { return moss_tts_version(); }
