// SPDX-License-Identifier: MIT

#include "audio_cpp_runtime.h"
#include "model_config.h"

#include "engine/framework/runtime/model.h"
#include "engine/framework/runtime/registry.h"
#include "engine/framework/runtime/session.h"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <exception>
#include <filesystem>
#include <future>
#include <iostream>
#include <memory>
#include <mutex>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

namespace {

using engine::runtime::AudioChunk;
using engine::runtime::CapabilitySet;
using engine::runtime::ILoadedVoiceModel;
using engine::runtime::IOfflineVoiceTaskSession;
using engine::runtime::IStreamingVoiceTaskSession;
using engine::runtime::IVoiceModelLoader;
using engine::runtime::IVoiceTaskSession;
using engine::runtime::ModelInspection;
using engine::runtime::ModelLoadRequest;
using engine::runtime::ModelMetadata;
using engine::runtime::RunMode;
using engine::runtime::SessionOptions;
using engine::runtime::SessionPreparationRequest;
using engine::runtime::StreamEvent;
using engine::runtime::TaskCapability;
using engine::runtime::TaskRequest;
using engine::runtime::TaskResult;
using engine::runtime::TaskSpec;
using engine::runtime::VoiceTaskKind;

void require(bool condition, const std::string & message) {
    if (!condition) {
        throw std::runtime_error(message);
    }
}

template <typename Function>
void require_throws(Function && function, const std::string & expected) {
    try {
        function();
    } catch (const std::exception & error) {
        require(
            std::string(error.what()).find(expected) != std::string::npos,
            "expected error containing '" + expected + "', got '" + error.what() + "'");
        return;
    }
    throw std::runtime_error("expected exception containing '" + expected + "'");
}

struct SessionGate {
    std::mutex mutex;
    std::condition_variable condition;
    bool first_entered = false;
    bool release_first = false;
    std::atomic<int> entries{0};
};

struct FakeState {
    std::mutex mutex;
    std::vector<std::string> events;
    CapabilitySet capabilities;
    bool fail_load = false;
    bool fail_session = false;
    int generation = 0;
    std::shared_ptr<SessionGate> gate;

    void record(std::string event) {
        std::lock_guard<std::mutex> lock(mutex);
        events.push_back(std::move(event));
    }
};

class FakeSession final
    : public IOfflineVoiceTaskSession,
      public IStreamingVoiceTaskSession {
public:
    FakeSession(
        std::shared_ptr<FakeState> state,
        int generation,
        TaskSpec task,
        SessionOptions options)
        : state_(std::move(state)),
          generation_(generation),
          task_(task),
          options_(std::move(options)) {}

    ~FakeSession() override {
        state_->record("session-" + std::to_string(generation_) + "-destroyed");
    }

    std::string family() const override { return "fake-family"; }
    VoiceTaskKind task_kind() const override { return task_.task; }
    RunMode run_mode() const override { return task_.mode; }

    void prepare(const SessionPreparationRequest & request) override {
        prepared_ = request;
    }

    TaskResult run(const TaskRequest &) override {
        if (state_->gate != nullptr) {
            const int entry = ++state_->gate->entries;
            if (entry == 1) {
                std::unique_lock<std::mutex> lock(state_->gate->mutex);
                state_->gate->first_entered = true;
                state_->gate->condition.notify_all();
                state_->gate->condition.wait(
                    lock,
                    [&] { return state_->gate->release_first; });
            }
        }

        TaskResult result;
        result.text_output = engine::runtime::Transcript{
            "generation-" + std::to_string(generation_),
            "en",
        };
        return result;
    }

    engine::runtime::StreamingPolicy streaming_policy() const override {
        engine::runtime::StreamingPolicy policy;
        policy.input = engine::runtime::StreamingInputKind::AudioChunks;
        policy.output = engine::runtime::StreamingOutputKind::PullEvents;
        policy.preferred_audio_chunk_samples = 160;
        return policy;
    }

    void start_stream(const TaskRequest &) override {
        streaming_ = true;
    }

    std::optional<StreamEvent> next_stream_event() override {
        return std::nullopt;
    }

    void set_stream_event_sink(engine::runtime::StreamEventCallback sink) override {
        sink_ = std::move(sink);
    }

    TaskResult finish_stream() override {
        streaming_ = false;
        return run({});
    }

    void reset() override {
        streaming_ = false;
    }

    StreamEvent process_audio_chunk(const AudioChunk & chunk) override {
        require(streaming_, "stream was not started");
        StreamEvent event;
        event.audio_output = engine::runtime::AudioBuffer{
            chunk.sample_rate,
            chunk.channels,
            chunk.samples,
        };
        if (sink_) {
            sink_(event);
        }
        return event;
    }

    TaskResult finalize() override {
        streaming_ = false;
        return run({});
    }

private:
    std::shared_ptr<FakeState> state_;
    int generation_;
    TaskSpec task_;
    SessionOptions options_;
    SessionPreparationRequest prepared_;
    engine::runtime::StreamEventCallback sink_;
    bool streaming_ = false;
};

class FakeLoadedModel final : public ILoadedVoiceModel {
public:
    FakeLoadedModel(std::shared_ptr<FakeState> state, int generation)
        : state_(std::move(state)),
          generation_(generation) {
        metadata_.family = "fake-family";
        metadata_.variant = "complete-fake";
        metadata_.description = "complete test implementation";
        metadata_.config_candidates = {"config.json"};
        metadata_.weight_candidates = {"weights.gguf"};
    }

