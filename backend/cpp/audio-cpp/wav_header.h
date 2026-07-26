#pragma once

// Builds the 44 byte canonical WAV header that precedes a streamed PCM body.
// Standard library only.
//
// TTSStream chunks travel in Reply.audio and the FIRST chunk must be this
// header, or an HTTP client has no format to decode the PCM with. Because the
// total length is unknown while the model is still generating, both size fields
// carry 0xFFFFFFFF; that is the convention backend/go/vibevoice-cpp established
// (govibevoicecpp.go, TTSStream) and the one core/backend/tts.go writes when it
// synthesises a header itself.
//
// WHY THIS BACKEND SENDS THE HEADER RATHER THAN LETTING GO DO IT.
// core/backend/tts.go's ModelTTSStream will emit a header of its own, but only
// when the FIRST Reply carries a non-empty `message` field holding a JSON blob
// with a sample_rate. This backend sends audio and never sets `message`, so
// that branch never fires and there is exactly one header on the wire: this
// one. Do not start setting Reply.message on this RPC without deleting the
// header below, or every stream gains a second header 44 bytes into the PCM.
//
// The header this produces is byte-identical to the one pkg/audio.WAVHeader
// serialises, which is what core/http/endpoints/openai/realtime_model.go
// assumes when it reads the sample rate out of byte offset 24 of the first
// callback.

#include <string>

namespace audiocpp_backend {

// 16-bit PCM, little endian, interleaved.
//
// `channels` is clamped to at least 1 and at most 65535, so a garbage channel
// count can never write a zero block align, which is what a reader divides the
// data size by. `sample_rate` is clamped to at least 0 rather than wrapped:
// a zero rate is visibly wrong to whoever reads the header, whereas the
// 4294967295 an unsigned conversion of -1 would write looks like a real field.
std::string streaming_wav_header(int sample_rate, int channels);

} // namespace audiocpp_backend
