// audio.cpp LocalAI gRPC backend.
//
// Links 0xShug0/audio.cpp's engine_runtime through its public
// include/engine/framework/** headers only. Nothing under upstream's app/,
// src/ or tests/ is used: those are application internals, they are where
// upstream expects churn, and upstream is Apache-2.0 while LocalAI is MIT.
//
// This commit adds LoadModel/Free/Status plus the AudioTranscription, VAD,
// Diarize, AudioTransform, TTS, SoundGeneration, TTSStream and
// AudioTranscriptionStream RPCs. The remaining audio RPCs land in later
// commits.

#include "backend.pb.h"
#include "backend.grpc.pb.h"

#include "audio_io.h"
#include "audio_units.h"
#include "capability_routing.h"
#include "generation_request.h"
#include "inference_lane.h"
#include "loaded_model.h"
#include "model_options.h"
#include "result_map.h"
#include "stem_selection.h"
#include "stream_delta.h"
#include "wav_header.h"

#include <grpcpp/grpcpp.h>
#include <grpcpp/server.h>
#include <grpcpp/server_builder.h>
#include <grpcpp/support/sync_stream.h>
#include <grpcpp/ext/proto_server_reflection_plugin.h>

#include <atomic>
#include <chrono>
#include <csignal>
#include <cstdint>
#include <cstdlib>
#include <exception>
#include <iostream>
#include <limits>
#include <memory>
#include <mutex>
#include <optional>
#include <set>
#include <string>
#include <thread>
#include <utility>
#include <vector>

using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
// Do NOT alias grpc::Status as Status: the Status RPC method would shadow the
// type and break every other method that names it as a return type.
using GStatus = ::grpc::Status;

