#include <cstddef>
#include <cstdint>

extern "C" {
int load_model(const char *lm_model_path, const char *text_encoder_path,
               const char *dit_model_path, const char *vae_model_path);
int generate_music(const char *caption, const char *lyrics, int bpm,
                   const char *keyscale, const char *timesignature,
                   float duration, float temperature, bool instrumental,
                   int seed, const char *dst, int threads);
}
