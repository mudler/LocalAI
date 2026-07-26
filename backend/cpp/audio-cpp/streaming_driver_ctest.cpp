// Tests for the streaming drivers in loaded_model: begin_stream,
// run_streaming_pull and run_streaming_audio.
//
// Engine-linked, so this runs through ctest rather than
// backend/cpp/run-unit-tests.sh. It builds no model and loads no file: a
// LoadedModel::Session is a plain struct holding a pointer to an engine
// interface, so a fake session exercises the drivers directly, which is the
// only way to assert the STATE OBLIGATION (prepare, then start_stream, on every
// stream) without a GPU and a gigabyte of weights.

#include "inference_lane.h"
#include "loaded_model.h"

#include "engine/framework/runtime/session.h"

#include <cstdio>
#include <memory>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

namespace rt = engine::runtime;

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

static void check_eq(const std::string &got, const std::string &want,
                     const std::string &name) {
    check(got == want, name + " (got \"" + got + "\" want \"" + want + "\")");
}

static std::string join(const std::vector<std::string> &parts) {
    std::string out;
    for (const auto &part : parts) {
        if (!out.empty()) {
            out += "|";
        }
        out += part;
    }
    return out;
}

// --------------------------------------------------------------------------
// Fakes
// --------------------------------------------------------------------------

// Consumes audio chunks, like every streaming ASR family.
//
// It deliberately does NOT override start_stream, so every test that drives it
// also pins the claim the drivers rely on: IStreamingVoiceTaskSession's BASE
// start_stream is a call to reset(). If upstream ever changes that base, the
// replay test below fails rather than the backend silently continuing the
// previous stream.
class FakeAudioSession : public rt::IStreamingVoiceTaskSession {
public:
    std::vector<std::string> calls;
    std::vector<rt::AudioChunk> chunks;
    rt::StreamingPolicy policy;
    // Set to make process_audio_chunk throw on the nth call (1-based).
    int throw_on_chunk = 0;
    // Cumulative partial text, which is voxtral_realtime's convention.
    bool report_partials = true;

    std::string family() const override { return "fake_audio"; }
    rt::VoiceTaskKind task_kind() const override { return rt::VoiceTaskKind::Asr; }
    rt::RunMode run_mode() const override { return rt::RunMode::Streaming; }

    void prepare(const rt::SessionPreparationRequest &request) override {
        calls.push_back("prepare");
        prepared_ = true;
        prepared_rate_ = request.audio.has_value() ? request.audio->sample_rate : 0;
    }

    rt::StreamingPolicy streaming_policy() const override { return policy; }

    void set_stream_event_sink(rt::StreamEventCallback sink) override {
        calls.push_back(sink ? "sink+" : "sink-");
        sink_ = std::move(sink);
    }

    void reset() override {
        if (!prepared_) {
            // Exactly what silero_vad does, and the reason prepare() has to come
            // first rather than being folded into session_for.
            throw std::runtime_error("fake: prepare() must be called before reset()");
        }
        calls.push_back("reset");
        seen_frames_ = 0;
        seen_chunks_ = 0;
        text_.clear();
    }

    rt::StreamEvent process_audio_chunk(const rt::AudioChunk &chunk) override {
        calls.push_back("chunk");
        chunks.push_back(chunk);
        ++seen_chunks_;
        if (throw_on_chunk == seen_chunks_) {
            throw std::runtime_error("fake: chunk failure");
        }
        const int channels = chunk.channels > 0 ? chunk.channels : 1;
        seen_frames_ += static_cast<std::int64_t>(chunk.samples.size()) / channels;
        text_ += "w" + std::to_string(seen_chunks_);
        rt::StreamEvent event;
        if (report_partials) {
            event.partial_text = rt::Transcript{text_, "en"};
        }
        return event;
    }

    rt::TaskResult finalize() override {
        calls.push_back("finalize");
        rt::TaskResult result;
        result.text_output = rt::Transcript{
            text_ + "/frames=" + std::to_string(seen_frames_), "en"};
        return result;
    }

    // Emits through the SINK the way nemotron_asr does, from inside the final
    // step rather than from process_audio_chunk.
    void emit_through_sink(const std::string &fragment) {
        if (!sink_) {
            return;
        }
        rt::StreamEvent event;
        event.partial_text = rt::Transcript{fragment, "en"};
        sink_(event);
    }