namespace {

// Set by the signal handler, read by the thread that owns the server. The whole
// point of the indirection is that the handler itself does nothing else: see
// signal_handler below.
std::atomic<bool> g_shutdown_requested{false};

// One model per process, matching every other LocalAI backend. The mutex guards
// the pointer itself, not the model: swapping or dropping it races with the
// handlers that read it, whereas the model's own concurrency is the inference
// lane's job.
//
// shared_ptr, not unique_ptr. An audio RPC runs for seconds and cannot hold
// g_model_mu for its duration, so it has to work from a reference taken under
// the lock and used after releasing it. Under a unique_ptr, a Free or a reload
// arriving mid-request destroys the model out from under that reference. Every
// handler instead takes a counted reference through snapshot_for(), so Free
// drops the global's reference and whichever request finishes last destroys the
// model.
std::mutex g_model_mu;
std::shared_ptr<audiocpp_backend::LoadedModel> g_model;

// The raw reference-taking primitive. Returns by value, so the caller owns a
// reference for as long as its local lives, and null when no model is loaded.
// Never return LoadedModel& from here: that is the shape that reintroduces the
// use-after-free.
//
// _unchecked because it performs NO request-level validation. Status is its
// only legitimate caller, because Status takes a HealthMessage, which carries
// no ModelIdentity to check. Every RPC that acts on a model must call
// snapshot_for instead; the name is deliberately unpleasant so that reaching
// for it is a visible decision rather than an omission.
std::shared_ptr<audiocpp_backend::LoadedModel> snapshot_unchecked() {
    std::lock_guard<std::mutex> lock(g_model_mu);
    return g_model;
}

// LocalAI's VADRequest carries raw floats and no sample rate at all. Every
// LocalAI VAD backend treats them as 16 kHz mono (backend/go/silero-vad/vad.go
// and backend/go/sherpa-onnx/backend.go both hardcode 16000) and core/backend
// feeds them from a 16 kHz pipeline, so the same assumption is made here rather
// than left implicit. If the proto ever grows a sample rate field, this is the
// constant to delete.
constexpr int kVadSampleRate = 16000;

// The rate every file-fed speech route reads its input at, and the rate the
// spans that come back are therefore interpreted in. Passed to read_audio_file,
// which resamples only when the file differs.
//
// Two independent reasons, and the second is the one that is easy to miss:
//
//   1. Some families refuse anything else outright. silero_vad throws
//      "Silero VAD 16k model only supports sample_rate=16000" and
//      sortformer_diar throws "Sortformer diar currently requires 16 kHz input
//      audio". That is what made a 44.1 kHz upload return INTERNAL with an
//      engine-internal message instead of an answer.
//   2. The families that do NOT refuse still do not all express their result
//      spans in the input's domain. nemotron_asr builds every word timestamp as
//      token_frame * hop_length * subsampling_factor, which is its own 16 kHz
//      feature domain whatever the input was; the handler then converts those
//      spans with the buffer's rate. Feed it 44.1 kHz and every emitted
//      timestamp is 2.76x too small, with a 200 and no diagnostic. Resampling
//      the input to 16 kHz makes the buffer domain and the span domain the same
//      one, which is the only reason the nanoseconds are right.
//
// The cost, stated plainly: vibevoice_asr resamples internally to 24 kHz, so a
// 48 kHz upload now travels 48 -> 16 -> 24 rather than 48 -> 24. That is not
// merely band limiting. resample_mono_linear is linear interpolation with no
// anti-alias filter (framework/audio/conversion.cpp), so the content above
// 8 kHz is ALIASED down on the way in, and the 16 -> 24 step aliases whatever
// the first step left. It is accepted because a silently wrong timestamp is
// worse than an aliased band, and because every other LocalAI ASR path already
// feeds 16 kHz. If the framework ever publishes a per-model preferred input
// rate, this constant is what should become that lookup.
constexpr int kSpeechSampleRate = 16000;

// The rate AudioTransform reads BOTH its input and its reference clip at. Zero
// means "the file's own rate and its own channel count", i.e. this handler does
// no resampling and no downmix at all, and it is the only value that is correct
// for all four tasks this RPC can route to.
//
// Separation forces it. htdemucs and mel_band_roformer refuse anything but
// their own rate outright, from prepare(): "HTDemucs prepare() sample rate
// mismatch: expected N" (src/models/demucs/session.cpp) and the same shape in
// src/models/roformer/session.cpp. That N is the CHECKPOINT'S declared rate,
// read from the packaged config ("samplerate" in demucs/assets.cpp,
// "sample_rate" in roformer/assets.cpp), not a constant: it is 44100 for every
// published checkpoint of both, which is why the messages below say 44100, but
// a checkpoint declaring something else would demand that instead, and only
// passing the file through unchanged can satisfy either. Passing
// kSpeechSampleRate here would
// therefore turn every separation request into an INTERNAL, which is a loud
// failure. The downmix half is the quiet one: both models declare two channels
// and ACCEPT mono, by duplicating it across both, so a mono read would be taken
// and would merely delete the stereo image, which is the cue that separates a
// centred vocal from a wide mix. Verified rather than reasoned: a mono input
// comes back as mono stems, a stereo input as stereo ones.
//
// The three conversion tasks do not force it, and were checked one family at a
// time rather than assumed, because "it happens to work" and "it is documented
// to work" are different claims:
//
//   seed_vc      (vc, svc) seed_vc_prepare_audio_for_sample_rate takes the
//                buffer's rate and channel count and resamples to its own mel
//                rate: soxr when it is available, falling back to
//                resample_mono_torchaudio_sinc_hann (seed_vc/audio_features.cpp).
//   vevo2        (vc, s2s, svc) normalize_audio_to_24k_mono converts whatever
//                it is given to 24 kHz mono before anything else runs, linearly.
//   miocodec     (vc, s2s) prepare_miocodec_mono_audio mixes down, then
//                resamples to the model's rate with sinc-hann.
//   chatterbox   (vc) ChatterboxVcComponent::convert normalizes the source to
//                16 kHz and the reference to 24 kHz itself, linearly.
//
// So every one of them resamples internally, and passing the file through
// unchanged is better than folding it first. The reason is the DOUBLE
// conversion, not resampler quality: two of the four resample linearly, exactly
// as read_audio_file would. Their outputs run at 22.05 to 44.1 kHz, so
// pre-folding to 16 kHz mono with read_audio_file's LINEAR, unfiltered
// resampler would band-limit at 8 kHz and alias, and then the family would
// resample that damaged signal a second time on its way up. One conversion is
// the floor; this constant is what keeps it at one.
//
// The cost, stated plainly: a family that cannot handle the file's rate reports
// it from inside the engine as a plain runtime_error, which to_status maps to
// INTERNAL rather than INVALID_ARGUMENT. That is the accepted trade, since the
// only families that refuse are the separators and what they want is an
// ordinary music file at its own rate.
constexpr int kTransformSampleRate = 0;

// The rate TTS reads its speaker reference clip at, and the rate
// SoundGeneration reads its `src` editing clip at. Zero, i.e. the file's own
// rate and its own channel count, no resample and no downmix.
//
// Settled from upstream rather than reasoned about, and upstream settles it
// twice over:
//
//   1. Upstream does exactly this itself. Both its CLI and its HTTP server load
//      a voice reference with minitts::cli::read_audio_buffer (app/cli/request.cpp),
//      which is read_wav_f32 and then {wav.sample_rate, wav.channels,
//      wav.samples} verbatim. app/server/runtime.cpp's build_speech_request
//      puts that buffer straight into voice.speaker->audio, and its
//      audio_input path (line 518) does the same for the editing clip. Every
//      family that consumes a reference has therefore only ever been tested
//      against native-rate, native-channel input.
//   2. Every consuming family converts it itself, and most of them with a
//      BETTER resampler than read_audio_file's. Checked one at a time:
//        chatterbox      to_mono_audio, then 24 kHz, then 16 kHz derived from
//                        the 24 kHz path (conditionals.cpp), matching Python's
//                        prepare_conditionals ordering.
//        index_tts2      mixdown_interleaved_to_mono_average, then a soxr or
//                        torchaudio sinc-hann resample to its mel rate and to
//                        16 kHz (audio_features.cpp, waveform_22k).
//        higgs_audio_tts mixdown, then sinc-hann to 24 kHz and 16 kHz.
//        irodori_tts     mixdown, then sinc-hann (codec.cpp).
//        voxcpm2         mixdown, then soxr-or-linear (audiovae.cpp).
//        omnivoice       mixdown, then sinc-hann (audio_tokenizer.cpp).
//        qwen3_tts       convert_interleaved_audio_to_mono_linear_resampled.
//      For the SoundGeneration side, ace_step resamples the LEFT and RIGHT
//      channels SEPARATELY (pre_dit.cpp), and stable_audio resamples per
//      channel too, so a mono downmix here would destroy input those two are
//      built to consume as stereo.
//
// So folding to 16 kHz mono first would band-limit at 8 kHz through
// read_audio_file's LINEAR, unfiltered resampler, alias what is above it, and
// then hand that damaged signal to a good resampler for a second conversion.
// One conversion is the floor, and passing the file through unchanged is what
// keeps it at one. Same argument as kTransformSampleRate, same value, kept as a
// separate constant because the routes are different and a future per-model
// preferred-rate lookup would have to answer them separately.
constexpr int kVoiceReferenceSampleRate = 0;

// Parses ModelOptions.MainGPU into a device index.
//
// Not std::atoi: it returns 0 for anything unparseable, so "gpu1" or a device
// UUID would silently become device 0 and the model would load on the wrong
// device with no diagnostic anywhere. A refusal the operator can read beats a
// wrong answer they cannot see.
int parse_device_index(const std::string &value) {
    size_t consumed = 0;
    long parsed = 0;
    try {
        parsed = std::stol(value, &consumed);
    } catch (const std::exception &) {
        consumed = 0;
    }
    if (consumed != value.size() || parsed < 0 ||
        parsed > std::numeric_limits<int>::max()) {
        throw audiocpp_backend::ConfigError(
            "audio-cpp: main_gpu must be a non-negative device index, got '" +
            value + "'");
    }
    return static_cast<int>(parsed);
}

// Mirrors pkg/grpc/server.go's checkModelIdentity, backend/cpp/llama-cpp,
// backend/cpp/ds4, backend/cpp/privacy-filter and
// backend/python/common/model_identity.py.
//
// Why every backend has to carry this: in distributed mode a worker can recycle
// a stopped backend's gRPC port for a different model's backend, and the
// controller's liveness-only health probe cannot tell a stale cached route from
// a live one. Only the backend knows which model it actually loaded, so only
// the backend can catch it (#10952). Without this, an audio-cpp process reached
// through a stale route answers with a DIFFERENT model's VAD or diarization
// result and a 200, which is a wrong answer nobody can see.
//
// Either side empty means "skip". The request side is empty for a controller
// that predates the field; the loaded side is empty when such a controller
// performed the load. Neither can judge the other, and a false rejection is
// worse than the miss it prevents.
//
// Templated over the request type because every guarded message exposes
// modelidentity(), and one body keeps the rule identical across RPCs rather
// than letting it drift per handler.
//
// The loaded identity is read off the LoadedModel rather than a separate
// global, which is where this differs from llama-cpp. A handler holding the
// model through snapshot_for() is then necessarily judging against the identity
// THAT model was loaded with, and a concurrent reload cannot swap one without
// the other.
template <typename Request>
GStatus check_model_identity(const audiocpp_backend::LoadedModel &model,
                             const Request *request) {
    if (request == nullptr || request->modelidentity().empty()) {
        return GStatus::OK;
    }
    const std::string &loaded = model.identity();
    if (loaded.empty() || loaded == request->modelidentity()) {
        return GStatus::OK;
    }
    // NOT_FOUND plus this exact sentinel is the cross-language wire contract
    // the router matches on (grpcerrors.ModelMismatchSentinel). The code alone
    // is not enough, since NOT_FOUND is returned for unrelated reasons
    // elsewhere, so the substring "model identity mismatch" must survive
    // verbatim through any edit to this message.
    return GStatus(grpc::StatusCode::NOT_FOUND,
                   "audio-cpp: model identity mismatch: loaded \"" + loaded +
                       "\", requested \"" + request->modelidentity() + "\"");
}

// The ONE way an RPC handler should reach the model. Takes the counted
// reference, refuses when nothing is loaded, and runs the identity check, in
// that order. Returns null with `out` set to the status to return; on success
// returns the model and leaves `out` OK.
//
// This exists as a structural guarantee rather than a convenience. The identity
// check used to be two lines every handler had to remember, and nothing failed
// if a new handler forgot them: there is no C++ equivalent of
// pkg/grpc/model_identity_modalities_test.go, so the convention could only rot.
// Every handler already has to call something to obtain the model, so making
// the guarded call the shortest path means the default is correct and skipping
// it requires deliberately typing snapshot_unchecked.
//
// The order is forced: the identity being compared against belongs to the model
// this call is about to use, so it cannot precede taking the reference. And it
// must precede routing, or a mismatched request to a model that cannot serve
// the RPC leaks UNIMPLEMENTED instead of the NOT_FOUND the router matches on.
template <typename Request>
std::shared_ptr<audiocpp_backend::LoadedModel>
snapshot_for(const Request *request, GStatus &out) {
    auto model = snapshot_unchecked();
    if (model == nullptr) {
        out = GStatus(grpc::StatusCode::FAILED_PRECONDITION,
                      "audio-cpp: no model is loaded; call LoadModel first");
        return nullptr;
    }
    out = check_model_identity(*model, request);
    if (!out.ok()) {
        return nullptr;
    }
    return model;
}

// Builds the TaskRequest for a transcription-shaped RPC. `task` is the ROUTED
// audio.cpp task, not a guess from the request: it decides how
// TranscriptRequest.prompt is used, and routing has already decided it.
//
// The audio is taken by value and moved in. A long recording runs to tens of
// megabytes and the caller has no use for it afterwards; the previous shape,
// a const reference, copied it.
engine::runtime::TaskRequest
build_transcription_request(const backend::TranscriptRequest &request,
                            audiocpp_backend::Task task,
                            engine::runtime::AudioBuffer audio) {
    engine::runtime::TaskRequest task_request;
    task_request.audio_input = std::move(audio);

    if (task == audiocpp_backend::Task::Alignment) {
        // For forced alignment the prompt IS the transcript to align, so it
        // becomes the text input rather than a decoding hint. Set even when
        // empty: an aligner given no text should say so itself rather than be
        // handed an audio-only request it cannot describe.
        engine::runtime::Transcript transcript;
        transcript.text = request.prompt();
        transcript.language = request.language();
        task_request.text_input = transcript;
    } else if (!request.prompt().empty()) {
        // For ASR the prompt is decoding context, the whisper meaning.
        task_request.options["prompt"] = request.prompt();
    }

    if (!request.language().empty()) {
        task_request.options["language"] = request.language();
    }
    if (request.translate()) {
        task_request.options["translate"] = "true";
    }
    if (request.temperature() > 0.0f) {
        task_request.options["temperature"] = std::to_string(request.temperature());
    }
    for (const auto &granularity : request.timestamp_granularities()) {
        if (granularity == "word") {
            // return_timestamps is the key upstream actually reads, and it is
            // the one that does something: qwen3_asr defaults it to false
            // (include/engine/models/qwen3_asr/types.h) and, when set, both
            // runs its forced aligner and shortens its chunk window from 30 s
            // to 15 s (src/models/qwen3_asr/session.cpp). Without it, asking
            // for word granularity silently came back with no word timing.
            //
            // word_timestamps rides along as a forward-tolerant alias. NO
            // family in the pinned upstream reads that key, a grep over src/
            // and include/ returns nothing, so it is sent only so that a family
            // adopting the name later works with no change here.
            task_request.options["return_timestamps"] = "true";
            task_request.options["word_timestamps"] = "true";
        }
    }
    // What lands and what does not. Families look their request options up by
    // name (runtime::find_option) and ignore every key they do not know; the
    // unknown-key refusals upstream does have are on SESSION options, which
    // arrive at load time rather than here. So an unread key cannot turn a
    // valid request into an error, but it is also not a feature, and the
    // honest accounting against the pinned upstream is:
    //
    //   language           read, by nemotron_asr and vibevoice_asr among others.
    //   return_timestamps  read, by qwen3_asr.
    //   prompt             read by NO ASR family. Forwarded because it is the
    //                      whisper meaning of the field and a family adopting
    //                      it then works unchanged, not because it does
    //                      anything today.
    //   translate          read by nobody anywhere in upstream.
    //   temperature        read only by TTS and voice conversion families, none
    //                      of which this RPC can route to.
    //
    // Two request fields are deliberately not forwarded at all:
    //
    //   threads   thread count is a SessionOptions field fixed when the session
    //             was built, so a per-request value has nowhere to go.
    //   diarize   this RPC routes to Asr or Alignment. A family that diarizes
    //             is reached through Diarize, which has its own handler and its
    //             own response shape, so forwarding this would imply that
    //             setting it turns speaker labels on here, and nothing would.
    return task_request;
}

// Duration of a possibly multi-channel buffer, in seconds. Frames, not floats:
// a stereo buffer holds two floats per position and would otherwise report
// twice its real length.
float audio_duration_seconds(const engine::runtime::AudioBuffer &audio) {
    const std::int64_t frames = audiocpp_backend::interleaved_frame_count(
        audio.samples.size(), audio.channels);
    return audiocpp_backend::samples_to_seconds(frames, audio.sample_rate);
}

// Refuses a request that routing sent to voice cloning with no speaker
// reference clip. Shared by TTS and TTSStream so the two RPCs cannot drift into
// refusing the same request differently.
//
// Refused FROM THE ROUTE, the same trick AudioTransform uses for params[stem].
// Voice cloning without a clip is a request the family cannot answer, and every
// cloning family says so only from inside its own prepare(): chatterbox throws
// "Chatterbox prepare requires speaker reference audio", which to_status maps to
// INTERNAL and which names neither the RPC nor the field the caller has to set.
// chatterbox advertises clon and no tts at all, so before this every preset-only
// or voice-less request to it got that engine-internal message.
//
// It cannot misfire on the legitimate case: has_voice_reference is what made
// routing choose VoiceCloning in the first place, so reaching here with a clip
// present is impossible unless the model pinned task:clon, and a clon pin with
// no clip is exactly the misconfiguration worth naming.
//
// Deliberately NOT generalised to "every task whose family declares
// supports_speaker_reference". That flag lives on the engine's CapabilitySet, is
// not carried through this backend's Capabilities mirror, and would need its own
// answer to whether it is advisory or binding before routing could act on it.
// Tracked as a follow-up.
void refuse_cloning_without_a_clip(const audiocpp_backend::LoadedModel &model,
                                   const audiocpp_backend::Route &route,
                                   bool voice_is_file) {
    if (route.task != audiocpp_backend::Task::VoiceCloning || voice_is_file) {
        return;
    }
    throw audiocpp_backend::ConfigError(
        "audio-cpp: family '" + model.family() + "' routes this request to " +
        audiocpp_backend::task_name(route.task) +
        ", which needs a speaker reference clip; set TTSRequest.voice to the "
        "path of a WAV file (a voice that is not a file on disk is treated as a "
        "named preset)");
}

// Maps a thrown exception onto the gRPC status the client should see.
GStatus to_status(const std::exception &err) {
    if (dynamic_cast<const audiocpp_backend::ConfigError *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::INVALID_ARGUMENT, err.what());
    }
    if (dynamic_cast<const audiocpp_backend::CapabilityError *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::UNIMPLEMENTED, err.what());
    }
    // The lane was busy and this caller ran out of patience. UNAVAILABLE, not
    // RESOURCE_EXHAUSTED: the request is fine and retrying later is the right
    // response, which is what UNAVAILABLE tells a client.
    if (dynamic_cast<const audiocpp_backend::LaneUnavailable *>(&err) != nullptr) {
        return GStatus(grpc::StatusCode::UNAVAILABLE, err.what());
    }
    return GStatus(grpc::StatusCode::INTERNAL, err.what());
}

