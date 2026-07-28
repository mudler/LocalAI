#include <cstddef>
#include <cstdint>

extern "C" {
int load_model(const char *const model_path, int threads,
               const char *backend_name);
int set_codec_path(const char *path);
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
float *tts_synthesize(const char *text, int *out_n_samples); // 24kHz mono float, malloc'd; NULL on failure
void tts_free(float *pcm);
int tts_set_voice(const char *name); // best-effort speaker selection; 0 ok
int tts_set_voice_file(const char *path, const char *ref_text); // load voice pack (.gguf) or zero-shot clone (.wav + ref_text)

// --- word-level timestamp accessors ---
// Session-based (works for whisper-like backends)
void *get_result(void);
int get_word_count(int seg_i);
const char *get_word_text(int seg_i, int word_i);
int64_t get_word_t0(int seg_i, int word_i);
int64_t get_word_t1(int seg_i, int word_i);

// Parakeet-specific (global word list, no segment index)
int get_parakeet_word_count(void);
const char *get_parakeet_word_text(int word_i);
int64_t get_parakeet_word_t0(int word_i);
int64_t get_parakeet_word_t1(int word_i);
}
