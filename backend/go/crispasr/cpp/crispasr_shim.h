#include <cstddef>
#include <cstdint>

extern "C" {
int load_model(const char *const model_path);
int load_model_vad(const char *const model_path);
int vad(float pcmf32[], size_t pcmf32_size, float **segs_out,
        size_t *segs_out_len);
int transcribe(uint32_t threads, char *lang, bool translate, bool tdrz,
               float pcmf32[], size_t pcmf32_len, size_t *segs_out_len,
               char *prompt);
const char *get_segment_text(int i);
int64_t get_segment_t0(int i);
int64_t get_segment_t1(int i);
int n_tokens(int i);
int32_t get_token_id(int i, int j);
bool get_segment_speaker_turn_next(int i);
void set_abort(int v);

// Function pointer from Go (returned by purego.NewCallback). Invoked once
// per new-segment event during whisper_full(). The callback runs on the
// decode thread - if Go blocks (slow gRPC consumer), the decode blocks
// too. That is the intended backpressure path.
typedef void (*go_new_segment_cb)(int idx_first, int n_new, uintptr_t user_data);

// Install the callback used by the next transcribe() call. Pass cb=0 to
// clear. user_data is opaque to C; the Go side uses it to look up
// per-call state.
void set_new_segment_callback(uintptr_t cb_ptr, uintptr_t user_data);
}
