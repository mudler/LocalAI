#include "stable-diffusion.h"
#include <cmath>
#include <memory>
#include <climits>
#include <cstdint>
#define GGML_MAX_NAME 128

#include <stdio.h>
#include <string.h>
#include <time.h>
#include <string>
#include <vector>
#include <map>
#include <filesystem>
#include <algorithm>
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
#include <regex>
#include <errno.h>
#include <inttypes.h>
#include <signal.h>
#include <unistd.h>
#include <sys/wait.h>



sd_ctx_t* sd_c;
// Moved from the context (load time) to generation time options. The sampler
// and scheduler are kept as the strings the operator gave; upstream parses
// them when they are set on the generation options. Empty means "auto".
static std::string sampler_name;
static std::string scheduler_name;
float flow_shift = INFINITY;

// Typed option setters. A rejected value is logged and ignored, so an invalid
// operator option falls back to the upstream default like before.
static bool opt_str(sd_opt_t* o, const char* key, const char* v) {
    if (!v || !*v) return true;
    if (sd_opt_set_string(o, key, v) != SD_STATUS_OK) {
        fprintf(stderr, "Option %s=%s rejected: %s\n", key, v, sd_get_last_error());
        return false;
    }
    return true;
}
static bool opt_bool(sd_opt_t* o, const char* key, bool v) {
    if (sd_opt_set_bool(o, key, v) != SD_STATUS_OK) {
        fprintf(stderr, "Option %s rejected: %s\n", key, sd_get_last_error());
        return false;
    }
    return true;
}
static bool opt_int(sd_opt_t* o, const char* key, int64_t v) {
    if (sd_opt_set_int(o, key, v) != SD_STATUS_OK) {
        fprintf(stderr, "Option %s=%lld rejected: %s\n", key, (long long)v, sd_get_last_error());
        return false;
    }
    return true;
}
static bool opt_float(sd_opt_t* o, const char* key, double v) {
    if (sd_opt_set_float(o, key, v) != SD_STATUS_OK) {
        fprintf(stderr, "Option %s=%g rejected: %s\n", key, v, sd_get_last_error());
        return false;
    }
    return true;
}

struct opt_deleter { void operator()(sd_opt_t* o) const { sd_opt_free(o); } };
using opt_ptr = std::unique_ptr<sd_opt_t, opt_deleter>;
struct tensor_deleter { void operator()(sd_tensor_t* t) const { sd_tensor_free(t); } };
using tensor_ptr = std::unique_ptr<sd_tensor_t, tensor_deleter>;
struct result_deleter { void operator()(sd_result_t* r) const { sd_result_free(r); } };
using result_ptr = std::unique_ptr<sd_result_t, result_deleter>;

// Copy an interleaved 8-bit image (width * height * channels bytes) into a tensor.
static tensor_ptr make_u8_image(const uint8_t* data, int width, int height, int channels) {
    int64_t shape[3] = {channels, width, height};
    return tensor_ptr(sd_tensor_create(SD_TENSOR_U8, 3, shape, data, (size_t)width * height * channels));
}

// Storage for embeddings (needs to persist for the lifetime of ctx_params)
static std::vector<sd_embedding_t> embedding_vec;
// Storage for embedding strings (needs to persist as long as embedding_vec references them)
static std::vector<std::string> embedding_strings;

// Storage for LoRAs (needs to persist for the lifetime of generation params)
static std::vector<sd_lora_t> lora_vec;
// Storage for LoRA strings (needs to persist as long as lora_vec references them)
static std::vector<std::string> lora_strings;
// Storage for lora_dir path
static std::string lora_dir_path;

// Build embeddings vector from directory, similar to upstream CLI
static void build_embedding_vec(const char* embedding_dir) {
    embedding_vec.clear();
    embedding_strings.clear();

    if (!embedding_dir || strlen(embedding_dir) == 0) {
        return;
    }

    if (!std::filesystem::exists(embedding_dir) || !std::filesystem::is_directory(embedding_dir)) {
        fprintf(stderr, "Embedding directory does not exist or is not a directory: %s\n", embedding_dir);
        return;
    }

    static const std::vector<std::string> valid_ext = {".pt", ".safetensors", ".gguf"};

    for (const auto& entry : std::filesystem::directory_iterator(embedding_dir)) {
        if (!entry.is_regular_file()) {
            continue;
        }

        auto path = entry.path();
        std::string ext = path.extension().string();

        bool valid = false;
        for (const auto& e : valid_ext) {
            if (ext == e) {
                valid = true;
                break;
            }
        }
        if (!valid) {
            continue;
        }

        std::string name = path.stem().string();
        std::string full_path = path.string();

        // Store strings in persistent storage
        embedding_strings.push_back(name);
        embedding_strings.push_back(full_path);

        sd_embedding_t item;
        item.name = embedding_strings[embedding_strings.size() - 2].c_str();
        item.path = embedding_strings[embedding_strings.size() - 1].c_str();

        embedding_vec.push_back(item);
        fprintf(stderr, "Found embedding: %s -> %s\n", item.name, item.path);
    }

    fprintf(stderr, "Loaded %zu embeddings from %s\n", embedding_vec.size(), embedding_dir);
}

// Discover LoRA files in directory and build a map of name -> path
static std::map<std::string, std::string> discover_lora_files(const char* lora_dir) {
    std::map<std::string, std::string> lora_map;

    if (!lora_dir || strlen(lora_dir) == 0) {
        fprintf(stderr, "LoRA directory not specified\n");
        return lora_map;
    }

    if (!std::filesystem::exists(lora_dir) || !std::filesystem::is_directory(lora_dir)) {
        fprintf(stderr, "LoRA directory does not exist or is not a directory: %s\n", lora_dir);
        return lora_map;
    }

    static const std::vector<std::string> valid_ext = {".safetensors", ".ckpt", ".pt", ".gguf"};

    fprintf(stderr, "Discovering LoRA files in: %s\n", lora_dir);

    for (const auto& entry : std::filesystem::directory_iterator(lora_dir)) {
        if (!entry.is_regular_file()) {
            continue;
        }

        auto path = entry.path();
        std::string ext = path.extension().string();

        bool valid = false;
        for (const auto& e : valid_ext) {
            if (ext == e) {
                valid = true;
                break;
            }
        }
        if (!valid) {
            continue;
        }

        std::string name = path.stem().string();  // stem() already removes extension
        std::string full_path = path.string();

        // Store the name (without extension) -> full path mapping
        // This allows users to specify just the name in <lora:name:strength>
        lora_map[name] = full_path;

        fprintf(stderr, "Found LoRA file: %s -> %s\n", name.c_str(), full_path.c_str());
    }

    fprintf(stderr, "Discovered %zu LoRA files in %s\n", lora_map.size(), lora_dir);
    return lora_map;
}

// Helper function to check if a path is absolute (matches upstream)
static bool is_absolute_path(const std::string& p) {
#ifdef _WIN32
    // Windows: C:/path or C:\path
    return p.size() > 1 && std::isalpha(static_cast<unsigned char>(p[0])) && p[1] == ':';
#else
    // Unix: /path
    return !p.empty() && p[0] == '/';
#endif
}

