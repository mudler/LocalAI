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

// Reads a WAV file at its native sample rate and channel count. Throws
// ConfigError when the file is missing, is not readable as WAV, or declares a
// non-positive sample rate: all three are user-fixable input problems rather
// than backend faults.
//
// A declared sample rate of zero is refused rather than passed on, because
// every downstream conversion in audio_units answers 0 for a non-positive rate.
// Accepting it would turn a corrupt header into a response full of zero
// timestamps, which reads as a real answer.
engine::runtime::AudioBuffer read_audio_file(const std::string &path);

// Writes 16-bit PCM WAV, creating parent directories. Throws ConfigError when
// the destination cannot be written.
void write_audio_file(const std::string &path,
                      const engine::runtime::AudioBuffer &audio);

engine::runtime::AudioBuffer buffer_from_mono(std::vector<float> samples,
                                              int sample_rate);

} // namespace audiocpp_backend
