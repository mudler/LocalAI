// SPDX-License-Identifier: MIT
#include <skintokens/skintokens.h>
#include <nlohmann/json.hpp>
#include <cstdio>
#include <exception>
#include <string>

// Keep compiler-dependent option structs behind the native boundary.
extern "C" ST_API int localai_skintokens_load(const char *bundle, const char *device,
    uint32_t threads, const char *backend_directory, st_model **model,
    char *error, size_t capacity) {
    auto options = st_default_runtime_options();
    const std::string name(device);
    if (name == "cpu") options.device = ST_DEVICE_CPU;
    else if (name == "vulkan") options.device = ST_DEVICE_VULKAN;
    else if (name != "auto") return ST_INVALID_ARGUMENT;
    options.threads = threads;
    options.backend_directory = backend_directory;
    return st_model_load(bundle, &options, model, error, capacity);
}

extern "C" ST_API int localai_skintokens_run(st_model *model, const char *operation,
    const char *mesh, const char *secondary, const char *destination,
    const char *parameters, int *learned, char *error, size_t capacity) {
    try {
        const auto params = nlohmann::json::parse(parameters);
        const std::string op(operation);
        if (op == "retarget") {
            st_retarget_options *options = nullptr;
            auto status = st_retarget_options_create(&options, error, capacity);
            if (status != ST_OK) return status;
            status = st_retarget_soma30_glb_files(mesh, secondary, destination,
                nullptr, options, error, capacity);
            st_retarget_options_free(options);
            return status;
        }
        auto options = st_default_generation_options();
        if (params.contains("seed")) options.seed = std::stoull(params.at("seed").get<std::string>());
        if (params.contains("top_k")) options.top_k = static_cast<uint32_t>(std::stoul(params.at("top_k").get<std::string>()));
        if (params.contains("top_p")) options.top_p = std::stof(params.at("top_p").get<std::string>());
        if (params.contains("temperature")) options.temperature = std::stof(params.at("temperature").get<std::string>());
        if (params.contains("repetition_penalty")) options.repetition_penalty = std::stof(params.at("repetition_penalty").get<std::string>());
        if (params.contains("beams")) options.beams = static_cast<uint32_t>(std::stoul(params.at("beams").get<std::string>()));
        if (params.contains("max_tokens")) options.max_tokens = std::stoull(params.at("max_tokens").get<std::string>());
        if (op == "rig") {
            options.target_rig = ST_TARGET_GENERATED;
            return st_rig_file(model, mesh, destination, &options, learned, error, capacity);
        }
        if (op == "skin") {
            const auto fit = params.value("fit", "global");
            options.target_rig = ST_TARGET_SOMA30;
            return st_skin_files(model, mesh, secondary, destination,
                fit == "none" ? ST_FIT_NONE : ST_FIT_GLOBAL_SIMILARITY,
                &options, learned, error, capacity);
        }
        return ST_INVALID_ARGUMENT;
    } catch (const std::exception &exception) {
        if (capacity) std::snprintf(error, capacity, "%s", exception.what());
        return ST_INVALID_ARGUMENT;
    }
}