// Parse LoRAs from prompt string (e.g., "<lora:name:1.0>" or "<lora:name>")
// Returns a vector of LoRA info and the cleaned prompt with LoRA tags removed
// Matches upstream implementation more closely
static std::pair<std::vector<sd_lora_t>, std::string> parse_loras_from_prompt(const std::string& prompt, const char* lora_dir) {
    std::vector<sd_lora_t> loras;
    std::string cleaned_prompt = prompt;

    if (!lora_dir || strlen(lora_dir) == 0) {
        fprintf(stderr, "LoRA directory not set, cannot parse LoRAs from prompt\n");
        return {loras, cleaned_prompt};
    }

    // Discover LoRA files for name-based lookup
    std::map<std::string, std::string> discovered_lora_map = discover_lora_files(lora_dir);

    // Map to accumulate multipliers for the same LoRA (matches upstream)
    std::map<std::string, float> lora_map;
    std::map<std::string, float> high_noise_lora_map;

    static const std::regex re(R"(<lora:([^:>]+):([^>]+)>)");
    static const std::vector<std::string> valid_ext = {".pt", ".safetensors", ".gguf"};
    std::smatch m;

    std::string tmp = prompt;

    fprintf(stderr, "Parsing LoRAs from prompt: %s\n", prompt.c_str());

    while (std::regex_search(tmp, m, re)) {
        std::string raw_path = m[1].str();
        const std::string raw_mul = m[2].str();

        float mul = 0.f;
        try {
            mul = std::stof(raw_mul);
        } catch (...) {
            tmp = m.suffix().str();
            cleaned_prompt = std::regex_replace(cleaned_prompt, re, "", std::regex_constants::format_first_only);
            fprintf(stderr, "Invalid LoRA multiplier '%s', skipping\n", raw_mul.c_str());
            continue;
        }

        bool is_high_noise = false;
        static const std::string prefix = "|high_noise|";
        if (raw_path.rfind(prefix, 0) == 0) {
            raw_path.erase(0, prefix.size());
            is_high_noise = true;
        }

        std::filesystem::path final_path;
        if (is_absolute_path(raw_path)) {
            final_path = raw_path;
        } else {
            // Try name-based lookup first
            auto it = discovered_lora_map.find(raw_path);
            if (it != discovered_lora_map.end()) {
                final_path = it->second;
            } else {
                // Try case-insensitive lookup
                bool found = false;
                for (const auto& pair : discovered_lora_map) {
                    std::string lower_name = raw_path;
                    std::string lower_key = pair.first;
                    std::transform(lower_name.begin(), lower_name.end(), lower_name.begin(), ::tolower);
                    std::transform(lower_key.begin(), lower_key.end(), lower_key.begin(), ::tolower);
                    if (lower_name == lower_key) {
                        final_path = pair.second;
                        found = true;
                        break;
                    }
                }
                if (!found) {
                    // Try as relative path in lora_dir
                    final_path = std::filesystem::path(lora_dir) / raw_path;
                }
            }
        }

        // Try adding extensions if file doesn't exist
        if (!std::filesystem::exists(final_path)) {
            bool found = false;
            for (const auto& ext : valid_ext) {
                std::filesystem::path try_path = final_path;
                try_path += ext;
                if (std::filesystem::exists(try_path)) {
                    final_path = try_path;
                    found = true;
                    break;
                }
            }
            if (!found) {
                fprintf(stderr, "WARNING: LoRA file not found: %s\n", final_path.lexically_normal().string().c_str());
                tmp = m.suffix().str();
                cleaned_prompt = std::regex_replace(cleaned_prompt, re, "", std::regex_constants::format_first_only);
                continue;
            }
        }

        // Normalize path (matches upstream)
        const std::string key = final_path.lexically_normal().string();

        // Accumulate multiplier if same LoRA appears multiple times (matches upstream)
        if (is_high_noise) {
            high_noise_lora_map[key] += mul;
        } else {
            lora_map[key] += mul;
        }

        fprintf(stderr, "Parsed LoRA: path='%s', multiplier=%.2f, is_high_noise=%s\n",
                key.c_str(), mul, is_high_noise ? "true" : "false");

        cleaned_prompt = std::regex_replace(cleaned_prompt, re, "", std::regex_constants::format_first_only);
        tmp = m.suffix().str();
    }

    // Build final LoRA vector from accumulated maps (matches upstream)
    // Store all path strings first to ensure they persist
    for (const auto& kv : lora_map) {
        lora_strings.push_back(kv.first);
    }
    for (const auto& kv : high_noise_lora_map) {
        lora_strings.push_back(kv.first);
    }

    // Now build the LoRA vector with pointers to the stored strings
    size_t string_idx = 0;
    for (const auto& kv : lora_map) {
        sd_lora_t item;
        item.is_high_noise = false;
        item.path = lora_strings[string_idx].c_str();
        item.multiplier = kv.second;
        loras.push_back(item);
        string_idx++;
    }

    for (const auto& kv : high_noise_lora_map) {
        sd_lora_t item;
        item.is_high_noise = true;
        item.path = lora_strings[string_idx].c_str();
        item.multiplier = kv.second;
        loras.push_back(item);
        string_idx++;
    }

    // Clean up extra spaces
    std::regex space_regex(R"(\s+)");
    cleaned_prompt = std::regex_replace(cleaned_prompt, space_regex, " ");
    // Trim leading/trailing spaces
    size_t first = cleaned_prompt.find_first_not_of(" \t");
    if (first != std::string::npos) {
        cleaned_prompt.erase(0, first);
    }
    size_t last = cleaned_prompt.find_last_not_of(" \t");
    if (last != std::string::npos) {
        cleaned_prompt.erase(last + 1);
    }

    fprintf(stderr, "Parsed %zu LoRA(s) from prompt. Cleaned prompt: %s\n", loras.size(), cleaned_prompt.c_str());

    return {loras, cleaned_prompt};
}

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
        case SD_LOG_VERBOSE:
            level_str = "VERB";
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
    const char *audio_vae_path = "";
    const char *embeddings_connectors_path = "";
    const char *scheduler_str = "";
    const char *sampler = "";
    const char *clip_vision_path = "";
    const char *llm_path = "";
    const char *llm_vision_path = "";
    const char *diffusion_model_path = stableDiffusionModel;
    const char *high_noise_diffusion_model_path = "";
    const char *uncond_diffusion_model_path = "";
    const char *taesd_path  = "";
    const char *control_net_path = "";
    const char *embedding_dir = "";
    const char *photo_maker_path = "";
    const char *pulid_weights_path = "";
    const char *tensor_type_rules = "";
    const char *tokenizer_path = "";
    char *lora_dir = model_path;

    // Upstream backend/parameter placement specs (see docs/.../stablediffusion).
    // Empty means "leave at upstream default" (nullptr).
    const char *backend_arg = "";
    const char *params_backend_arg = "";
    const char *rpc_servers_arg = "";
    const char *max_vram_arg = "";

    int n_threads = threads;
    // Enum-like options are strings in the upstream option API; the setter
    // validates them.
    std::string wtype_str;
    std::string rng_type_str = "cuda";
    std::string sampler_rng_type_str;
    std::string prediction_str;
    std::string lora_apply_mode_str;
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
    fprintf(stderr, "parsing options: %p\n", options);

    // If options is not NULL, parse options
    for (int i = 0; options[i] != NULL; i++) {
        const char *optname = strtok(options[i], ":");
        // Take everything after the first ':' as the value so values may
        // themselves contain colons (e.g. rpc_servers host:port lists).
        const char *optval = strtok(NULL, "");
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
        if (!strcmp(optname, "audio_vae_path")) {
            audio_vae_path = strdup(optval);
        }
        if (!strcmp(optname, "embeddings_connectors_path")) {
            embeddings_connectors_path = strdup(optval);
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
                lora_dir_path = full_lora_path.string();
                fprintf(stderr, "LoRA dir resolved to: %s\n", lora_dir);
            } else {
                lora_dir = strdup(optval);
                lora_dir_path = std::string(optval);
                fprintf(stderr, "No model path provided, using lora dir as-is: %s\n", lora_dir);
            }
            // Discover LoRAs immediately when directory is set
            if (lora_dir && strlen(lora_dir) > 0) {
                discover_lora_files(lora_dir);
            }
        }

        // New parsing
        if (!strcmp(optname, "clip_vision_path")) clip_vision_path = strdup(optval);
        if (!strcmp(optname, "llm_path")) llm_path = strdup(optval);
        if (!strcmp(optname, "llm_vision_path")) llm_vision_path = strdup(optval);
        if (!strcmp(optname, "diffusion_model_path")) diffusion_model_path = strdup(optval);
        if (!strcmp(optname, "high_noise_diffusion_model_path")) high_noise_diffusion_model_path = strdup(optval);
        if (!strcmp(optname, "uncond_diffusion_model_path")) uncond_diffusion_model_path = strdup(optval);
        if (!strcmp(optname, "taesd_path")) taesd_path = strdup(optval);
        if (!strcmp(optname, "control_net_path")) control_net_path = strdup(optval);
        if (!strcmp(optname, "embedding_dir")) {
            // Path join with model dir
            if (model_path && strlen(model_path) > 0) {
                std::filesystem::path model_path_str(model_path);
                std::filesystem::path embedding_path(optval);
                std::filesystem::path full_embedding_path = model_path_str / embedding_path;
                embedding_dir = strdup(full_embedding_path.string().c_str());
                fprintf(stderr, "Embedding dir resolved to: %s\n", embedding_dir);
            } else {
                embedding_dir = strdup(optval);
                fprintf(stderr, "No model path provided, using embedding dir as-is: %s\n", embedding_dir);
            }
        }
        if (!strcmp(optname, "photo_maker_path")) photo_maker_path = strdup(optval);
        if (!strcmp(optname, "pulid_weights_path")) pulid_weights_path = strdup(optval);
        if (!strcmp(optname, "tensor_type_rules")) tensor_type_rules = strdup(optval);
        if (!strcmp(optname, "tokenizer")) tokenizer_path = strdup(optval);

        // Backend / parameter placement specs (see prepare_backend_assignments
        // in the upstream CLI). These compose with the legacy keep_*_on_cpu /
        // offload_params_to_cpu booleans below.
        if (!strcmp(optname, "backend")) backend_arg = strdup(optval);
        if (!strcmp(optname, "params_backend")) params_backend_arg = strdup(optval);
        if (!strcmp(optname, "rpc_servers")) rpc_servers_arg = strdup(optval);
        if (!strcmp(optname, "max_vram")) max_vram_arg = strdup(optval);
        if (!strcmp(optname, "stream_layers")) {
            // Retained as a no-op for existing configurations. Upstream now
            // selects segmented weight streaming automatically.
        }

        // vae_decode_only is still accepted for backwards compatibility with
        // existing gallery configs, but upstream dropped the option (the model
        // now decides), so it is parsed and ignored.
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

        if (!strcmp(optname, "rng_type")) rng_type_str = optval;
        if (!strcmp(optname, "sampler_rng_type")) sampler_rng_type_str = optval;
        if (!strcmp(optname, "prediction")) prediction_str = optval;
        if (!strcmp(optname, "lora_apply_mode")) lora_apply_mode_str = optval;
        if (!strcmp(optname, "wtype")) wtype_str = optval;
    }

    fprintf(stderr, "parsed options\n");

    // Build embeddings vector from directory if provided
    build_embedding_vec(embedding_dir);

    fprintf (stderr, "Creating context\n");
    opt_ptr ctx_opt(sd_ctx_opt_create());
    if (!ctx_opt) {
        fprintf(stderr, "failed to allocate context options\n");
        return 1;
    }
    sd_opt_t* co = ctx_opt.get();
    opt_str(co, "model_path", model);
    opt_str(co, "clip_l_path", clip_l_path);
    opt_str(co, "clip_g_path", clip_g_path);
    opt_str(co, "clip_vision_path", clip_vision_path);
    opt_str(co, "t5xxl_path", t5xxl_path);
    opt_str(co, "llm_path", llm_path);
    opt_str(co, "llm_vision_path", llm_vision_path);
    opt_str(co, "diffusion_model_path", diffusion_model_path);
    opt_str(co, "high_noise_diffusion_model_path", high_noise_diffusion_model_path);
    opt_str(co, "uncond_diffusion_model_path", uncond_diffusion_model_path);
    opt_str(co, "vae_path", vae_path);
    opt_str(co, "audio_vae_path", audio_vae_path);
    opt_str(co, "embeddings_connectors_path", embeddings_connectors_path);
    opt_str(co, "taesd_path", taesd_path);
    opt_str(co, "control_net_path", control_net_path);
    if (lora_dir && strlen(lora_dir) > 0) {
        lora_dir_path = std::string(lora_dir);
        fprintf(stderr, "LoRA model directory set to: %s\n", lora_dir);
        // Discover LoRAs at load time for logging
        discover_lora_files(lora_dir);
    } else {
        fprintf(stderr, "WARNING: LoRA model directory not set. LoRAs in prompts will not be loaded.\n");
    }
    if (!embedding_vec.empty()) {
        if (sd_opt_set_embeddings(co, "embeddings", embedding_vec.data(), embedding_vec.size()) != SD_STATUS_OK) {
            fprintf(stderr, "Embeddings rejected: %s\n", sd_get_last_error());
        }
    }
    opt_str(co, "photo_maker_path", photo_maker_path);
    opt_str(co, "pulid_weights_path", pulid_weights_path);
    opt_str(co, "tensor_type_rules", tensor_type_rules);
    opt_str(co, "tokenizer", tokenizer_path);
    opt_int(co, "n_threads", n_threads);
    opt_str(co, "rng_type", rng_type_str.c_str());
    opt_str(co, "sampler_rng_type", sampler_rng_type_str.c_str());
    opt_str(co, "wtype", wtype_str.c_str());
    opt_str(co, "prediction", prediction_str.c_str());
    opt_str(co, "lora_apply_mode", lora_apply_mode_str.c_str());
    // Backend / parameter placement specs. Upstream replaced the boolean
    // CPU-offload knobs (offload_params_to_cpu, keep_clip_on_cpu, keep_vae_on_cpu,
    // keep_control_net_on_cpu) with these specs. Seed from the explicit
    // backend/params_backend options, then prepend the legacy boolean-derived
    // assignments, mirroring prepare_backend_assignments() in the upstream CLI.
    std::string backend_spec = backend_arg;
    std::string params_backend_spec = params_backend_arg;
    auto prepend_spec = [](std::string& spec, const char* assignment) {
        spec = spec.empty() ? std::string(assignment) : std::string(assignment) + "," + spec;
    };
    if (offload_params_to_cpu) prepend_spec(params_backend_spec, "*=cpu");
    if (keep_clip_on_cpu) prepend_spec(backend_spec, "te=cpu");
    if (keep_vae_on_cpu) prepend_spec(backend_spec, "vae=cpu");
    if (keep_control_net_on_cpu) prepend_spec(backend_spec, "controlnet=cpu");
    opt_str(co, "backend", backend_spec.c_str());
    opt_str(co, "params_backend", params_backend_spec.c_str());
    // RPC servers: prefer the explicit option, otherwise fall back to the
    // LLAMACPP_GRPC_SERVERS env var. LocalAI's p2p worker mode populates that
    // var with discovered ggml rpc-server workers (shared with the llama.cpp
    // backend), so distributed image generation works with no extra config.
    if (strlen(rpc_servers_arg) > 0) {
        opt_str(co, "rpc_servers", rpc_servers_arg);
    } else {
        opt_str(co, "rpc_servers", std::getenv("LLAMACPP_GRPC_SERVERS"));
    }
    // max_vram is an optional GiB budget or per-backend spec for automatic
    // graph-cut execution. A zero value uses the live free-VRAM budget.
    opt_str(co, "max_vram", max_vram_arg);
    opt_bool(co, "diffusion_flash_attn", diffusion_flash_attn);
    opt_bool(co, "tae_preview_only", tae_preview_only);
    opt_bool(co, "diffusion_conv_direct", diffusion_conv_direct);
    opt_bool(co, "vae_conv_direct", vae_conv_direct);
    opt_bool(co, "force_sdxl_vae_conv_scale", force_sdxl_vae_conv_scale);
    // Chroma knobs are read from the generic model_args key=value spec.
    std::string model_args_spec =
        "chroma_use_dit_mask=" + std::string(chroma_use_dit_mask ? "true" : "false") +
        ",chroma_use_t5_mask=" + std::string(chroma_use_t5_mask ? "true" : "false") +
        ",chroma_t5_mask_pad=" + std::to_string(chroma_t5_mask_pad);
    opt_str(co, "model_args", model_args_spec.c_str());

    sd_ctx_t* sd_ctx = nullptr;
    if (sd_ctx_create(co, &sd_ctx) != SD_STATUS_OK || sd_ctx == NULL) {
        fprintf (stderr, "failed loading model: %s\n", sd_get_last_error());
        return 1;
    }
    fprintf (stderr, "Created context: OK\n");

    // Sampler and scheduler are applied per generation. Empty or invalid
    // values leave the upstream "auto" choice for the loaded model.
    sampler_name = sampler;
    scheduler_name = scheduler_str;
    sd_c = sd_ctx;

    return 0;
}

