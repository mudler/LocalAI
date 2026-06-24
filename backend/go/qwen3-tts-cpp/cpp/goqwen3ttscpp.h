#pragma once

extern "C" {

// Streaming PCM chunk callback. samples is mono float PCM at 24 kHz, valid
// only for the duration of the call. Return non-zero to continue, 0 to abort.
typedef int (*qt3_chunk_cb)(const float *samples, int n_samples,
                            void *user_data);

// Load the talker + codec/tokenizer GGUFs. use_fa / clamp_fp16 map to
// qt_init_params (the qt ABI exposes no thread count; ggml uses its own
// default). Returns 0 on success, non-zero on failure.
int qt3_load(const char *talker_path, const char *codec_path, int use_fa,
             int clamp_fp16);

// Synthesize to a malloc'd float PCM buffer (caller frees via qt3_pcm_free).
// The synthesis mode (base / custom_voice / voice_design) is auto-detected by
// qt from the talker GGUF; speaker is honoured only for custom_voice, instruct
// for voice_design / custom_voice, and ref_samples (+ optional ref_text) drive
// base-mode cloning. qt enforces the rules and we surface qt_last_error() on
// QT_STATUS_MODE_INVALID. Writes the sample count to *out_n. Returns NULL on
// failure (out_n set to 0).
float *qt3_tts(const char *text, const char *lang, const char *instruct,
               const char *speaker, const float *ref_samples, int ref_n,
               const char *ref_text, long long seed, float temperature,
               int top_k, float top_p, float repetition_penalty,
               int max_new_tokens, int *out_n);

// Streaming synthesis: cb is invoked per PCM chunk as audio is produced. Same
// param semantics as qt3_tts. Returns 0 on success.
int qt3_tts_stream(const char *text, const char *lang, const char *instruct,
                   const char *speaker, const float *ref_samples, int ref_n,
                   const char *ref_text, long long seed, float temperature,
                   int top_k, float top_p, float repetition_penalty,
                   int max_new_tokens, qt3_chunk_cb cb, void *user_data);

// Free a buffer returned by qt3_tts.
void qt3_pcm_free(float *p);

// Release the qt context.
void qt3_unload(void);

// Named-speaker introspection (custom_voice models). Returns 0 / NULL when no
// model is loaded or the index is out of range.
int qt3_n_speakers(void);
const char *qt3_speaker_name(int i);
}
