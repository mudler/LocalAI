#include "ggml-backend.h"
#include "whisper.h"
#include <vector>
#include "gowhisper.h"

static struct whisper_vad_context *vctx;
static std::vector<float> flat_segs;

int load_model(const char *const model_path) {
  ggml_backend_load_all();

  // struct whisper_context_params cparams = whisper_context_default_params();
  struct whisper_vad_context_params vcparams = whisper_vad_default_context_params();

  // XXX: Overridden to false in upstream due to performance?
  // vcparams.use_gpu = true;

  vctx = whisper_vad_init_from_file_with_params(model_path, vcparams);
  if (vctx == nullptr) {
    fprintf(stderr, "error: Failed to init VAD model\n");
    return 1;
  }

  return 0;
}

int vad(float pcmf32[], size_t pcmf32_size, float **segs_out, size_t *segs_out_len) {
  if (!whisper_vad_detect_speech(vctx, pcmf32, pcmf32_size)) {
    fprintf(stderr, "error: failed to detect speech\n");
    return 1;
  }

  struct whisper_vad_params params = whisper_vad_default_params();
  struct whisper_vad_segments *segs = whisper_vad_segments_from_probs(vctx, params);
  size_t segn = whisper_vad_segments_n_segments(segs);

  flat_segs.clear();

  for (int i = 0; i < segn; i++) {
    flat_segs.push_back(whisper_vad_segments_get_segment_t0(segs, i));
    flat_segs.push_back(whisper_vad_segments_get_segment_t1(segs, i));
  }

  *segs_out = flat_segs.data();
  *segs_out_len = flat_segs.size();

  whisper_vad_free_segments(segs);

  return 0;
}
