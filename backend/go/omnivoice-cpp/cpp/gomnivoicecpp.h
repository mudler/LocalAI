#pragma once

#include <cstdint>

extern "C" {

// Streaming PCM chunk callback. samples is mono float PCM at 24 kHz, valid
// only for the duration of the call. Return non-zero to continue, 0 to abort.
typedef int (*omni_pcm_chunk_cb)(const float *samples, int n_samples,
                                 void *user_data);

// Load the LM (model_path) + codec (codec_path) GGUFs. use_fa / clamp_fp16
// map to ov_init_params. Returns 0 on success, non-zero on failure.
int omni_load(const char *model_path, const char *codec_path, int use_fa,
              int clamp_fp16);

// Synthesize to a malloc'd float PCM buffer (caller frees via omni_pcm_free).
// ref_samples != null && ref_n > 0 => voice cloning (ref_text optional).
// instruct != null && non-empty => voice design. seed < 0 keeps the default
// MaskGIT seed. denoise toggles the <|denoise|> marker (only with a reference).
// Writes the sample count to *out_n. Returns NULL on failure (out_n set to 0).
float *omni_tts(const char *text, const char *lang, const char *instruct,
                const float *ref_samples, int ref_n, const char *ref_text,
                long long seed, int denoise, int *out_n);

// Streaming synthesis: cb is invoked per PCM chunk as audio is produced.
// Same reference/design/seed semantics as omni_tts. Returns 0 on success.
int omni_tts_stream(const char *text, const char *lang, const char *instruct,
                    const float *ref_samples, int ref_n, const char *ref_text,
                    long long seed, int denoise, omni_pcm_chunk_cb cb,
                    void *user_data);

// Free a buffer returned by omni_tts.
void omni_pcm_free(float *p);

// Release the OmniVoice context.
void omni_unload(void);
}