// Generation parameters collected from the Go side. Upstream now takes opaque
// option objects, so the values are kept here and applied by gen_image().
struct gosd_tiling_t {
    bool enabled = false;
    bool has_tile_sizes = false, has_rel_sizes = false, has_overlap = false;
    int tile_size_w = 0, tile_size_h = 0;
    float rel_size_w = 0, rel_size_h = 0;
    float target_overlap = 0;
};

struct gosd_img_gen_params_t {
    std::string prompt, negative_prompt;
    std::vector<sd_lora_t> loras;
    int width = 512, height = 512;
    int64_t seed = -1;
    std::vector<float> custom_sigmas;
    gosd_tiling_t vae_tiling;
};

void sd_tiling_params_set_enabled(gosd_tiling_t *params, bool enabled) {
    params->enabled = enabled;
}

void sd_tiling_params_set_tile_sizes(gosd_tiling_t *params, int tile_size_w, int tile_size_h) {
    params->tile_size_w = tile_size_w;
    params->tile_size_h = tile_size_h;
    params->has_tile_sizes = true;
}

void sd_tiling_params_set_rel_sizes(gosd_tiling_t *params, float rel_size_w, float rel_size_h) {
    params->rel_size_w = rel_size_w;
    params->rel_size_h = rel_size_h;
    params->has_rel_sizes = true;
}