    bool sink_installed() const { return static_cast<bool>(sink_); }
    int prepared_rate() const { return prepared_rate_; }

private:
    rt::StreamEventCallback sink_;
    bool prepared_ = false;
    int prepared_rate_ = 0;
    std::int64_t seen_frames_ = 0;
    int seen_chunks_ = 0;
    std::string text_;
};

// nemotron_asr's shape: partials arrive only through the sink, and only from
// inside the finalize step.
class SinkOnlyAudioSession : public FakeAudioSession {
public:
    SinkOnlyAudioSession() { report_partials = false; }

    rt::TaskResult finalize() override {
        emit_through_sink("late ");
        emit_through_sink("partial");
        return FakeAudioSession::finalize();
    }
};

// Pulls events, like every streaming TTS family. Overrides start_stream the way
// the seven real families do, calling reset() first.
class FakePullSession : public rt::IStreamingVoiceTaskSession {
public:
    std::vector<std::string> calls;
    std::size_t event_count = 3;
    bool final_on_second = false;

    std::string family() const override { return "fake_pull"; }
    rt::VoiceTaskKind task_kind() const override { return rt::VoiceTaskKind::Tts; }
    rt::RunMode run_mode() const override { return rt::RunMode::Streaming; }

    void prepare(const rt::SessionPreparationRequest &) override {
        calls.push_back("prepare");
        prepared_ = true;
    }

    rt::StreamingPolicy streaming_policy() const override {
        rt::StreamingPolicy policy;
        policy.input = rt::StreamingInputKind::None;
        policy.output = rt::StreamingOutputKind::PullEvents;
        return policy;
    }

    void start_stream(const rt::TaskRequest &request) override {
        calls.push_back("start_stream");
        (void)request;
        reset();
    }

    void set_stream_event_sink(rt::StreamEventCallback sink) override {
        calls.push_back(sink ? "sink+" : "sink-");
        sink_ = std::move(sink);
    }

    void reset() override {
        if (!prepared_) {
            throw std::runtime_error("fake: prepare() must be called before reset()");
        }
        calls.push_back("reset");
        emitted_ = 0;
    }

    std::optional<rt::StreamEvent> next_stream_event() override {
        if (emitted_ >= event_count) {
            return std::nullopt;
        }
        rt::StreamEvent event;
        rt::AudioBuffer audio;
        audio.sample_rate = 24000;
        audio.channels = 1;
        audio.samples.assign(4, 0.25F);
        // named_audio_outputs, NOT audio_output: this is where supertonic,
        // omnivoice and voxcpm2 all put their streamed chunks.
        event.named_audio_outputs.push_back(
            {"chunk_" + std::to_string(emitted_), std::move(audio), {}});
        ++emitted_;
        if (final_on_second && emitted_ == 2) {
            event.is_final = true;
        }
        calls.push_back("pull");
        return event;
    }

    rt::StreamEvent process_audio_chunk(const rt::AudioChunk &) override {
        throw std::runtime_error("fake_pull consumes no audio");
    }

    rt::TaskResult finalize() override {
        calls.push_back("finalize");
        rt::TaskResult result;
        rt::AudioBuffer merged;
        merged.sample_rate = 24000;
        merged.channels = 1;
        merged.samples.assign(4 * emitted_, 0.25F);
        result.audio_output = std::move(merged);
        return result;
    }

    bool sink_installed() const { return static_cast<bool>(sink_); }

private:
    rt::StreamEventCallback sink_;
    bool prepared_ = false;
    std::size_t emitted_ = 0;
};

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

static audiocpp_backend::LoadedModel::Session
streaming_session(rt::IStreamingVoiceTaskSession &fake,
                  audiocpp_backend::Task task) {
    audiocpp_backend::LoadedModel::Session session;
    session.task = task;
    session.mode = audiocpp_backend::Mode::Streaming;
    session.streaming = &fake;
    return session;
}