class AudioCppBackend final : public backend::Backend::Service {
public:
    GStatus Health(ServerContext *, const backend::HealthMessage *,
                   backend::Reply *reply) override {
        reply->set_message("OK");
        return GStatus::OK;
    }

    GStatus Status(ServerContext *, const backend::HealthMessage *,
                   backend::StatusResponse *response) override {
        // snapshot_unchecked is right here, and is the only place it is:
        // HealthMessage carries no ModelIdentity, so there is nothing to check.
        response->set_state(snapshot_unchecked()
                                ? backend::StatusResponse::READY
                                : backend::StatusResponse::UNINITIALIZED);
        return GStatus::OK;
    }

    GStatus LoadModel(ServerContext *, const backend::ModelOptions *request,
                      backend::Result *result) override {
        try {
            std::vector<std::string> entries(request->options().begin(),
                                             request->options().end());
            auto parsed = audiocpp_backend::parse_model_options(entries);
            if (!parsed.error.empty()) {
                throw audiocpp_backend::ConfigError(parsed.error);
            }
            // ModelOptions.Threads is the model YAML's threads setting; the
            // explicit threads: option wins when both are set.
            if (parsed.options.threads == 0 && request->threads() > 0) {
                parsed.options.threads = static_cast<int>(request->threads());
            }
            // MainGPU carries the device index for GPU backends, and is the
            // fallback: an explicit device: option wins, matching threads above.
            // device_set rather than `device == 0`, because 0 is a real device
            // index and the value alone cannot say whether anyone chose it.
            if (!parsed.options.device_set && !request->maingpu().empty()) {
                parsed.options.device = parse_device_index(request->maingpu());
            }

            const std::string path = audiocpp_backend::resolve_model_path(
                request->modelpath(), request->modelfile(), request->model());

            // ModelOptions.Model is the UNTRANSLATED controller-side name, and
            // is what every ModelIdentity field on a request is compared
            // against. Passed at construction so the model and the identity it
            // was loaded with are published together.
            auto loaded = std::make_shared<audiocpp_backend::LoadedModel>(
                path, parsed.options, request->model());

            // Read everything the reply and the log line need while this scope
            // still owns the model. After the move, `loaded` is null and the
            // global is only safe to touch under the mutex.
            const std::string family = loaded->family();
            const std::string variant = loaded->variant();
            const std::string capabilities =
                audiocpp_backend::describe_capabilities(loaded->capabilities());
            std::shared_ptr<audiocpp_backend::LoadedModel> replaced;
            {
                std::lock_guard<std::mutex> lock(g_model_mu);
                replaced = std::move(g_model);
                g_model = std::move(loaded);
            }
            // The previous model, if any, is dropped outside the lock, for the
            // same reason Free does it: teardown must not hold up Status.
            replaced.reset();

            // Logged once at load time so an operator can see what the model
            // can do without making a request. ModelMetadata is deliberately
            // not implemented: its response message is chat-template oriented
            // and has no field for family, variant, languages or capabilities,
            // so there is nothing truthful to put in it.
            std::cerr << "audio-cpp: loaded family '" << family << "' variant '"
                      << variant << "' capabilities: " << capabilities << "\n";

            result->set_success(true);
            result->set_message("loaded audio.cpp family " + family);
            return GStatus::OK;
        } catch (const std::exception &err) {
            result->set_success(false);
            result->set_message(err.what());
            // A failed load must also be a gRPC error, or the model loader's
            // backend probe treats this backend as having accepted the model.
            return to_status(err);
        }
    }

