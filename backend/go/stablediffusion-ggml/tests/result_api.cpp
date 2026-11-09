// SPDX-License-Identifier: MIT
// Model-free binding checks against the pinned upstream header. Linker section
// GC lets the test stub only the native calls used by these entry points.
#include "../cpp/gosd.cpp"
#include <cassert>
#include <stdexcept>

static int mode = 0;
static int image_frees = 0;
static int video_frees = 0;

static sd_image_generation_result_t* image_result(int width, int height, int count) {
    auto* result = new sd_image_generation_result_t{};
    result->count = count;
    result->items = new sd_image_result_t[count]{};
    for (int i = 0; i < count; ++i) {
        result->items[i].image = {uint32_t(width), uint32_t(height), 3,
                                 new uint8_t[width * height * 3]{}};
        result->items[i].generation_info = new char[3]{'{', '}', 0};
    }
    return result;
}

void free_sd_image_generation_result(sd_image_generation_result_t* result) {
    if (!result) return;
    ++image_frees;
    for (int i = 0; i < result->count; ++i) {
        delete[] result->items[i].image.data;
        delete[] result->items[i].generation_info;
    }
    delete[] result->items;
    delete result;
}

void free_sd_video_generation_result(sd_video_generation_result_t* result) {
    if (!result) return;
    ++video_frees;
    for (int i = 0; i < result->frame_count; ++i) delete[] result->frames[i].data;
    delete[] result->frames;
    if (result->audio) {
        delete[] result->audio->data;
        delete result->audio;
    }
    delete[] result->generation_info;
    delete result;
}

char* sd_ctx_params_to_str(const sd_ctx_params_t*) { return const_cast<char*>("test"); }
char* sd_img_gen_params_to_str(const sd_img_gen_params_t*) { return const_cast<char*>("test"); }
void free_upscaler_ctx(upscaler_ctx_t*) {}

bool generate_image(sd_ctx_t*, const sd_img_gen_params_t*, sd_image_generation_result_t** out) {
    *out = nullptr;
    if (mode == 1) return false;
    *out = image_result(2, 2, mode == 2 ? 0 : 2);
    if (mode == 3) {
        delete[] (*out)->items[0].image.data;
        (*out)->items[0].image.data = nullptr;
    }
    return true;
}

bool generate_video(sd_ctx_t*, const sd_vid_gen_params_t*, sd_video_generation_result_t** out) {
    *out = nullptr;
    if (mode == 1) return false;
    *out = new sd_video_generation_result_t{};
    (*out)->fps = 12;
    (*out)->audio = new sd_audio_t{16000, 1, 1, new float[1]{}};
    (*out)->generation_info = new char[3]{'{', '}', 0};
    // A frame without pixels makes muxing fail before launching ffmpeg.
    if (mode == 3) {
        (*out)->frame_count = 1;
        (*out)->frames = new sd_image_t[1]{};
    }
    return true;
}

bool upscale(upscaler_ctx_t*, sd_image_t image, uint32_t scale, sd_image_generation_result_t** out) {
    *out = nullptr;
    if (mode == 1) return false;
    *out = image_result(image.width * scale, image.height * scale, mode == 2 ? 0 : 1);
    if (mode == 3) (*out)->items[0].image.width = 1;
    if (mode == 4) throw std::runtime_error("inference failure after allocation");
    return true;
}

int main(int argc, char** argv) {
    assert(argc == 2);
    std::string root = argv[1];
    std::string input = root + "/input.png";
    std::string output = root + "/output.png";
    std::string missing = root + "/missing/output.png";
    uint8_t pixels[12]{};
    assert(stbi_write_png(input.c_str(), 2, 2, 3, pixels, 0));

    for (mode = 0; mode <= 4; ++mode) {
        auto* params = static_cast<sd_img_gen_params_t*>(calloc(1, sizeof(sd_img_gen_params_t)));
        params->width = params->height = 2;
        int before = image_frees;
        int rc = gen_image(params, 1, const_cast<char*>((mode == 4 ? missing : output).c_str()),
                           1, nullptr, 0, nullptr, nullptr, 0);
        assert((rc == 0) == (mode == 0));
        assert(image_frees - before == (mode == 1 ? 0 : 1));
    }
    for (mode = 1; mode <= 3; ++mode) {
        auto* params = static_cast<sd_vid_gen_params_t*>(calloc(1, sizeof(sd_vid_gen_params_t)));
        int before = video_frees;
        assert(gen_video(params, 1, const_cast<char*>(output.c_str()), 1, 24,
                         nullptr, nullptr, nullptr, nullptr, nullptr) != 0);
        assert(video_frees - before == (mode == 1 ? 0 : 1));
    }
    gosd_upscaler handle;
    for (mode = 0; mode <= 5; ++mode) {
        int before = image_frees;
        int rc = gosd_upscaler_run(&handle, input.c_str(),
                                   (mode == 5 ? missing : output).c_str(), 2);
        const int expected[] = {0, 3, 4, 4, 3, 5};
        assert(rc == expected[mode]);
        assert(image_frees - before == (mode == 1 ? 0 : 1));
    }
    // Audio layout: sd.cpp hands out interleaved samples (data[frame * channels
    // + channel]). A stereo ramp with distinct channels must reach the WAV file
    // unchanged, and reading that WAV back must give the same buffer.
    {
        const uint64_t frames = 5;
        float ramp[10];
        for (uint64_t i = 0; i < frames; ++i) {
            ramp[i * 2] = 0.1f * (i + 1);       // left rises
            ramp[i * 2 + 1] = -0.1f * (i + 1);  // right falls
        }
        sd_audio_t audio{32000, 2, frames, ramp};
        char wav[4096] = {0};
        snprintf(wav, sizeof(wav), "%s/audio-layout.wav", root.c_str());
        // write_interleaved_float_wav stages into TMPDIR; use it, then inspect.
        setenv("TMPDIR", root.c_str(), 1);
        assert(write_interleaved_float_wav(&audio, wav, sizeof(wav)) == 0);
        FILE* f = fopen(wav, "rb");
        assert(f);
        uint8_t header[44];
        float body[10];
        assert(fread(header, 1, 44, f) == 44);
        assert(fread(body, sizeof(float), 10, f) == 10);
        fclose(f);
        assert(memcmp(body, ramp, sizeof(ramp)) == 0);
        sd_audio_t back{};
        assert(load_wav_audio(wav, &back));
        assert(back.channels == 2 && back.sample_rate == 32000 && back.sample_count == frames);
        assert(memcmp(back.data, ramp, sizeof(ramp)) == 0);
        free(back.data);
        unlink(wav);
    }
    puts("result API binding checks passed");
}
