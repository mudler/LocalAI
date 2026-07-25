// SPDX-License-Identifier: MIT

#include "audio_cpp_runtime.h"

#include <algorithm>
#include <stdexcept>
#include <string>
#include <utility>

namespace audio_cpp {
namespace {

using engine::runtime::CapabilitySet;
using engine::runtime::RunMode;
using engine::runtime::TaskSpec;

const engine::runtime::TaskCapability * find_task_capability(
    const CapabilitySet & capabilities,
    const TaskSpec & task) {
    const auto it = std::find_if(
        capabilities.supported_tasks.begin(),
        capabilities.supported_tasks.end(),
        [&](const engine::runtime::TaskCapability & capability) {
            return capability.task == task.task;
        });
    return it == capabilities.supported_tasks.end() ? nullptr : &*it;
}

void validate_capability(
    const engine::runtime::ILoadedVoiceModel & model,
    const AudioCppModelConfig & config) {
    if (model.metadata().family != config.family) {
        throw std::runtime_error(
            "loaded audio.cpp model family '" + model.metadata().family +
            "' does not match requested family '" + config.family + "'");
    }

    const auto * capability = find_task_capability(
        model.capabilities(),
        config.task);
    if (capability == nullptr) {
        throw std::runtime_error(
            "loaded audio.cpp model does not support requested task '" +
            std::string(engine::runtime::to_string(config.task.task)) + "'");
    }
    if (std::find(
            capability->modes.begin(),
            capability->modes.end(),
            config.task.mode) == capability->modes.end()) {
        throw std::runtime_error(
            "loaded audio.cpp model does not support requested mode '" +
            std::string(engine::runtime::to_string(config.task.mode)) +
            "' for task '" +
            std::string(engine::runtime::to_string(config.task.task)) + "'");
    }
}

void validate_session(
    const engine::runtime::IVoiceTaskSession & session,
    const AudioCppModelConfig & config) {
    if (session.family() != config.family) {
        throw std::runtime_error("audio.cpp session returned the wrong family");
    }
    if (session.task_kind() != config.task.task) {
        throw std::runtime_error("audio.cpp session returned the wrong task");
    }
    if (session.run_mode() != config.task.mode) {
        throw std::runtime_error("audio.cpp session returned the wrong mode");
    }
    if (config.task.mode == RunMode::Offline &&
        dynamic_cast<const engine::runtime::IOfflineVoiceTaskSession *>(&session) == nullptr) {
        throw std::runtime_error("audio.cpp session does not implement offline execution");
    }
    if (config.task.mode == RunMode::Streaming &&
        dynamic_cast<const engine::runtime::IStreamingVoiceTaskSession *>(&session) == nullptr) {
        throw std::runtime_error("audio.cpp session does not implement streaming execution");
    }
}

}  // namespace

AudioCppRuntime::AudioCppRuntime()
    : registry_(engine::runtime::make_default_registry()) {}

AudioCppRuntime::AudioCppRuntime(engine::runtime::ModelRegistry registry)
    : registry_(std::move(registry)) {}

AudioCppRuntime::~AudioCppRuntime() {
    free();
}

void AudioCppRuntime::load(const AudioCppModelConfig & config) {
    std::lock_guard<std::mutex> lock(mutex_);

    auto candidate_model = registry_.load(config.load);
    if (candidate_model == nullptr) {
        throw std::runtime_error("audio.cpp registry returned a null model");
    }
    validate_capability(*candidate_model, config);

    auto candidate_session = candidate_model->create_task_session(
        config.task,
        config.session);
    if (candidate_session == nullptr) {
        throw std::runtime_error("audio.cpp model returned a null session");
    }
    validate_session(*candidate_session, config);

    free_locked();
    model_ = std::move(candidate_model);
    session_ = std::move(candidate_session);
}

void AudioCppRuntime::free() {
    std::lock_guard<std::mutex> lock(mutex_);
    free_locked();
}

engine::runtime::TaskResult AudioCppRuntime::run(
    const engine::runtime::TaskRequest & request) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto & session = require_session_locked();
    session.prepare(engine::runtime::build_preparation_request(request));
    return require_offline_locked().run(request);
}

void AudioCppRuntime::start_stream(
    const engine::runtime::TaskRequest & request) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto & session = require_session_locked();
    session.prepare(engine::runtime::build_preparation_request(request));
    require_streaming_locked().start_stream(request);
}

engine::runtime::StreamEvent AudioCppRuntime::process_audio_chunk(
    const engine::runtime::AudioChunk & chunk) {
    std::lock_guard<std::mutex> lock(mutex_);
    return require_streaming_locked().process_audio_chunk(chunk);
}

engine::runtime::TaskResult AudioCppRuntime::finish_stream() {
    std::lock_guard<std::mutex> lock(mutex_);
    return require_streaming_locked().finish_stream();
}

engine::runtime::IVoiceTaskSession & AudioCppRuntime::require_session_locked() {
    if (session_ == nullptr) {
        throw std::runtime_error("audio.cpp runtime has no loaded session");
    }
    return *session_;
}

engine::runtime::IOfflineVoiceTaskSession &
AudioCppRuntime::require_offline_locked() {
    auto * offline = dynamic_cast<engine::runtime::IOfflineVoiceTaskSession *>(
        &require_session_locked());
    if (offline == nullptr) {
        throw std::runtime_error("loaded audio.cpp session is not offline");
    }
    return *offline;
}

engine::runtime::IStreamingVoiceTaskSession &
AudioCppRuntime::require_streaming_locked() {
    auto * streaming = dynamic_cast<engine::runtime::IStreamingVoiceTaskSession *>(
        &require_session_locked());
    if (streaming == nullptr) {
        throw std::runtime_error("loaded audio.cpp session is not streaming");
    }
    return *streaming;
}

void AudioCppRuntime::free_locked() {
    session_.reset();
    model_.reset();
}

}  // namespace audio_cpp