    GStatus Free(ServerContext *, const backend::HealthMessage *,
                 backend::Result *result) override {
        // Drops the global's reference. Any handler still running holds its own
        // from snapshot_for(), so the model is destroyed by whichever of them
        // finishes last rather than underneath one of them. Destroying
        // LoadedModel releases its sessions first, then the model.
        std::shared_ptr<audiocpp_backend::LoadedModel> released;
        {
            std::lock_guard<std::mutex> lock(g_model_mu);
            released = std::move(g_model);
        }
        // Released outside the lock: if this is the last reference, the model
        // and its sessions are torn down here, and that must not block Status
        // or a fresh LoadModel behind g_model_mu.
        released.reset();
        result->set_success(true);
        return GStatus::OK;
    }

    GStatus AudioTranscription(ServerContext *,
                               const backend::TranscriptRequest *request,
                               backend::TranscriptResult *response) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            // has_prompt_text is what lets a family that can only align be
            // reached through this RPC at all, so it is not decoration.
            shape.has_prompt_text = !request->prompt().empty();
            shape.pinned_task = model->pinned_task();

            // Capability refusal before the lane and before the file read, for
            // the reasons spelled out in Diarize.
            model->check_can_serve(audiocpp_backend::Rpc::AudioTranscription, shape);

            // TranscriptRequest.dst is the INPUT audio path, despite the field
            // name. The HTTP layer materialises the upload to a temp file and
            // passes the path; nothing is written back.
            auto audio = audiocpp_backend::read_audio_file(request->dst(),
                                                           kSpeechSampleRate);
            // Both read before the buffer is moved into the request below. The
            // rate is the BUFFER's, which after read_audio_file is
            // kSpeechSampleRate and not necessarily the file's, and it is the
            // domain the result spans come back in.
            const int sample_rate = audio.sample_rate;
            const float duration = audio_duration_seconds(audio);

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session = model->session_for(
                audiocpp_backend::Rpc::AudioTranscription, shape, lane);

            // session.task, not the request: for Asr the prompt is decoding
            // context, for Alignment it is the transcript to align, and routing
            // has already decided which of those this is.
            const auto task_request =
                build_transcription_request(*request, session.task, std::move(audio));
            const auto result =
                audiocpp_backend::run_offline(session, task_request, lane);

            // text comes from result.text_output verbatim, never from the
            // segments. See THE RULE in result_map.h.
            audiocpp_backend::fill_transcript_result(result, sample_rate, duration,
                                                     response);
            // eou stays false. It marks a decode that ended on the model's
            // end-of-utterance token, which is a cache-aware STREAMING concept;
            // an offline run over a whole file has no turn to yield.
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    // Verifying this by hand: silero_vad is a SPEECH detector, so a synthetic
    // stimulus does not exercise it. A 220 Hz sine, broadband noise and a
    // harmonic buzz all return zero segments, correctly. Use real speech, which
    // upstream bundles and which therefore needs no download:
    //   audio.cpp/assets/resources/sample_16k.wav  (16 kHz mono, 14.07 s)
    // Taking 1 s from offset 8000 samples (0.5 s in), padded with 1 s of
    // silence either side, yields exactly one segment at start=0.9940
    // end=2.0460. A different excerpt of the same file shifts the start,
    // because it changes where speech actually begins inside the window.
    GStatus VAD(ServerContext *, const backend::VADRequest *request,
                backend::VADResponse *response) override {
        try {
            // snapshot_for, and the result is held for the whole call. Free may
            // arrive mid-request; it drops the global's reference only, so this
            // one keeps the model alive until the handler returns. It also
            // performs the not-loaded and identity checks, so a stale route is
            // refused before any work happens.
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            // Without this the `task:` model option is dead: routing is
            // otherwise derived from the RPC alone.
            shape.pinned_task = model->pinned_task();

            // Before the lane. A model that cannot do VAD at all answers
            // immediately instead of queueing behind somebody else's run only
            // to be refused; routing is pure and needs no lane. session_for
            // routes again below and reaches the same answer.
            model->check_can_serve(audiocpp_backend::Rpc::Vad, shape);

            engine::runtime::TaskRequest task;
            task.audio_input = audiocpp_backend::buffer_from_mono(
                std::vector<float>(request->audio().begin(), request->audio().end()),
                kVadSampleRate);

            // The lane is taken BEFORE session_for, not after. session_for
            // reads and writes an unsynchronised session cache and prepare()
            // mutates the session itself, so both belong inside the lane; see
            // the note on session_for in loaded_model.h. Bound to a named local
            // because LaneEntry is immovable and C++17 elides the return.
            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Vad, shape, lane);
            const auto result =
                audiocpp_backend::run_offline(session, task, lane);

            // VADSegment.start/end are float SECONDS, not sample indices.
            for (const auto &segment : result.speech_segments) {
                auto *out = response->add_segments();
                out->set_start(audiocpp_backend::samples_to_seconds(
                    segment.span.start_sample, kVadSampleRate));
                out->set_end(audiocpp_backend::samples_to_seconds(
                    segment.span.end_sample, kVadSampleRate));
            }
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    GStatus Diarize(ServerContext *, const backend::DiarizeRequest *request,
                    backend::DiarizeResponse *response) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            shape.pinned_task = model->pinned_task();

            // Route, then read, then lane, in that order, and every step of it
            // is deliberate.
            //
            // The capability refusal comes first because a family that cannot
            // diarize at all should say so, not complain about the input file:
            // on a VAD-only model, reading the audio first would surface
            // "cannot read /tmp/x.wav" and send the operator hunting a file
            // problem instead of a model choice.
            //
            // It also comes before the lane, which is the part that used to be
            // impossible. Routing needs no lane, so the refusal no longer waits
            // out somebody else's thirty second run to be told no, and neither
            // does the file read.
            model->check_can_serve(audiocpp_backend::Rpc::Diarize, shape);

            // DiarizeRequest.dst is the INPUT path, despite the field name: the
            // HTTP layer materialises the upload to a temp file and passes the
            // path here. Nothing is written back.
            //
            // Read at 16 kHz mono: sortformer refuses any other rate, and the
            // HTTP layer copies the upload byte for byte, so whatever the user
            // posted is what arrives. See kSpeechSampleRate.
            auto audio = audiocpp_backend::read_audio_file(request->dst(),
                                                           kSpeechSampleRate);
            const int sample_rate = audio.sample_rate;
            // Read off the buffer before it is moved into the request below.
            // Frames, not floats: a stereo input holds two floats per position
            // and would otherwise report twice its real duration.
            const std::int64_t frames = audiocpp_backend::interleaved_frame_count(
                audio.samples.size(), audio.channels);

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Diarize, shape, lane);

            engine::runtime::TaskRequest task;
            // Moved, not copied: a long recording runs to tens of megabytes and
            // this is its only owner.
            task.audio_input = std::move(audio);
            // READ THIS BEFORE WIRING THE FIRST DIARIZATION FAMILY. This
            // forwarding is currently DEAD for sortformer, the only diarizer
            // audio.cpp ships, and from the caller's side that is a silent
            // wrong answer, not a soft hint:
            //
            //   - backend.proto documents num_speakers as "exact speaker count
            //     if known (>0 forces)". A caller who asks for 2 and gets the
            //     model's 4 labels back with a 200 and no diagnostic has been
            //     given a wrong answer, so core/backend/diarization.go's
            //     "backends ignore what they don't act on" does not cover it.
            //   - sortformer takes its speaker count from the model package
            //     (assets.model_config.modules.num_speakers; the bundled
            //     variant is 4-speaker), and its postprocess config is parsed
            //     from runtime::SessionOptions, i.e. load time, not from
            //     TaskRequest.options at all.
            //   - worse, its unknown-option check only inspects keys prefixed
            //     "sortformer_diar.", so a bare "num_speakers" here is not even
            //     a recognised key: it is dropped without a trace.
            //
            // The keys are still forwarded, because a family that does read
            // them then works with no change here. But the family that lands
            // MUST either honour num_speakers or refuse it with
            // INVALID_ARGUMENT. No refusal path is added now because there is
            // no diarization family wired yet to test one against, and an
            // untested refusal is its own hazard.
            //
            // Also dropped today, and worth revisiting at the same time:
            // clustering_threshold, min_duration_on and min_duration_off.
            // The two min_duration_* fields are NOT family-specific: they are
            // generic post-filters over the returned turns (drop a turn shorter
            // than min_duration_on, merge two turns of the same speaker
            // separated by less than min_duration_off), so the handler could
            // honour them in about six lines whatever the family does.
            //
            // The knobs that DO reach sortformer (speaker_threshold,
            // speaker_min_frames, speaker_pad_frames, session_len_sec) are
            // session options and arrive through the model YAML's
            // `session.<key>:` entries at load time, not per request.
            if (request->num_speakers() > 0) {
                task.options["num_speakers"] =
                    std::to_string(request->num_speakers());
            }
            if (request->min_speakers() > 0) {
                task.options["min_speakers"] =
                    std::to_string(request->min_speakers());
            }
            if (request->max_speakers() > 0) {
                task.options["max_speakers"] =
                    std::to_string(request->max_speakers());
            }

