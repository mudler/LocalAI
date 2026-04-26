#ifndef LOCALAI_SHERPA_ONNX_SHIM_H
#define LOCALAI_SHERPA_ONNX_SHIM_H

#include <stdint.h>

// libsherpa-shim: purego-friendly wrapper around sherpa-onnx's C API.
// Purego can't access C struct fields and can't route C callbacks to Go
// funcs directly. Every function here is a fixed-signature trampoline
// that replaces one field read/write or callback handoff that the Go
// backend would otherwise have to do through cgo.
//
// String lifetime: setters strdup; _free walks every owned string and
// frees it. Callers may discard their input buffers the moment a setter
// returns.
//
// Opaque handles are `void *` in both directions. Nothing here holds a
// reference across calls except config handles (freed via _free) and
// sherpa-allocated results (freed via sherpa's own Destroy* entry
// points, which Go calls through purego pass-through).

#ifdef __cplusplus
extern "C" {
#endif

// --- VAD config -----------------------------------------------------
void *sherpa_shim_vad_config_new(void);
void  sherpa_shim_vad_config_free(void *cfg);
void  sherpa_shim_vad_config_set_silero_model(void *cfg, const char *path);
void  sherpa_shim_vad_config_set_silero_threshold(void *cfg, float v);
void  sherpa_shim_vad_config_set_silero_min_silence_duration(void *cfg, float v);
void  sherpa_shim_vad_config_set_silero_min_speech_duration(void *cfg, float v);
void  sherpa_shim_vad_config_set_silero_window_size(void *cfg, int32_t v);
void  sherpa_shim_vad_config_set_silero_max_speech_duration(void *cfg, float v);
void  sherpa_shim_vad_config_set_sample_rate(void *cfg, int32_t v);
void  sherpa_shim_vad_config_set_num_threads(void *cfg, int32_t v);
void  sherpa_shim_vad_config_set_provider(void *cfg, const char *v);
void  sherpa_shim_vad_config_set_debug(void *cfg, int32_t v);
void *sherpa_shim_create_vad(void *cfg, float buffer_size_seconds);

// --- Offline TTS config (VITS path — the only TTS family the backend uses) ---
void *sherpa_shim_tts_config_new(void);
void  sherpa_shim_tts_config_free(void *cfg);
void  sherpa_shim_tts_config_set_vits_model(void *cfg, const char *v);
void  sherpa_shim_tts_config_set_vits_tokens(void *cfg, const char *v);
void  sherpa_shim_tts_config_set_vits_lexicon(void *cfg, const char *v);
void  sherpa_shim_tts_config_set_vits_data_dir(void *cfg, const char *v);
void  sherpa_shim_tts_config_set_vits_noise_scale(void *cfg, float v);
void  sherpa_shim_tts_config_set_vits_noise_scale_w(void *cfg, float v);
void  sherpa_shim_tts_config_set_vits_length_scale(void *cfg, float v);
void  sherpa_shim_tts_config_set_num_threads(void *cfg, int32_t v);
void  sherpa_shim_tts_config_set_debug(void *cfg, int32_t v);
void  sherpa_shim_tts_config_set_provider(void *cfg, const char *v);
void  sherpa_shim_tts_config_set_max_num_sentences(void *cfg, int32_t v);
void *sherpa_shim_create_offline_tts(void *cfg);

// --- Offline recognizer config (Whisper / Paraformer / SenseVoice / Omnilingual) ---
void *sherpa_shim_offline_recog_config_new(void);
void  sherpa_shim_offline_recog_config_free(void *cfg);
void  sherpa_shim_offline_recog_config_set_num_threads(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_debug(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_provider(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_tokens(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_feat_sample_rate(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_feat_feature_dim(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_decoding_method(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_whisper_encoder(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_whisper_decoder(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_whisper_language(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_whisper_task(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_whisper_tail_paddings(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_paraformer_model(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_sense_voice_model(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_sense_voice_language(void *cfg, const char *v);
void  sherpa_shim_offline_recog_config_set_sense_voice_use_itn(void *cfg, int32_t v);
void  sherpa_shim_offline_recog_config_set_omnilingual_model(void *cfg, const char *v);
void *sherpa_shim_create_offline_recognizer(void *cfg);

// --- Online recognizer config (streaming zipformer transducer) ---
void *sherpa_shim_online_recog_config_new(void);
void  sherpa_shim_online_recog_config_free(void *cfg);
void  sherpa_shim_online_recog_config_set_transducer_encoder(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_transducer_decoder(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_transducer_joiner(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_tokens(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_num_threads(void *cfg, int32_t v);
void  sherpa_shim_online_recog_config_set_debug(void *cfg, int32_t v);
void  sherpa_shim_online_recog_config_set_provider(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_feat_sample_rate(void *cfg, int32_t v);
void  sherpa_shim_online_recog_config_set_feat_feature_dim(void *cfg, int32_t v);
void  sherpa_shim_online_recog_config_set_decoding_method(void *cfg, const char *v);
void  sherpa_shim_online_recog_config_set_enable_endpoint(void *cfg, int32_t v);
void  sherpa_shim_online_recog_config_set_rule1_min_trailing_silence(void *cfg, float v);
void  sherpa_shim_online_recog_config_set_rule2_min_trailing_silence(void *cfg, float v);
void  sherpa_shim_online_recog_config_set_rule3_min_utterance_length(void *cfg, float v);
void *sherpa_shim_create_online_recognizer(void *cfg);

// --- Result accessors (sherpa-allocated; caller destroys via sherpa's own Destroy*) ---
int32_t      sherpa_shim_wave_sample_rate(const void *wave);
int32_t      sherpa_shim_wave_num_samples(const void *wave);
const float *sherpa_shim_wave_samples(const void *wave);

const char *sherpa_shim_offline_result_text(const void *result);
const char *sherpa_shim_online_result_text(const void *result);

int32_t      sherpa_shim_generated_audio_sample_rate(const void *audio);
int32_t      sherpa_shim_generated_audio_n(const void *audio);
const float *sherpa_shim_generated_audio_samples(const void *audio);

int32_t sherpa_shim_speech_segment_start(const void *seg);
int32_t sherpa_shim_speech_segment_n(const void *seg);

// --- TTS streaming callback trampoline -----------------------------
// Replaces the //export sherpaTtsGoCallback + callbacks.c bridge pattern.
// `callback_ptr` is the C-callable function pointer returned by
// purego.NewCallback. `user_data` is an integer the Go side uses to
// look up its state (sync.Map keyed by uint64).
//
// Returns the sherpa-allocated SherpaOnnxGeneratedAudio. Destroy with
// SherpaOnnxDestroyOfflineTtsGeneratedAudio (callable directly from
// Go via purego).
void *sherpa_shim_tts_generate_with_callback(
    void *tts, const char *text, int32_t sid, float speed,
    uintptr_t callback_ptr, uintptr_t user_data);

#ifdef __cplusplus
}
#endif

#endif
