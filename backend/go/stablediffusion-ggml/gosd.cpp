#define GGML_MAX_NAME 128

#include <stdio.h>
#include <string.h>
#include <time.h>
#include <iostream>
#include <random>
#include <string>
#include <vector>
#include <filesystem>
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

int load_model(char *model, char *model_path, char* options[], int threads, int diff) {
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
    char *lora_dir = model_path;
    bool lora_dir_allocated = false;

    fprintf(stderr, "parsing options\n");

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
        if (!strcmp(optname, "lora_dir")) {
            // Path join with model dir
            if (model_path && strlen(model_path) > 0) {
                std::filesystem::path model_path_str(model_path);
                std::filesystem::path lora_path(optval);
                std::filesystem::path full_lora_path = model_path_str / lora_path;
                lora_dir = strdup(full_lora_path.string().c_str());
                lora_dir_allocated = true;
                fprintf(stderr, "Lora dir resolved to: %s\n", lora_dir);
            } else {
                lora_dir = optval;
                fprintf(stderr, "No model path provided, using lora dir as-is: %s\n", lora_dir);
            }
        }
    }

    fprintf(stderr, "parsed options\n");

    int sample_method_found = -1;
    for (int m = 0; m < SAMPLE_METHOD_COUNT; m++) {
        if (!strcmp(sampler, sample_method_str[m])) {
            sample_method_found = m;
            fprintf(stderr, "Found sampler: %s\n", sampler);
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
    ctx_params.lora_model_dir = lora_dir;
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
        // Clean up allocated memory
        if (lora_dir_allocated && lora_dir) {
            free(lora_dir);
        }
        return 1;
    }
    fprintf (stderr, "Created context: OK\n");

    sd_c = sd_ctx;

    // Clean up allocated memory
    if (lora_dir_allocated && lora_dir) {
        free(lora_dir);
    }

    return 0;
}

