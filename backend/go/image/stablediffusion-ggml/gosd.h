#ifdef __cplusplus
extern "C" {
#endif
int load_model(char *model, char *schedule_selected, int threads);
int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed , char* sample_method_selected, char *dst );
#ifdef __cplusplus
}
#endif