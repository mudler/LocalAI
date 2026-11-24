// SPDX-License-Identifier: MIT
// Model-free binding checks against the pinned upstream header. Linker section
// GC lets the test stub only the native calls used by these entry points.
#include "../cpp/gosd.cpp"
#include <cassert>
#include <stdexcept>

// Fake opaque upstream types. The test owns their layout, the binding only
// sees them through the public header.
struct sd_tensor_t {
    sd_tensor_type_t type;
    std::vector<int64_t> shape;
    std::vector<uint8_t> data;
    bool no_data = false;
};
struct sd_opt {};
struct sd_result {
    std::vector<sd_tensor_t*> images;
    sd_audio_t audio{};
    sd_tensor_t* audio_tensor = nullptr;
    int fps = 0;
};
struct sd_upscaler {};

static int mode = 0;
static int tensors_live = 0;
static int opts_live = 0;
static int results_live = 0;
static int result_frees = 0;

static sd_tensor_t* new_tensor(sd_tensor_type_t type, std::vector<int64_t> shape, size_t bytes) {
    auto* t = new sd_tensor_t{};
    t->type = type;
    t->shape = std::move(shape);
    t->data.assign(bytes, 0);
    ++tensors_live;
    return t;
}

const char* sd_get_last_error(void) { return "test error"; }

sd_tensor_t* sd_tensor_create(sd_tensor_type_t type, size_t rank, const int64_t* shape,
                              const void* data, size_t size) {
    auto* t = new_tensor(type, std::vector<int64_t>(shape, shape + rank), 0);
    t->data.assign((const uint8_t*)data, (const uint8_t*)data + size);
    return t;
}
void sd_tensor_free(sd_tensor_t* t) {
    if (!t) return;
    --tensors_live;
    delete t;
}
size_t sd_tensor_rank(const sd_tensor_t* t) { return t->shape.size(); }
int64_t sd_tensor_dim(const sd_tensor_t* t, size_t axis) { return t->shape[axis]; }
const void* sd_tensor_data(const sd_tensor_t* t) { return t->no_data ? nullptr : t->data.data(); }

