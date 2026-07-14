#ifndef GOVOXTRAL_H
#define GOVOXTRAL_H

extern int load_model(const char *model_dir);
extern const char *transcribe(const char *wav_path);
extern void free_result(void);

#endif /* GOVOXTRAL_H */