void sd_tiling_params_set_target_overlap(gosd_tiling_t *params, float target_overlap) {
    params->target_overlap = target_overlap;
    params->has_overlap = true;
}

gosd_tiling_t* sd_img_gen_params_get_vae_tiling_params(gosd_img_gen_params_t *params) {
    return &params->vae_tiling;
}

gosd_img_gen_params_t* sd_img_gen_params_new(void) {
    return new gosd_img_gen_params_t();
}

void sd_img_gen_params_set_prompts(gosd_img_gen_params_t *params, const char *prompt, const char *negative_prompt) {
    // Clear previous LoRA data
    lora_vec.clear();
    lora_strings.clear();

    std::string prompt_str = prompt ? prompt : "";
    std::string negative_prompt_str = negative_prompt ? negative_prompt : "";

    // Use the stored LoRA directory
    const char* lora_dir_to_use = lora_dir_path.empty() ? nullptr : lora_dir_path.c_str();

    auto [loras, cleaned_prompt] = parse_loras_from_prompt(prompt_str, lora_dir_to_use);
    lora_vec = loras;

    // Also check negative prompt for LoRAs (though this is less common)
    auto [neg_loras, cleaned_negative] = parse_loras_from_prompt(negative_prompt_str, lora_dir_to_use);
    if (!neg_loras.empty()) {
        fprintf(stderr, "Note: Found %zu LoRAs in negative prompt (may not be supported)\n", neg_loras.size());
    }

    params->prompt = cleaned_prompt;
    params->negative_prompt = cleaned_negative;
    params->loras = lora_vec;

    fprintf(stderr, "Set prompts with %zu LoRAs. Original prompt: %s\n", lora_vec.size(), prompt ? prompt : "(null)");
    fprintf(stderr, "Cleaned prompt: %s\n", params->prompt.c_str());
}

void sd_img_gen_params_set_dimensions(gosd_img_gen_params_t *params, int width, int height) {
    params->width = width;
    params->height = height;
}

void sd_img_gen_params_set_seed(gosd_img_gen_params_t *params, int64_t seed) {
    params->seed = seed;
}

void sd_img_gen_params_set_custom_sigmas(gosd_img_gen_params_t *params, float *sigmas, int count) {
    params->custom_sigmas.assign(sigmas, sigmas + count);
}

// Apply the sampler choices shared by image and video generation. The
// scheduler and flow shift are shared with the high-noise pass upstream.
static void apply_sampling_options(sd_opt_t* o, int steps, float cfg_scale) {
    static const int skip_layers[] = {7, 8, 9};
    opt_float(o, "sample.guidance.txt_cfg", cfg_scale);
    if (sd_opt_set_ints(o, "sample.guidance.slg.layers", skip_layers, 3) != SD_STATUS_OK) {
        fprintf(stderr, "SLG layers rejected: %s\n", sd_get_last_error());
    }
    opt_str(o, "sample.method", sampler_name.c_str());
    opt_int(o, "sample.steps", steps);
    opt_str(o, "sample.scheduler", scheduler_name.c_str());
    if (std::isfinite(flow_shift)) opt_float(o, "sample.flow_shift", flow_shift);
}

// Load an image file as RGB (or one channel for a mask) and resize it to the
// target size. Returns an empty pointer on failure.
static std::unique_ptr<uint8_t, decltype(&free)> load_resized(const char* path, int channels, int width, int height) {
    int c = 0, w = 0, h = 0;
    uint8_t* buf = stbi_load(path, &w, &h, &c, channels);
    if (!buf) {
        fprintf(stderr, "Failed to load image from '%s'\n", path);
        return {nullptr, free};
    }
    if (w != width || h != height) {
        fprintf(stderr, "Resizing image from %dx%d to %dx%d\n", w, h, width, height);
        uint8_t* resized = (uint8_t*)malloc((size_t)width * height * channels);
        if (!resized) {
            free(buf);
            return {nullptr, free};
        }
        stbir_resize(buf, w, h, 0,
                     resized, width, height, 0, STBIR_TYPE_UINT8,
                     channels, STBIR_ALPHA_CHANNEL_NONE, 0,
                     STBIR_EDGE_CLAMP, STBIR_EDGE_CLAMP,
                     STBIR_FILTER_BOX, STBIR_FILTER_BOX,
                     STBIR_COLORSPACE_SRGB, nullptr);
        free(buf);
        buf = resized;
    }
    return {buf, free};
}

int gen_image(gosd_img_gen_params_t *p_raw, int steps, char *dst, float cfg_scale, char *src_image, float strength, char *mask_image, char* ref_images[], int ref_images_count) {
    // The Go side hands over ownership of the parameters.
    std::unique_ptr<gosd_img_gen_params_t> p(p_raw);

    fprintf (stderr, "Generating image\n");

    opt_ptr gen(sd_img_gen_opt_create());
    if (!gen) {
        fprintf(stderr, "failed to allocate generation options\n");
        return 1;
    }
    sd_opt_t* o = gen.get();

    opt_str(o, "prompt", p->prompt.c_str());
    opt_str(o, "negative_prompt", p->negative_prompt.c_str());
    opt_int(o, "width", p->width);
    opt_int(o, "height", p->height);
    opt_int(o, "seed", p->seed);
    apply_sampling_options(o, steps, cfg_scale);

    if (p->custom_sigmas.size() > 0) {
        if (sd_opt_set_floats(o, "sample.sigmas", p->custom_sigmas.data(), p->custom_sigmas.size()) != SD_STATUS_OK) {
            fprintf(stderr, "custom sigmas rejected: %s\n", sd_get_last_error());
        }
    }
    if (!p->loras.empty()) {
        if (sd_opt_set_loras(o, "loras", p->loras.data(), p->loras.size()) != SD_STATUS_OK) {
            fprintf(stderr, "LoRAs rejected: %s\n", sd_get_last_error());
        }
        for (size_t i = 0; i < p->loras.size(); i++) {
            fprintf(stderr, "  LoRA[%zu]: path='%s', multiplier=%.2f, is_high_noise=%s\n", i,
                    p->loras[i].path, p->loras[i].multiplier, p->loras[i].is_high_noise ? "true" : "false");
        }
    } else {
        fprintf(stderr, "No LoRAs specified for this generation\n");
    }

    // VAE tiling: only the values the operator set are applied, an unset tile
    // size or overlap keeps the library default.
    const gosd_tiling_t& t = p->vae_tiling;
    opt_bool(o, "vae_tiling.enabled", t.enabled);
    if (t.has_tile_sizes) {
        opt_int(o, "vae_tiling.tile_size_w", t.tile_size_w);
        opt_int(o, "vae_tiling.tile_size_h", t.tile_size_h);
    }
    if (t.has_rel_sizes) {
        opt_float(o, "vae_tiling.rel_size_w", t.rel_size_w);
        opt_float(o, "vae_tiling.rel_size_h", t.rel_size_h);
    }
    if (t.has_overlap) opt_float(o, "vae_tiling.target_overlap", t.target_overlap);

    const int width = p->width;
    const int height = p->height;

    // img2img input
    if (src_image != NULL && strlen(src_image) > 0) {
        fprintf(stderr, "Loading input image: %s\n", src_image);
        auto buf = load_resized(src_image, 3, width, height);
        if (!buf) return 1;
        tensor_ptr tensor = make_u8_image(buf.get(), width, height, 3);
        if (!tensor || sd_opt_set_tensor(o, "init_image", tensor.get()) != SD_STATUS_OK) {
            fprintf(stderr, "init_image rejected: %s\n", sd_get_last_error());
            return 1;
        }
        opt_float(o, "strength", strength);
        fprintf(stderr, "Using img2img with strength: %.2f\n", strength);
    }

    // inpainting mask
    if (mask_image != NULL && strlen(mask_image) > 0) {
        fprintf(stderr, "Loading mask image: %s\n", mask_image);
        auto buf = load_resized(mask_image, 1, width, height);
        if (!buf) return 1;
        tensor_ptr tensor = make_u8_image(buf.get(), width, height, 1);
        if (!tensor || sd_opt_set_tensor(o, "mask_image", tensor.get()) != SD_STATUS_OK) {
            fprintf(stderr, "mask_image rejected: %s\n", sd_get_last_error());
            return 1;
        }
        fprintf(stderr, "Using inpainting with mask\n");
    }

    // reference images
    std::vector<tensor_ptr> ref_tensors;
    if (ref_images_count > 0 && ref_images != NULL) {
        fprintf(stderr, "Loading %d reference images\n", ref_images_count);
        for (int i = 0; i < ref_images_count; i++) {
            if (ref_images[i] == NULL || strlen(ref_images[i]) == 0) {
                continue;
            }
            fprintf(stderr, "Loading reference image %d: %s\n", i + 1, ref_images[i]);
            auto buf = load_resized(ref_images[i], 3, width, height);
            if (!buf) continue;
            tensor_ptr tensor = make_u8_image(buf.get(), width, height, 3);
            if (tensor) ref_tensors.push_back(std::move(tensor));
        }
        if (!ref_tensors.empty()) {
            std::vector<const sd_tensor_t*> raw;
            for (auto& r : ref_tensors) raw.push_back(r.get());
            if (sd_opt_set_tensors(o, "ref_images", raw.data(), raw.size()) != SD_STATUS_OK) {
                fprintf(stderr, "ref_images rejected: %s\n", sd_get_last_error());
                return 1;
            }
            fprintf(stderr, "Using %zu reference images\n", ref_tensors.size());
        }
    }

    sd_result_t* generated = nullptr;
    sd_status_t status = sd_generate_image(sd_c, o, &generated);
    result_ptr output(generated);

    if (status != SD_STATUS_OK || !output || sd_result_get_image_count(output.get()) == 0) {
        fprintf (stderr, "NO results: %s\n", sd_get_last_error());
        return 1;
    }

    const sd_tensor_t* image = sd_result_get_image(output.get(), 0);
    const void* data = image ? sd_tensor_data(image) : nullptr;
    if (!data || sd_tensor_rank(image) != 3) {
        fprintf (stderr, "Results with no data\n");
        return 1;
    }
    const int channel = (int)sd_tensor_dim(image, 0);
    const int out_width = (int)sd_tensor_dim(image, 1);
    const int out_height = (int)sd_tensor_dim(image, 2);

    fprintf (stderr, "Writing PNG\n");
    fprintf (stderr, "DST: %s\n", dst);
    fprintf (stderr, "Width: %d\n", out_width);
    fprintf (stderr, "Height: %d\n", out_height);
    fprintf (stderr, "Channel: %d\n", channel);

    int ret = stbi_write_png(dst, out_width, out_height, channel, data, 0);
    if (ret)
      fprintf (stderr, "Saved resulting image to '%s'\n", dst);
    else
      fprintf(stderr, "Failed to write image to '%s'\n", dst);

    fprintf (stderr, "gen_image is done: %s\n", dst);
    fflush(stderr);

    return !ret;
}

