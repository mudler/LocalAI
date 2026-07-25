// SPDX-License-Identifier: MIT

#include "model_config.h"

#include <limits>
#include <stdexcept>
#include <string>

namespace audio_cpp {
namespace {

using engine::core::BackendType;
using engine::runtime::RunMode;
using engine::runtime::VoiceTaskKind;

std::string require_option(
    const std::unordered_map<std::string, std::string> & options,
    const std::string & name) {
    const auto it = options.find(name);
    if (it == options.end() || it->second.empty()) {
        throw std::invalid_argument("audio.cpp model config requires " + name);
    }
    return it->second;
}

VoiceTaskKind parse_task(const std::string & value) {
    static const std::unordered_map<std::string, VoiceTaskKind> tasks = {
        {"vad", VoiceTaskKind::Vad},
        {"asr", VoiceTaskKind::Asr},
        {"diarization", VoiceTaskKind::Diarization},
        {"source-separation", VoiceTaskKind::SourceSeparation},
        {"audio-generation", VoiceTaskKind::AudioGeneration},
        {"tts", VoiceTaskKind::Tts},
        {"voice-cloning", VoiceTaskKind::VoiceCloning},
        {"voice-conversion", VoiceTaskKind::VoiceConversion},
        {"speech-to-speech", VoiceTaskKind::SpeechToSpeech},
        {"alignment", VoiceTaskKind::Alignment},
        {"voice-design", VoiceTaskKind::VoiceDesign},
        {"speaker-recognition", VoiceTaskKind::SpeakerRecognition},
        {"svc", VoiceTaskKind::Svc},
    };
    const auto it = tasks.find(value);
    if (it == tasks.end()) {
        throw std::invalid_argument("unsupported audio.cpp task: " + value);
    }
    return it->second;
}

RunMode parse_mode(const std::string & value) {
    if (value == "offline") {
        return RunMode::Offline;
    }
    if (value == "streaming") {
        return RunMode::Streaming;
    }
    throw std::invalid_argument("unsupported audio.cpp mode: " + value);
}

BackendType parse_backend(const std::string & value) {
    if (value == "cpu") {
        return BackendType::Cpu;
    }
    if (value == "cuda") {
        return BackendType::Cuda;
    }
    if (value == "vulkan") {
        return BackendType::Vulkan;
    }
    if (value == "metal") {
        return BackendType::Metal;
    }
    if (value == "best") {
        return BackendType::BestAvailable;
    }
    throw std::invalid_argument("unsupported audio.cpp backend: " + value);
}

int parse_integer(
    const std::string & name,
    const std::string & value,
    int minimum) {
    size_t parsed = 0;
    long result = 0;
    try {
        result = std::stol(value, &parsed);
    } catch (const std::exception &) {
        throw std::invalid_argument("invalid audio.cpp " + name + ": " + value);
    }
    if (parsed != value.size() ||
        result < minimum ||
        result > std::numeric_limits<int>::max()) {
        throw std::invalid_argument("invalid audio.cpp " + name + ": " + value);
    }
    return static_cast<int>(result);
}

void copy_namespaced_option(
    const std::string & key,
    const std::string & prefix,
    const std::string & value,
    std::unordered_map<std::string, std::string> & destination) {
    const std::string name = key.substr(prefix.size());
    if (name.empty()) {
        throw std::invalid_argument("audio.cpp option namespace requires a name: " + key);
    }
    destination[name] = value;
}

}  // namespace

AudioCppModelConfig parse_model_config(
    const std::filesystem::path & model_path,
    const std::unordered_map<std::string, std::string> & options) {
    AudioCppModelConfig config;
    config.model_path = model_path;
    config.family = require_option(options, "family");
    config.task.task = parse_task(require_option(options, "task"));
    config.task.mode = RunMode::Offline;
    config.load.model_path = model_path;
    config.load.family_hint = config.family;
    config.session.backend.type = BackendType::Cpu;

    if (const auto it = options.find("mode"); it != options.end()) {
        config.task.mode = parse_mode(it->second);
    }
    if (const auto it = options.find("backend"); it != options.end()) {
        config.session.backend.type = parse_backend(it->second);
    }
    if (const auto it = options.find("device"); it != options.end()) {
        config.session.backend.device = parse_integer("device", it->second, 0);
    }
    if (const auto it = options.find("threads"); it != options.end()) {
        config.session.backend.threads = parse_integer("threads", it->second, 1);
    }
    if (const auto it = options.find("model_spec"); it != options.end()) {
        config.load.model_spec_override = std::filesystem::path(it->second);
    }
    if (const auto it = options.find("config_id"); it != options.end()) {
        config.load.config_id = it->second;
    }
    if (const auto it = options.find("weight_id"); it != options.end()) {
        config.load.weight_id = it->second;
    }

    for (const auto & [key, value] : options) {
        if (key.rfind("load.", 0) == 0) {
            copy_namespaced_option(key, "load.", value, config.load.options);
        } else if (key.rfind("session.", 0) == 0) {
            copy_namespaced_option(key, "session.", value, config.session.options);
        }
    }

    return config;
}

}  // namespace audio_cpp
