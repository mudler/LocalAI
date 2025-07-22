#include <stdio.h>
#include <string.h>
#include <time.h>
#include <iostream>
#include <random>
#include <string>
#include <vector>
#include "gosd.h"

// #include "preprocessing.hpp"
#include "flux.hpp"
#include "stable-diffusion.h"

#define STB_IMAGE_IMPLEMENTATION
#define STB_IMAGE_STATIC
#include "stb_image.h"

#define STB_IMAGE_WRITE_IMPLEMENTATION
#define STB_IMAGE_WRITE_STATIC
#include "stb_image_write.h"

#define STB_IMAGE_RESIZE_IMPLEMENTATION
#define STB_IMAGE_RESIZE_STATIC
#include "stb_image_resize.h"

// Names of the sampler method, same order as enum sample_method in stable-diffusion.h
const char* sample_method_str[] = {
    "euler_a",
    "euler",
    "heun",
    "dpm2",
    "dpm++2s_a",
    "dpm++2m",
    "dpm++2mv2",
    "ipndm",
    "ipndm_v",
    "lcm",
    "ddim_trailing",
    "tcd",
};

// Names of the sigma schedule overrides, same order as sample_schedule in stable-diffusion.h
const char* schedule_str[] = {
    "default",
    "discrete",
    "karras",
    "exponential",
    "ays",
    "gits",
};

sd_ctx_t* sd_c;

sample_method_t sample_method;

// Copied from the upstream CLI
void sd_log_cb(enum sd_log_level_t level, const char* log, void* data) {
    //SDParams* params = (SDParams*)data;
    const char* level_str;

    if (!log /*|| (!params->verbose && level <= SD_LOG_DEBUG)*/) {
        return;
    }

    switch (level) {
        case SD_LOG_DEBUG:
            level_str = "DEBUG";
            break;
        case SD_LOG_INFO:
            level_str = "INFO";
            break;
        case SD_LOG_WARN:
            level_str = "WARN";
            break;
        case SD_LOG_ERROR:
            level_str = "ERROR";
            break;
        default: /* Potential future-proofing */
            level_str = "?????";
            break;
    }

    fprintf(stderr, "[%-5s] ", level_str);
    fputs(log, stderr);
    fflush(stderr);
}

int load_model(char *model, char* options[], int threads, int diff) {
    fprintf (stderr, "Loading model!\n");

    sd_set_log_callback(sd_log_cb, NULL);

    char *stableDiffusionModel = "";
    if (diff == 1 ) {
        stableDiffusionModel = model;
        model = "";
    }

    // decode options. Options are in form optname:optvale, or if booleans only optname.
    char *clip_l_path  = "";
    char *clip_g_path  = "";
    char *t5xxl_path  = "";
    char *vae_path  = "";
    char *scheduler = "";
    char *sampler = "";

    // If options is not NULL, parse options
    for (int i = 0; options[i] != NULL; i++) {
        char *optname = strtok(options[i], ":");
        char *optval = strtok(NULL, ":");
        if (optval == NULL) {
            optval = "true";
        }

        if (!strcmp(optname, "clip_l_path")) {
            clip_l_path = optval;
        }
        if (!strcmp(optname, "clip_g_path")) {
            clip_g_path = optval;
        }
        if (!strcmp(optname, "t5xxl_path")) {
            t5xxl_path = optval;
        }
        if (!strcmp(optname, "vae_path")) {
            vae_path = optval;
        }
        if (!strcmp(optname, "scheduler")) {
            scheduler = optval;
        }
        if (!strcmp(optname, "sampler")) {
            sampler = optval;
        }
    }

    int sample_method_found = -1;
    for (int m = 0; m < SAMPLE_METHOD_COUNT; m++) {
        if (!strcmp(sampler, sample_method_str[m])) {
            sample_method_found = m;
        }
    }
    if (sample_method_found == -1) {
        fprintf(stderr, "Invalid sample method, default to EULER_A!\n");
        sample_method_found = EULER_A;
    }
    sample_method = (sample_method_t)sample_method_found;

    int schedule_found            = -1;
    for (int d = 0; d < SCHEDULE_COUNT; d++) {
        if (!strcmp(scheduler, schedule_str[d])) {
            schedule_found = d;
                fprintf (stderr, "Found scheduler: %s\n", scheduler);

        }
    }

    if (schedule_found == -1) {
        fprintf (stderr, "Invalid scheduler! using DEFAULT\n");
        schedule_found = DEFAULT;
    }

    schedule_t schedule = (schedule_t)schedule_found;

    fprintf (stderr, "Creating context\n");
    sd_ctx_params_t ctx_params;
    sd_ctx_params_init(&ctx_params);
    ctx_params.model_path = model;
    ctx_params.clip_l_path = clip_l_path;
    ctx_params.clip_g_path = clip_g_path;
    ctx_params.t5xxl_path = t5xxl_path;
    ctx_params.diffusion_model_path = stableDiffusionModel;
    ctx_params.vae_path = vae_path;
    ctx_params.taesd_path = "";
    ctx_params.control_net_path = "";
    ctx_params.lora_model_dir = "";
    ctx_params.embedding_dir = "";
    ctx_params.stacked_id_embed_dir = "";
    ctx_params.vae_decode_only = false;
    ctx_params.vae_tiling = false;
    ctx_params.free_params_immediately = false;
    ctx_params.n_threads = threads;
    ctx_params.rng_type = STD_DEFAULT_RNG;
    ctx_params.schedule = schedule;
    sd_ctx_t* sd_ctx = new_sd_ctx(&ctx_params);

    if (sd_ctx == NULL) {
        fprintf (stderr, "failed loading model (generic error)\n");
        return 1;
    }
    fprintf (stderr, "Created context: OK\n");

    sd_c = sd_ctx;

    return 0;
}

int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed , char *dst, float cfg_scale) {

    sd_image_t* results;

    std::vector<int> skip_layers = {7, 8, 9};

    fprintf (stderr, "Generating image\n");

    sd_img_gen_params_t p;
    sd_img_gen_params_init(&p);

    p.prompt = text;
    p.negative_prompt = negativeText;
    p.guidance.txt_cfg = cfg_scale;
    p.guidance.slg.layers = skip_layers.data();
    p.guidance.slg.layer_count = skip_layers.size();
    p.width = width;
    p.height = height;
    p.sample_method = sample_method;
    p.sample_steps = steps;
    p.seed = seed;
    p.input_id_images_path = "";

    results = generate_image(sd_c, &p);

    if (results == NULL) {
        fprintf (stderr, "NO results\n");
        return 1;
    }

    if (results[0].data == NULL) {
        fprintf (stderr, "Results with no data\n");
        return 1;
    }

    fprintf (stderr, "Writing PNG\n");

    fprintf (stderr, "DST: %s\n", dst);
    fprintf (stderr, "Width: %d\n", results[0].width);
    fprintf (stderr, "Height: %d\n", results[0].height);
    fprintf (stderr, "Channel: %d\n", results[0].channel);
    fprintf (stderr, "Data: %p\n", results[0].data);

    stbi_write_png(dst, results[0].width, results[0].height, results[0].channel,
                       results[0].data, 0, NULL);
    fprintf (stderr, "Saved resulting image to '%s'\n", dst);

    // TODO: free results. Why does it crash?

    free(results[0].data);
    results[0].data = NULL;
    free(results);
    fprintf (stderr, "gen_image is done", dst);

    return 0;
}

int unload() {
    free_sd_ctx(sd_c);
}