    ~FakeLoadedModel() override {
        state_->record("model-" + std::to_string(generation_) + "-destroyed");
    }

    const ModelMetadata & metadata() const noexcept override {
        return metadata_;
    }

    const CapabilitySet & capabilities() const noexcept override {
        return state_->capabilities;
    }

    std::unique_ptr<IVoiceTaskSession> create_task_session(
        const TaskSpec & task,
        const SessionOptions & options) const override {
        if (state_->fail_session) {
            throw std::runtime_error("session creation failed");
        }
        return std::make_unique<FakeSession>(state_, generation_, task, options);
    }

private:
    std::shared_ptr<FakeState> state_;
    int generation_;
    ModelMetadata metadata_;
};

class FakeLoader final : public IVoiceModelLoader {
public:
    explicit FakeLoader(std::shared_ptr<FakeState> state)
        : state_(std::move(state)) {}

    std::string family() const override { return "fake-family"; }

    bool can_load(const ModelLoadRequest & request) const override {
        return request.family_hint == family();
    }

    ModelInspection inspect(const ModelLoadRequest & request) const override {
        ModelInspection inspection;
        inspection.metadata.family = family();
        inspection.metadata.variant = "complete-fake";
        inspection.metadata.description = "complete test loader";
        inspection.metadata.config_candidates = {"config.json"};
        inspection.metadata.weight_candidates = {"weights.gguf"};
        inspection.capabilities = state_->capabilities;
        inspection.model_root = request.model_path;
        return inspection;
    }

    std::unique_ptr<ILoadedVoiceModel> load(
        const ModelLoadRequest &) const override {
        if (state_->fail_load) {
            throw std::runtime_error("model load failed");
        }
        const int generation = ++state_->generation;
        return std::make_unique<FakeLoadedModel>(state_, generation);
    }

    CapabilitySet advertised_capabilities() const override {
        return state_->capabilities;
    }

    std::string advertised_instructions_policy() const override {
        return "explicit";
    }

    std::vector<std::string> advertised_api_endpoints() const override {
        return {"/v1/audio/transcriptions"};
    }

private:
    std::shared_ptr<FakeState> state_;
};

audio_cpp::AudioCppModelConfig offline_asr_config(
    const std::filesystem::path & model_path) {
    return audio_cpp::parse_model_config(
        model_path,
        {
            {"family", "fake-family"},
            {"task", "asr"},
            {"mode", "offline"},
            {"backend", "cpu"},
            {"device", "2"},
            {"threads", "3"},
            {"load.cache", "memory"},
            {"session.language", "en"},
        });
}

std::unique_ptr<audio_cpp::AudioCppRuntime> make_runtime(
    const std::shared_ptr<FakeState> & state) {
    engine::runtime::ModelRegistry registry;
    registry.register_loader(std::make_shared<FakeLoader>(state));
    return std::make_unique<audio_cpp::AudioCppRuntime>(std::move(registry));
}

void test_model_config(const std::filesystem::path & model_path) {
    const auto config = offline_asr_config(model_path);
    require(config.model_path == model_path, "model path was not preserved");
    require(config.family == "fake-family", "family was not parsed");
    require(config.task.task == VoiceTaskKind::Asr, "task was not parsed");
    require(config.task.mode == RunMode::Offline, "mode was not parsed");
    require(
        config.session.backend.type == engine::core::BackendType::Cpu,
        "backend was not parsed");
    require(config.session.backend.device == 2, "device was not parsed");
    require(config.session.backend.threads == 3, "threads were not parsed");
    require(config.load.options.at("cache") == "memory", "load option prefix was not stripped");
    require(
        config.session.options.at("language") == "en",
        "session option prefix was not stripped");

    require_throws(
        [&] { audio_cpp::parse_model_config(model_path, {{"task", "asr"}}); },
        "family");
    require_throws(
        [&] { audio_cpp::parse_model_config(model_path, {{"family", "fake-family"}}); },
        "task");
    require_throws(
        [&] {
            audio_cpp::parse_model_config(
                model_path,
                {{"family", "fake-family"}, {"task", "asr"}, {"mode", "batch"}});
        },
        "mode");
    require_throws(
        [&] {
            audio_cpp::parse_model_config(
                model_path,
                {{"family", "fake-family"}, {"task", "asr"}, {"backend", "tpu"}});
        },
        "backend");
}

void test_capability_validation(const std::filesystem::path & model_path) {
    auto state = std::make_shared<FakeState>();
    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Tts, {RunMode::Offline}},
    };
    auto runtime = make_runtime(state);

    require_throws(
        [&] { runtime->load(offline_asr_config(model_path)); },
        "task");

    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Asr, {RunMode::Streaming}},
    };
    require_throws(
        [&] { runtime->load(offline_asr_config(model_path)); },
        "mode");
}

