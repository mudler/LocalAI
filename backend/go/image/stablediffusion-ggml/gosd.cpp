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



const char* rng_type_to_str[] = {
    "std_default",
    "cuda",
};

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

const char* modes_str[] = {
    "txt2img",
    "img2img",
    "img2vid",
    "convert",
};

enum SDMode {
    TXT2IMG,
    IMG2IMG,
    IMG2VID,
    CONVERT,
    MODE_COUNT
};

struct SDParams {
    int n_threads = -1;
    SDMode mode   = TXT2IMG;
    std::string model_path;
    std::string clip_l_path;
    std::string clip_g_path;
    std::string t5xxl_path;
    std::string diffusion_model_path;
    std::string vae_path;
    std::string taesd_path;
    std::string esrgan_path;
    std::string controlnet_path;
    std::string embeddings_path;
    std::string stacked_id_embeddings_path;
    std::string input_id_images_path;
    sd_type_t wtype = SD_TYPE_COUNT;
    std::string lora_model_dir;
    std::string output_path = "output.png";
    std::string input_path;
    std::string control_image_path;

    std::string prompt;
    std::string negative_prompt;
    float min_cfg     = 1.0f;
    float cfg_scale   = 7.0f;
    float guidance    = 3.5f;
    float style_ratio = 20.f;
    int clip_skip     = -1;  // <= 0 represents unspecified
    int width         = 512;
    int height        = 512;
    int batch_count   = 1;

    int video_frames         = 6;
    int motion_bucket_id     = 127;
    int fps                  = 6;
    float augmentation_level = 0.f;

    sample_method_t sample_method = EULER_A;
    schedule_t schedule           = DEFAULT;
    int sample_steps              = 20;
    float strength                = 0.75f;
    float control_strength        = 0.9f;
    rng_type_t rng_type           = CUDA_RNG;
    int64_t seed                  = 42;
    bool verbose                  = false;
    bool vae_tiling               = false;
    bool control_net_cpu          = false;
    bool normalize_input          = false;
    bool clip_on_cpu              = false;
    bool vae_on_cpu               = false;
    bool diffusion_flash_attn     = false;
    bool canny_preprocess         = false;
    bool color                    = false;
    int upscale_repeats           = 1;

    std::vector<int> skip_layers = {7, 8, 9};
    float slg_scale              = 0.;
    float skip_layer_start       = 0.01;
    float skip_layer_end         = 0.2;
};

