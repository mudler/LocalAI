// SPDX-License-Identifier: MIT

#pragma once

#include "engine/framework/runtime/model.h"
#include "engine/framework/runtime/session.h"

#include <filesystem>
#include <string>
#include <unordered_map>

namespace audio_cpp {

struct AudioCppModelConfig {
    std::filesystem::path model_path;
    std::string family;
    engine::runtime::TaskSpec task;
    engine::runtime::ModelLoadRequest load;
    engine::runtime::SessionOptions session;
};

AudioCppModelConfig parse_model_config(
    const std::filesystem::path & model_path,
    const std::unordered_map<std::string, std::string> & options);

}  // namespace audio_cpp
