#ifdef __cplusplus
extern "C" {
#endif
int load_model(char *model, char *model_path, char* options[], int threads, int diffusionModel);
int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed, char *dst, float cfg_scale, char *src_image, float strength, char *mask_image, char **ref_images, int ref_images_count);
#ifdef __cplusplus
}
#endif