#include "shim.h"
#include "c-api.h"

#include <stdlib.h>
#include <string.h>

// Replace the char* field pointed to by `slot` with a strdup of `s`
// (or NULL if s is NULL). Frees any prior value. Silently no-ops when
// strdup fails — the caller will see a Create* failure downstream.
static void shim_set_str(const char **slot, const char *s) {
    free((char *)*slot);
    *slot = s ? strdup(s) : NULL;
}

// ==================================================================
// VAD config
// ==================================================================

void *sherpa_shim_vad_config_new(void) {
    return calloc(1, sizeof(SherpaOnnxVadModelConfig));
}

void sherpa_shim_vad_config_free(void *h) {
    if (!h) return;
    SherpaOnnxVadModelConfig *c = (SherpaOnnxVadModelConfig *)h;
    free((char *)c->silero_vad.model);
    free((char *)c->provider);
    free(c);
}

void sherpa_shim_vad_config_set_silero_model(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxVadModelConfig *)h)->silero_vad.model, v);
}
void sherpa_shim_vad_config_set_silero_threshold(void *h, float v) {
    ((SherpaOnnxVadModelConfig *)h)->silero_vad.threshold = v;
}
void sherpa_shim_vad_config_set_silero_min_silence_duration(void *h, float v) {
    ((SherpaOnnxVadModelConfig *)h)->silero_vad.min_silence_duration = v;
}
void sherpa_shim_vad_config_set_silero_min_speech_duration(void *h, float v) {
    ((SherpaOnnxVadModelConfig *)h)->silero_vad.min_speech_duration = v;
}
void sherpa_shim_vad_config_set_silero_window_size(void *h, int32_t v) {
    ((SherpaOnnxVadModelConfig *)h)->silero_vad.window_size = v;
}
void sherpa_shim_vad_config_set_silero_max_speech_duration(void *h, float v) {
    ((SherpaOnnxVadModelConfig *)h)->silero_vad.max_speech_duration = v;
}
void sherpa_shim_vad_config_set_sample_rate(void *h, int32_t v) {
    ((SherpaOnnxVadModelConfig *)h)->sample_rate = v;
}
void sherpa_shim_vad_config_set_num_threads(void *h, int32_t v) {
    ((SherpaOnnxVadModelConfig *)h)->num_threads = v;
}
void sherpa_shim_vad_config_set_provider(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxVadModelConfig *)h)->provider, v);
}
void sherpa_shim_vad_config_set_debug(void *h, int32_t v) {
    ((SherpaOnnxVadModelConfig *)h)->debug = v;
}

void *sherpa_shim_create_vad(void *h, float buffer_size_seconds) {
    return (void *)SherpaOnnxCreateVoiceActivityDetector(
        (const SherpaOnnxVadModelConfig *)h, buffer_size_seconds);
}

// ==================================================================
// Offline TTS config (VITS)
// ==================================================================

void *sherpa_shim_tts_config_new(void) {
    return calloc(1, sizeof(SherpaOnnxOfflineTtsConfig));
}

void sherpa_shim_tts_config_free(void *h) {
    if (!h) return;
    SherpaOnnxOfflineTtsConfig *c = (SherpaOnnxOfflineTtsConfig *)h;
    free((char *)c->model.vits.model);
    free((char *)c->model.vits.tokens);
    free((char *)c->model.vits.lexicon);
    free((char *)c->model.vits.data_dir);
    free((char *)c->model.provider);
    free(c);
}

void sherpa_shim_tts_config_set_vits_model(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineTtsConfig *)h)->model.vits.model, v);
}
void sherpa_shim_tts_config_set_vits_tokens(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineTtsConfig *)h)->model.vits.tokens, v);
}
void sherpa_shim_tts_config_set_vits_lexicon(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineTtsConfig *)h)->model.vits.lexicon, v);
}
void sherpa_shim_tts_config_set_vits_data_dir(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineTtsConfig *)h)->model.vits.data_dir, v);
}
void sherpa_shim_tts_config_set_vits_noise_scale(void *h, float v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->model.vits.noise_scale = v;
}
void sherpa_shim_tts_config_set_vits_noise_scale_w(void *h, float v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->model.vits.noise_scale_w = v;
}
void sherpa_shim_tts_config_set_vits_length_scale(void *h, float v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->model.vits.length_scale = v;
}
void sherpa_shim_tts_config_set_num_threads(void *h, int32_t v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->model.num_threads = v;
}
void sherpa_shim_tts_config_set_debug(void *h, int32_t v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->model.debug = v;
}
void sherpa_shim_tts_config_set_provider(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineTtsConfig *)h)->model.provider, v);
}
void sherpa_shim_tts_config_set_max_num_sentences(void *h, int32_t v) {
    ((SherpaOnnxOfflineTtsConfig *)h)->max_num_sentences = v;
}

