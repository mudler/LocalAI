#include "gowhisper.h"
#include "ggml-backend.h"
#include "whisper.h"
#include <vector>

static struct whisper_vad_context *vctx;
static struct whisper_context *ctx;
static std::vector<float> flat_segs;

static void ggml_log_cb(enum ggml_log_level level, const char *log,
                        void *data) {
  const char *level_str;

  if (!log) {
    return;
  }

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
  default: /* Potential future-proofing */
    level_str = "?????";
    break;
  }

  fprintf(stderr, "[%-5s] ", level_str);
  fputs(log, stderr);
  fflush(stderr);
}

int load_model(const char *const model_path) {
  whisper_log_set(ggml_log_cb, nullptr);
  ggml_backend_load_all();

  struct whisper_context_params cparams = whisper_context_default_params();

  ctx = whisper_init_from_file_with_params(model_path, cparams);
  if (ctx == nullptr) {
    fprintf(stderr, "error: Also failed to init model as transcriber\n");
    return 1;
  }

  return 0;
}

int load_model_vad(const char *const model_path) {
  whisper_log_set(ggml_log_cb, nullptr);
  ggml_backend_load_all();

  struct whisper_vad_context_params vcparams =
      whisper_vad_default_context_params();

  // XXX: Overridden to false in upstream due to performance?
  // vcparams.use_gpu = true;

  vctx = whisper_vad_init_from_file_with_params(model_path, vcparams);
  if (vctx == nullptr) {
    fprintf(stderr, "error: Failed to init model as VAD\n");
    return 1;
  }

  return 0;
}

int vad(float pcmf32[], size_t pcmf32_len, float **segs_out,
        size_t *segs_out_len) {
  if (!whisper_vad_detect_speech(vctx, pcmf32, pcmf32_len)) {
    fprintf(stderr, "error: failed to detect speech\n");
    return 1;
  }

  struct whisper_vad_params params = whisper_vad_default_params();
  struct whisper_vad_segments *segs =
      whisper_vad_segments_from_probs(vctx, params);
  size_t segn = whisper_vad_segments_n_segments(segs);

  // fprintf(stderr, "Got segments %zd\n", segn);

  flat_segs.clear();

  for (int i = 0; i < segn; i++) {
    flat_segs.push_back(whisper_vad_segments_get_segment_t0(segs, i));
    flat_segs.push_back(whisper_vad_segments_get_segment_t1(segs, i));
  }

  // fprintf(stderr, "setting out variables: %p=%p -> %p, %p=%zx -> %zx\n",
  //         segs_out, *segs_out, flat_segs.data(), segs_out_len, *segs_out_len,
  //         flat_segs.size());
  *segs_out = flat_segs.data();
  *segs_out_len = flat_segs.size();

  // fprintf(stderr, "freeing segs\n");
  whisper_vad_free_segments(segs);

  // fprintf(stderr, "returning\n");
  return 0;
}

int transcribe(uint32_t threads, char *lang, bool translate, bool tdrz,
               float pcmf32[], size_t pcmf32_len, size_t *segs_out_len, char *prompt) {
  whisper_full_params wparams =
      whisper_full_default_params(WHISPER_SAMPLING_GREEDY);

  wparams.n_threads = threads;
  if (*lang != '\0')
    wparams.language = lang;
  else {
    wparams.language = nullptr;
  }

  wparams.translate = translate;
  wparams.debug_mode = true;
  wparams.print_progress = true;
  wparams.tdrz_enable = tdrz;
  wparams.initial_prompt = prompt;

  fprintf(stderr, "info: Enable tdrz: %d\n", tdrz);
  fprintf(stderr, "info: Initial prompt: \"%s\"\n", prompt);

  if (whisper_full(ctx, wparams, pcmf32, pcmf32_len)) {
    fprintf(stderr, "error: transcription failed\n");
    return 1;
  }

  *segs_out_len = whisper_full_n_segments(ctx);

  return 0;
}

const char *get_segment_text(int i) {
  return whisper_full_get_segment_text(ctx, i);
}

int64_t get_segment_t0(int i) { return whisper_full_get_segment_t0(ctx, i); }

int64_t get_segment_t1(int i) { return whisper_full_get_segment_t1(ctx, i); }

int n_tokens(int i) { return whisper_full_n_tokens(ctx, i); }

int32_t get_token_id(int i, int j) {
  return whisper_full_get_token_id(ctx, i, j);
}

bool get_segment_speaker_turn_next(int i) {
  return whisper_full_get_segment_speaker_turn_next(ctx, i);
}
