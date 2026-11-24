#pragma once

// Thin wrappers over the framework's public audio IO. Engine-linked, so this
// unit is built and tested through the CMake target rather than by
// backend/cpp/run-unit-tests.sh. The pure part of the arithmetic these
// wrappers feed lives in audio_units, which is stdlib-only and does have a
// standalone test.

#include "engine/framework/runtime/session.h"

#include <string>
#include <vector>

namespace audiocpp_backend {

// Reads a WAV file. Throws ConfigError when the file is missing, is not
// readable as WAV, or declares a non-positive sample rate: all three are
// user-fixable input problems rather than backend faults.
//
// A declared sample rate of zero is refused rather than passed on, because
// every downstream conversion in audio_units answers 0 for a non-positive rate.
// Accepting it would turn a corrupt header into a response full of zero
// timestamps, which reads as a real answer.
//
// `target_sample_rate` is the rate the CALLER needs, in Hz:
//
//   0 (or negative)  keep the file's own rate and channel count.
//   positive         downmix to mono and resample to that rate. Resampling is
//                    skipped when the file already declares it, so passing the
//                    rate a route needs costs nothing on the common input.
//
// It is a parameter, and not a constant inside this function, because the
// routes that read audio do not agree on an answer. Speech routes want 16 kHz
// mono; source separation does not, and folding a 44.1 kHz stereo input to
// 16 kHz mono for demucs or roformer would destroy the very thing they separate
// (both refuse a rate other than their own outright). Making the caller name
// the rate keeps that decision where the route is known.
//
// Downmixing along with the resample is not an extra liberty: every family a
// positive rate is used for (silero_vad, sortformer_diar and every ASR family)
// begins by calling the same mixdown_interleaved_to_mono_average on whatever it
// is given. Doing it once here produces the identical samples and halves the
// buffer that is then moved through the request.
engine::runtime::AudioBuffer read_audio_file(const std::string &path,
                                             int target_sample_rate);

// Writes 16-bit PCM WAV, creating parent directories.
//
// Throws ConfigError, i.e. INVALID_ARGUMENT, ONLY for an empty path, which is a
// malformed request. Every other failure throws a plain runtime_error, i.e.
// INTERNAL: the destination is LocalAI's own generated-content directory and
// not anything the caller named, so a full disk or a permission fault there is
// a server fault and is worth retrying, which is the opposite of what
// INVALID_ARGUMENT tells a client.
void write_audio_file(const std::string &path,
                      const engine::runtime::AudioBuffer &audio);

engine::runtime::AudioBuffer buffer_from_mono(std::vector<float> samples,
                                              int sample_rate);

} // namespace audiocpp_backend