void *sherpa_shim_create_offline_tts(void *h) {
    return (void *)SherpaOnnxCreateOfflineTts(
        (const SherpaOnnxOfflineTtsConfig *)h);
}

// ==================================================================
// Offline recognizer config
// ==================================================================

void *sherpa_shim_offline_recog_config_new(void) {
    return calloc(1, sizeof(SherpaOnnxOfflineRecognizerConfig));
}

void sherpa_shim_offline_recog_config_free(void *h) {
    if (!h) return;
    SherpaOnnxOfflineRecognizerConfig *c = (SherpaOnnxOfflineRecognizerConfig *)h;
    free((char *)c->model_config.provider);
    free((char *)c->model_config.tokens);
    free((char *)c->model_config.whisper.encoder);
    free((char *)c->model_config.whisper.decoder);
    free((char *)c->model_config.whisper.language);
    free((char *)c->model_config.whisper.task);
    free((char *)c->model_config.paraformer.model);
    free((char *)c->model_config.sense_voice.model);
    free((char *)c->model_config.sense_voice.language);
    free((char *)c->model_config.omnilingual.model);
    free((char *)c->decoding_method);
    free(c);
}

void sherpa_shim_offline_recog_config_set_num_threads(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.num_threads = v;
}
void sherpa_shim_offline_recog_config_set_debug(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.debug = v;
}
void sherpa_shim_offline_recog_config_set_provider(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.provider, v);
}
void sherpa_shim_offline_recog_config_set_tokens(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.tokens, v);
}
void sherpa_shim_offline_recog_config_set_feat_sample_rate(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->feat_config.sample_rate = v;
}
void sherpa_shim_offline_recog_config_set_feat_feature_dim(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->feat_config.feature_dim = v;
}
void sherpa_shim_offline_recog_config_set_decoding_method(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->decoding_method, v);
}
void sherpa_shim_offline_recog_config_set_whisper_encoder(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.whisper.encoder, v);
}
void sherpa_shim_offline_recog_config_set_whisper_decoder(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.whisper.decoder, v);
}
void sherpa_shim_offline_recog_config_set_whisper_language(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.whisper.language, v);
}
void sherpa_shim_offline_recog_config_set_whisper_task(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.whisper.task, v);
}
void sherpa_shim_offline_recog_config_set_whisper_tail_paddings(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.whisper.tail_paddings = v;
}
void sherpa_shim_offline_recog_config_set_paraformer_model(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.paraformer.model, v);
}
void sherpa_shim_offline_recog_config_set_sense_voice_model(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.sense_voice.model, v);
}
void sherpa_shim_offline_recog_config_set_sense_voice_language(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.sense_voice.language, v);
}
void sherpa_shim_offline_recog_config_set_sense_voice_use_itn(void *h, int32_t v) {
    ((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.sense_voice.use_itn = v;
}
void sherpa_shim_offline_recog_config_set_omnilingual_model(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOfflineRecognizerConfig *)h)->model_config.omnilingual.model, v);
}

void *sherpa_shim_create_offline_recognizer(void *h) {
    return (void *)SherpaOnnxCreateOfflineRecognizer(
        (const SherpaOnnxOfflineRecognizerConfig *)h);
}

// ==================================================================
// Online recognizer config
// ==================================================================

void *sherpa_shim_online_recog_config_new(void) {
    return calloc(1, sizeof(SherpaOnnxOnlineRecognizerConfig));
}

void sherpa_shim_online_recog_config_free(void *h) {
    if (!h) return;
    SherpaOnnxOnlineRecognizerConfig *c = (SherpaOnnxOnlineRecognizerConfig *)h;
    free((char *)c->model_config.transducer.encoder);
    free((char *)c->model_config.transducer.decoder);
    free((char *)c->model_config.transducer.joiner);
    free((char *)c->model_config.tokens);
    free((char *)c->model_config.provider);
    free((char *)c->decoding_method);
    free(c);
}

