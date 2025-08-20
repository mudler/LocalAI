#include <cstddef>

extern "C" {
  int load_model(const char *const model_path);
  int vad(float pcmf32[], size_t pcmf32_size, float **segs_out, size_t *segs_out_len);
}
