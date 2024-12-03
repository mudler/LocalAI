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

int load_model(char *model, char* options[], int threads, int diff) {
    fprintf (stderr, "Loading model!\n");

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
    for (int m = 0; m < N_SAMPLE_METHODS; m++) {
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
    for (int d = 0; d < N_SCHEDULES; d++) {
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
    sd_ctx_t* sd_ctx = new_sd_ctx(model,
                                  clip_l_path,
                                  clip_g_path,
                                  t5xxl_path,
                                  stableDiffusionModel,
                                  vae_path,
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  false,
                                  false,
                                  false,
                                  threads,
                                  SD_TYPE_COUNT,
                                  STD_DEFAULT_RNG,
                                  schedule,
                                  false,
                                  false,
                                  false,
                                  false);

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

    results = txt2img(sd_c,
                            text,
                            negativeText,
                            -1, //clip_skip
                            cfg_scale, // sfg_scale
                            3.5f,
                            width,
                            height,
                            sample_method, 
                            steps,
                            seed,
                            1,
                            NULL,
                            0.9f,
                            20.f,
                            false,
                            "",
                            skip_layers.data(),
                            skip_layers.size(),
                            0,
                            0.01,
                            0.2);

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

