#include "govoxtral.h"
#include "voxtral.h"
#include "voxtral_audio.h"
#ifdef USE_METAL
#include "voxtral_metal.h"
#endif
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

static vox_ctx_t *ctx = NULL;
static char *last_result = NULL;
static int metal_initialized = 0;

int load_model(const char *model_dir) {
    if (ctx != NULL) {
        vox_free(ctx);
        ctx = NULL;
    }

#ifdef USE_METAL
    if (!metal_initialized) {
        vox_metal_init();
        metal_initialized = 1;
    }
#endif

    ctx = vox_load(model_dir);
    if (ctx == NULL) {
        fprintf(stderr, "error: failed to load voxtral model from %s\n", model_dir);
        return 1;
    }

    return 0;
}

const char *transcribe(const char *wav_path) {
    if (ctx == NULL) {
        fprintf(stderr, "error: model not loaded\n");
        return "";
    }

    if (last_result != NULL) {
        free(last_result);
        last_result = NULL;
    }

    last_result = vox_transcribe(ctx, wav_path);
    if (last_result == NULL) {
        fprintf(stderr, "error: transcription failed for %s\n", wav_path);
        return "";
    }

    return last_result;
}

void free_result(void) {
    if (last_result != NULL) {
        free(last_result);
        last_result = NULL;
    }
}