static rt::TaskRequest audio_request(int sample_rate, int channels,
                                     std::int64_t frames) {
    rt::TaskRequest request;
    rt::AudioBuffer audio;
    audio.sample_rate = sample_rate;
    audio.channels = channels;
    audio.samples.assign(static_cast<std::size_t>(frames * channels), 0.5F);
    request.audio_input = std::move(audio);
    return request;
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

static void test_begin_stream_prepares_then_starts() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakePullSession fake;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Tts);

    rt::TaskRequest request;
    audiocpp_backend::begin_stream(session, request, entry);

    check_eq(join(fake.calls), "prepare|start_stream|reset",
             "begin_stream prepares before it starts, and start_stream resets");
}

// The base implementation of start_stream IS a reset(). FakeAudioSession does
// not override start_stream, so this is that guarantee, read out of the pinned
// header rather than assumed.
static void test_base_start_stream_resets() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);

    audiocpp_backend::begin_stream(session, audio_request(16000, 1, 10), entry);
    check_eq(join(fake.calls), "prepare|reset",
             "the interface's own start_stream resets the session");
}

static void test_begin_stream_refuses_a_non_streaming_session() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    audiocpp_backend::LoadedModel::Session session;
    session.mode = audiocpp_backend::Mode::Offline;

    bool threw_capability = false;
    try {
        rt::TaskRequest request;
        audiocpp_backend::begin_stream(session, request, entry);
    } catch (const audiocpp_backend::CapabilityError &) {
        threw_capability = true;
    } catch (const std::exception &) {
    }
    check(threw_capability,
          "begin_stream on an offline session throws CapabilityError, not a null deref");
}

// THE ONE THIS TASK IS ABOUT. A streaming session is cached, so the second
// stream gets the object the first one left behind. Two identical runs against
// the SAME session must produce identical output.
static void test_a_refetched_session_replays_identically() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 512;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(16000, 1, 1536);

    std::vector<std::string> first_fragments;
    const auto first = audiocpp_backend::run_streaming_audio(
        session, request, *request.audio_input,
        [&](const rt::StreamEvent &event) {
            if (event.partial_text.has_value()) {
                first_fragments.push_back(event.partial_text->text);
            }
        },
        entry);

    std::vector<std::string> second_fragments;
    const auto second = audiocpp_backend::run_streaming_audio(
        session, request, *request.audio_input,
        [&](const rt::StreamEvent &event) {
            if (event.partial_text.has_value()) {
                second_fragments.push_back(event.partial_text->text);
            }
        },
        entry);

    check_eq(join(second_fragments), join(first_fragments),
             "a re-fetched streaming session replays the same partials");
    check_eq(second.text_output.has_value() ? second.text_output->text : "",
             first.text_output.has_value() ? first.text_output->text : "",
             "a re-fetched streaming session replays the same final text");
    check_eq(first.text_output.has_value() ? first.text_output->text : "",
             "w1w2w3/frames=1536",
             "the first run saw exactly the audio it was given");
    // Not a tautology: without the reset the second run reports six words and
    // 3072 frames, and both checks above fail.
    check_eq(join(first_fragments), "w1|w1w2|w1w2w3", "cumulative partials");
}

static void test_run_streaming_audio_installs_and_clears_the_sink() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    SinkOnlyAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 1024;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(16000, 1, 1024);

    std::vector<std::string> fragments;
    const auto result = audiocpp_backend::run_streaming_audio(
        session, request, *request.audio_input,
        [&](const rt::StreamEvent &event) {
            if (event.partial_text.has_value()) {
                fragments.push_back(event.partial_text->text);
            }
        },
        entry);

    check_eq(join(fragments), "late |partial",
             "a family that reports only through the sink is not silent");
    check(!fake.sink_installed(),
          "the sink is cleared before returning, so the cached session holds no "
          "reference to the caller's frame");
    check_eq(join(fake.calls), "sink+|prepare|reset|chunk|finalize|sink-",
             "the sink is installed before the stream begins and cleared after it ends");
    check(result.text_output.has_value(), "the final result still comes back");
}

static void test_the_sink_is_cleared_when_the_stream_throws() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 512;
    fake.throw_on_chunk = 1;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(16000, 1, 1024);

    bool threw = false;
    try {
        audiocpp_backend::run_streaming_audio(
            session, request, *request.audio_input,
            [](const rt::StreamEvent &) {}, entry);
    } catch (const std::exception &) {
        threw = true;
    }
    check(threw, "a failing chunk propagates");
    check(!fake.sink_installed(),
          "the sink is cleared on the exception path too");
}