void sherpa_shim_online_recog_config_set_transducer_encoder(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.transducer.encoder, v);
}
void sherpa_shim_online_recog_config_set_transducer_decoder(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.transducer.decoder, v);
}
void sherpa_shim_online_recog_config_set_transducer_joiner(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.transducer.joiner, v);
}
void sherpa_shim_online_recog_config_set_tokens(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.tokens, v);
}
void sherpa_shim_online_recog_config_set_num_threads(void *h, int32_t v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.num_threads = v;
}
void sherpa_shim_online_recog_config_set_debug(void *h, int32_t v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.debug = v;
}
void sherpa_shim_online_recog_config_set_provider(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->model_config.provider, v);
}
void sherpa_shim_online_recog_config_set_feat_sample_rate(void *h, int32_t v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->feat_config.sample_rate = v;
}
void sherpa_shim_online_recog_config_set_feat_feature_dim(void *h, int32_t v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->feat_config.feature_dim = v;
}
void sherpa_shim_online_recog_config_set_decoding_method(void *h, const char *v) {
    shim_set_str(&((SherpaOnnxOnlineRecognizerConfig *)h)->decoding_method, v);
}
void sherpa_shim_online_recog_config_set_enable_endpoint(void *h, int32_t v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->enable_endpoint = v;
}
void sherpa_shim_online_recog_config_set_rule1_min_trailing_silence(void *h, float v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->rule1_min_trailing_silence = v;
}
void sherpa_shim_online_recog_config_set_rule2_min_trailing_silence(void *h, float v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->rule2_min_trailing_silence = v;
}
void sherpa_shim_online_recog_config_set_rule3_min_utterance_length(void *h, float v) {
    ((SherpaOnnxOnlineRecognizerConfig *)h)->rule3_min_utterance_length = v;
}

void *sherpa_shim_create_online_recognizer(void *h) {
    return (void *)SherpaOnnxCreateOnlineRecognizer(
        (const SherpaOnnxOnlineRecognizerConfig *)h);
}

// ==================================================================
// Result-struct accessors
// ==================================================================

int32_t sherpa_shim_wave_sample_rate(const void *h) {
    return ((const SherpaOnnxWave *)h)->sample_rate;
}
int32_t sherpa_shim_wave_num_samples(const void *h) {
    return ((const SherpaOnnxWave *)h)->num_samples;
}
const float *sherpa_shim_wave_samples(const void *h) {
    return ((const SherpaOnnxWave *)h)->samples;
}

const char *sherpa_shim_offline_result_text(const void *h) {
    return ((const SherpaOnnxOfflineRecognizerResult *)h)->text;
}
const char *sherpa_shim_online_result_text(const void *h) {
    return ((const SherpaOnnxOnlineRecognizerResult *)h)->text;
}

int32_t sherpa_shim_generated_audio_sample_rate(const void *h) {
    return ((const SherpaOnnxGeneratedAudio *)h)->sample_rate;
}
int32_t sherpa_shim_generated_audio_n(const void *h) {
    return ((const SherpaOnnxGeneratedAudio *)h)->n;
}
const float *sherpa_shim_generated_audio_samples(const void *h) {
    return ((const SherpaOnnxGeneratedAudio *)h)->samples;
}

int32_t sherpa_shim_speech_segment_start(const void *h) {
    return ((const SherpaOnnxSpeechSegment *)h)->start;
}
int32_t sherpa_shim_speech_segment_n(const void *h) {
    return ((const SherpaOnnxSpeechSegment *)h)->n;
}

// ==================================================================
// TTS streaming callback trampoline
// ==================================================================

void *sherpa_shim_tts_generate_with_callback(
    void *tts, const char *text, int32_t sid, float speed,
    uintptr_t callback_ptr, uintptr_t user_data) {
    SherpaOnnxGeneratedAudioCallbackWithArg cb =
        (SherpaOnnxGeneratedAudioCallbackWithArg)callback_ptr;
    return (void *)SherpaOnnxOfflineTtsGenerateWithCallbackWithArg(
        (const SherpaOnnxOfflineTts *)tts, text, sid, speed, cb,
        (void *)user_data);
}