// ---------------- Video generation ----------------

struct gosd_vid_gen_params_t {
    std::string prompt, negative_prompt;
    std::vector<sd_lora_t> loras;
    int width = 512, height = 512;
    int64_t seed = -1;
    int video_frames = 0;
};

gosd_vid_gen_params_t* sd_vid_gen_params_new(void) {
    return new gosd_vid_gen_params_t();
}

void sd_vid_gen_params_set_prompts(gosd_vid_gen_params_t *params, const char *prompt, const char *negative_prompt) {
    lora_vec.clear();
    lora_strings.clear();

    std::string prompt_str = prompt ? prompt : "";
    std::string negative_prompt_str = negative_prompt ? negative_prompt : "";

    const char* lora_dir_to_use = lora_dir_path.empty() ? nullptr : lora_dir_path.c_str();

    auto [loras, cleaned_prompt] = parse_loras_from_prompt(prompt_str, lora_dir_to_use);
    lora_vec = loras;

    auto [neg_loras, cleaned_negative] = parse_loras_from_prompt(negative_prompt_str, lora_dir_to_use);

    params->prompt = cleaned_prompt;
    params->negative_prompt = cleaned_negative;
    params->loras = lora_vec;
}

void sd_vid_gen_params_set_dimensions(gosd_vid_gen_params_t *params, int width, int height) {
    params->width = width;
    params->height = height;
}

void sd_vid_gen_params_set_seed(gosd_vid_gen_params_t *params, int64_t seed) {
    params->seed = seed;
}

void sd_vid_gen_params_set_video_frames(gosd_vid_gen_params_t *params, int n) {
    params->video_frames = n;
}

// Decoded reference audio: interleaved float samples (frame-major).
struct gosd_audio {
    uint32_t sample_rate;
    uint32_t channels;
    uint64_t sample_count;
    float* data;
};

// One decoded video frame, borrowed from a result tensor.
struct gosd_frame {
    int width;
    int height;
    int channel;
    const uint8_t* data;
};

// Read a RIFF/WAVE file (PCM 16/24/32-bit or IEEE float 32-bit) into an
// gosd_audio holding interleaved float samples (frame-major: ch0_s0, ch1_s0,
// ch0_s1, ...), the layout sd.cpp reads as data[frame * channels + channel].
// The caller owns out->data (free()).
// Returns false and prints the reason on any unsupported or malformed input.
static bool load_wav_audio(const char* path, gosd_audio* out) {
    *out = {0, 0, 0, nullptr};
    FILE* f = fopen(path, "rb");
    if (!f) { fprintf(stderr, "Failed to open reference audio '%s'\n", path); return false; }

    auto rd16 = [](const uint8_t* b) { return (uint32_t)b[0] | ((uint32_t)b[1] << 8); };
    auto rd32 = [](const uint8_t* b) { return (uint32_t)b[0] | ((uint32_t)b[1] << 8) | ((uint32_t)b[2] << 16) | ((uint32_t)b[3] << 24); };

    uint8_t hdr[12];
    if (fread(hdr, 1, 12, f) != 12 || memcmp(hdr, "RIFF", 4) != 0 || memcmp(hdr + 8, "WAVE", 4) != 0) {
        fprintf(stderr, "Reference audio '%s' is not a WAV file (convert it to WAV first)\n", path);
        fclose(f);
        return false;
    }

    uint32_t format = 0, channels = 0, rate = 0, bits = 0;
    std::vector<uint8_t> data;
    bool have_data = false;
    uint8_t ch[8];
    while (fread(ch, 1, 8, f) == 8) {
        uint32_t size = rd32(ch + 4);
        if (memcmp(ch, "fmt ", 4) == 0 && size >= 16) {
            std::vector<uint8_t> fmt(size);
            if (fread(fmt.data(), 1, size, f) != size) break;
            format = rd16(fmt.data());
            channels = rd16(fmt.data() + 2);
            rate = rd32(fmt.data() + 4);
            bits = rd16(fmt.data() + 14);
            // WAVE_FORMAT_EXTENSIBLE: the real format tag is the first two bytes of the sub-format GUID.
            if (format == 0xFFFE && size >= 26) format = rd16(fmt.data() + 24);
        } else if (memcmp(ch, "data", 4) == 0) {
            data.resize(size);
            size_t got = fread(data.data(), 1, size, f);
            data.resize(got);
            have_data = true;
            break;
        } else if (fseek(f, (long)size, SEEK_CUR) != 0) {
            break;
        }
        if (size & 1) fseek(f, 1, SEEK_CUR);
    }
    fclose(f);

    bool is_float = format == 3 && bits == 32;
    bool is_pcm = format == 1 && (bits == 16 || bits == 24 || bits == 32);
    if (!have_data || channels == 0 || rate == 0 || (!is_float && !is_pcm)) {
        fprintf(stderr, "Unsupported or malformed WAV '%s' (format=%u bits=%u channels=%u rate=%u)\n",
                path, format, bits, channels, rate);
        return false;
    }

    size_t bytes_per_sample = bits / 8;
    uint64_t frames = data.size() / (bytes_per_sample * channels);
    if (frames == 0) { fprintf(stderr, "Reference audio '%s' has no samples\n", path); return false; }
    float* interleaved = (float*)malloc((size_t)frames * channels * sizeof(float));
    if (!interleaved) return false;
    for (uint64_t i = 0; i < frames; i++) {
        for (uint32_t c = 0; c < channels; c++) {
            const uint8_t* b = data.data() + (i * channels + c) * bytes_per_sample;
            float v;
            if (is_float) {
                memcpy(&v, b, 4);
            } else if (bits == 16) {
                v = (int16_t)rd16(b) / 32768.0f;
            } else if (bits == 24) {
                int32_t x = (int32_t)((uint32_t)b[0] << 8 | (uint32_t)b[1] << 16 | (uint32_t)b[2] << 24) >> 8;
                v = x / 8388608.0f;
            } else {
                v = (int32_t)rd32(b) / 2147483648.0f;
            }
            interleaved[i * channels + c] = v;
        }
    }
    *out = {rate, channels, frames, interleaved};
    return true;
}

