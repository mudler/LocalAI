#include <cstdint>
#include "stable-diffusion.h"

#ifdef __cplusplus
extern "C" {
#endif

void sd_tiling_params_set_enabled(sd_tiling_params_t *params, bool enabled);
void sd_tiling_params_set_tile_sizes(sd_tiling_params_t *params, int tile_size_x, int tile_size_y);
void sd_tiling_params_set_rel_sizes(sd_tiling_params_t *params, float rel_size_x, float rel_size_y);
void sd_tiling_params_set_target_overlap(sd_tiling_params_t *params, float target_overlap);
sd_tiling_params_t* sd_img_gen_params_get_vae_tiling_params(sd_img_gen_params_t *params);

sd_img_gen_params_t* sd_img_gen_params_new(void);
void sd_img_gen_params_set_prompts(sd_img_gen_params_t *params, const char *prompt, const char *negative_prompt);
void sd_img_gen_params_set_dimensions(sd_img_gen_params_t *params, int width, int height);
void sd_img_gen_params_set_seed(sd_img_gen_params_t *params, int64_t seed);

int load_model(const char *model, char *model_path, char* options[], int threads, int diffusionModel);
int gen_image(sd_img_gen_params_t *p, int steps, char *dst, float cfg_scale, char *src_image, float strength, char *mask_image, char* ref_images[], int ref_images_count);

sd_vid_gen_params_t* sd_vid_gen_params_new(void);
void sd_vid_gen_params_set_prompts(sd_vid_gen_params_t *params, const char *prompt, const char *negative_prompt);
void sd_vid_gen_params_set_dimensions(sd_vid_gen_params_t *params, int width, int height);
void sd_vid_gen_params_set_seed(sd_vid_gen_params_t *params, int64_t seed);
void sd_vid_gen_params_set_video_frames(sd_vid_gen_params_t *params, int n);
int gen_video(sd_vid_gen_params_t *p, int steps, char *dst, float cfg_scale, int fps, char *init_image, char *end_image);
#ifdef __cplusplus
}
#endif