static void test_chunking_honours_the_policy_sample_count() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 16000;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    // 2.5 chunks, so the last one is short.
    const auto request = audio_request(16000, 1, 40000);

    audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);

    check(fake.chunks.size() == 3, "40000 frames at 16000 per chunk is three chunks");
    if (fake.chunks.size() == 3) {
        check(fake.chunks[0].samples.size() == 16000, "first chunk is full");
        check(fake.chunks[1].samples.size() == 16000, "second chunk is full");
        check(fake.chunks[2].samples.size() == 8000, "last chunk is the remainder");
        check(fake.chunks[0].start_sample == 0, "first chunk starts at zero");
        check(fake.chunks[1].start_sample == 16000, "second chunk start index");
        check(fake.chunks[2].start_sample == 32000, "third chunk start index");
        check(fake.chunks[0].sample_rate == 16000, "chunk carries the buffer's rate");
        check(fake.chunks[0].channels == 1, "chunk carries the buffer's channel count");
    }
}

// A buffer whose float count is not a whole number of frames is REFUSED rather
// than truncated. The integer division would otherwise drop the tail floats
// from the fed audio, and therefore from the transcript, with no diagnostic.
static void test_a_partial_trailing_frame_is_refused() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 100;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);

    // 501 floats across 2 channels: 250 whole frames and one stray float.
    rt::TaskRequest request;
    rt::AudioBuffer audio;
    audio.sample_rate = 48000;
    audio.channels = 2;
    audio.samples.assign(501, 0.5F);
    request.audio_input = std::move(audio);

    bool threw_config = false;
    try {
        audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                              [](const rt::StreamEvent &) {}, entry);
    } catch (const audiocpp_backend::ConfigError &) {
        threw_config = true;
    } catch (const std::exception &) {
    }
    check(threw_config,
          "a buffer that is not a whole number of frames is refused with ConfigError");
    check(fake.calls.empty(),
          "the refusal precedes every call into the session, so no half-started "
          "stream is left on the cached one");
}

// higgs_audio_stt states its window in seconds and leaves the sample count at
// zero, so this branch is a real family's path rather than a defensive one.
static void test_chunking_falls_back_to_the_policy_seconds() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 0;
    fake.policy.preferred_audio_chunk_seconds = 4.0;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(16000, 1, 96000); // 6 s

    audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);

    check(fake.chunks.size() == 2, "6 s at a 4 s window is two chunks");
    if (fake.chunks.size() == 2) {
        check(fake.chunks[0].samples.size() == 64000, "first window is 4 s");
        check(fake.chunks[1].samples.size() == 32000, "second window is the 2 s remainder");
    }
}

static void test_chunking_falls_back_to_the_interface_default() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 0;
    fake.policy.preferred_audio_chunk_seconds = 0.0;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(16000, 1, 1024);

    audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);

    check(fake.chunks.size() == 2, "a policy naming no window uses the interface's 512");
    if (!fake.chunks.empty()) {
        check(fake.chunks[0].samples.size() == 512, "default window is 512 frames");
    }
}

// A zero sample rate must not turn a seconds-only policy into a zero-length
// chunk, which would loop forever.
static void test_a_seconds_policy_with_no_rate_falls_through() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 0;
    fake.policy.preferred_audio_chunk_seconds = 4.0;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(0, 1, 1024);

    audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);
    check(fake.chunks.size() == 2, "a rateless buffer still chunks at the default 512");
}

// FRAMES, not floats. vibevoice_asr refuses a chunk whose sample count is not
// divisible by its channel count, and offsets every span it reports by the
// chunk's start_sample.
static void test_stereo_chunks_are_frame_aligned() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 300;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);
    const auto request = audio_request(48000, 2, 750);

    audiocpp_backend::run_streaming_audio(session, request, *request.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);

    check(fake.chunks.size() == 3, "750 frames at 300 frames per chunk is three chunks");
    for (const auto &chunk : fake.chunks) {
        check(chunk.samples.size() % 2 == 0, "every stereo chunk is a whole number of frames");
    }
    if (fake.chunks.size() == 3) {
        check(fake.chunks[0].samples.size() == 600, "300 stereo frames is 600 floats");
        check(fake.chunks[1].start_sample == 300,
              "start_sample counts frames, not floats");
        check(fake.chunks[2].samples.size() == 300, "the remainder is 150 frames");
    }
}

