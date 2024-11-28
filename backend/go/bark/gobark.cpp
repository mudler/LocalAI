#include <iostream>
#include <tuple>

#include "bark.h"
#include "gobark.h"
#include "common.h"
#include "ggml.h"

struct bark_context *c;

void bark_print_progress_callback(struct bark_context *bctx, enum bark_encoding_step step, int progress, void *user_data) {
    if (step == bark_encoding_step::SEMANTIC) {
        printf("\rGenerating semantic tokens... %d%%", progress);
    } else if (step == bark_encoding_step::COARSE) {
        printf("\rGenerating coarse tokens... %d%%", progress);
    } else if (step == bark_encoding_step::FINE) {
        printf("\rGenerating fine tokens... %d%%", progress);
    }
    fflush(stdout);
}

int load_model(char *model) {
    // initialize bark context
    struct bark_context_params ctx_params = bark_context_default_params();
    bark_params params;

    params.model_path = model;

   // ctx_params.verbosity = verbosity;
    ctx_params.progress_callback = bark_print_progress_callback;
    ctx_params.progress_callback_user_data = nullptr;

    struct bark_context *bctx = bark_load_model(params.model_path.c_str(), ctx_params, params.seed);
    if (!bctx) {
        fprintf(stderr, "%s: Could not load model\n", __func__);
        return 1;
    }

    c = bctx;

    return 0;
}

int tts(char *text,int  threads, char *dst ) {

    ggml_time_init();
    const int64_t t_main_start_us = ggml_time_us();

    // generate audio
    if (!bark_generate_audio(c, text, threads)) {
        fprintf(stderr, "%s: An error occured. If the problem persists, feel free to open an issue to report it.\n", __func__);
        return 1;
    }

    const float *audio_data = bark_get_audio_data(c);
    if (audio_data == NULL) {
        fprintf(stderr, "%s: Could not get audio data\n", __func__);
        return 1;
    }

    const int audio_arr_size = bark_get_audio_data_size(c);

    std::vector<float> audio_arr(audio_data, audio_data + audio_arr_size);

    write_wav_on_disk(audio_arr, dst);

    // report timing
    {
        const int64_t t_main_end_us = ggml_time_us();
        const int64_t t_load_us = bark_get_load_time(c);
        const int64_t t_eval_us = bark_get_eval_time(c);

        printf("\n\n");
        printf("%s:     load time = %8.2f ms\n", __func__, t_load_us / 1000.0f);
        printf("%s:     eval time = %8.2f ms\n", __func__, t_eval_us / 1000.0f);
        printf("%s:    total time = %8.2f ms\n", __func__, (t_main_end_us - t_main_start_us) / 1000.0f);
    }
    
    return 0;
}

int unload() {
    bark_free(c);
}

