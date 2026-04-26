#include "sam3.h"
#include "gosam3.h"

#include <cstdio>
#include <cstring>
#include <memory>
#include <vector>

#define STB_IMAGE_WRITE_IMPLEMENTATION
#define STB_IMAGE_WRITE_STATIC
#include "stb_image_write.h"

// Static state
static std::shared_ptr<sam3_model> g_model;
static sam3_state_ptr g_state;
static sam3_result g_result;
static std::vector<std::vector<unsigned char>> g_mask_pngs;

// Callback for stbi_write_png_to_mem via stbi_write_png_to_func
static void png_write_callback(void *context, void *data, int size) {
    auto *buf = static_cast<std::vector<unsigned char>*>(context);
    auto *bytes = static_cast<unsigned char*>(data);
    buf->insert(buf->end(), bytes, bytes + size);
}

// Encode all masks as PNGs after segmentation
static void encode_masks_as_png() {
    g_mask_pngs.clear();
    g_mask_pngs.resize(g_result.detections.size());

    for (size_t i = 0; i < g_result.detections.size(); i++) {
        const auto &mask = g_result.detections[i].mask;
        if (mask.width > 0 && mask.height > 0 && !mask.data.empty()) {
            stbi_write_png_to_func(png_write_callback, &g_mask_pngs[i],
                                   mask.width, mask.height, 1,
                                   mask.data.data(), mask.width);
        }
    }
}

extern "C" {

int sam3_cpp_load_model(const char *model_path, int threads) {
    sam3_params params;
    params.model_path = model_path;
    params.n_threads = threads;
    params.use_gpu = true;

    g_model = sam3_load_model(params);
    if (!g_model) {
        fprintf(stderr, "[sam3-cpp] Failed to load model: %s\n", model_path);
        return 1;
    }

    g_state = sam3_create_state(*g_model, params);
    if (!g_state) {
        fprintf(stderr, "[sam3-cpp] Failed to create state\n");
        g_model.reset();
        return 2;
    }

    fprintf(stderr, "[sam3-cpp] Model loaded: %s (threads=%d)\n", model_path, threads);
    return 0;
}

int sam3_cpp_encode_image(const char *image_path) {
    if (!g_model || !g_state) {
        fprintf(stderr, "[sam3-cpp] Model not loaded\n");
        return 1;
    }

    sam3_image img = sam3_load_image(image_path);
    if (img.data.empty()) {
        fprintf(stderr, "[sam3-cpp] Failed to load image: %s\n", image_path);
        return 2;
    }

    if (!sam3_encode_image(*g_state, *g_model, img)) {
        fprintf(stderr, "[sam3-cpp] Failed to encode image\n");
        return 3;
    }

    return 0;
}

int sam3_cpp_segment_pvs(float *points, int n_point_triples,
                         float *boxes, int n_box_quads,
                         float threshold) {
    if (!g_model || !g_state) {
        return -1;
    }

    sam3_pvs_params pvs_params;

    // Parse points: each triple is [x, y, label]
    for (int i = 0; i < n_point_triples; i++) {
        float x = points[i * 3];
        float y = points[i * 3 + 1];
        float label = points[i * 3 + 2];
        sam3_point pt = {x, y};
        if (label > 0.5f) {
            pvs_params.pos_points.push_back(pt);
        } else {
            pvs_params.neg_points.push_back(pt);
        }
    }

    // Parse boxes: each quad is [x1, y1, x2, y2], use only first box
    if (n_box_quads > 0) {
        pvs_params.box = {boxes[0], boxes[1], boxes[2], boxes[3]};
        pvs_params.use_box = true;
    }

    g_result = sam3_segment_pvs(*g_state, *g_model, pvs_params);
    encode_masks_as_png();

    return static_cast<int>(g_result.detections.size());
}

int sam3_cpp_segment_pcs(const char *text_prompt, float threshold) {
    if (!g_model || !g_state) {
        return -1;
    }

    // PCS mode requires SAM 3 (full model with text encoder)
    if (sam3_is_visual_only(*g_model) ||
        sam3_get_model_type(*g_model) != SAM3_MODEL_SAM3) {
        fprintf(stderr, "[sam3-cpp] PCS mode requires full SAM 3 model\n");
        return -1;
    }

    sam3_pcs_params pcs_params;
    pcs_params.text_prompt = text_prompt;
    pcs_params.score_threshold = threshold > 0 ? threshold : 0.5f;

    g_result = sam3_segment_pcs(*g_state, *g_model, pcs_params);
    encode_masks_as_png();

    return static_cast<int>(g_result.detections.size());
}

int sam3_cpp_get_n_detections(void) {
    return static_cast<int>(g_result.detections.size());
}

float sam3_cpp_get_detection_x(int i) {
    if (i < 0 || i >= static_cast<int>(g_result.detections.size())) return 0;
    return g_result.detections[i].box.x0;
}

float sam3_cpp_get_detection_y(int i) {
    if (i < 0 || i >= static_cast<int>(g_result.detections.size())) return 0;
    return g_result.detections[i].box.y0;
}

float sam3_cpp_get_detection_w(int i) {
    if (i < 0 || i >= static_cast<int>(g_result.detections.size())) return 0;
    const auto &box = g_result.detections[i].box;
    return box.x1 - box.x0;
}

float sam3_cpp_get_detection_h(int i) {
    if (i < 0 || i >= static_cast<int>(g_result.detections.size())) return 0;
    const auto &box = g_result.detections[i].box;
    return box.y1 - box.y0;
}

float sam3_cpp_get_detection_score(int i) {
    if (i < 0 || i >= static_cast<int>(g_result.detections.size())) return 0;
    return g_result.detections[i].score;
}

int sam3_cpp_get_detection_mask_png(int i, unsigned char *buf, int buf_size) {
    if (i < 0 || i >= static_cast<int>(g_mask_pngs.size())) return 0;

    const auto &png = g_mask_pngs[i];
    int size = static_cast<int>(png.size());

    if (buf == nullptr) {
        return size;
    }

    int to_copy = size < buf_size ? size : buf_size;
    memcpy(buf, png.data(), to_copy);
    return to_copy;
}

void sam3_cpp_free_results(void) {
    g_result.detections.clear();
    g_mask_pngs.clear();
}

} // extern "C"
