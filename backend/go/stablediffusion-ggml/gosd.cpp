#include "stable-diffusion.h"
#include <cmath>
#include <cstdint>
#define GGML_MAX_NAME 128

#include <stdio.h>
#include <string.h>
#include <time.h>
#include <string>
#include <vector>
#include <filesystem>
#include "gosd.h"

#define STB_IMAGE_IMPLEMENTATION
#define STB_IMAGE_STATIC
#include "stb_image.h"

#define STB_IMAGE_WRITE_IMPLEMENTATION
#define STB_IMAGE_WRITE_STATIC
#include "stb_image_write.h"

#define STB_IMAGE_RESIZE_IMPLEMENTATION
#define STB_IMAGE_RESIZE_STATIC
#include "stb_image_resize.h"
#include <stdlib.h>

// Names of the sampler method, same order as enum sample_method in stable-diffusion.h
const char* sample_method_str[] = {
    "euler",
    "euler_a",
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

static_assert(std::size(sample_method_str) == SAMPLE_METHOD_COUNT, "sample method mismatch");

// Names of the sigma schedule overrides, same order as sample_schedule in stable-diffusion.h
const char* schedulers[] = {
    "discrete",
    "karras",
    "exponential",
    "ays",
    "gits",
    "sgm_uniform",
    "simple",
    "smoothstep",
    "lcm",
};

static_assert(std::size(schedulers) == SCHEDULER_COUNT, "schedulers mismatch");

// New enum string arrays
const char* rng_type_str[] = {
    "std_default",
    "cuda",
    "cpu",
};
static_assert(std::size(rng_type_str) == RNG_TYPE_COUNT, "rng type mismatch");

const char* prediction_str[] = {
    "default",
    "epsilon",
    "v",
    "edm_v",
    "sd3_flow",
    "flux_flow",
    "flux2_flow",
};
static_assert(std::size(prediction_str) == PREDICTION_COUNT, "prediction mismatch");

const char* lora_apply_mode_str[] = {
    "auto",
    "immediately",
    "at_runtime",
};
static_assert(std::size(lora_apply_mode_str) == LORA_APPLY_MODE_COUNT, "lora apply mode mismatch");

constexpr const char* sd_type_str[] = {
    "f32",      // 0
    "f16",      // 1
    "q4_0",     // 2
    "q4_1",     // 3
    nullptr,    // 4
    nullptr,    // 5
    "q5_0",     // 6
    "q5_1",     // 7
    "q8_0",     // 8
    "q8_1",     // 9
    "q2_k",     // 10
    "q3_k",     // 11
    "q4_k",     // 12
    "q5_k",     // 13
    "q6_k",     // 14
    "q8_k",     // 15
    "iq2_xxs",  // 16
    "iq2_xs",   // 17
    "iq3_xxs",  // 18
    "iq1_s",    // 19
    "iq4_nl",   // 20
    "iq3_s",    // 21
    "iq2_s",    // 22
    "iq4_xs",   // 23
    "i8",       // 24
    "i16",      // 25
    "i32",      // 26
    "i64",      // 27
    "f64",      // 28
    "iq1_m",    // 29
    "bf16",     // 30
    nullptr, nullptr, nullptr, nullptr,  // 31-34
    "tq1_0",    // 35
    "tq2_0",    // 36
    nullptr, nullptr,           // 37-38
    "mxfp4"     // 39
};
static_assert(std::size(sd_type_str) == SD_TYPE_COUNT, "sd type mismatch");

sd_ctx_params_t ctx_params;
sd_ctx_t* sd_c;
// Moved from the context (load time) to generation time params
scheduler_t scheduler = SCHEDULER_COUNT;
sample_method_t sample_method = SAMPLE_METHOD_COUNT;

// Copied from the upstream CLI
static void sd_log_cb(enum sd_log_level_t level, const char* log, void* data) {
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

int load_model(const char *model, char *model_path, char* options[], int threads, int diff) {
    fprintf (stderr, "Loading model: %p=%s\n", model, model);

    sd_set_log_callback(sd_log_cb, NULL);

    const char *stableDiffusionModel = "";
    if (diff == 1 ) {
        stableDiffusionModel = strdup(model);
        model = "";
    }

    // decode options. Options are in form optname:optvale, or if booleans only optname.
    const char *clip_l_path  = "";
    const char *clip_g_path  = "";
    const char *t5xxl_path  = "";
    const char *vae_path  = "";
    const char *scheduler_str = "";
    const char *sampler = "";
    const char *clip_vision_path = "";
    const char *llm_path = "";
    const char *llm_vision_path = "";
    const char *diffusion_model_path = stableDiffusionModel;
    const char *high_noise_diffusion_model_path = "";
    const char *taesd_path  = "";
    const char *control_net_path = "";
    const char *embedding_dir = "";
    const char *photo_maker_path = "";
    const char *tensor_type_rules = "";
    char *lora_dir = model_path;

    bool vae_decode_only = true;
    int n_threads = threads;
    enum sd_type_t wtype = SD_TYPE_COUNT;
    enum rng_type_t rng_type = CUDA_RNG;
    enum rng_type_t sampler_rng_type = RNG_TYPE_COUNT;
    enum prediction_t prediction = DEFAULT_PRED;
    enum lora_apply_mode_t lora_apply_mode = LORA_APPLY_AUTO;
    bool offload_params_to_cpu = false;
    bool keep_clip_on_cpu = false;
    bool keep_control_net_on_cpu = false;
    bool keep_vae_on_cpu = false;
    bool diffusion_flash_attn = false;
    bool tae_preview_only = false;
    bool diffusion_conv_direct = false;
    bool vae_conv_direct = false;
    bool force_sdxl_vae_conv_scale = false;
    bool chroma_use_dit_mask = true;
    bool chroma_use_t5_mask = false;
    int chroma_t5_mask_pad = 1;
    float flow_shift = INFINITY;

    fprintf(stderr, "parsing options: %p\n", options);

    // If options is not NULL, parse options
    for (int i = 0; options[i] != NULL; i++) {
        const char *optname = strtok(options[i], ":");
        const char *optval = strtok(NULL, ":");
        if (optval == NULL) {
            optval = "true";
        }

        if (!strcmp(optname, "clip_l_path")) {
            clip_l_path = strdup(optval);
        }
        if (!strcmp(optname, "clip_g_path")) {
            clip_g_path = strdup(optval);
        }
        if (!strcmp(optname, "t5xxl_path")) {
            t5xxl_path = strdup(optval);
        }
        if (!strcmp(optname, "vae_path")) {
            vae_path = strdup(optval);
        }
        if (!strcmp(optname, "scheduler")) {
            scheduler_str = optval;
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
                fprintf(stderr, "Lora dir resolved to: %s\n", lora_dir);
            } else {
                lora_dir = strdup(optval);
                fprintf(stderr, "No model path provided, using lora dir as-is: %s\n", lora_dir);
            }
        }

        // New parsing
        if (!strcmp(optname, "clip_vision_path")) clip_vision_path = strdup(optval);
        if (!strcmp(optname, "llm_path")) llm_path = strdup(optval);
        if (!strcmp(optname, "llm_vision_path")) llm_vision_path = strdup(optval);
        if (!strcmp(optname, "diffusion_model_path")) diffusion_model_path = strdup(optval);
        if (!strcmp(optname, "high_noise_diffusion_model_path")) high_noise_diffusion_model_path = strdup(optval);
        if (!strcmp(optname, "taesd_path")) taesd_path = strdup(optval);
        if (!strcmp(optname, "control_net_path")) control_net_path = strdup(optval);
        if (!strcmp(optname, "embedding_dir")) embedding_dir = strdup(optval);
        if (!strcmp(optname, "photo_maker_path")) photo_maker_path = strdup(optval);
        if (!strcmp(optname, "tensor_type_rules")) tensor_type_rules = strdup(optval);

        if (!strcmp(optname, "vae_decode_only")) vae_decode_only = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "offload_params_to_cpu")) offload_params_to_cpu = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "keep_clip_on_cpu")) keep_clip_on_cpu = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "keep_control_net_on_cpu")) keep_control_net_on_cpu = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "keep_vae_on_cpu")) keep_vae_on_cpu = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "diffusion_flash_attn")) diffusion_flash_attn = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "tae_preview_only")) tae_preview_only = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "diffusion_conv_direct")) diffusion_conv_direct = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "vae_conv_direct")) vae_conv_direct = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "force_sdxl_vae_conv_scale")) force_sdxl_vae_conv_scale = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "chroma_use_dit_mask")) chroma_use_dit_mask = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);
        if (!strcmp(optname, "chroma_use_t5_mask")) chroma_use_t5_mask = (strcmp(optval, "true") == 0 || strcmp(optval, "1") == 0);

        if (!strcmp(optname, "n_threads")) n_threads = atoi(optval);
        if (!strcmp(optname, "chroma_t5_mask_pad")) chroma_t5_mask_pad = atoi(optval);

        if (!strcmp(optname, "flow_shift")) flow_shift = atof(optval);

        if (!strcmp(optname, "rng_type")) {
            int found = -1;
            for (int m = 0; m < RNG_TYPE_COUNT; m++) {
                if (!strcmp(optval, rng_type_str[m])) {
                    found = m;
                    break;
                }
            }
            if (found != -1) {
                rng_type = (rng_type_t)found;
                fprintf(stderr, "Found rng_type: %s\n", optval);
            } else {
                fprintf(stderr, "Invalid rng_type: %s, using default\n", optval);
            }
        }
        if (!strcmp(optname, "sampler_rng_type")) {
            int found = -1;
            for (int m = 0; m < RNG_TYPE_COUNT; m++) {
                if (!strcmp(optval, rng_type_str[m])) {
                    found = m;
                    break;
                }
            }
            if (found != -1) {
                sampler_rng_type = (rng_type_t)found;
                fprintf(stderr, "Found sampler_rng_type: %s\n", optval);
            } else {
                fprintf(stderr, "Invalid sampler_rng_type: %s, using default\n", optval);
            }
        }
        if (!strcmp(optname, "prediction")) {
            int found = -1;
            for (int m = 0; m < PREDICTION_COUNT; m++) {
                if (!strcmp(optval, prediction_str[m])) {
                    found = m;
                    break;
                }
            }
            if (found != -1) {
                prediction = (prediction_t)found;
                fprintf(stderr, "Found prediction: %s\n", optval);
            } else {
                fprintf(stderr, "Invalid prediction: %s, using default\n", optval);
            }
        }
        if (!strcmp(optname, "lora_apply_mode")) {
            int found = -1;
            for (int m = 0; m < LORA_APPLY_MODE_COUNT; m++) {
                if (!strcmp(optval, lora_apply_mode_str[m])) {
                    found = m;
                    break;
                }
            }
            if (found != -1) {
                lora_apply_mode = (lora_apply_mode_t)found;
                fprintf(stderr, "Found lora_apply_mode: %s\n", optval);
            } else {
                fprintf(stderr, "Invalid lora_apply_mode: %s, using default\n", optval);
            }
        }
        if (!strcmp(optname, "wtype")) {
            int found = -1;
            for (int m = 0; m < SD_TYPE_COUNT; m++) {
                if (sd_type_str[m] && !strcmp(optval, sd_type_str[m])) {
                    found = m;
                    break;
                }
            }
            if (found != -1) {
                wtype = (sd_type_t)found;
                fprintf(stderr, "Found wtype: %s\n", optval);
            } else {
                fprintf(stderr, "Invalid wtype: %s, using default\n", optval);
            }
        }
    }

    fprintf(stderr, "parsed options\n");

    fprintf (stderr, "Creating context\n");
    sd_ctx_params_init(&ctx_params);
    ctx_params.model_path = model;
    ctx_params.clip_l_path = clip_l_path;
    ctx_params.clip_g_path = clip_g_path;
    ctx_params.clip_vision_path = clip_vision_path;
    ctx_params.t5xxl_path = t5xxl_path;
    ctx_params.llm_path = llm_path;
    ctx_params.llm_vision_path = llm_vision_path;
    ctx_params.diffusion_model_path = diffusion_model_path;
    ctx_params.high_noise_diffusion_model_path = high_noise_diffusion_model_path;
    ctx_params.vae_path = vae_path;
    ctx_params.taesd_path = taesd_path;
    ctx_params.control_net_path = control_net_path;
    ctx_params.lora_model_dir = lora_dir;
    ctx_params.embedding_dir = embedding_dir;
    ctx_params.photo_maker_path = photo_maker_path;
    ctx_params.tensor_type_rules = tensor_type_rules;
    ctx_params.vae_decode_only = vae_decode_only;
    // XXX: Setting to true causes a segfault on the second run
    ctx_params.free_params_immediately = false;
    ctx_params.n_threads = n_threads;
    ctx_params.rng_type = rng_type;
    ctx_params.keep_clip_on_cpu = keep_clip_on_cpu;
    if (wtype != SD_TYPE_COUNT) ctx_params.wtype = wtype;
    if (sampler_rng_type != RNG_TYPE_COUNT) ctx_params.sampler_rng_type = sampler_rng_type;
    if (prediction != PREDICTION_COUNT) ctx_params.prediction = prediction;
    if (lora_apply_mode != LORA_APPLY_MODE_COUNT) ctx_params.lora_apply_mode = lora_apply_mode;
    ctx_params.offload_params_to_cpu = offload_params_to_cpu;
    ctx_params.keep_control_net_on_cpu = keep_control_net_on_cpu;
    ctx_params.keep_vae_on_cpu = keep_vae_on_cpu;
    ctx_params.diffusion_flash_attn = diffusion_flash_attn;
    ctx_params.tae_preview_only = tae_preview_only;
    ctx_params.diffusion_conv_direct = diffusion_conv_direct;
    ctx_params.vae_conv_direct = vae_conv_direct;
    ctx_params.force_sdxl_vae_conv_scale = force_sdxl_vae_conv_scale;
    ctx_params.chroma_use_dit_mask = chroma_use_dit_mask;
    ctx_params.chroma_use_t5_mask = chroma_use_t5_mask;
    ctx_params.chroma_t5_mask_pad = chroma_t5_mask_pad;
    ctx_params.flow_shift = flow_shift;
    sd_ctx_t* sd_ctx = new_sd_ctx(&ctx_params);

    if (sd_ctx == NULL) {
        fprintf (stderr, "failed loading model (generic error)\n");
        // TODO: Clean up allocated memory
        return 1;
    }
    fprintf (stderr, "Created context: OK\n");

    int sample_method_found = -1;
    for (int m = 0; m < SAMPLE_METHOD_COUNT; m++) {
        if (!strcmp(sampler, sample_method_str[m])) {
            sample_method_found = m;
            fprintf(stderr, "Found sampler: %s\n", sampler);
        }
    }
    if (sample_method_found == -1) {
        sample_method_found = sd_get_default_sample_method(sd_ctx);
        fprintf(stderr, "Invalid sample method, using default: %s\n", sample_method_str[sample_method_found]);
    }
    sample_method = (sample_method_t)sample_method_found;

    for (int d = 0; d < SCHEDULER_COUNT; d++) {
        if (!strcmp(scheduler_str, schedulers[d])) {
            scheduler = (scheduler_t)d;
            fprintf (stderr, "Found scheduler: %s\n", scheduler_str);
        }
    }
    if (scheduler == SCHEDULER_COUNT) {
      scheduler = sd_get_default_scheduler(sd_ctx);
      fprintf(stderr, "Invalid scheduler, using default: %s\n", schedulers[scheduler]);
    }

    sd_c = sd_ctx;

    return 0;
}

