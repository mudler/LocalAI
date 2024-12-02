#ifdef __cplusplus
extern "C" {
#endif
int load_model(char *model, char* options[], int threads, int diffusionModel);
int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed, char *dst, float cfg_scale);
#ifdef __cplusplus
}
#endif