            const auto result =
                audiocpp_backend::run_offline(session, task, lane);

            // Emitted verbatim, in the order the model produced them. NOT
            // sorted, merged or de-overlapped: sortformer binarizes each speaker
            // independently, so a turn nested inside another speaker's turn is
            // correct output for overlapped speech. LocalAI is overlap-tolerant
            // downstream (core/backend/diarization.go passes segments through
            // and the RTTM renderer handles overlap), so smoothing here would
            // destroy real information.
            std::set<std::string> speakers;
            int id = 0;
            for (const auto &turn : result.speaker_turns) {
                auto *out = response->add_segments();
                out->set_id(id++);
                // Seconds, like VADSegment. Only TranscriptSegment/Word are ns.
                //
                // Converted against the INPUT rate, which is correct and has
                // been checked against upstream rather than assumed: every
                // TimeSpan sortformer emits is built as
                // llround(seconds * 16000.0) (postprocess.cpp). The read above
                // guarantees the buffer is 16 kHz, so the span domain and the
                // input domain are the same one, and this is not a place where
                // a resampling family could silently scale every timestamp by a
                // constant.
                out->set_start(audiocpp_backend::samples_to_seconds(
                    turn.span.start_sample, sample_rate));
                out->set_end(audiocpp_backend::samples_to_seconds(
                    turn.span.end_sample, sample_rate));
                out->set_speaker(turn.speaker_id);
                // text stays EMPTY, including when include_text is set.
                // audio.cpp's SpeakerTurn carries a span and a speaker label
                // only, and TaskResult has no per-segment text anywhere, so
                // there is nothing truthful to put here.
                speakers.insert(turn.speaker_id);
            }
            response->set_num_speakers(static_cast<int>(speakers.size()));
            response->set_duration(
                audiocpp_backend::samples_to_seconds(frames, sample_rate));

            // Only set when the family bundles transcription, which no
            // diarization-only family does; kept because a combined family
            // would fill it in and the field is documented as optional.
            if (result.text_output.has_value()) {
                response->set_language(result.text_output->language);
            }
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    // Serves the four tasks LocalAI's AudioTransform can represent: voice
    // conversion, singing voice conversion, speech to speech and source
    // separation. Which one a request becomes is routing's decision, not this
    // handler's; see task_candidates for Rpc::AudioTransform, and note that svc
    // is reachable only through an explicit task: pin because no request signal
    // means "this input is singing".
    //
    // THE STEM COMPROMISE. AudioTransformResult carries a single dst, while
    // htdemucs and mel_band_roformer produce four and two named stems from one
    // run. Running inference once per stem would cost four full separations of
    // the same file, so this runs ONCE, writes every stem to a sibling file
    // <dst-stem>.<name>.<ext>, and puts the selected one in dst. Those siblings
    // are real files in the caller's output directory that LocalAI's caller
    // does not know about and will not clean up: a documented trade, not an
    // oversight, and the reason params["stem"] exists to say which one dst gets.
    GStatus AudioTransform(ServerContext *,
                           const backend::AudioTransformRequest *request,
                           backend::AudioTransformResult *response) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            const bool has_reference = !request->reference_path().empty();
            audiocpp_backend::RequestShape shape;
            // Unused by AudioTransform's own routing today, since none of its
            // four candidate tasks is chosen by the presence of a reference
            // clip. Set anyway, because leaving a shape field stale is how a
            // later routing rule silently reads the wrong thing.
            shape.has_voice_reference = has_reference;
            shape.pinned_task = model->pinned_task();

            // First, before the lane, before the file reads, and before the
            // argument check below. A family that cannot transform at all is
            // answering a question about ITSELF, so it must not first queue
            // behind somebody else's thirty second run, and it must not blame
            // the caller's paths for a decision that had nothing to do with
            // them. Same ordering as Diarize and AudioTranscription.
            const audiocpp_backend::Route route =
                model->check_can_serve(audiocpp_backend::Rpc::AudioTransform, shape);

            if (request->audio_path().empty() || request->dst().empty()) {
                throw audiocpp_backend::ConfigError(
                    "audio-cpp: AudioTransform needs both audio_path and dst");
            }

            engine::runtime::TaskRequest task;
            std::string requested_stem;
            for (const auto &param : request->params()) {
                if (param.first == "stem") {
                    // CONSUMED here and deliberately NOT forwarded into
                    // task.options: it selects which output the caller
                    // receives, it does not tune the model. Forwarding it would
                    // put a key no family reads into every request and imply
                    // the model had been asked to produce only that stem.
                    requested_stem = param.second;
                    continue;
                }
                task.options[param.first] = param.second;
            }

            // Refused from the ROUTE, before the file reads and before the run.
            // Only source separation produces named stems, and the route says
            // whether this is separation without running anything: the identical
            // refusal below, taken from the empty named_audio_outputs list,
            // cannot fire until a full conversion has been paid for (measured at
            // 1.6 s on miocodec, far worse on seed_vc or vevo2). The one below
            // stays as the backstop for a separation-routed family that returns
            // no stems anyway.
            if (!requested_stem.empty() &&
                route.task != audiocpp_backend::Task::SourceSeparation) {
                throw audiocpp_backend::ConfigError(
                    "audio-cpp: family '" + model->family() + "' routes this " +
                    "request to " + audiocpp_backend::task_name(route.task) +
                    ", which produces a single output with no named stems, so "
                    "params[stem]='" + requested_stem + "' cannot be honoured");
            }

            // Native rate, native channels, for both files. See
            // kTransformSampleRate: separation is destroyed by a downmix and
            // every conversion family resamples internally anyway.
            task.audio_input = audiocpp_backend::read_audio_file(
                request->audio_path(), kTransformSampleRate);
            if (has_reference) {
                engine::runtime::VoiceReference reference;
                reference.audio = audiocpp_backend::read_audio_file(
                    request->reference_path(), kTransformSampleRate);
                engine::runtime::VoiceCondition condition;
                condition.speaker = std::move(reference);
                task.voice = std::move(condition);
            }