void sd_tiling_params_set_enabled(sd_tiling_params_t *params, bool enabled) {
    params->enabled = enabled;
}

void sd_tiling_params_set_tile_sizes(sd_tiling_params_t *params, int tile_size_x, int tile_size_y) {
    params->tile_size_x = tile_size_x;
    params->tile_size_y = tile_size_y;
}

void sd_tiling_params_set_rel_sizes(sd_tiling_params_t *params, float rel_size_x, float rel_size_y) {
    params->rel_size_x = rel_size_x;
    params->rel_size_y = rel_size_y;
}

void sd_tiling_params_set_target_overlap(sd_tiling_params_t *params, float target_overlap) {
    params->target_overlap = target_overlap;
}

sd_tiling_params_t* sd_img_gen_params_get_vae_tiling_params(sd_img_gen_params_t *params) {
    return &params->vae_tiling_params;
}

sd_img_gen_params_t* sd_img_gen_params_new(void) {
    sd_img_gen_params_t *params = (sd_img_gen_params_t *)std::malloc(sizeof(sd_img_gen_params_t));
    sd_img_gen_params_init(params);
    sd_sample_params_init(&params->sample_params);
    sd_easycache_params_init(&params->easycache);
    params->control_strength = 0.9f;
    return params;
}

void sd_img_gen_params_set_prompts(sd_img_gen_params_t *params, const char *prompt, const char *negative_prompt) {
    params->prompt = prompt;
    params->negative_prompt = negative_prompt;
}

