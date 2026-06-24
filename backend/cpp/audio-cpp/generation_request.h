#pragma once

// Builds the engine::runtime::TaskRequest for the two audio-PRODUCING offline
// RPCs, TTS and SoundGeneration, and answers the one filesystem question TTS
// routing depends on.
//
// It is a unit of its own rather than a pair of statics in grpc-server.cpp so
// that it can be tested: grpc-server.cpp has a main() and cannot be linked into
// a test binary, and everything here is a pure function of its arguments once
// the file read has been lifted out (which is why the reference clip arrives as
// an already-read buffer rather than a path). TTSStream reuses build_tts_request
// unchanged.
//
// Only the plain structs in engine/framework/runtime/session.h are touched, so
// this compiles against the header without linking engine_runtime, the same way
// result_map does.

#include "backend.pb.h"
#include "capability_routing.h"

#include "engine/framework/runtime/session.h"

#include <optional>
#include <string>

namespace audiocpp_backend {

// True when TTSRequest.voice names an existing regular file, in which case it
// is a speaker reference clip and routing prefers VoiceCloning; false when it is
// a named preset (or empty).
//
// The overload is LocalAI's, not this backend's: `voice` is the OpenAI speech
// field and different LocalAI backends have always read it both ways. Deciding
// it from the filesystem needs no new option and matches how somebody actually
// configures a cloning family, which is by pointing at a clip.
//
// A DIRECTORY is deliberately not a reference: is_regular_file, not exists. A
// directory named as a voice cannot be read as a WAV, and treating it as a
// reference would turn a preset typo into "cannot read /x as WAV" instead of
// letting it travel as the preset name it looks like.
//
// The error_code overload is used so an unreadable parent directory answers
// false rather than throwing. That is the right answer here: the name is then
// passed on as a preset, and if it really was meant to be a clip the family
// refuses a request it cannot serve, which is a better message than a
// filesystem exception thrown while classifying a string.
bool voice_is_reference_file(const std::string &voice);

// Everything routing needs to know about a TTSRequest, in one place, so that TTS
// and TTSStream cannot describe the same request differently.
//
// `pinned_task` is deliberately NOT filled here: it comes off the LoadedModel,
// not off the request, and this unit links no engine. The caller must still
// write `shape.pinned_task = model->pinned_task();` or the model's `task:`
// option is dead. That is the one field a new handler can forget, so it is the
// one field left visible at the call site rather than hidden behind this
// helper.
RequestShape build_tts_shape(const backend::TTSRequest &request);

// `reference_audio` is the already-read speaker clip, present exactly when
// voice_is_reference_file(request.voice()) was true. Passing it in rather than a
// path keeps this function pure and lets the caller do the read where the
// ordering rules (capability refusal first, then the lane) are enforced.
//
// It is taken BY VALUE and moved in: a reference clip is seconds of audio and
// the caller has no use for it afterwards.
engine::runtime::TaskRequest
build_tts_request(const backend::TTSRequest &request,
                  std::optional<engine::runtime::AudioBuffer> reference_audio);

// `source_audio` is SoundGenerationRequest.src already read, present exactly
// when the field was set and non-empty. Same reasoning as above.
engine::runtime::TaskRequest
build_sound_generation_request(const backend::SoundGenerationRequest &request,
                               std::optional<engine::runtime::AudioBuffer> source_audio);

// Lifts a text-conditioned transform route's text out of the request params
// into TaskRequest.text_input, and reports whether it set one.
//
// WHY THIS EXISTS. AudioTransform is an audio-in / audio-out RPC and its proto
// message has no text field, but not every task it routes to is audio-only.
// vevo2's speech-to-speech and prosody routes read their text from
// request.text_input (src/models/vevo2/session.cpp fills refs.target_text from
// exactly there and nowhere else) and refuse the run without one: "Vevo2
// text/prosody route requires text_input or target_text". The params map is the
// only channel AudioTransform has that reaches the engine, so the text travels
// through it and is unpacked here. Without this, s2s is not merely awkward to
// reach through this RPC, it is unreachable.
//
// CALL IT AFTER the params have been copied into task.options, and note that it
// does NOT erase the keys it reads. vevo2's loader advertises "target_text" in
// its own documented request-option table, so a family that looks there keeps
// finding it; the copy in text_input is what the session actually reads today.
//
// "target_text" is canonical and "text" is its alias, the same order vevo2's
// option table declares them in. A request setting both gets target_text, so
// the canonical spelling wins rather than whichever the map happened to store
// first. An empty value is not a text: it means the caller sent the key with
// nothing in it, and a family asked to vocalise "" should say so itself rather
// than be handed an empty Transcript that looks deliberate.
//
// "language" rides along when a text was found, and only then. On its own it
// conditions nothing, and setting text_input for it alone would turn a plain
// separation request that happened to carry a language hint into a text-routed
// one.
bool apply_transform_text_input(engine::runtime::TaskRequest &task);

} // namespace audiocpp_backend
