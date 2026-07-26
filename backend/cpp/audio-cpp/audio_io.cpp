#include "audio_io.h"

#include "loaded_model.h"

#include "engine/framework/audio/conversion.h"
#include "engine/framework/audio/wav_reader.h"
#include "engine/framework/audio/wav_writer.h"

#include <filesystem>
#include <string>
#include <utility>

namespace audiocpp_backend {

engine::runtime::AudioBuffer read_audio_file(const std::string &path,
                                             int target_sample_rate) {
    if (path.empty()) {
        throw ConfigError("audio-cpp: no input audio path was supplied");
    }
    std::error_code ec;
    const bool present = std::filesystem::exists(std::filesystem::path(path), ec);
    if (ec) {
        // exists() returning false with ec set does NOT mean the file is
        // absent, it means the question could not be answered: most often a
        // parent directory is not searchable. Reporting that as "does not
        // exist" sends the operator after the file when the fault is the
        // permissions on the directory above it.
        throw ConfigError("audio-cpp: cannot stat input audio " + path + ": " +
                          ec.message());
    }
    if (!present) {
        throw ConfigError("audio-cpp: input audio does not exist: " + path);
    }
    engine::audio::WavData wav;
    try {
        wav = engine::audio::read_wav_f32(std::filesystem::path(path));
    } catch (const std::exception &err) {
        throw ConfigError("audio-cpp: cannot read " + path +
                          " as WAV: " + err.what());
    }
    if (wav.sample_rate <= 0) {
        throw ConfigError("audio-cpp: " + path +
                          " declares a non-positive sample rate; every "
                          "timestamp derived from it would be zero");
    }
    // AudioBuffer's own default is 1, and a reader that reports 0 channels
    // still gave us an interleaving of one. Normalised before the conversion
    // below rather than after, because mixdown_interleaved_to_mono_average
    // throws on a non-positive channel count.
    if (wav.channels <= 0) {
        wav.channels = 1;
    }

    engine::runtime::AudioBuffer buffer;
    if (target_sample_rate <= 0) {
        buffer.sample_rate = wav.sample_rate;
        buffer.channels = wav.channels;
        buffer.samples = std::move(wav.samples);
        return buffer;
    }

    buffer.sample_rate = target_sample_rate;
    buffer.channels = 1;
    try {
        // A no-op copy when the rates already match, so the common 16 kHz
        // upload pays only the mono mixdown it would have paid inside the
        // family anyway.
        buffer.samples =
            engine::audio::convert_wav_to_mono_linear_resampled(wav, target_sample_rate);
    } catch (const std::exception &err) {
        // ConfigError, so this is INVALID_ARGUMENT rather than INTERNAL. What
        // reaches here is a malformed input: a sample count that is not a whole
        // number of frames is the realistic one, and it is the uploader's file
        // that is truncated, not this backend that is broken.
        throw ConfigError("audio-cpp: cannot resample " + path + " from " +
                          std::to_string(wav.sample_rate) + " Hz to " +
                          std::to_string(target_sample_rate) +
                          " Hz: " + err.what());
    }
    return buffer;
}

void write_audio_file(const std::string &path,
                      const engine::runtime::AudioBuffer &audio) {
    if (path.empty()) {
        throw ConfigError("audio-cpp: no output path was supplied");
    }
    const std::filesystem::path destination(path);
    if (destination.has_parent_path()) {
        // Best effort: a failure here shows up as a write failure below, with a
        // message naming the file the caller actually asked for.
        std::error_code ec;
        std::filesystem::create_directories(destination.parent_path(), ec);
    }
    try {
        engine::audio::write_pcm16_wav(destination, audio.sample_rate,
                                       audio.channels > 0 ? audio.channels : 1,
                                       audio.samples);
    } catch (const std::exception &err) {
        throw ConfigError("audio-cpp: cannot write " + path + ": " + err.what());
    }
}

engine::runtime::AudioBuffer buffer_from_mono(std::vector<float> samples,
                                              int sample_rate) {
    engine::runtime::AudioBuffer buffer;
    buffer.sample_rate = sample_rate;
    buffer.channels = 1;
    buffer.samples = std::move(samples);
    return buffer;
}

} // namespace audiocpp_backend
