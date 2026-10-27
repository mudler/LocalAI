// SPDX-License-Identifier: MIT
// Unit tests for the bounded media preparation helpers (multimodal_embedding.h).
//
//   g++ -std=c++17 multimodal_embedding_test.cpp -o t && ./t
//
// The inputs are synthetic container headers built in memory. The point is that
// every bound is decided from the header alone, before a decoder allocates.
#include <cassert>
#include <iostream>
#include <string>
#include <vector>

#include "multimodal_embedding.h"

using namespace llama_grpc::mmembed;

static int failures = 0;
static void check(bool ok, const std::string & name) {
    if (!ok) {
        std::cerr << "FAIL " << name << "\n";
        failures++;
    }
}

static void put32be(std::vector<uint8_t> & v, uint32_t x) { for (int s = 24; s >= 0; s -= 8) v.push_back((x >> s) & 0xff); }
static void put32le(std::vector<uint8_t> & v, uint32_t x) { for (int s = 0; s < 32; s += 8) v.push_back((x >> s) & 0xff); }
static void put16le(std::vector<uint8_t> & v, uint32_t x) { v.push_back(x & 0xff); v.push_back((x >> 8) & 0xff); }
static void puts_(std::vector<uint8_t> & v, const char * s) { while (*s) v.push_back((uint8_t) *s++); }

