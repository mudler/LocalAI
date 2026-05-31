#include "crispasr_shim.h"
#include "ggml-backend.h"
#include "crispasr.h"
#include <atomic>
#include <vector>

// Opaque session types. crispasr.h declares `struct crispasr_session;` but not
// the result type nor the open/transcribe/result accessors — those are
// CA_EXPORT extern "C" symbols in src/crispasr_c_api.cpp, so we forward-declare
// exactly the ones we use. Signatures verified against
// sources/CrispASR/src/crispasr_c_api.cpp.
struct crispasr_session_result;
extern "C" {
crispasr_session *crispasr_session_open(const char *model_path, int n_threads);
crispasr_session *crispasr_session_open_explicit(const char *model_path,
                                                 const char *backend_name,
                                                 int n_threads);
int crispasr_session_set_codec_path(crispasr_session *s, const char *path);
void crispasr_session_close(crispasr_session *s);
const char *crispasr_session_backend(crispasr_session *s);
int crispasr_session_set_translate(crispasr_session *s, int enable);
crispasr_session_result *crispasr_session_transcribe_lang(
    crispasr_session *s, const float *pcm, int n_samples, const char *language);
int crispasr_session_result_n_segments(crispasr_session_result *r);
const char *crispasr_session_result_segment_text(crispasr_session_result *r,
                                                  int i);
int64_t crispasr_session_result_segment_t0(crispasr_session_result *r, int i);
int64_t crispasr_session_result_segment_t1(crispasr_session_result *r, int i);
void crispasr_session_result_free(crispasr_session_result *r);
float *crispasr_session_synthesize(crispasr_session *s, const char *text,
                                   int *out_n_samples);
void crispasr_pcm_free(float *pcm);
int crispasr_session_set_speaker_name(crispasr_session *s, const char *name);
int crispasr_session_set_voice(crispasr_session *s, const char *path,
                               const char *ref_text_or_null);
}

static crispasr_session *g_session = nullptr;
static crispasr_session_result *g_result = nullptr;

static struct whisper_vad_context *vctx;
static std::vector<float> flat_segs;

static std::atomic<int> g_abort{0};

extern "C" void set_abort(int v) {
  g_abort.store(v, std::memory_order_relaxed);
}

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

int load_model(const char *const model_path, int threads,
               const char *backend_name) {
  whisper_log_set(ggml_log_cb, nullptr);
  ggml_backend_load_all();

  if (backend_name && *backend_name) {
    g_session =
        crispasr_session_open_explicit(model_path, backend_name, threads);
  } else {
    g_session = crispasr_session_open(model_path, threads);
  }
  if (g_session == nullptr) {
    fprintf(stderr, "error: failed to open CrispASR session for model\n");
    return 1;
  }

  fprintf(stderr, "info: CrispASR backend selected: %s\n",
          crispasr_session_backend(g_session));
  return 0;
}

// set_codec_path forwards a companion file (qwen3-tts codec, orpheus SNAC,
// chatterbox s3gen, or mimo-asr tokenizer) to the active session. Returns 0 on
// success or when the active backend needs no companion, negative on failure,
// and -1 when no session is open.
int set_codec_path(const char *path) {
  return g_session ? crispasr_session_set_codec_path(g_session, path) : -1;
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

// threads, diarize and prompt are accepted for Go-side API parity but unused
// in Phase 1: the thread count is fixed at session open, and diarization and
// the initial prompt are separate CrispASR features not yet wired through the
// session ASR path.
int transcribe(uint32_t threads, char *lang, bool translate, bool diarize,
               float pcmf32[], size_t pcmf32_len, size_t *segs_out_len,
               char *prompt) {
  (void)threads;
  (void)diarize;
  (void)prompt;

  if (!g_session) {
    return 1;
  }

  // Reset stale abort flag from any prior cancelled call. set_abort remains
  // best-effort: the session transcribe call is blocking and exposes no abort
  // hook, so a mid-decode abort cannot interrupt it.
  g_abort.store(0, std::memory_order_relaxed);

  crispasr_session_set_translate(g_session, translate ? 1 : 0);

  if (g_result) {
    crispasr_session_result_free(g_result);
    g_result = nullptr;
  }

  const char *language = (lang && *lang) ? lang : nullptr;
  g_result = crispasr_session_transcribe_lang(g_session, pcmf32, (int)pcmf32_len,
                                              language);
  if (!g_result) {
    fprintf(stderr, "error: transcription failed\n");
    return 1;
  }

  *segs_out_len = crispasr_session_result_n_segments(g_result);
  return 0;
}

const char *get_segment_text(int i) {
  if (!g_result) {
    return "";
  }
  return crispasr_session_result_segment_text(g_result, i);
}

int64_t get_segment_t0(int i) {
  if (!g_result) {
    return 0;
  }
  return crispasr_session_result_segment_t0(g_result, i);
}

int64_t get_segment_t1(int i) {
  if (!g_result) {
    return 0;
  }
  return crispasr_session_result_segment_t1(g_result, i);
}

const char *get_backend(void) {
  return g_session ? crispasr_session_backend(g_session) : "";
}

// TTS uses the already-open session (crispasr_session_open auto-detects a TTS
// model). Output is 24 kHz mono float PCM (upstream CrispASR convention),
// malloc'd by the C API; the caller must release it via tts_free.
float *tts_synthesize(const char *text, int *out_n_samples) {
  if (out_n_samples) *out_n_samples = 0;
  if (!g_session || !text) return nullptr;
  return crispasr_session_synthesize(g_session, text, out_n_samples);
}

void tts_free(float *pcm) {
  if (pcm) crispasr_pcm_free(pcm);
}

int tts_set_voice(const char *name) {
  if (!g_session || !name || !*name) return 0;
  return crispasr_session_set_speaker_name(g_session, name);
}

// tts_set_voice_file loads a voice from a file: a .gguf path selects a voice
// pack, a .wav path with a non-empty ref_text performs zero-shot voice cloning
// (the C API returns -2 when ref_text is required but missing). Returns -1 when
// no session is open or path is null.
int tts_set_voice_file(const char *path, const char *ref_text) {
  if (!g_session || !path) return -1;
  const char *ref = (ref_text && *ref_text) ? ref_text : nullptr;
  return crispasr_session_set_voice(g_session, path, ref);
}