            // Named local: LaneEntry is immovable and must span both
            // session_for and run_offline, which is the constraint the proof of
            // holding parameter exists to enforce.
            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::AudioTransform, shape, lane);
            const auto result = audiocpp_backend::run_offline(session, task, lane);

            const engine::runtime::AudioBuffer *chosen = nullptr;
            if (!result.named_audio_outputs.empty()) {
                std::vector<std::string> names;
                names.reserve(result.named_audio_outputs.size());
                for (const auto &named : result.named_audio_outputs) {
                    names.push_back(named.id);
                }
                // Selected BEFORE the first write, not after the loop. An
                // unknown stem name is a refused request, and a refused request
                // must not leave four files in the caller's output directory
                // that it then reports nothing about.
                //
                // The guarantee is exactly that and no more: a request REFUSED
                // ON ITS ARGUMENTS writes nothing. A write that FAILS partway
                // through the loop below, on a full disk say, still leaves the
                // siblings written before it, with no dst. Nothing is rolled
                // back, because deleting files after a disk error is its own
                // way to lose data, and the caller sees the failure.
                const auto choice =
                    audiocpp_backend::select_named_output(names, requested_stem);
                if (!choice.error.empty()) {
                    throw audiocpp_backend::ConfigError(choice.error);
                }

                for (size_t i = 0; i < result.named_audio_outputs.size(); ++i) {
                    const std::string sibling =
                        audiocpp_backend::sibling_stem_path(request->dst(), names[i]);
                    audiocpp_backend::write_audio_file(
                        sibling, result.named_audio_outputs[i].audio);
                    // Named in the response, in the model's own order. Without
                    // this the siblings are files nobody can find, and a caller
                    // that wants the drums as well as the vocals has to run the
                    // whole separation again per stem, which is the cost the
                    // single run exists to avoid.
                    auto *stem = response->add_stems();
                    stem->set_name(names[i]);
                    stem->set_dst(sibling);
                }
                chosen = &result.named_audio_outputs[static_cast<size_t>(choice.index)]
                              .audio;
                // dst last, so it is the file that exists only once every stem
                // beside it does. Its content duplicates the selected sibling
                // on purpose: the caller reads dst, an operator reads the
                // siblings, and neither should have to know about the other.
                audiocpp_backend::write_audio_file(request->dst(), *chosen);
            } else if (result.audio_output.has_value()) {
                if (!requested_stem.empty()) {
                    // A conversion family produces one unnamed output, so there
                    // is no stem to choose. Refused rather than ignored: a
                    // caller who asked for "vocals" and received the whole
                    // converted signal has been answered with something else.
                    throw audiocpp_backend::ConfigError(
                        "audio-cpp: family '" + model->family() +
                        "' produces a single output with no named stems, so "
                        "params[stem]='" + requested_stem + "' cannot be honoured");
                }
                chosen = &*result.audio_output;
                audiocpp_backend::write_audio_file(request->dst(), *chosen);
            } else {
                // Reached only if a family routed successfully and then
                // returned no audio at all, which is a broken family rather
                // than a wrong request. CapabilityError so it reads as "this
                // model does not do that" instead of as an internal fault.
                throw audiocpp_backend::CapabilityError(
                    "audio-cpp: family '" + model->family() +
                    "' produced no audio for the AudioTransform RPC");
            }

            response->set_dst(request->dst());
            response->set_sample_rate(chosen->sample_rate);
            // FRAMES, not floats. Separation output is stereo, so reporting
            // samples.size() would tell the caller a 3 second stem is 6 seconds
            // long.
            response->set_samples(static_cast<int>(
                audiocpp_backend::interleaved_frame_count(chosen->samples.size(),
                                                          chosen->channels)));
            response->set_reference_provided(has_reference);
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }

    // The first RPC that GENERATES audio from text, together with
    // SoundGeneration below. AudioTransform already wrote files, but it
    // transformed audio it was given; these two have no required input audio at
    // all, which is why both carry a Result with a success flag rather than a
    // response message describing what was found.
    //
    // TTSRequest.voice is overloaded across LocalAI backends, some reading it as
    // a named preset and some as a path to a clip. The rule here is decided from
    // the filesystem: an existing regular file is a speaker reference and
    // routing prefers VoiceCloning, anything else is a preset. Instructions with
    // no clip prefer VoiceDesign, and a clip outranks instructions when both are
    // set. None of that is re-derived here; it is capability_routing's
    // task_candidates, and this handler's job is only to describe the request
    // truthfully in the RequestShape.
    GStatus TTS(ServerContext *, const backend::TTSRequest *request,
                backend::Result *result) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                // Answered with the STATUS ALONE, leaving Result at its default.
                // core/backend/tts.go checks the transport error before it looks
                // at res.Success and returns on it, so the Result is never read
                // on this path; and the router matches a stale route on the
                // NOT_FOUND code plus the sentinel in the status message, which
                // is where check_model_identity already put it.
                return refusal;
            }

            // One description of the request, shared with TTSStream, so the two
            // handlers cannot describe the same request differently.
            // pinned_task stays here on purpose: it comes off the model rather
            // than the request, and it is the field a new handler forgets.
            audiocpp_backend::RequestShape shape =
                audiocpp_backend::build_tts_shape(*request);
            const bool voice_is_file = shape.has_voice_reference;
            shape.pinned_task = model->pinned_task();

            // FIRST, before the lane and before any file read. A family that
            // cannot synthesise at all is answering a question about itself, so
            // it must not queue behind somebody else's run to be told no, and it
            // must not blame the caller's reference clip for a decision that had
            // nothing to do with it. Same ordering as Diarize and
            // AudioTransform.
            const audiocpp_backend::Route route =
                model->check_can_serve(audiocpp_backend::Rpc::Tts, shape);

            refuse_cloning_without_a_clip(*model, route, voice_is_file);

            if (request->dst().empty()) {
                throw audiocpp_backend::ConfigError(
                    "audio-cpp: TTS needs a dst output path");
            }

            std::optional<engine::runtime::AudioBuffer> reference;
            if (voice_is_file) {
                // Native rate, native channels: see kVoiceReferenceSampleRate.
                reference = audiocpp_backend::read_audio_file(
                    request->voice(), kVoiceReferenceSampleRate);
            }
            // Read before the lane, like every other file-fed handler here, so
            // a slow or large clip is not decoded while holding it.
            const auto task =
                audiocpp_backend::build_tts_request(*request, std::move(reference));

            // Named local: LaneEntry is immovable and has to span both
            // session_for and run_offline.
            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::Tts, shape, lane);
            const auto task_result =
                audiocpp_backend::run_offline(session, task, lane);

            if (!task_result.audio_output.has_value()) {
                // A family that routed successfully and then produced no audio
                // is a broken family rather than a wrong request, but from the
                // caller's side the actionable fact is that this model does not
                // do this, so CapabilityError. Same judgement as AudioTransform.
                throw audiocpp_backend::CapabilityError(
                    "audio-cpp: family '" + model->family() +
                    "' produced no audio for the TTS RPC");
            }
            // Throws a plain runtime_error, i.e. INTERNAL, on a write failure:
            // dst is LocalAI's own generated-content path and not anything the
            // caller named, so a full disk there is a server fault and is worth
            // retrying. Only an empty dst is INVALID_ARGUMENT, and that is
            // already refused above with a message naming the RPC.
            audiocpp_backend::write_audio_file(request->dst(),
                                               *task_result.audio_output);
            result->set_success(true);
            // The path, because that is what core/backend/tts.go's caller reads
            // back and what every other LocalAI TTS backend puts here.
            result->set_message(request->dst());
            return GStatus::OK;
        } catch (const std::exception &err) {
            // DEFENSIVE, not load-bearing, and worth saying so plainly. gRPC
            // discards the response message entirely when the status is not OK,
            // so a client that checks the status, which core/backend/tts.go
            // does before it ever looks at res.Success, receives a nil Result
            // and never sees these two fields. They are set for a client that
            // ignores the status, and because a half-filled Result is a worse
            // thing to leave behind than a filled one.
            result->set_success(false);
            result->set_message(err.what());
            return to_status(err);
        }
    }

    GStatus SoundGeneration(ServerContext *,
                            const backend::SoundGenerationRequest *request,
                            backend::Result *result) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            // No request signal chooses between generation tasks: this RPC has
            // exactly one candidate, AudioGeneration. pinned_task is still
            // copied, because without it the model's `task:` option is dead
            // here, which is how a gen family pinned to something else would
            // silently be routed to generation anyway.
            shape.pinned_task = model->pinned_task();

            model->check_can_serve(audiocpp_backend::Rpc::SoundGeneration, shape);

            if (request->dst().empty()) {
                throw audiocpp_backend::ConfigError(
                    "audio-cpp: SoundGeneration needs a dst output path");
            }

            // DO NOT WIRE src INTO THE HTTP LAYER UNTIL THE PIN IS BUMPED PAST
            // THE FIX. Setting src on a stable_audio model CORRUPTS THE HEAP AND
            // ABORTS THE PROCESS in the pinned upstream: "free(): invalid size"
            // / "munmap_chunk(): invalid pointer", SIGABRT, backend gone. It is
            // upstream's, not this handler's, and it was attributed rather than
            // assumed: upstream's own audiocpp_cli, built from this same
            // checkout, aborts identically with --audio at exit 134, at both
            // 44.1 kHz stereo and 24 kHz mono, and completes cleanly with no
            // --audio at all. It is therefore neither caused nor worsened by
            // reading the clip at its native rate below.
            //
            // The only thing keeping that off the network today is an OMISSION:
            // core/http/endpoints/elevenlabs/soundgeneration.go passes nil for
            // sourceFile, and schema.ElevenLabsSoundGenerationRequest has no
            // field for it, so the sole caller that can set src is
            // core/cli/soundgeneration.go. Adding the field to that schema turns
            // a local CLI crash into a remotely reachable heap corruption with
            // fully attacker-influenced input. Nobody reading that Go schema
            // would know why the field is missing, which is why this is written
            // here as well as in the task report.
            //
            // No family blocklist is applied: ace_step's editing routes
            // legitimately need src, and a family-specific guard here would rot.
            std::optional<engine::runtime::AudioBuffer> source;
            if (request->has_src() && !request->src().empty()) {
                // Native rate and channels. ace_step resamples left and right
                // separately and stable_audio resamples per channel, so a
                // downmix here would delete the stereo image of the very clip
                // they are editing.
                source = audiocpp_backend::read_audio_file(
                    request->src(), kVoiceReferenceSampleRate);
            }
            const auto task = audiocpp_backend::build_sound_generation_request(
                *request, std::move(source));

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session = model->session_for(
                audiocpp_backend::Rpc::SoundGeneration, shape, lane);
            const auto task_result =
                audiocpp_backend::run_offline(session, task, lane);

            if (!task_result.audio_output.has_value()) {
                throw audiocpp_backend::CapabilityError(
                    "audio-cpp: family '" + model->family() +
                    "' produced no audio for the SoundGeneration RPC");
            }
            audiocpp_backend::write_audio_file(request->dst(),
                                               *task_result.audio_output);
            result->set_success(true);
            result->set_message(request->dst());
            return GStatus::OK;
        } catch (const std::exception &err) {
            result->set_success(false);
            result->set_message(err.what());
            return to_status(err);
        }
    }

    // The streaming counterpart of TTS, and the first RPC here that writes to
    // the wire while the model is still generating.
    //
    // THE WIRE CONTRACT, which is the thing most likely to go wrong.
    // pkg/grpc/server.go's TTSStream wrapper puts every chunk in Reply.audio,
    // and core/backend/tts.go's ModelTTSStream forwards those bytes to the HTTP
    // response verbatim. There is no framing and no format negotiation, so THE
    // FIRST CHUNK MUST BE A WAV HEADER or the client receives raw PCM it has no
    // way to interpret; browsers simply refuse the stream. Because the total
    // length is unknown while generating, both size fields carry 0xFFFFFFFF,
    // which is the convention backend/go/vibevoice-cpp established.
    //
    // ModelTTSStream will synthesise a header of its own, but ONLY when the
    // first Reply carries a non-empty `message` holding JSON with a sample_rate.
    // Nothing here ever sets Reply.message, so that branch never fires and there
    // is exactly one header on the wire. Setting `message` on this RPC without
    // deleting the header below would put a second header 44 bytes into the PCM.
    //
    // There is no dst: streaming TTS writes to the response, not to a file, and
    // core/backend/tts.go sends an empty dst on purpose. That is the one place
    // this RPC deliberately diverges from TTS.
    GStatus TTSStream(ServerContext *, const backend::TTSRequest *request,
                      grpc::ServerWriter<backend::Reply> *writer) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            // The SAME shape builder TTS uses, so the two RPCs cannot describe
            // one request differently. pinned_task stays at the call site: it
            // comes off the model rather than the request.
            audiocpp_backend::RequestShape shape =
                audiocpp_backend::build_tts_shape(*request);
            const bool voice_is_file = shape.has_voice_reference;
            shape.pinned_task = model->pinned_task();

            // FIRST, before the lane and before any file read, exactly as in
            // TTS. Note that this RPC's mode candidates are streaming ONLY: a
            // family that can synthesise but cannot stream is refused here
            // rather than quietly served a whole buffer at the end, because a
            // caller that asked to stream is asking for time to first audio.
            const audiocpp_backend::Route route =
                model->check_can_serve(audiocpp_backend::Rpc::TtsStream, shape);
            refuse_cloning_without_a_clip(*model, route, voice_is_file);

            std::optional<engine::runtime::AudioBuffer> reference;
            if (voice_is_file) {
                // Native rate, native channels: see kVoiceReferenceSampleRate.
                reference = audiocpp_backend::read_audio_file(
                    request->voice(), kVoiceReferenceSampleRate);
            }
            const auto task =
                audiocpp_backend::build_tts_request(*request, std::move(reference));

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session =
                model->session_for(audiocpp_backend::Rpc::TtsStream, shape, lane);

            bool header_sent = false;
            bool client_gone = false;
            int stream_rate = 0;
            int stream_channels = 0;
            const auto write_audio =
                [&](const engine::runtime::AudioBuffer &audio) {
                    if (client_gone || audio.samples.empty()) {
                        return;
                    }
                    const int channels = audio.channels > 0 ? audio.channels : 1;
                    if (!header_sent) {
                        backend::Reply head;
                        head.set_audio(audiocpp_backend::streaming_wav_header(
                            audio.sample_rate, channels));
                        if (!writer->Write(head)) {
                            client_gone = true;
                            return;
                        }
                        header_sent = true;
                        stream_rate = audio.sample_rate;
                        stream_channels = channels;
                    } else if (audio.sample_rate != stream_rate ||
                               channels != stream_channels) {
                        // The header already went out declaring the first
                        // chunk's format, and it cannot be taken back. Every
                        // later byte would be decoded at the wrong rate or the
                        // wrong interleaving, which plays as a speed or channel
                        // fault nobody can see in a 200. Fail loudly instead.
                        throw std::runtime_error(
                            "audio-cpp: family '" + model->family() +
                            "' changed its output format mid-stream (" +
                            std::to_string(stream_rate) + " Hz x" +
                            std::to_string(stream_channels) + " to " +
                            std::to_string(audio.sample_rate) + " Hz x" +
                            std::to_string(channels) + ")");
                    }
                    backend::Reply reply;
                    reply.set_audio(audiocpp_backend::f32_to_s16le(audio.samples));
                    if (!writer->Write(reply)) {
                        client_gone = true;
                    }
                };

            // BOTH fields, and named_audio_outputs is the one that carries the
            // audio in practice. All three streaming TTS families put their
            // chunks there ("chunk_0", "chunk_1", ...) and leave audio_output
            // empty until the very end: supertonic and omnivoice build the event
            // in next_stream_event, voxcpm2 replays the ones its start_stream
            // produced. Reading only audio_output, which is the obvious field,
            // yields a stream with no audio in it at all.
            const auto emit = [&](const engine::runtime::StreamEvent &event) {
                if (event.audio_output.has_value()) {
                    write_audio(*event.audio_output);
                }
                for (const auto &named : event.named_audio_outputs) {
                    write_audio(named.audio);
                }
            };

            const auto result =
                audiocpp_backend::run_streaming_pull(session, task, emit, lane);

            // The finish_stream result is the session's own MERGED WHOLE, not a
            // tail the pull loop missed: supertonic returns its accumulated
            // buffer, omnivoice post-processes the merge, voxcpm2 hands back the
            // stored result. Emitting it after the chunks would send the entire
            // utterance twice. It is therefore used only when the family
            // streamed nothing, which is what a family that held everything back
            // to finalize looks like.
            //
            // The cost of that choice, stated plainly: what a client hears is
            // the concatenation of the chunks, and for omnivoice that is not
            // byte-identical to what the unary TTS RPC returns, because its
            // postprocessor runs over the merged buffer at the end. Audio
            // already on the wire cannot be post-processed, so any streaming
            // implementation has to accept this.
            //
            // This guard also depends on the pull loop having DRAINED the
            // session. It does today: no family sets is_final on a pulled
            // event, so the loop runs to nullopt. If one ever does, the loop
            // breaks early, and omnivoice's finish_stream drains and merges the
            // chunks that were never emitted (session.cpp:576) into the result
            // this branch then discards, silently truncating the audio. Whoever
            // teaches a family to end a pull stream early has to revisit the
            // pair together.
            if (!header_sent) {
                if (result.audio_output.has_value()) {
                    write_audio(*result.audio_output);
                } else {
                    for (const auto &named : result.named_audio_outputs) {
                        write_audio(named.audio);
                    }
                }
            }

            if (!header_sent && !client_gone) {
                // Routed successfully and produced nothing. Same judgement as
                // TTS: from the caller's side the actionable fact is that this
                // model does not do this.
                throw audiocpp_backend::CapabilityError(
                    "audio-cpp: family '" + model->family() +
                    "' produced no audio for the TTSStream RPC");
            }
            // client_gone is NOT an error: the client hung up, gRPC already
            // knows, and there is nobody left to tell.
            return GStatus::OK;
        } catch (const std::exception &err) {
            // No Result message to fill in on this RPC. A status mid-stream is
            // what the client sees, and gRPC delivers it after whatever chunks
            // already went out.
            return to_status(err);
        }
    }

    // Server-streaming transcription: zero or more TranscriptStreamResponse
    // messages carrying `delta`, then exactly one carrying `final_result`.
    //
    // THE DELTA CONTRACT. core/backend/transcript.go documents Delta as "an
    // incremental text fragment" and the HTTP layer appends them, so a client
    // that concatenates every delta must end up with the final text and must
    // never see a prefix repeated. The families do not agree on what they report
    // (nemotron_asr and vibevoice_asr send fragments, voxtral_realtime sends the
    // whole hypothesis and repeats the last event of each batch), and a delta
    // must additionally be valid UTF-8 or the Go client cannot unmarshal it at
    // all, so the reconciliation lives in TranscriptDeltaTracker rather than
    // here. See stream_delta.h.
    //
    // THE OFFLINE FALLBACK. mode_candidates lets this RPC fall back to an
    // offline route, so a family with no streaming ASR still answers rather than
    // returning UNIMPLEMENTED. It then runs once and the reconciliation below
    // emits the whole transcript as a single delta: the same message sequence a
    // streaming family produces, with one delta instead of many. That is a
    // truthful degradation, and it needs no branch of its own, because a tracker
    // that observed nothing reconciles to exactly one fragment.
    GStatus AudioTranscriptionStream(
        ServerContext *, const backend::TranscriptRequest *request,
        grpc::ServerWriter<backend::TranscriptStreamResponse> *writer) override {
        try {
            GStatus refusal = GStatus::OK;
            const auto model = snapshot_for(request, refusal);
            if (model == nullptr) {
                return refusal;
            }

            audiocpp_backend::RequestShape shape;
            shape.has_prompt_text = !request->prompt().empty();
            shape.pinned_task = model->pinned_task();

            // Before the lane and before the file read, for the reasons spelled
            // out in Diarize.
            model->check_can_serve(audiocpp_backend::Rpc::AudioTranscriptionStream,
                                   shape);

            // TranscriptRequest.dst is the INPUT audio path, despite the field
            // name; nothing is written back. Read at 16 kHz mono for the reasons
            // in kSpeechSampleRate, which apply identically here: the streaming
            // sessions build their spans in the same feature domain their
            // offline halves do.
            auto audio = audiocpp_backend::read_audio_file(request->dst(),
                                                           kSpeechSampleRate);
            const int sample_rate = audio.sample_rate;
            const float duration = audio_duration_seconds(audio);

            audiocpp_backend::LaneEntry lane = model->acquire(0);
            const auto session = model->session_for(
                audiocpp_backend::Rpc::AudioTranscriptionStream, shape, lane);

            // session.task, not the request: for Asr the prompt is decoding
            // context, for Alignment it is the transcript to align.
            const auto task = build_transcription_request(*request, session.task,
                                                          std::move(audio));
            if (!task.audio_input.has_value()) {
                // Unreachable through build_transcription_request, which always
                // sets it. Checked because the alternative is dereferencing an
                // empty optional below.
                throw std::runtime_error(
                    "audio-cpp: transcription request carries no audio");
            }

            audiocpp_backend::TranscriptDeltaTracker tracker;
            bool client_gone = false;
            const auto write_delta = [&](const std::string &fragment) {
                if (fragment.empty() || client_gone) {
                    return;
                }
                backend::TranscriptStreamResponse response;
                response.set_delta(fragment);
                if (!writer->Write(response)) {
                    client_gone = true;
                }
            };
            const auto emit = [&](const engine::runtime::StreamEvent &event) {
                if (!event.partial_text.has_value()) {
                    return;
                }
                write_delta(tracker.observe(event.partial_text->text));
            };

            engine::runtime::TaskResult result;
            if (session.mode == audiocpp_backend::Mode::Streaming) {
                // The buffer inside the request is the chunk source, so the
                // audio is held once rather than copied for the feed.
                result = audiocpp_backend::run_streaming_audio(
                    session, task, *task.audio_input, emit, lane);
            } else {
                result = audiocpp_backend::run_offline(session, task, lane);
            }

            // The reconciliation, and the only place the offline fallback needs
            // to be thought about: with no partials observed this IS the single
            // delta carrying the whole transcript.
            if (result.text_output.has_value()) {
                write_delta(tracker.reconcile(result.text_output->text));
            }

            backend::TranscriptStreamResponse final_response;
            // sample_rate is the BUFFER's, which after read_audio_file is
            // kSpeechSampleRate and not necessarily the file's; it is the domain
            // the result spans come back in.
            audiocpp_backend::fill_transcript_result(
                result, sample_rate, duration,
                final_response.mutable_final_result());
            if (!client_gone) {
                writer->Write(final_response);
            }
            return GStatus::OK;
        } catch (const std::exception &err) {
            return to_status(err);
        }
    }
};