void test_atomic_replacement(const std::filesystem::path & model_path) {
    auto state = std::make_shared<FakeState>();
    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Asr, {RunMode::Offline}},
    };
    auto runtime = make_runtime(state);
    runtime->load(offline_asr_config(model_path));

    state->fail_load = true;
    require_throws(
        [&] { runtime->load(offline_asr_config(model_path)); },
        "model load failed");
    require(
        runtime->run({}).text_output->text == "generation-1",
        "old model was not retained after load failure");

    state->fail_load = false;
    state->fail_session = true;
    require_throws(
        [&] { runtime->load(offline_asr_config(model_path)); },
        "session creation failed");
    require(
        runtime->run({}).text_output->text == "generation-1",
        "old model was not retained after session creation failure");
}

void test_teardown_order(const std::filesystem::path & model_path) {
    auto state = std::make_shared<FakeState>();
    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Asr, {RunMode::Offline}},
    };
    auto runtime = make_runtime(state);
    runtime->load(offline_asr_config(model_path));
    runtime->free();

    std::lock_guard<std::mutex> lock(state->mutex);
    require(state->events.size() == 2, "expected one session and one model teardown");
    require(
        state->events[0] == "session-1-destroyed",
        "session was not destroyed before model");
    require(
        state->events[1] == "model-1-destroyed",
        "model teardown event was not second");
}

void test_runtime_serializes_calls(const std::filesystem::path & model_path) {
    auto state = std::make_shared<FakeState>();
    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Asr, {RunMode::Offline}},
    };
    state->gate = std::make_shared<SessionGate>();
    auto runtime = make_runtime(state);
    runtime->load(offline_asr_config(model_path));

    auto first = std::async(std::launch::async, [&] { return runtime->run({}); });
    {
        std::unique_lock<std::mutex> lock(state->gate->mutex);
        state->gate->condition.wait(
            lock,
            [&] { return state->gate->first_entered; });
    }

    std::promise<void> release_second;
    std::shared_future<void> second_barrier = release_second.get_future().share();
    std::promise<void> second_attempted_promise;
    auto second_attempted = second_attempted_promise.get_future();
    auto second = std::async(std::launch::async, [&] {
        second_barrier.wait();
        second_attempted_promise.set_value();
        return runtime->run({});
    });

    release_second.set_value();
    second_attempted.wait();
    require(
        second.wait_for(std::chrono::milliseconds(50)) == std::future_status::timeout,
        "second call completed while first call held the runtime");
    require(
        state->gate->entries.load() == 1,
        "second call entered the upstream session concurrently");

    {
        std::lock_guard<std::mutex> lock(state->gate->mutex);
        state->gate->release_first = true;
    }
    state->gate->condition.notify_all();
    first.get();
    second.get();
    require(state->gate->entries.load() == 2, "second call never reached the session");
}

void test_streaming_surface(const std::filesystem::path & model_path) {
    auto state = std::make_shared<FakeState>();
    state->capabilities.supported_tasks = {
        {VoiceTaskKind::Asr, {RunMode::Streaming}},
    };
    auto runtime = make_runtime(state);
    auto config = offline_asr_config(model_path);
    config.task.mode = RunMode::Streaming;
    runtime->load(config);
    runtime->start_stream({});
    const auto event = runtime->process_audio_chunk({16000, 1, 0, {0.25f}});
    require(event.audio_output.has_value(), "streaming chunk result was lost");
    require(
        event.audio_output->samples == std::vector<float>{0.25f},
        "streaming chunk samples changed");
    require(
        runtime->finish_stream().text_output->text == "generation-1",
        "streaming final result was lost");
}

}  // namespace

int main(int argc, char ** argv) {
    try {
        require(argc == 2, "runtime_test requires an existing model path argument");
        const std::filesystem::path model_path(argv[1]);
        test_model_config(model_path);
        test_capability_validation(model_path);
        test_atomic_replacement(model_path);
        test_teardown_order(model_path);
        test_runtime_serializes_calls(model_path);
        test_streaming_surface(model_path);
        std::cout << "audio.cpp runtime unit tests: PASS\n";
        return 0;
    } catch (const std::exception & error) {
        std::cerr << "audio.cpp runtime unit tests: FAIL: " << error.what() << '\n';
        return 1;
    }
}
