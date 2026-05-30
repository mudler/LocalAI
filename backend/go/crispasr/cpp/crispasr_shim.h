#include <cstddef>
#include <cstdint>

extern "C" {
int load_model(const char *const model_path, int threads);
int load_model_vad(const char *const model_path);
int vad(float pcmf32[], size_t pcmf32_size, float **segs_out,
        size_t *segs_out_len);
int transcribe(uint32_t threads, char *lang, bool translate, bool diarize,
               float pcmf32[], size_t pcmf32_len, size_t *segs_out_len,
               char *prompt);
const char *get_segment_text(int i);
int64_t get_segment_t0(int i);
int64_t get_segment_t1(int i);
const char *get_backend(void);
void set_abort(int v);
}
