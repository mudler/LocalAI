#include "ggml-backend.h"
#include "whisper.h"
#include "gowhisper.h"

static struct whisper_vad_context *vctx;

int load_model(const char *const model_path) {
  ggml_backend_load_all();

  // struct whisper_context_params cparams = whisper_context_default_params();
  struct whisper_vad_context_params vcparams = whisper_vad_default_context_params();

  // XXX: Overridden to false in upstream due to performance?
  vcparams.use_gpu = true;

  vctx = whisper_vad_init_from_file_with_params(model_path, vcparams);
  if (vctx == nullptr) {
    fprintf(stderr, "error: Failed to init VAD model\n");
    return 1;
  }

  return 0;
}

int vad() {
  return 0;
}