void RunServer(const std::string &addr) {
    AudioCppBackend service;
    grpc::EnableDefaultHealthCheckService(true);
    grpc::reflection::InitProtoReflectionServerBuilderPlugin();

    ServerBuilder builder;
    builder.AddListeningPort(addr, grpc::InsecureServerCredentials());
    builder.RegisterService(&service);
    // Audio payloads (PCM buffers, encoded frames) are far larger than the
    // 4 MiB gRPC default.
    builder.SetMaxReceiveMessageSize(256 * 1024 * 1024);
    builder.SetMaxSendMessageSize(256 * 1024 * 1024);

    std::unique_ptr<Server> server(builder.BuildAndStart());
    if (!server) {
        std::cerr << "audio-cpp grpc-server: failed to bind " << addr << "\n";
        std::exit(1);
    }
    std::cerr << "audio-cpp grpc-server listening on " << addr << "\n";

    // Wait() runs off the main thread so the main thread stays free to notice
    // the shutdown flag and call Shutdown itself, outside any signal context.
    std::thread serving([&server] { server->Wait(); });

    // Polled rather than waited on: notifying a condition variable from a
    // signal handler is not async-signal-safe either, so a condition variable
    // would move the same defect rather than fix it. Ten wakeups a second on an
    // otherwise idle process is not worth a more elaborate scheme.
    while (!g_shutdown_requested.load(std::memory_order_relaxed)) {
        std::this_thread::sleep_for(std::chrono::milliseconds(100));
    }

    // In-flight RPCs get three seconds to drain before the server stops hard.
    server->Shutdown(std::chrono::system_clock::now() + std::chrono::seconds(3));
    serving.join();
}