void sd_img_gen_params_set_dimensions(sd_img_gen_params_t *params, int width, int height) {
    params->width = width;
    params->height = height;
}

void sd_img_gen_params_set_seed(sd_img_gen_params_t *params, int64_t seed) {
    params->seed = seed;
}

int gen_image(sd_img_gen_params_t *p, int steps, char *dst, float cfg_scale, char *src_image, float strength, char *mask_image, char* ref_images[], int ref_images_count) {

    sd_image_t* results;

    std::vector<int> skip_layers = {7, 8, 9};

    fprintf (stderr, "Generating image\n");

    p->sample_params.guidance.txt_cfg = cfg_scale;
    p->sample_params.guidance.slg.layers = skip_layers.data();
    p->sample_params.guidance.slg.layer_count = skip_layers.size();
    p->sample_params.sample_method = sample_method;
    p->sample_params.sample_steps = steps;
    p->sample_params.scheduler = scheduler;

    int width = p->width;
    int height = p->height;

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

        p->init_image = {(uint32_t)width, (uint32_t)height, 3, input_image_buffer};
        p->strength = strength;
        fprintf(stderr, "Using img2img with strength: %.2f\n", strength);
    } else {
        // No input image, use empty image for text-to-image
        p->init_image = {(uint32_t)width, (uint32_t)height, 3, NULL};
        p->strength = 0.0f;
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

        p->mask_image = {(uint32_t)width, (uint32_t)height, 1, mask_image_buffer};
        fprintf(stderr, "Using inpainting with mask\n");
    } else {
        // No mask image, create default full mask
        default_mask_image_vec.resize(width * height, 255);
        p->mask_image = {(uint32_t)width, (uint32_t)height, 1, default_mask_image_vec.data()};
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
            p->ref_images = ref_images_vec.data();
            p->ref_images_count = ref_images_vec.size();
            fprintf(stderr, "Using %zu reference images\n", ref_images_vec.size());
        }
    }

    fprintf(stderr, "Generating image with params: \nctx\n---\n%s\ngen\n---\n%s\n",
            sd_ctx_params_to_str(&ctx_params),
            sd_img_gen_params_to_str(p));

    results = generate_image(sd_c, p);

    std::free(p);

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

    int ret = stbi_write_png(dst, results[0].width, results[0].height, results[0].channel,
                             results[0].data, 0, NULL);
    if (ret)
      fprintf (stderr, "Saved resulting image to '%s'\n", dst);
    else
      fprintf(stderr, "Failed to write image to '%s'\n", dst);

    // Clean up
    free(results[0].data);
    results[0].data = NULL;
    free(results);
    if (input_image_buffer) free(input_image_buffer);
    if (mask_image_buffer) free(mask_image_buffer);
    for (auto buffer : ref_image_buffers) {
        if (buffer) free(buffer);
    }
    fprintf (stderr, "gen_image is done: %s\n", dst);
    fflush(stderr);

    return !ret;
}

int unload() {
    free_sd_ctx(sd_c);
    return 0;
}
