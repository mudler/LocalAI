#include "goacestepcpp.h"
#include "ggml-backend.h"

#include "audio-io.h"
#include "bpe.h"
#include "cond-enc.h"
#include "dit-sampler.h"
#include "dit.h"
#include "gguf-weights.h"
#include "philox.h"
#include "qwen3-enc.h"
#include "qwen3-lm.h"
#include "request.h"
#include "vae.h"

#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <random>
#include <string>
#include <vector>

// Global model contexts (loaded once, reused across requests)
static DiTGGML       g_dit       = {};
static DiTGGMLConfig g_dit_cfg;
static VAEGGML       g_vae       = {};
static bool          g_dit_loaded = false;
static bool          g_vae_loaded = false;
static bool          g_is_turbo   = false;

// Silence latent [15000, 64] — read once from DiT GGUF
static std::vector<float> g_silence_full;

// Paths for per-request loading (text encoder, tokenizer)
static std::string g_text_enc_path;
static std::string g_dit_path;
static std::string g_lm_path;

static void ggml_log_cb(enum ggml_log_level level, const char * log, void * data) {
    const char * level_str;
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

int load_model(const char * lm_model_path, const char * text_encoder_path,
               const char * dit_model_path, const char * vae_model_path) {
    ggml_log_set(ggml_log_cb, nullptr);
    ggml_backend_load_all();

    g_lm_path       = lm_model_path;
    g_text_enc_path = text_encoder_path;
    g_dit_path      = dit_model_path;

    // Load DiT model
    fprintf(stderr, "[acestep-cpp] Loading DiT from %s\n", dit_model_path);
    dit_ggml_init_backend(&g_dit);
    if (!dit_ggml_load(&g_dit, dit_model_path, g_dit_cfg, nullptr, 0.0f)) {
        fprintf(stderr, "[acestep-cpp] FATAL: failed to load DiT from %s\n", dit_model_path);
        return 1;
    }
    g_dit_loaded = true;

    // Read DiT GGUF metadata + silence_latent
    {
        GGUFModel gf = {};
        if (gf_load(&gf, dit_model_path)) {
            g_is_turbo           = gf_get_bool(gf, "acestep.is_turbo");
            const void * sl_data = gf_get_data(gf, "silence_latent");
            if (sl_data) {
                g_silence_full.resize(15000 * 64);
                memcpy(g_silence_full.data(), sl_data, 15000 * 64 * sizeof(float));
                fprintf(stderr, "[acestep-cpp] silence_latent: [15000, 64] loaded\n");
            } else {
                fprintf(stderr, "[acestep-cpp] FATAL: silence_latent not found in %s\n", dit_model_path);
                gf_close(&gf);
                return 2;
            }
            gf_close(&gf);
        } else {
            fprintf(stderr, "[acestep-cpp] FATAL: cannot read GGUF metadata from %s\n", dit_model_path);
            return 2;
        }
    }

    // Load VAE model
    fprintf(stderr, "[acestep-cpp] Loading VAE from %s\n", vae_model_path);
    vae_ggml_load(&g_vae, vae_model_path);
    g_vae_loaded = true;

    fprintf(stderr, "[acestep-cpp] All models loaded successfully (turbo=%d)\n", g_is_turbo);
    return 0;
}

int generate_music(const char * caption, const char * lyrics, int bpm,
                   const char * keyscale, const char * timesignature,
                   float duration, float temperature, bool instrumental,
                   int seed, const char * dst, int threads) {
    if (!g_dit_loaded || !g_vae_loaded) {
        fprintf(stderr, "[acestep-cpp] ERROR: models not loaded\n");
        return 1;
    }

    const int FRAMES_PER_SECOND = 25;

    // Defaults
    if (duration <= 0)
        duration = 30.0f;
    std::string cap_str    = caption ? caption : "";
    std::string lyrics_str = (instrumental || !lyrics) ? "" : lyrics;
    std::string ks_str     = keyscale ? keyscale : "N/A";
    std::string ts_str     = timesignature ? timesignature : "4/4";
    std::string lang_str   = "unknown";
    char        bpm_str[16];
    if (bpm > 0) {
        snprintf(bpm_str, sizeof(bpm_str), "%d", bpm);
    } else {
        snprintf(bpm_str, sizeof(bpm_str), "N/A");
    }

    int   num_steps      = 8;
    float guidance_scale = g_is_turbo ? 1.0f : 7.0f;
    float shift          = 1.0f;

    if (seed < 0) {
        std::random_device rd;
        seed = (int)(rd() & 0x7FFFFFFF);
    }

    // Compute T (latent frames at 25Hz)
    int T = (int)(duration * FRAMES_PER_SECOND);
    T     = ((T + g_dit_cfg.patch_size - 1) / g_dit_cfg.patch_size) * g_dit_cfg.patch_size;
    int S = T / g_dit_cfg.patch_size;

    if (T > 15000) {
        fprintf(stderr, "[acestep-cpp] ERROR: T=%d exceeds max 15000\n", T);
        return 2;
    }

    int Oc     = g_dit_cfg.out_channels;      // 64
    int ctx_ch = g_dit_cfg.in_channels - Oc;  // 128

    fprintf(stderr, "[acestep-cpp] T=%d, S=%d, duration=%.1fs, seed=%d\n", T, S, duration, seed);

    // 1. Load BPE tokenizer from text encoder GGUF
    BPETokenizer tok;
    if (!load_bpe_from_gguf(&tok, g_text_enc_path.c_str())) {
        fprintf(stderr, "[acestep-cpp] FATAL: failed to load BPE tokenizer\n");
        return 3;
    }

    // 2. Build formatted prompts (matches dit-vae.cpp text2music template)
    std::string instruction = "Fill the audio semantic mask based on the given conditions:";

    char metas[512];
    snprintf(metas, sizeof(metas),
             "- bpm: %s\n- timesignature: %s\n- keyscale: %s\n- duration: %d seconds\n",
             bpm_str, ts_str.c_str(), ks_str.c_str(), (int)duration);

    std::string text_str  = std::string("# Instruction\n") + instruction + "\n\n" +
                            "# Caption\n" + cap_str + "\n\n" +
                            "# Metas\n" + metas + "<|endoftext|>\n";
    std::string lyric_str = std::string("# Languages\n") + lang_str + "\n\n# Lyric\n" +
                            lyrics_str + "<|endoftext|>";

    // 3. Tokenize
    auto text_ids  = bpe_encode(&tok, text_str.c_str(), true);
    auto lyric_ids = bpe_encode(&tok, lyric_str.c_str(), true);
    int  S_text    = (int)text_ids.size();
    int  S_lyric   = (int)lyric_ids.size();

    fprintf(stderr, "[acestep-cpp] caption: %d tokens, lyrics: %d tokens\n", S_text, S_lyric);

    // 4. Text encoder forward
    Qwen3GGML text_enc = {};
    qwen3_init_backend(&text_enc);
    if (!qwen3_load_text_encoder(&text_enc, g_text_enc_path.c_str())) {
        fprintf(stderr, "[acestep-cpp] FATAL: failed to load text encoder\n");
        return 4;
    }

    int                H_text = text_enc.cfg.hidden_size;  // 1024
    std::vector<float> text_hidden(H_text * S_text);

    qwen3_forward(&text_enc, text_ids.data(), S_text, text_hidden.data());
    fprintf(stderr, "[acestep-cpp] TextEncoder forward done\n");

    // 5. Lyric embedding
    std::vector<float> lyric_embed(H_text * S_lyric);
    qwen3_embed_lookup(&text_enc, lyric_ids.data(), S_lyric, lyric_embed.data());

    // 6. Condition encoder
    CondGGML cond = {};
    cond_ggml_init_backend(&cond);
    if (!cond_ggml_load(&cond, g_dit_path.c_str())) {
        fprintf(stderr, "[acestep-cpp] FATAL: failed to load condition encoder\n");
        qwen3_free(&text_enc);
        return 5;
    }

    const int          S_ref = 750;
    std::vector<float> silence_feats(S_ref * 64);
    memcpy(silence_feats.data(), g_silence_full.data(), S_ref * 64 * sizeof(float));

    int                enc_S = 0;
    std::vector<float> enc_hidden;
    cond_ggml_forward(&cond, text_hidden.data(), S_text, lyric_embed.data(), S_lyric,
                      silence_feats.data(), S_ref, enc_hidden, &enc_S);
    fprintf(stderr, "[acestep-cpp] ConditionEncoder done, enc_S=%d\n", enc_S);

    qwen3_free(&text_enc);
    cond_ggml_free(&cond);

    // 7. Build context [T, ctx_ch] = silence[64] + mask[64]
    std::vector<float> context(T * ctx_ch);
    for (int t = 0; t < T; t++) {
        const float * src = g_silence_full.data() + t * Oc;
        for (int c = 0; c < Oc; c++) {
            context[t * ctx_ch + c] = src[c];
        }
        for (int c = 0; c < Oc; c++) {
            context[t * ctx_ch + Oc + c] = 1.0f;
        }
    }

    // 8. Build schedule
    std::vector<float> schedule(num_steps);
    for (int i = 0; i < num_steps; i++) {
        float t     = 1.0f - (float)i / (float)num_steps;
        schedule[i] = shift * t / (1.0f + (shift - 1.0f) * t);
    }

    // 9. Generate noise (Philox)
    std::vector<float> noise(Oc * T);
    philox_randn((long long)seed, noise.data(), Oc * T, true);

    // 10. DiT generate
    std::vector<float> output(Oc * T);
    fprintf(stderr, "[acestep-cpp] DiT generate: T=%d, steps=%d, guidance=%.1f\n", T, num_steps, guidance_scale);

    dit_ggml_generate(&g_dit, noise.data(), context.data(), enc_hidden.data(), enc_S,
                      T, 1, num_steps, schedule.data(), output.data(), guidance_scale,
                      nullptr, nullptr, -1);
    fprintf(stderr, "[acestep-cpp] DiT generation done\n");

    // 11. VAE decode
    int                T_audio_max = T * 1920;
    std::vector<float> audio(2 * T_audio_max);

    int T_audio = vae_ggml_decode_tiled(&g_vae, output.data(), T, audio.data(), T_audio_max, 256, 64);
    if (T_audio < 0) {
        fprintf(stderr, "[acestep-cpp] ERROR: VAE decode failed\n");
        return 6;
    }
    fprintf(stderr, "[acestep-cpp] VAE decode done: %d samples (%.2fs @ 48kHz)\n", T_audio,
            (float)T_audio / 48000.0f);

    // 12. Peak normalization to -1.0 dB
    {
        float peak      = 0.0f;
        int   n_samples = 2 * T_audio;
        for (int i = 0; i < n_samples; i++) {
            float a = audio[i] < 0 ? -audio[i] : audio[i];
            if (a > peak) {
                peak = a;
            }
        }
        if (peak > 1e-6f) {
            const float target_amp = powf(10.0f, -1.0f / 20.0f);
            float       gain       = target_amp / peak;
            for (int i = 0; i < n_samples; i++) {
                audio[i] *= gain;
            }
        }
    }

    // 13. Write WAV output
    if (!audio_write_wav(dst, audio.data(), T_audio, 48000)) {
        fprintf(stderr, "[acestep-cpp] ERROR: failed to write %s\n", dst);
        return 7;
    }

    fprintf(stderr, "[acestep-cpp] Wrote %s: %d samples (%.2fs @ 48kHz stereo)\n",
            dst, T_audio, (float)T_audio / 48000.0f);
    return 0;
}
