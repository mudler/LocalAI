// Tests for audio_io's reading contract, and in particular for the resampling
// that keeps a 44.1 or 48 kHz upload from reaching a family that only accepts
// 16 kHz.
//
// NAMED _ctest AND NOT _test ON PURPOSE: see the note at the top of
// result_map_ctest.cpp. This file links the audio.cpp engine, so it is built
// and run by ctest, not by backend/cpp/run-unit-tests.sh.
//
//   make -C backend/cpp/audio-cpp test-engine

#include "audio_io.h"

#include "loaded_model.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <filesystem>
#include <string>
#include <vector>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

using namespace audiocpp_backend;

// A one-second tone, interleaved across `channels`. Real audio rather than
// silence so a resample that dropped its input would be visible as a flat
// buffer, not just as a different length.
static engine::runtime::AudioBuffer tone(int sample_rate, int channels,
                                         float seconds) {
    engine::runtime::AudioBuffer buffer;
    buffer.sample_rate = sample_rate;
    buffer.channels = channels;
    const auto frames =
        static_cast<size_t>(static_cast<double>(sample_rate) * seconds);
    buffer.samples.reserve(frames * static_cast<size_t>(channels));
    for (size_t frame = 0; frame < frames; ++frame) {
        const float value = 0.5f * std::sin(2.0f * 3.14159265f * 220.0f *
                                            static_cast<float>(frame) /
                                            static_cast<float>(sample_rate));
        for (int channel = 0; channel < channels; ++channel) {
            buffer.samples.push_back(value);
        }
    }
    return buffer;
}

static float peak(const std::vector<float> &samples) {
    float highest = 0.0f;
    for (const float sample : samples) {
        highest = std::max(highest, std::abs(sample));
    }
    return highest;
}

static std::filesystem::path scratch_dir() {
    const auto dir = std::filesystem::temp_directory_path() / "audiocpp-io-ctest";
    std::filesystem::create_directories(dir);
    return dir;
}

// The I2 fixture. Before the resample this returned a 44.1 kHz buffer, which
// silero_vad and sortformer_diar both reject with a plain runtime_error, which
// the server maps to INTERNAL. A 44.1 kHz WAV is an ordinary upload.
static void test_441k_stereo_is_read_as_16k_mono() {
    const auto path = scratch_dir() / "input-44100-stereo.wav";
    write_audio_file(path.string(), tone(44100, 2, 1.0f));

    const auto audio = read_audio_file(path.string(), 16000);
    check(audio.sample_rate == 16000, "44.1 kHz input is resampled to 16 kHz");
    check(audio.channels == 1, "stereo input is downmixed to mono");
    // Linear resampling lands within a sample or two of the exact ratio.
    const auto frames = static_cast<long long>(audio.samples.size());
    check(frames > 15990 && frames < 16010,
          "one second in stays one second out");
    check(peak(audio.samples) > 0.2f,
          "the resampled buffer still carries the signal");
}

static void test_48k_is_read_as_16k() {
    const auto path = scratch_dir() / "input-48000-mono.wav";
    write_audio_file(path.string(), tone(48000, 1, 0.5f));

    const auto audio = read_audio_file(path.string(), 16000);
    check(audio.sample_rate == 16000, "48 kHz input is resampled to 16 kHz");
    const auto frames = static_cast<long long>(audio.samples.size());
    check(frames > 7990 && frames < 8010, "half a second in, half a second out");
}

// The common case: the upload is already 16 kHz mono, and nothing is resampled.
static void test_16k_mono_passes_through_unchanged() {
    const auto path = scratch_dir() / "input-16000-mono.wav";
    const auto source = tone(16000, 1, 1.0f);
    write_audio_file(path.string(), source);

    const auto audio = read_audio_file(path.string(), 16000);
    check(audio.sample_rate == 16000, "16 kHz stays 16 kHz");
    check(audio.channels == 1, "mono stays mono");
    check(audio.samples.size() == source.samples.size(),
          "a matching rate resamples nothing");
}

// Rate 0 means "give me the file as it is", which is what a source separation
// route needs: demucs and roformer refuse anything but their own 44.1 kHz and
// work on stereo, so the reader must not force them to 16 kHz mono.
static void test_zero_target_keeps_the_native_format() {
    const auto path = scratch_dir() / "input-native.wav";
    write_audio_file(path.string(), tone(44100, 2, 0.25f));

    const auto audio = read_audio_file(path.string(), 0);
    check(audio.sample_rate == 44100, "a zero target keeps the file's rate");
    check(audio.channels == 2, "a zero target keeps the file's channels");
}

static void test_missing_file_is_a_config_error() {
    bool threw_config_error = false;
    try {
        read_audio_file((scratch_dir() / "does-not-exist.wav").string(), 16000);
    } catch (const ConfigError &) {
        threw_config_error = true;
    } catch (const std::exception &) {
        // Any other type maps to INTERNAL, which is what this asserts against.
    }
    check(threw_config_error, "a missing input file is INVALID_ARGUMENT, not INTERNAL");
}

static void test_unreadable_file_is_a_config_error() {
    const auto path = scratch_dir() / "not-a-wav.wav";
    {
        FILE *file = fopen(path.string().c_str(), "wb");
        if (file != nullptr) {
            fputs("this is not a RIFF header", file);
            fclose(file);
        }
    }
    bool threw_config_error = false;
    try {
        read_audio_file(path.string(), 16000);
    } catch (const ConfigError &) {
        threw_config_error = true;
    } catch (const std::exception &) {
    }
    check(threw_config_error, "a non-WAV input is INVALID_ARGUMENT, not INTERNAL");
}

int main() {
    test_441k_stereo_is_read_as_16k_mono();
    test_48k_is_read_as_16k();
    test_16k_mono_passes_through_unchanged();
    test_zero_target_keeps_the_native_format();
    test_missing_file_is_a_config_error();
    test_unreadable_file_is_a_config_error();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all audio_io checks passed\n");
    return 0;
}