static std::vector<uint8_t> png(uint32_t w, uint32_t h) {
    std::vector<uint8_t> v = {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'};
    put32be(v, 13);
    puts_(v, "IHDR");
    put32be(v, w);
    put32be(v, h);
    for (int i = 0; i < 5; i++) v.push_back(0);
    return v;
}

static std::vector<uint8_t> jpeg(uint32_t w, uint32_t h, bool with_comment_first) {
    std::vector<uint8_t> v = {0xff, 0xd8};
    if (with_comment_first) {
        v.insert(v.end(), {0xff, 0xfe, 0x00, 0x06, 'a', 'b', 'c', 'd'});
    }
    v.insert(v.end(), {0xff, 0xc2, 0x00, 0x11, 0x08, (uint8_t) (h >> 8), (uint8_t) h, (uint8_t) (w >> 8), (uint8_t) w, 0x03});
    for (int i = 0; i < 9; i++) v.push_back(0);
    return v;
}

static std::vector<uint8_t> wav(uint32_t rate, uint32_t channels, uint32_t bits, uint32_t frames, uint32_t tag = 1) {
    std::vector<uint8_t> v;
    puts_(v, "RIFF");
    put32le(v, 0);
    puts_(v, "WAVE");
    puts_(v, "fmt ");
    put32le(v, 16);
    put16le(v, tag);
    put16le(v, channels);
    put32le(v, rate);
    put32le(v, rate * channels * bits / 8);
    put16le(v, channels * bits / 8);
    put16le(v, bits);
    puts_(v, "data");
    const uint32_t bytes = frames * channels * bits / 8;
    put32le(v, bytes);
    v.resize(v.size() + bytes, 0);
    return v;
}

static std::vector<uint8_t> flac(uint32_t rate, uint32_t channels, uint64_t total) {
    std::vector<uint8_t> v;
    puts_(v, "fLaC");
    v.insert(v.end(), {0x00, 0x00, 0x00, 0x22});
    v.insert(v.end(), {0x10, 0x00, 0x10, 0x00, 0, 0, 0, 0, 0, 0});
    v.push_back((rate >> 12) & 0xff);
    v.push_back((rate >> 4) & 0xff);
    v.push_back(((rate & 0xf) << 4) | (((channels - 1) & 7) << 1) | 0);
    v.push_back((uint8_t) (0x0f & (total >> 32)) | 0x00);
    put32be(v, (uint32_t) total);
    for (int i = 0; i < 16; i++) v.push_back(0);
    return v;
}

// MPEG-1 Layer III, 128 kbit/s, 44.1 kHz, stereo: 417 or 418 bytes per frame.
static std::vector<uint8_t> mp3(int frames) {
    std::vector<uint8_t> v;
    for (int i = 0; i < frames; i++) {
        v.insert(v.end(), {0xff, 0xfb, 0x90, 0x00});
        v.resize(v.size() + 413, 0);
    }
    return v;
}

static Budget full() {
    Budget b;
    b.frames = 120; b.pixels = 64'000'000; b.audio_allocation_bytes = 64u << 20; b.source_duration_ms = 60'000;
    b.temporary_bytes = 256u << 20; b.preparation_ms = 60'000; b.compressed_bytes = 32u << 20; b.text_bytes = 1u << 20; b.source_parts = 128;
    return b;
}

int main() {
    {
        ImageInfo i;
        auto p = png(640, 480);
        check(probe_image(p.data(), p.size(), i).ok() && i.width == 640 && i.height == 480 && i.format == "png", "png size");
        auto j = jpeg(300, 200, false);
        check(probe_image(j.data(), j.size(), i).ok() && i.width == 300 && i.height == 200 && i.format == "jpeg", "jpeg size");
        auto jc = jpeg(300, 200, true);
        check(probe_image(jc.data(), jc.size(), i).ok() && i.width == 300, "jpeg size after a comment segment");
        auto bad = png(0, 10);
        check(probe_image(bad.data(), bad.size(), i).reason == Reason::InvalidInput, "png zero width");
        std::vector<uint8_t> gif = {'G', 'I', 'F', '8', '9', 'a', 0, 0, 0, 0, 0, 0};
        check(probe_image(gif.data(), gif.size(), i).reason == Reason::UnsupportedFormat, "gif unsupported");
        std::vector<uint8_t> trunc = {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0};
        check(probe_image(trunc.data(), trunc.size(), i).reason == Reason::InvalidInput, "truncated png");
    }
    {
        // A tiny file that claims a huge image is refused from the header alone.
        Plan plan(full());
        ImageInfo i;
        auto bomb = png(60000, 60000);
        check(plan.add_image(bomb.data(), bomb.size(), i).reason == Reason::LimitExceeded, "decompression bomb refused");
        check(plan.consumption().pixels == 0 && plan.remaining().source_parts == 128, "refused image charges nothing");
        auto big = png(4001, 4000); // 16,004,000 pixels: over the per-image bound
        check(plan.add_image(big.data(), big.size(), i).reason == Reason::LimitExceeded, "per-image pixel bound");
        auto ok = png(4000, 4000);
        check(plan.add_image(ok.data(), ok.size(), i).ok(), "image at the bound");
        check(plan.consumption().pixels == 16'000'000 && plan.consumption().compressed_bytes == ok.size(), "image charged");
    }
    {
        // Request-wide pixel allowance shrinks across images.
        Budget b = full();
        b.pixels = 1'500'000;
        Plan plan(b);
        ImageInfo i;
        auto one = png(1000, 1000);
        check(plan.add_image(one.data(), one.size(), i).ok(), "first image fits");
        check(plan.add_image(one.data(), one.size(), i).reason == Reason::LimitExceeded, "second image exceeds the remaining pixels");
    }
    {
        AudioInfo a;
        auto w = wav(16000, 1, 16, 32000);
        check(probe_audio(w.data(), w.size(), 16000, a).ok() && a.duration_ms == 2000 && a.out_frames == 32000, "wav 2 s at the model rate");
        auto w2 = wav(48000, 2, 16, 48000);
        check(probe_audio(w2.data(), w2.size(), 16000, a).ok() && a.duration_ms == 1000 && a.out_frames == 16000 && a.channels == 2, "wav resampled length");
        auto bad = wav(16000, 3, 16, 100);
        check(probe_audio(bad.data(), bad.size(), 16000, a).reason == Reason::LimitExceeded, "three channels refused");
        auto fast = wav(200000, 1, 16, 100);
        check(probe_audio(fast.data(), fast.size(), 16000, a).reason == Reason::LimitExceeded, "sample rate above the bound");
        auto adpcm = wav(16000, 1, 4, 100, 2);
        check(probe_audio(adpcm.data(), adpcm.size(), 16000, a).reason == Reason::UnsupportedFormat, "adpcm unsupported");
        check(probe_audio(w.data(), w.size(), 0, a).reason == Reason::UnsupportedFormat, "model without audio input");
        // A header that claims more data than the file holds is bounded by the file.
        auto lie = wav(16000, 1, 16, 10);
        lie[40] = 0xff; lie[41] = 0xff; lie[42] = 0xff; lie[43] = 0xff;
        check(probe_audio(lie.data(), lie.size(), 16000, a).ok() && a.out_frames == 10, "data size above the file is clamped");
    }
    {
        AudioInfo a;
        auto f = flac(44100, 2, 88200);
        check(probe_audio(f.data(), f.size(), 16000, a).ok() && a.sample_rate == 44100 && a.channels == 2 && a.duration_ms == 2000, "flac streaminfo");
        auto unknown = flac(44100, 2, 0);
        check(probe_audio(unknown.data(), unknown.size(), 16000, a).reason == Reason::UnsupportedFormat, "flac without sample count");
        auto m = mp3(40);
        check(probe_audio(m.data(), m.size(), 16000, a).ok() && a.sample_rate == 44100 && a.channels == 2, "mp3 frames");
        // 40 frames of 1152 samples at 44.1 kHz is 1.044 s.
        check(a.duration_ms >= 1040 && a.duration_ms <= 1050, "mp3 duration from frames, got " + std::to_string(a.duration_ms));
        std::vector<uint8_t> noise(4096, 0x00);
        noise[0] = 0xff; noise[1] = 0xfb; noise[2] = 0x90;
        check(probe_audio(noise.data(), noise.size(), 16000, a).reason == Reason::InvalidInput, "an isolated sync word is not audio");
        std::vector<uint8_t> junk = {'O', 'g', 'g', 'S', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0};
        check(probe_audio(junk.data(), junk.size(), 16000, a).reason == Reason::UnsupportedFormat, "ogg unsupported");
    }
    {
        // Duration and allocation bounds are decided before decoding.
        Plan plan(full());
        AudioInfo a;
        auto long_clip = wav(16000, 1, 16, 16000 * 31);
        check(plan.add_audio(long_clip.data(), long_clip.size(), 16000, a).reason == Reason::LimitExceeded, "31 s clip refused");
        auto clip = wav(16000, 1, 16, 16000 * 30);
        check(plan.add_audio(clip.data(), clip.size(), 16000, a).ok(), "30 s clip accepted");
        check(plan.consumption().source_duration_ms == 30000 && plan.consumption().audio_allocation_bytes == 16000u * 30 * 4, "audio charged");
        check(plan.add_audio(clip.data(), clip.size(), 16000, a).ok(), "second 30 s clip uses the rest of 60 s");
        check(plan.add_audio(clip.data(), clip.size(), 16000, a).reason == Reason::LimitExceeded, "no duration left");

        Budget small = full();
        small.audio_allocation_bytes = 1000;
        Plan tight(small);
        auto two = wav(16000, 1, 16, 32000);
        check(tight.add_audio(two.data(), two.size(), 16000, a).reason == Reason::LimitExceeded, "audio allocation allowance");
    }
    {
        // Zero is exhausted, not unlimited.
        Budget zero;
        Plan plan(zero);
        check(plan.add_text(0).reason == Reason::LimitExceeded, "zero source parts is exhausted");
        Budget one = full();
        one.text_bytes = 3;
        Plan t(one);
        check(t.add_text(3).ok() && t.add_text(1).reason == Reason::LimitExceeded, "text bytes allowance");
        Budget parts = full();
        parts.source_parts = 1;
        Plan p(parts);
        ImageInfo i;
        auto im = png(10, 10);
        check(p.add_image(im.data(), im.size(), i).ok() && p.add_image(im.data(), im.size(), i).reason == Reason::LimitExceeded, "source part allowance");
        Budget bytes = full();
        bytes.compressed_bytes = 10;
        Plan c(bytes);
        check(c.add_image(im.data(), im.size(), i).reason == Reason::LimitExceeded, "compressed byte allowance");
    }
    {
        Budget b = full();
        b.preparation_ms = 100;
        Plan plan(b);
        check(plan.check_time(100).ok(), "time at the allowance");
        check(plan.check_time(101).reason == Reason::Deadline, "time over the allowance");
    }
    {
        check(contains_marker("a <__media__> b", "<__media__>"), "marker found");
        check(!contains_marker("plain", "<__media__>"), "marker absent");
        check(!contains_marker("anything", ""), "empty marker never matches");
        check(reason_prefix(Reason::LimitExceeded) == "limit_exceeded", "limit prefix");
        check(reason_prefix(Reason::UnsupportedFormat) == "unsupported_media_format", "format prefix");
    }
    if (failures) {
        std::cerr << failures << " failure(s)\n";
        return 1;
    }
    std::cout << "ok\n";
    return 0;
}