// Write sd.cpp's audio buffer to a temp WAV file (IEEE float, interleaved).
// The audio data is already interleaved (frame-major: ch0_s0, ch1_s0, ch0_s1,
// ...): sd.cpp permutes its [samples, channels] waveform before copying it out
// and its own writers read data[frame * channels + channel]. The buffer is
// copied as is. Reading it as planar swaps the halves of the clip between the
// channels and stretches each half to the full length (slow, one octave lower).
// Returns 0 on success and fills wav_path.
static int write_interleaved_float_wav(const gosd_audio* a, char* wav_path, size_t wav_path_sz) {
    if (!a || !a->data || a->sample_count == 0 || a->channels == 0 || a->sample_rate == 0) {
        return -1;
    }

    const char* temp_dir = getenv("TMPDIR");
    if (!temp_dir || temp_dir[0] == '\0') {
        temp_dir = "/tmp";
    }
    int path_len = snprintf(wav_path, wav_path_sz, "%s/gosd-audio-XXXXXX.wav", temp_dir);
    if (path_len < 0 || (size_t)path_len >= wav_path_sz) {
        fprintf(stderr, "temporary directory path is too long\n");
        return -1;
    }
    int fd = mkstemps(wav_path, 4);
    if (fd < 0) { perror("mkstemps wav"); return -1; }
    FILE* f = fdopen(fd, "wb");
    if (!f) { perror("fdopen wav"); close(fd); unlink(wav_path); return -1; }

    uint64_t frames = a->sample_count;
    uint32_t channels = a->channels;
    uint32_t sample_rate = a->sample_rate;
    uint64_t total_samples64 = frames * (uint64_t)channels;
    uint64_t data_bytes64 = total_samples64 * sizeof(float);
    if (data_bytes64 > 0xFFFFFFFFull - 44) {
        fprintf(stderr, "audio too large for 32-bit WAV (%" PRIu64 " bytes)\n", data_bytes64);
        fclose(f);
        unlink(wav_path);
        return -1;
    }
    uint32_t data_bytes = (uint32_t)data_bytes64;
    uint32_t riff_size = 36 + data_bytes;
    uint16_t fmt_code = 3;                // WAVE_FORMAT_IEEE_FLOAT
    uint16_t bits_per_sample = 32;
    uint16_t block_align = (uint16_t)(channels * sizeof(float));
    uint32_t byte_rate = sample_rate * block_align;
    uint16_t ch16 = (uint16_t)channels;
    uint32_t fmt_size = 16;

    fwrite("RIFF", 1, 4, f);
    fwrite(&riff_size, 4, 1, f);
    fwrite("WAVEfmt ", 1, 8, f);
    fwrite(&fmt_size, 4, 1, f);
    fwrite(&fmt_code, 2, 1, f);
    fwrite(&ch16, 2, 1, f);
    fwrite(&sample_rate, 4, 1, f);
    fwrite(&byte_rate, 4, 1, f);
    fwrite(&block_align, 2, 1, f);
    fwrite(&bits_per_sample, 2, 1, f);
    fwrite("data", 1, 4, f);
    fwrite(&data_bytes, 4, 1, f);

    // The buffer is interleaved already: [ch0_s0, ch1_s0, ch0_s1, ch1_s1, ...]
    if (fwrite(a->data, sizeof(float), (size_t)total_samples64, f) != (size_t)total_samples64) {
        perror("fwrite wav data");
        fclose(f);
        unlink(wav_path);
        return -1;
    }
    fclose(f);
    return 0;
}

// Video length in seconds ("-t" value) and the matching "apad" filter that
// pads the audio with silence up to that length. The filter needs ffmpeg 4.2
// or newer (whole_dur).
static void format_mux_duration(int num_frames, int fps, char* dur, size_t dur_size,
                                char* apad, size_t apad_size) {
    snprintf(dur, dur_size, "%.6f", fps > 0 ? (double)num_frames / fps : 0.0);
    snprintf(apad, apad_size, "apad=whole_dur=%s", dur);
}

// Pipe raw RGB/RGBA frames to ffmpeg stdin and let it produce an MP4 at dst.
// Uses fork+execvp to avoid shell interpretation of dst. When `audio` is
// non-null, the audio waveform is staged to a temp WAV and added as a second
// ffmpeg input so the final MP4 contains both video and AAC audio.
static int ffmpeg_mux_raw_to_mp4(const gosd_frame* frames, int num_frames, int fps,
                                  const gosd_audio* audio, const char* dst) {
    if (num_frames <= 0 || !frames || !frames[0].data) {
        fprintf(stderr, "ffmpeg_mux: empty frames\n");
        return 1;
    }
    int width = (int)frames[0].width;
    int height = (int)frames[0].height;
    int channels = (int)frames[0].channel;
    const char* pix_fmt_in = (channels == 4) ? "rgba" : "rgb24";

    char size_str[32];
    char fps_str[32];
    snprintf(size_str, sizeof(size_str), "%dx%d", width, height);
    snprintf(fps_str, sizeof(fps_str), "%d", fps);
    char dur_str[32];
    char apad_str[48];
    format_mux_duration(num_frames, fps, dur_str, sizeof(dur_str), apad_str, sizeof(apad_str));

    // Optional audio: write a temp WAV file if the model produced audio.
    char wav_path[4096] = {0};
    bool have_audio = false;
    if (audio && audio->data && audio->sample_count > 0 && audio->channels > 0 && audio->sample_rate > 0) {
        if (write_interleaved_float_wav(audio, wav_path, sizeof(wav_path)) == 0) {
            have_audio = true;
            fprintf(stderr, "ffmpeg_mux: audio %u Hz × %u ch × %" PRIu64 " frames → %s\n",
                    audio->sample_rate, audio->channels, audio->sample_count, wav_path);
        } else {
            fprintf(stderr, "ffmpeg_mux: failed to stage audio; producing silent video\n");
        }
    }

    int pipefd[2];
    if (pipe(pipefd) != 0) {
        perror("pipe");
        if (have_audio) unlink(wav_path);
        return 1;
    }

    pid_t pid = fork();
    if (pid < 0) {
        perror("fork");
        close(pipefd[0]); close(pipefd[1]);
        if (have_audio) unlink(wav_path);
        return 1;
    }

    if (pid == 0) {
        // child
        close(pipefd[1]);
        if (dup2(pipefd[0], STDIN_FILENO) < 0) { perror("dup2"); _exit(127); }
        close(pipefd[0]);
        std::vector<char*> argv;
        argv.push_back(const_cast<char*>("ffmpeg"));
        argv.push_back(const_cast<char*>("-y"));
        argv.push_back(const_cast<char*>("-hide_banner"));
        argv.push_back(const_cast<char*>("-loglevel"));
        argv.push_back(const_cast<char*>("warning"));
        // Input 0: raw video from stdin
        argv.push_back(const_cast<char*>("-f"));
        argv.push_back(const_cast<char*>("rawvideo"));
        argv.push_back(const_cast<char*>("-pix_fmt"));
        argv.push_back(const_cast<char*>(pix_fmt_in));
        argv.push_back(const_cast<char*>("-s"));
        argv.push_back(size_str);
        argv.push_back(const_cast<char*>("-framerate"));
        argv.push_back(fps_str);
        argv.push_back(const_cast<char*>("-i"));
        argv.push_back(const_cast<char*>("-"));
        // Input 1: optional audio WAV
        if (have_audio) {
            argv.push_back(const_cast<char*>("-i"));
            argv.push_back(wav_path);
            argv.push_back(const_cast<char*>("-map"));
            argv.push_back(const_cast<char*>("0:v:0"));
            argv.push_back(const_cast<char*>("-map"));
            argv.push_back(const_cast<char*>("1:a:0"));
            argv.push_back(const_cast<char*>("-c:a"));
            argv.push_back(const_cast<char*>("aac"));
            argv.push_back(const_cast<char*>("-b:a"));
            argv.push_back(const_cast<char*>("192k"));
            // Make the audio exactly as long as the video: pad it with silence
            // and cut the output at the last frame. The audio of a video model
            // can be a little shorter than the frames (MiniMax-H3 has 40 audio
            // latents per second, so 56 frames at 24 fps give 2.325 s of audio
            // for 2.333 s of video), and -shortest then dropped the last frame.
            // An audio buffer longer than the video is still cut.
            if (fps > 0) {
                argv.push_back(const_cast<char*>("-af"));
                argv.push_back(apad_str);
                argv.push_back(const_cast<char*>("-t"));
                argv.push_back(dur_str);
            } else {
                argv.push_back(const_cast<char*>("-shortest"));
            }
        }
        argv.push_back(const_cast<char*>("-c:v"));
        argv.push_back(const_cast<char*>("libx264"));
        argv.push_back(const_cast<char*>("-pix_fmt"));
        argv.push_back(const_cast<char*>("yuv420p"));
        argv.push_back(const_cast<char*>("-movflags"));
        argv.push_back(const_cast<char*>("+faststart"));
        // Force MP4 container. Distributed LocalAI hands us a staging
        // path (e.g. /staging/localai-output-NNN.tmp) with a non-standard
        // extension; relying on filename suffix makes ffmpeg bail with
        // "Unable to choose an output format".
        argv.push_back(const_cast<char*>("-f"));
        argv.push_back(const_cast<char*>("mp4"));
        argv.push_back(const_cast<char*>(dst));
        argv.push_back(nullptr);
        execvp(argv[0], argv.data());
        perror("execvp ffmpeg");
        _exit(127);
    }

    // parent
    close(pipefd[0]);

    // Ignore SIGPIPE so a dying ffmpeg surfaces via write() errno instead of killing us.
    signal(SIGPIPE, SIG_IGN);

    for (int i = 0; i < num_frames; i++) {
        if (!frames[i].data) continue;
        size_t frame_bytes = (size_t)frames[i].width * frames[i].height * frames[i].channel;
        const uint8_t* p = frames[i].data;
        size_t remaining = frame_bytes;
        while (remaining > 0) {
            ssize_t n = write(pipefd[1], p, remaining);
            if (n < 0) {
                if (errno == EINTR) continue;
                perror("write frame to ffmpeg");
                close(pipefd[1]);
                int status;
                waitpid(pid, &status, 0);
                if (have_audio) unlink(wav_path);
                return 1;
            }
            p += n;
            remaining -= (size_t)n;
        }
    }
    close(pipefd[1]);

    int status = 0;
    while (waitpid(pid, &status, 0) < 0) {
        if (errno != EINTR) {
            perror("waitpid");
            if (have_audio) unlink(wav_path);
            return 1;
        }
    }
    if (have_audio) unlink(wav_path);
    if (!WIFEXITED(status) || WEXITSTATUS(status) != 0) {
        fprintf(stderr, "ffmpeg exited with status %d\n", status);
        return 1;
    }
    return 0;
}

