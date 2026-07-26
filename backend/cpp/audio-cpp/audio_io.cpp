#include "audio_io.h"

#include "loaded_model.h"

#include "engine/framework/audio/wav_reader.h"
#include "engine/framework/audio/wav_writer.h"

#include <filesystem>
#include <utility>

namespace audiocpp_backend {

engine::runtime::AudioBuffer read_audio_file(const std::string &path) {
    if (path.empty()) {
        throw ConfigError("audio-cpp: no input audio path was supplied");
    }
    std::error_code ec;
    if (!std::filesystem::exists(std::filesystem::path(path), ec)) {
        throw ConfigError("audio-cpp: input audio does not exist: " + path);
    }
    engine::runtime::AudioBuffer buffer;
    try {
        const engine::audio::WavData wav =
            engine::audio::read_wav_f32(std::filesystem::path(path));
        buffer.sample_rate = wav.sample_rate;
        // AudioBuffer's own default is 1, and a reader that reports 0 channels
        // still gave us an interleaving of one.
        buffer.channels = wav.channels > 0 ? wav.channels : 1;
        buffer.samples = wav.samples;
    } catch (const std::exception &err) {
        throw ConfigError("audio-cpp: cannot read " + path +
                          " as WAV: " + err.what());
    }
    if (buffer.sample_rate <= 0) {
        throw ConfigError("audio-cpp: " + path +
                          " declares a non-positive sample rate; every "
                          "timestamp derived from it would be zero");
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