static void test_pull_drains_every_event_and_installs_no_sink() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakePullSession fake;
    fake.event_count = 3;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Tts);

    std::vector<std::string> ids;
    rt::TaskRequest request;
    const auto result = audiocpp_backend::run_streaming_pull(
        session, request,
        [&](const rt::StreamEvent &event) {
            for (const auto &named : event.named_audio_outputs) {
                ids.push_back(named.id);
            }
        },
        entry);

    check_eq(join(ids), "chunk_0|chunk_1|chunk_2", "every pulled event reaches the caller");
    check(!fake.sink_installed(),
          "no stream event sink is installed on the pull path, so voxcpm2 cannot "
          "deliver every chunk twice");
    check_eq(join(fake.calls), "prepare|start_stream|reset|pull|pull|pull|finalize",
             "prepare, start, drain, finish");
    check(result.audio_output.has_value(), "the merged result comes back");
    check(result.audio_output.has_value() && result.audio_output->samples.size() == 12,
          "the merged result is the whole synthesis, not a tail");
}

static void test_pull_stops_on_a_final_event() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakePullSession fake;
    fake.event_count = 5;
    fake.final_on_second = true;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Tts);

    int events = 0;
    rt::TaskRequest request;
    audiocpp_backend::run_streaming_pull(
        session, request, [&](const rt::StreamEvent &) { ++events; }, entry);

    check(events == 2, "an event marked final ends the pull loop");
}

static void test_pull_refuses_a_non_streaming_session() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    audiocpp_backend::LoadedModel::Session session;

    bool threw_capability = false;
    try {
        rt::TaskRequest request;
        audiocpp_backend::run_streaming_pull(
            session, request, [](const rt::StreamEvent &) {}, entry);
    } catch (const audiocpp_backend::CapabilityError &) {
        threw_capability = true;
    } catch (const std::exception &) {
    }
    check(threw_capability, "run_streaming_pull refuses a session with no streaming half");
}

// prepare() runs on EVERY stream, not once per session: the preparation request
// is derived from the request (audio contract, text, voice), so a second stream
// at a different rate would otherwise run against the first one's contract.
static void test_prepare_tracks_the_request_not_the_session() {
    audiocpp_backend::InferenceLane lane("test");
    audiocpp_backend::LaneEntry entry(lane, 0);
    FakeAudioSession fake;
    fake.policy.preferred_audio_chunk_samples = 4096;
    const auto session = streaming_session(fake, audiocpp_backend::Task::Asr);

    const auto first = audio_request(16000, 1, 4096);
    audiocpp_backend::run_streaming_audio(session, first, *first.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);
    check(fake.prepared_rate() == 16000, "the first stream prepares at its own rate");

    const auto second = audio_request(44100, 1, 4096);
    audiocpp_backend::run_streaming_audio(session, second, *second.audio_input,
                                          [](const rt::StreamEvent &) {}, entry);
    check(fake.prepared_rate() == 44100,
          "the second stream prepares at ITS rate, not the first one's");
}

int main() {
    test_begin_stream_prepares_then_starts();
    test_base_start_stream_resets();
    test_begin_stream_refuses_a_non_streaming_session();
    test_a_refetched_session_replays_identically();
    test_run_streaming_audio_installs_and_clears_the_sink();
    test_the_sink_is_cleared_when_the_stream_throws();
    test_chunking_honours_the_policy_sample_count();
    test_a_partial_trailing_frame_is_refused();
    test_chunking_falls_back_to_the_policy_seconds();
    test_chunking_falls_back_to_the_interface_default();
    test_a_seconds_policy_with_no_rate_falls_through();
    test_stereo_chunks_are_frame_aligned();
    test_pull_drains_every_event_and_installs_no_sink();
    test_pull_stops_on_a_final_event();
    test_pull_refuses_a_non_streaming_session();
    test_prepare_tracks_the_request_not_the_session();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all streaming driver checks passed\n");
    return 0;
}