int gen_video(gosd_vid_gen_params_t *p_raw, int steps, char *dst, float cfg_scale, int fps, char *init_image, char *end_image, char *ref_images, char *ref_videos, char *ref_audios) {
    if (!p_raw) return 1;
    // The Go side hands over ownership of the parameters.
    std::unique_ptr<gosd_vid_gen_params_t> p(p_raw);
    if (!dst || strlen(dst) == 0) {
        fprintf(stderr, "gen_video: dst is empty\n");
        return 1;
    }

    fprintf(stderr, "Generating video: %dx%d, frames=%d, fps=%d, steps=%d, cfg=%.2f\n",
            p->width, p->height, p->video_frames, fps, steps, cfg_scale);

    opt_ptr gen(sd_vid_gen_opt_create());
    if (!gen) {
        fprintf(stderr, "failed to allocate generation options\n");
        return 1;
    }
    sd_opt_t* o = gen.get();

    opt_str(o, "prompt", p->prompt.c_str());
    opt_str(o, "negative_prompt", p->negative_prompt.c_str());
    opt_int(o, "width", p->width);
    opt_int(o, "height", p->height);
    opt_int(o, "seed", p->seed);
    if (p->video_frames > 0) opt_int(o, "video_frames", p->video_frames);
    // Pin output fps; upstream uses it for audio sync (and we also mux at this rate).
    opt_int(o, "fps", fps);
    if (!p->loras.empty() &&
        sd_opt_set_loras(o, "loras", p->loras.data(), p->loras.size()) != SD_STATUS_OK) {
        fprintf(stderr, "LoRAs rejected: %s\n", sd_get_last_error());
    }

    // Sampling. MoE models use the high-noise set during the first phase;
    // upstream shares the scheduler and flow shift between both passes.
    apply_sampling_options(o, steps, cfg_scale);
    {
        static const int skip_layers[] = {7, 8, 9};
        opt_str(o, "high_noise_sample.method", sampler_name.c_str());
        opt_int(o, "high_noise_sample.steps", steps);
        opt_float(o, "high_noise_sample.guidance.txt_cfg", cfg_scale);
        if (sd_opt_set_ints(o, "high_noise_sample.guidance.slg.layers", skip_layers, 3) != SD_STATUS_OK) {
            fprintf(stderr, "high-noise SLG layers rejected: %s\n", sd_get_last_error());
        }
    }

    // Owned tensors. Options retain what they need, these handles are released
    // when the function returns.
    std::vector<tensor_ptr> keep;
    auto set_one = [&](const char* key, const std::string& path, int w, int h) {
        auto buf = load_resized(path.c_str(), 3, w, h);
        if (!buf) return false;
        tensor_ptr t = make_u8_image(buf.get(), w, h, 3);
        if (!t || sd_opt_set_tensor(o, key, t.get()) != SD_STATUS_OK) {
            fprintf(stderr, "%s rejected: %s\n", key, sd_get_last_error());
            return false;
        }
        keep.push_back(std::move(t));
        return true;
    };
    if (init_image && strlen(init_image) > 0 && !set_one("init_image", init_image, p->width, p->height)) return 1;
    if (end_image && strlen(end_image) > 0 && !set_one("end_image", end_image, p->width, p->height)) return 1;

    // MiniMax-H3 Ref2VA references. Keyframes and references are mutually
    // exclusive upstream; the Go side rejects the mix before getting here.
    // The lists arrive flattened because purego cannot pass arrays of strings
    // here: ref_images and ref_audios are newline-separated paths, ref_videos
    // is one "frames_dir\x1ffps\x1faudio" record per line.
    auto split = [](const char* list, char sep) {
        std::vector<std::string> out;
        if (!list) return out;
        std::string cur;
        for (const char* c = list; *c; c++) {
            if (*c == sep) { if (!cur.empty()) out.push_back(cur); cur.clear(); }
            else cur += *c;
        }
        if (!cur.empty()) out.push_back(cur);
        return out;
    };
    // Load an image at its native size: upstream keeps the aspect ratio of a
    // reference image and only downscales it when it exceeds the output canvas.
    auto load_native = [](const std::string& path, std::vector<uint8_t>* pixels, int* w, int* h) {
        int c = 0;
        uint8_t* buf = stbi_load(path.c_str(), w, h, &c, 3);
        if (!buf) { fprintf(stderr, "Failed to load reference image from '%s'\n", path.c_str()); return false; }
        pixels->assign(buf, buf + (size_t)*w * *h * 3);
        stbi_image_free(buf);
        return true;
    };
    // Make an F32 [channels, samples] tensor and wrap it with its rate.
    auto make_audio = [&](const std::string& path, sd_audio_t* out) {
        gosd_audio a;
        if (!load_wav_audio(path.c_str(), &a)) return false;
        std::unique_ptr<float, decltype(&free)> owned(a.data, free);
        int64_t shape[2] = {(int64_t)a.channels, (int64_t)a.sample_count};
        tensor_ptr t(sd_tensor_create(SD_TENSOR_F32, 2, shape, a.data,
                                      (size_t)a.channels * a.sample_count * sizeof(float)));
        if (!t) { fprintf(stderr, "audio tensor failed: %s\n", sd_get_last_error()); return false; }
        out->sample_rate = a.sample_rate;
        out->data = t.get();
        keep.push_back(std::move(t));
        return true;
    };

    std::vector<tensor_ptr> ref_imgs;
    for (const auto& path : split(ref_images, '\n')) {
        std::vector<uint8_t> px; int w = 0, h = 0;
        if (!load_native(path, &px, &w, &h)) return 1;
        tensor_ptr t = make_u8_image(px.data(), w, h, 3);
        if (!t) return 1;
        ref_imgs.push_back(std::move(t));
    }
    if (!ref_imgs.empty()) {
        std::vector<const sd_tensor_t*> raw;
        for (auto& r : ref_imgs) raw.push_back(r.get());
        if (sd_opt_set_tensors(o, "ref_images", raw.data(), raw.size()) != SD_STATUS_OK) {
            fprintf(stderr, "ref_images rejected: %s\n", sd_get_last_error());
            return 1;
        }
    }

    std::vector<sd_audio_t> ref_auds;
    for (const auto& path : split(ref_audios, '\n')) {
        sd_audio_t aud = {0, nullptr};
        if (!make_audio(path, &aud)) return 1;
        ref_auds.push_back(aud);
    }
    if (!ref_auds.empty() &&
        sd_opt_set_audios(o, "ref_audios", ref_auds.data(), ref_auds.size()) != SD_STATUS_OK) {
        fprintf(stderr, "ref_audios rejected: %s\n", sd_get_last_error());
        return 1;
    }

    std::vector<sd_video_t> ref_vids;
    for (const auto& rec : split(ref_videos, '\n')) {
        size_t sep1 = rec.find('\x1f');
        size_t sep2 = sep1 == std::string::npos ? std::string::npos : rec.find('\x1f', sep1 + 1);
        if (sep2 == std::string::npos) { fprintf(stderr, "Malformed reference video record\n"); return 1; }
        std::string dir = rec.substr(0, sep1);
        int vid_fps = atoi(rec.substr(sep1 + 1, sep2 - sep1 - 1).c_str());
        std::string vid_audio = rec.substr(sep2 + 1);

        std::vector<std::string> files;
        std::error_code ec;
        for (const auto& entry : std::filesystem::directory_iterator(dir, ec)) {
            if (entry.is_regular_file()) files.push_back(entry.path().string());
        }
        if (ec || files.empty()) {
            fprintf(stderr, "Reference video directory '%s' is missing or has no frames\n", dir.c_str());
            return 1;
        }
        std::sort(files.begin(), files.end());
        // One U8 [3, W, H, T] tensor: all frames must have the same size.
        std::vector<uint8_t> pixels;
        int vw = 0, vh = 0;
        for (const auto& file : files) {
            std::vector<uint8_t> px; int w = 0, h = 0;
            if (!load_native(file, &px, &w, &h)) return 1;
            if (vw == 0) { vw = w; vh = h; }
            if (w != vw || h != vh) {
                fprintf(stderr, "Reference video frames in '%s' differ in size\n", dir.c_str());
                return 1;
            }
            pixels.insert(pixels.end(), px.begin(), px.end());
        }
        int64_t shape[4] = {3, vw, vh, (int64_t)files.size()};
        tensor_ptr t(sd_tensor_create(SD_TENSOR_U8, 4, shape, pixels.data(), pixels.size()));
        if (!t) { fprintf(stderr, "reference video tensor failed: %s\n", sd_get_last_error()); return 1; }
        sd_video_t vid;
        vid.data = t.get();
        vid.fps = vid_fps;
        vid.audio = {0, nullptr};
        keep.push_back(std::move(t));
        if (!vid_audio.empty() && !make_audio(vid_audio, &vid.audio)) return 1;
        ref_vids.push_back(vid);
    }
    if (!ref_vids.empty() &&
        sd_opt_set_videos(o, "ref_videos", ref_vids.data(), ref_vids.size()) != SD_STATUS_OK) {
        fprintf(stderr, "ref_videos rejected: %s\n", sd_get_last_error());
        return 1;
    }

    // Generate
    sd_result_t* generated = nullptr;
    sd_status_t status = sd_generate_video(sd_c, o, &generated);
    result_ptr output(generated);

    size_t frame_count = output ? sd_result_get_image_count(output.get()) : 0;
    if (status != SD_STATUS_OK || frame_count == 0) {
        fprintf(stderr, "generate_video produced no frames: %s\n", sd_get_last_error());
        return 1;
    }

    std::vector<gosd_frame> frames;
    for (size_t i = 0; i < frame_count; i++) {
        const sd_tensor_t* f = sd_result_get_image(output.get(), i);
        if (!f || sd_tensor_rank(f) != 3) { fprintf(stderr, "invalid frame %zu\n", i); return 1; }
        frames.push_back({(int)sd_tensor_dim(f, 1), (int)sd_tensor_dim(f, 2), (int)sd_tensor_dim(f, 0),
                          (const uint8_t*)sd_tensor_data(f)});
    }

    // Optional audio produced by the model: F32 [channels, samples], interleaved.
    gosd_audio audio = {0, 0, 0, nullptr};
    const sd_audio_t* out_audio = sd_result_get_audio(output.get());
    if (out_audio && out_audio->data && sd_tensor_rank(out_audio->data) == 2) {
        audio.sample_rate = out_audio->sample_rate;
        audio.channels = (uint32_t)sd_tensor_dim(out_audio->data, 0);
        audio.sample_count = (uint64_t)sd_tensor_dim(out_audio->data, 1);
        audio.data = (float*)const_cast<void*>(sd_tensor_data(out_audio->data));
    }
    int out_fps = sd_result_get_fps(output.get());
    if (out_fps <= 0) out_fps = fps;

    fprintf(stderr, "Generated %zu frames, muxing to %s via ffmpeg\n", frame_count, dst);

    int rc = ffmpeg_mux_raw_to_mp4(frames.data(), (int)frame_count, out_fps, &audio, dst);

    if (rc == 0) {
        fprintf(stderr, "gen_video done: %s\n", dst);
    }
    fflush(stderr);
    return rc;
}

