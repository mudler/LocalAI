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

} // namespace audiocpp_backend
