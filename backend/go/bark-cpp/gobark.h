#ifdef __cplusplus
extern "C" {
#endif
int load_model(char *model);
int tts(char *text,int  threads, char *dst );
#ifdef __cplusplus
}
#endif