int unload() {
    sd_ctx_free(sd_c);
    sd_c = nullptr;
    return 0;
}


struct gosd_upscaler {
    sd_upscaler_t* ctx = nullptr;
    ~gosd_upscaler() { if (ctx) sd_upscaler_free(ctx); }
};

void* gosd_upscaler_create(const char* path, bool direct, int threads, int tile_size,
                          const char* backend, const char* params_backend) {
    if (!path || !*path || tile_size < 0) return nullptr;
    try {
        opt_ptr opt(sd_upscaler_opt_create());
        if (!opt) return nullptr;
        // Setters copy their strings, nothing retains a Go pointer.
        if (!opt_str(opt.get(), "model_path", path) ||
            !opt_bool(opt.get(), "conv_direct", direct) ||
            !opt_int(opt.get(), "n_threads", threads) ||
            !opt_int(opt.get(), "tile_size", tile_size) ||
            !opt_str(opt.get(), "backend", backend) ||
            !opt_str(opt.get(), "params_backend", params_backend)) {
            return nullptr;
        }
        auto h = std::make_unique<gosd_upscaler>();
        if (sd_upscaler_create(opt.get(), &h->ctx) != SD_STATUS_OK || !h->ctx) {
            fprintf(stderr, "upscaler load failed: %s\n", sd_get_last_error());
            h->ctx = nullptr;
            return nullptr;
        }
        return h.release();
    } catch (...) { return nullptr; }
}

int gosd_upscaler_scale(void* handle) {
    if (!handle) return 0;
    try { return sd_upscaler_get_scale(static_cast<gosd_upscaler*>(handle)->ctx); }
    catch (...) { return 0; }
}

void gosd_upscaler_destroy(void* handle) {
    delete static_cast<gosd_upscaler*>(handle);
}

int gosd_upscaler_run(void* handle, const char* src, const char* dst, int scale) {
    if (!handle || !src || !*src || !dst || !*dst || scale <= 0) return 1;
    int width = 0, height = 0, channels = 0;
    std::unique_ptr<unsigned char, decltype(&stbi_image_free)> input(
        stbi_load(src, &width, &height, &channels, 3), stbi_image_free);
    if (!input || width <= 0 || height <= 0) return 2;
    const uint64_t out_width = uint64_t(width) * scale;
    const uint64_t out_height = uint64_t(height) * scale;
    if (out_width > INT_MAX / 3 || out_height > INT_MAX ||
        out_width * out_height > SIZE_MAX / 3) return 4;
    try {
        tensor_ptr image = make_u8_image(input.get(), width, height, 3);
        opt_ptr opt(sd_upscale_opt_create());
        if (!image || !opt) return 3;
        if (sd_opt_set_tensor(opt.get(), "image", image.get()) != SD_STATUS_OK) return 3;
        sd_result_t* raw = nullptr;
        sd_status_t status = sd_upscale(static_cast<gosd_upscaler*>(handle)->ctx, opt.get(), &raw);
        result_ptr output(raw);
        if (status != SD_STATUS_OK) {
            fprintf(stderr, "upscale failed: %s\n", sd_get_last_error());
            return 3;
        }
        if (!output || sd_result_get_image_count(output.get()) != 1) return 4;
        const sd_tensor_t* result = sd_result_get_image(output.get(), 0);
        if (!result || sd_tensor_rank(result) != 3 || !sd_tensor_data(result)) return 4;
        // The scale is fixed by the loaded model; check it matches the request.
        if (sd_tensor_dim(result, 0) != 3 || uint64_t(sd_tensor_dim(result, 1)) != out_width ||
            uint64_t(sd_tensor_dim(result, 2)) != out_height) return 4;
        if (!stbi_write_png(dst, int(out_width), int(out_height), 3,
                            sd_tensor_data(result), int(out_width) * 3)) return 5;
        return 0;
    } catch (...) { return 3; }
}
