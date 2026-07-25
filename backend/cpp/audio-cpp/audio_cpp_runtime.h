// SPDX-License-Identifier: MIT

#pragma once

#include "model_config.h"

#include "engine/framework/runtime/registry.h"
#include "engine/framework/runtime/session.h"

#include <memory>
#include <mutex>

namespace audio_cpp {

class AudioCppRuntime {
public:
    AudioCppRuntime();
    explicit AudioCppRuntime(engine::runtime::ModelRegistry registry);
    ~AudioCppRuntime();

    AudioCppRuntime(const AudioCppRuntime &) = delete;
    AudioCppRuntime & operator=(const AudioCppRuntime &) = delete;

    void load(const AudioCppModelConfig & config);
    void free();

    engine::runtime::TaskResult run(
        const engine::runtime::TaskRequest & request);
    void start_stream(const engine::runtime::TaskRequest & request);
    engine::runtime::StreamEvent process_audio_chunk(
        const engine::runtime::AudioChunk & chunk);
    engine::runtime::TaskResult finish_stream();

private:
    engine::runtime::IVoiceTaskSession & require_session_locked();
    engine::runtime::IOfflineVoiceTaskSession & require_offline_locked();
    engine::runtime::IStreamingVoiceTaskSession & require_streaming_locked();
    void free_locked();

    std::mutex mutex_;
    engine::runtime::ModelRegistry registry_;
    std::unique_ptr<engine::runtime::ILoadedVoiceModel> model_;
    std::unique_ptr<engine::runtime::IVoiceTaskSession> session_;
};

}  // namespace audio_cpp