int gen_image(char *text, char *negativeText, int width, int height, int steps, int seed , char *dst, float cfg_scale, char *src_image, float strength, char *mask_image, char **ref_images, int ref_images_count) {

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

    // Handle input image for img2img
    bool has_input_image = (src_image != NULL && strlen(src_image) > 0);
    bool has_mask_image = (mask_image != NULL && strlen(mask_image) > 0);
    
    uint8_t* input_image_buffer = NULL;
    uint8_t* mask_image_buffer = NULL;
    std::vector<uint8_t> default_mask_image_vec;
    
    if (has_input_image) {
        fprintf(stderr, "Loading input image: %s\n", src_image);
        
        int c = 0;
        int img_width = 0;
        int img_height = 0;
        input_image_buffer = stbi_load(src_image, &img_width, &img_height, &c, 3);
        if (input_image_buffer == NULL) {
            fprintf(stderr, "Failed to load input image from '%s'\n", src_image);
            return 1;
        }
        if (c < 3) {
            fprintf(stderr, "Input image must have at least 3 channels, got %d\n", c);
            free(input_image_buffer);
            return 1;
        }
        
        // Resize input image if dimensions don't match
        if (img_width != width || img_height != height) {
            fprintf(stderr, "Resizing input image from %dx%d to %dx%d\n", img_width, img_height, width, height);
            
            uint8_t* resized_image_buffer = (uint8_t*)malloc(height * width * 3);
            if (resized_image_buffer == NULL) {
                fprintf(stderr, "Failed to allocate memory for resized image\n");
                free(input_image_buffer);
                return 1;
            }
            
            stbir_resize(input_image_buffer, img_width, img_height, 0,
                         resized_image_buffer, width, height, 0, STBIR_TYPE_UINT8,
                         3, STBIR_ALPHA_CHANNEL_NONE, 0,
                         STBIR_EDGE_CLAMP, STBIR_EDGE_CLAMP,
                         STBIR_FILTER_BOX, STBIR_FILTER_BOX,
                         STBIR_COLORSPACE_SRGB, nullptr);
            
            free(input_image_buffer);
            input_image_buffer = resized_image_buffer;
        }
        
        p.init_image = {(uint32_t)width, (uint32_t)height, 3, input_image_buffer};
        p.strength = strength;
        fprintf(stderr, "Using img2img with strength: %.2f\n", strength);
    } else {
        // No input image, use empty image for text-to-image
        p.init_image = {(uint32_t)width, (uint32_t)height, 3, NULL};
        p.strength = 0.0f;
    }
    
    // Handle mask image for inpainting
    if (has_mask_image) {
        fprintf(stderr, "Loading mask image: %s\n", mask_image);
        
        int c = 0;
        int mask_width = 0;
        int mask_height = 0;
        mask_image_buffer = stbi_load(mask_image, &mask_width, &mask_height, &c, 1);
        if (mask_image_buffer == NULL) {
            fprintf(stderr, "Failed to load mask image from '%s'\n", mask_image);
            if (input_image_buffer) free(input_image_buffer);
            return 1;
        }
        
        // Resize mask if dimensions don't match
        if (mask_width != width || mask_height != height) {
            fprintf(stderr, "Resizing mask image from %dx%d to %dx%d\n", mask_width, mask_height, width, height);
            
            uint8_t* resized_mask_buffer = (uint8_t*)malloc(height * width);
            if (resized_mask_buffer == NULL) {
                fprintf(stderr, "Failed to allocate memory for resized mask\n");
                free(mask_image_buffer);
                if (input_image_buffer) free(input_image_buffer);
                return 1;
            }
            
            stbir_resize(mask_image_buffer, mask_width, mask_height, 0,
                         resized_mask_buffer, width, height, 0, STBIR_TYPE_UINT8,
                         1, STBIR_ALPHA_CHANNEL_NONE, 0,
                         STBIR_EDGE_CLAMP, STBIR_EDGE_CLAMP,
                         STBIR_FILTER_BOX, STBIR_FILTER_BOX,
                         STBIR_COLORSPACE_SRGB, nullptr);
            
            free(mask_image_buffer);
            mask_image_buffer = resized_mask_buffer;
        }
        
        p.mask_image = {(uint32_t)width, (uint32_t)height, 1, mask_image_buffer};
        fprintf(stderr, "Using inpainting with mask\n");
    } else {
        // No mask image, create default full mask
        default_mask_image_vec.resize(width * height, 255);
        p.mask_image = {(uint32_t)width, (uint32_t)height, 1, default_mask_image_vec.data()};
    }

    // Handle reference images
    std::vector<sd_image_t> ref_images_vec;
    std::vector<uint8_t*> ref_image_buffers;
    
    if (ref_images_count > 0 && ref_images != NULL) {
        fprintf(stderr, "Loading %d reference images\n", ref_images_count);
        
        for (int i = 0; i < ref_images_count; i++) {
            if (ref_images[i] == NULL || strlen(ref_images[i]) == 0) {
                continue;
            }
            
            fprintf(stderr, "Loading reference image %d: %s\n", i + 1, ref_images[i]);
            
            int c = 0;
            int ref_width = 0;
            int ref_height = 0;
            uint8_t* ref_image_buffer = stbi_load(ref_images[i], &ref_width, &ref_height, &c, 3);
            if (ref_image_buffer == NULL) {
                fprintf(stderr, "Failed to load reference image from '%s'\n", ref_images[i]);
                continue;
            }
            if (c < 3) {
                fprintf(stderr, "Reference image must have at least 3 channels, got %d\n", c);
                free(ref_image_buffer);
                continue;
            }
            
            // Resize reference image if dimensions don't match
            if (ref_width != width || ref_height != height) {
                fprintf(stderr, "Resizing reference image from %dx%d to %dx%d\n", ref_width, ref_height, width, height);
                
                uint8_t* resized_ref_buffer = (uint8_t*)malloc(height * width * 3);
                if (resized_ref_buffer == NULL) {
                    fprintf(stderr, "Failed to allocate memory for resized reference image\n");
                    free(ref_image_buffer);
                    continue;
                }
                
                stbir_resize(ref_image_buffer, ref_width, ref_height, 0,
                             resized_ref_buffer, width, height, 0, STBIR_TYPE_UINT8,
                             3, STBIR_ALPHA_CHANNEL_NONE, 0,
                             STBIR_EDGE_CLAMP, STBIR_EDGE_CLAMP,
                             STBIR_FILTER_BOX, STBIR_FILTER_BOX,
                             STBIR_COLORSPACE_SRGB, nullptr);
                
                free(ref_image_buffer);
                ref_image_buffer = resized_ref_buffer;
            }
            
            ref_image_buffers.push_back(ref_image_buffer);
            ref_images_vec.push_back({(uint32_t)width, (uint32_t)height, 3, ref_image_buffer});
        }
        
        if (!ref_images_vec.empty()) {
            p.ref_images = ref_images_vec.data();
            p.ref_images_count = ref_images_vec.size();
            fprintf(stderr, "Using %zu reference images\n", ref_images_vec.size());
        }
    }

    results = generate_image(sd_c, &p);

    if (results == NULL) {
        fprintf (stderr, "NO results\n");
        if (input_image_buffer) free(input_image_buffer);
        if (mask_image_buffer) free(mask_image_buffer);
        for (auto buffer : ref_image_buffers) {
            if (buffer) free(buffer);
        }
        return 1;
    }

    if (results[0].data == NULL) {
        fprintf (stderr, "Results with no data\n");
        if (input_image_buffer) free(input_image_buffer);
        if (mask_image_buffer) free(mask_image_buffer);
        for (auto buffer : ref_image_buffers) {
            if (buffer) free(buffer);
        }
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

    // Clean up
    free(results[0].data);
    results[0].data = NULL;
    free(results);
    if (input_image_buffer) free(input_image_buffer);
    if (mask_image_buffer) free(mask_image_buffer);
    for (auto buffer : ref_image_buffers) {
        if (buffer) free(buffer);
    }
    fprintf (stderr, "gen_image is done", dst);

    return 0;
}

int unload() {
    free_sd_ctx(sd_c);
}

