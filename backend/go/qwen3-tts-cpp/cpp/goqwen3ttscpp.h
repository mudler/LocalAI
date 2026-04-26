#pragma once

#include <cstddef>
#include <cstdint>

extern "C" {
int load_model(const char *model_dir, int n_threads);
int synthesize(const char *text, const char *ref_audio_path, const char *dst,
               const char *language, float temperature, float top_p,
               int top_k, float repetition_penalty, int max_audio_tokens,
               int n_threads);
}