sd_img_gen_opt_t* sd_img_gen_opt_create(void) { ++opts_live; return new sd_opt{}; }
sd_vid_gen_opt_t* sd_vid_gen_opt_create(void) { ++opts_live; return new sd_opt{}; }
sd_upscale_opt_t* sd_upscale_opt_create(void) { ++opts_live; return new sd_opt{}; }
void sd_opt_free(sd_opt_t* o) { if (o) { --opts_live; delete o; } }
sd_status_t sd_opt_set_bool(sd_opt_t*, const char*, bool) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_int(sd_opt_t*, const char*, int64_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_float(sd_opt_t*, const char*, double) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_string(sd_opt_t*, const char*, const char*) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_tensor(sd_opt_t*, const char*, const sd_tensor_t*) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_tensors(sd_opt_t*, const char*, const sd_tensor_t* const*, size_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_floats(sd_opt_t*, const char*, const float*, size_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_ints(sd_opt_t*, const char*, const int32_t*, size_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_loras(sd_opt_t*, const char*, const sd_lora_t*, size_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_audios(sd_opt_t*, const char*, const sd_audio_t*, size_t) { return SD_STATUS_OK; }
sd_status_t sd_opt_set_videos(sd_opt_t*, const char*, const sd_video_t*, size_t) { return SD_STATUS_OK; }

static sd_result_t* image_result(int width, int height, int count) {
    auto* r = new sd_result{};
    ++results_live;
    for (int i = 0; i < count; ++i) {
        r->images.push_back(new_tensor(SD_TENSOR_U8, {3, width, height}, (size_t)width * height * 3));
    }
    return r;
}

size_t sd_result_get_image_count(const sd_result_t* r) { return r->images.size(); }
const sd_tensor_t* sd_result_get_image(const sd_result_t* r, size_t i) { return i < r->images.size() ? r->images[i] : nullptr; }
const sd_audio_t* sd_result_get_audio(const sd_result_t* r) { return r->audio_tensor ? &r->audio : nullptr; }
int sd_result_get_fps(const sd_result_t* r) { return r->fps; }
void sd_result_free(sd_result_t* r) {
    if (!r) return;
    ++result_frees;
    --results_live;
    for (auto* t : r->images) sd_tensor_free(t);
    sd_tensor_free(r->audio_tensor);
    delete r;
}

sd_status_t sd_generate_image(sd_ctx_t*, const sd_img_gen_opt_t*, sd_result_t** out) {
    *out = nullptr;
    if (mode == 1) return SD_STATUS_GENERATION_FAILED;
    *out = image_result(2, 2, mode == 2 ? 0 : 2);
    if (mode == 3) (*out)->images[0]->no_data = true;
    return SD_STATUS_OK;
}

sd_status_t sd_generate_video(sd_ctx_t*, const sd_vid_gen_opt_t*, sd_result_t** out) {
    *out = nullptr;
    if (mode == 1) return SD_STATUS_GENERATION_FAILED;
    // Mode 2: no frames. Mode 3: a frame without pixels, which makes muxing
    // fail before launching ffmpeg.
    *out = image_result(2, 2, mode == 2 ? 0 : 1);
    if (mode == 3) (*out)->images[0]->no_data = true;
    (*out)->fps = 12;
    (*out)->audio_tensor = new_tensor(SD_TENSOR_F32, {1, 1}, sizeof(float));
    (*out)->audio = {16000, (*out)->audio_tensor};
    return SD_STATUS_OK;
}

sd_status_t sd_upscale(sd_upscaler_t*, const sd_upscale_opt_t*, sd_result_t** out) {
    *out = nullptr;
    if (mode == 1) return SD_STATUS_GENERATION_FAILED;
    // An exception leaves no result behind: the output pointer stays null.
    if (mode == 4) throw std::runtime_error("inference failure");
    *out = image_result(4, 4, mode == 2 ? 0 : 1);
    if (mode == 3) (*out)->images[0]->shape[1] = 1;
    return SD_STATUS_OK;
}
void sd_upscaler_free(sd_upscaler_t*) {}

int main(int argc, char** argv) {
    assert(argc == 2);
    std::string root = argv[1];
    std::string input = root + "/input.png";
    std::string output = root + "/output.png";
    std::string missing = root + "/missing/output.png";
    uint8_t pixels[12]{};
    assert(stbi_write_png(input.c_str(), 2, 2, 3, pixels, 0));

    for (mode = 0; mode <= 4; ++mode) {
        gosd_img_gen_params_t* params = sd_img_gen_params_new();
        sd_img_gen_params_set_dimensions(params, 2, 2);
        int before = result_frees;
        int rc = gen_image(params, 1, const_cast<char*>((mode == 4 ? missing : output).c_str()),
                           1, nullptr, 0, nullptr, nullptr, 0);
        assert((rc == 0) == (mode == 0));
        assert(result_frees - before == (mode == 1 ? 0 : 1));
        assert(tensors_live == 0 && opts_live == 0 && results_live == 0);
    }
    for (mode = 1; mode <= 3; ++mode) {
        gosd_vid_gen_params_t* params = sd_vid_gen_params_new();
        int before = result_frees;
        assert(gen_video(params, 1, const_cast<char*>(output.c_str()), 1, 24,
                         nullptr, nullptr, nullptr, nullptr, nullptr) != 0);
        assert(result_frees - before == (mode == 1 ? 0 : 1));
        assert(tensors_live == 0 && opts_live == 0 && results_live == 0);
    }
    gosd_upscaler handle;
    for (mode = 0; mode <= 5; ++mode) {
        int before = result_frees;
        // Mode 5 is a good result that cannot be written.
        int rc = gosd_upscaler_run(&handle, input.c_str(),
                                   (mode == 5 ? missing : output).c_str(), 2);
        const int expected[] = {0, 3, 4, 4, 3, 5};
        assert(rc == expected[mode]);
        assert(result_frees - before == (mode == 1 || mode == 4 ? 0 : 1));
        assert(tensors_live == 0 && opts_live == 0 && results_live == 0);
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
        gosd_audio audio{32000, 2, frames, ramp};
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
        gosd_audio back{};
        assert(load_wav_audio(wav, &back));
        assert(back.channels == 2 && back.sample_rate == 32000 && back.sample_count == frames);
        assert(memcmp(back.data, ramp, sizeof(ramp)) == 0);
        free(back.data);
        unlink(wav);
    }
    // Mux length: 56 frames at 24 fps must give the exact video duration and an
    // apad filter for the same value, so the audio is padded and not cut short.
    {
        char dur[32], apad[48];
        format_mux_duration(56, 24, dur, sizeof(dur), apad, sizeof(apad));
        assert(strcmp(dur, "2.333333") == 0);
        assert(strcmp(apad, "apad=whole_dur=2.333333") == 0);
        format_mux_duration(24, 24, dur, sizeof(dur), apad, sizeof(apad));
        assert(strcmp(dur, "1.000000") == 0);
    }
    puts("result API binding checks passed");
}