void signal_handler(int) {
    // Sets a lock-free atomic and returns. Nothing else belongs here.
    //
    // Calling grpc::Server::Shutdown directly from a signal handler, which is
    // what this used to do, takes an absl::Mutex. That is not async-signal-safe:
    // the handler can interrupt a thread that already holds the same mutex, and
    // abseil's deadlock detector sees the reentrant acquisition and aborts the
    // process. The observed result was exit 134 and a "dying due to potential
    // deadlock" stack on every SIGTERM, which is exactly the crash signature a
    // real teardown bug would have to compete with.
    g_shutdown_requested.store(true, std::memory_order_relaxed);
}

} // namespace

int main(int argc, char *argv[]) {
    std::string addr = "127.0.0.1:50051";
    for (int i = 1; i < argc; ++i) {
        std::string a = argv[i];
        const std::string addr_flag = "--addr=";
        if (a.rfind(addr_flag, 0) == 0) {
            addr = a.substr(addr_flag.size());
        } else if (a == "--addr" && i + 1 < argc) {
            addr = argv[++i];
        } else if (a == "--help" || a == "-h") {
            std::cout << "Usage: grpc-server --addr=HOST:PORT\n";
            return 0;
        }
    }
    std::signal(SIGINT, signal_handler);
    std::signal(SIGTERM, signal_handler);
    RunServer(addr);
    return 0;
}