void print_params(SDParams params) {
    printf("Option: \n");
    printf("    n_threads:         %d\n", params.n_threads);
    printf("    mode:              %s\n", modes_str[params.mode]);
    printf("    model_path:        %s\n", params.model_path.c_str());
    printf("    wtype:             %s\n", params.wtype < SD_TYPE_COUNT ? sd_type_name(params.wtype) : "unspecified");
    printf("    clip_l_path:       %s\n", params.clip_l_path.c_str());
    printf("    clip_g_path:       %s\n", params.clip_g_path.c_str());
    printf("    t5xxl_path:        %s\n", params.t5xxl_path.c_str());
    printf("    diffusion_model_path:   %s\n", params.diffusion_model_path.c_str());
    printf("    vae_path:          %s\n", params.vae_path.c_str());
    printf("    taesd_path:        %s\n", params.taesd_path.c_str());
    printf("    esrgan_path:       %s\n", params.esrgan_path.c_str());
    printf("    controlnet_path:   %s\n", params.controlnet_path.c_str());
    printf("    embeddings_path:   %s\n", params.embeddings_path.c_str());
    printf("    stacked_id_embeddings_path:   %s\n", params.stacked_id_embeddings_path.c_str());
    printf("    input_id_images_path:   %s\n", params.input_id_images_path.c_str());
    printf("    style ratio:       %.2f\n", params.style_ratio);
    printf("    normalize input image :  %s\n", params.normalize_input ? "true" : "false");
    printf("    output_path:       %s\n", params.output_path.c_str());
    printf("    init_img:          %s\n", params.input_path.c_str());
    printf("    control_image:     %s\n", params.control_image_path.c_str());
    printf("    clip on cpu:       %s\n", params.clip_on_cpu ? "true" : "false");
    printf("    controlnet cpu:    %s\n", params.control_net_cpu ? "true" : "false");
    printf("    vae decoder on cpu:%s\n", params.vae_on_cpu ? "true" : "false");
    printf("    diffusion flash attention:%s\n", params.diffusion_flash_attn ? "true" : "false");
    printf("    strength(control): %.2f\n", params.control_strength);
    printf("    prompt:            %s\n", params.prompt.c_str());
    printf("    negative_prompt:   %s\n", params.negative_prompt.c_str());
    printf("    min_cfg:           %.2f\n", params.min_cfg);
    printf("    cfg_scale:         %.2f\n", params.cfg_scale);
    printf("    slg_scale:         %.2f\n", params.slg_scale);
    printf("    guidance:          %.2f\n", params.guidance);
    printf("    clip_skip:         %d\n", params.clip_skip);
    printf("    width:             %d\n", params.width);
    printf("    height:            %d\n", params.height);
    printf("    sample_method:     %s\n", sample_method_str[params.sample_method]);
    printf("    schedule:          %s\n", schedule_str[params.schedule]);
    printf("    sample_steps:      %d\n", params.sample_steps);
    printf("    strength(img2img): %.2f\n", params.strength);
    printf("    rng:               %s\n", rng_type_to_str[params.rng_type]);
    printf("    seed:              %ld\n", params.seed);
    printf("    batch_count:       %d\n", params.batch_count);
    printf("    vae_tiling:        %s\n", params.vae_tiling ? "true" : "false");
    printf("    upscale_repeats:   %d\n", params.upscale_repeats);
}


 sd_ctx_t* sd_c;

int load_model(char *model, char *schedule_selected, int threads) {
    fprintf (stderr, "Loading model!\n");

    int schedule_found            = -1;
    for (int d = 0; d < N_SCHEDULES; d++) {
        if (!strcmp(schedule_selected, schedule_str[d])) {
            schedule_found = d;
        }
    }
    if (schedule_found == -1) {
        fprintf (stderr, "Invalid scheduler! using DEFAULT\n");
        schedule_found = DEFAULT;
    }


    schedule_t schedule = (schedule_t)schedule_found;

    sd_ctx_t* sd_ctx = new_sd_ctx(model,
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  "",
                                  false,
                                  false,
                                  true,
                                  threads,
                                  SD_TYPE_COUNT,
                                  STD_DEFAULT_RNG,
                                  schedule,
                                  false,
                                  false,
                                  false,
                                  false);

    if (sd_ctx == NULL) {
        fprintf (stderr, "Null context!\n");
        return 1;
    }

    sd_c = sd_ctx;

    return 0;
}

int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed , char* sample_method_selected, char *dst ) {

    sd_image_t* results;


    int sample_method_found            = -1;
    for (int m = 0; m < N_SAMPLE_METHODS; m++) {
        if (!strcmp(sample_method_selected, sample_method_str[m])) {
            sample_method_found = m;
        }
    }
    if (sample_method_found == -1) {
        fprintf(stderr, "Invalid sample method, default to EULER_A!\n");
        sample_method_found = EULER_A;
        return 1;
    }
    sample_method_t sample_method = (sample_method_t)sample_method_found;
    std::vector<int> skip_layers = {7, 8, 9};
    results = txt2img(sd_c,
                            text,
                            negativeText,
                            -1, //clip_skip
                            7.0f, // sfg_scale
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
        printf("generate failed\n");
        return 1;
    }

    if (results[0].data == NULL) {
        printf("generate failed\n");
        return 1;
    }

  stbi_write_png(dst, results[0].width, results[0].height, results[0].channel,
                       results[0].data, 0, "");
    printf("save result image to '%s'\n", dst);
    free(results[0].data);
    results[0].data = NULL;

    free(results);
}

int unload() {
    free_sd_ctx(sd_c);
}

