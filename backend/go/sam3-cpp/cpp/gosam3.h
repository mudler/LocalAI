#ifndef GOSAM3_H
#define GOSAM3_H

#ifdef __cplusplus
extern "C" {
#endif

// Load model from file. Returns 0 on success, non-zero on failure.
int sam3_cpp_load_model(const char *model_path, int threads);

// Encode an image from file path. Must be called before segmentation.
// Returns 0 on success.
int sam3_cpp_encode_image(const char *image_path);

// Segment with point/box prompts (PVS mode).
// points: flat array of [x, y, label] triples (label: 1=positive, 0=negative)
// boxes: flat array of [x1, y1, x2, y2] quads
// Returns number of detections, or -1 on error.
int sam3_cpp_segment_pvs(float *points, int n_point_triples,
                         float *boxes, int n_box_quads,
                         float threshold);

// Segment with text prompt (PCS mode, SAM 3 only).
// Returns number of detections, or -1 on error.
int sam3_cpp_segment_pcs(const char *text_prompt, float threshold);

// Access detection results (valid after a segment call).
int sam3_cpp_get_n_detections(void);

// Get bounding box for detection i (as x, y, width, height).
float sam3_cpp_get_detection_x(int i);
float sam3_cpp_get_detection_y(int i);
float sam3_cpp_get_detection_w(int i);
float sam3_cpp_get_detection_h(int i);

// Get confidence score for detection i.
float sam3_cpp_get_detection_score(int i);

// Get mask as PNG-encoded bytes.
// If buf is NULL, returns the required buffer size.
// Otherwise writes up to buf_size bytes and returns bytes written.
int sam3_cpp_get_detection_mask_png(int i, unsigned char *buf, int buf_size);

// Free current detection results.
void sam3_cpp_free_results(void);

#ifdef __cplusplus
}
#endif

#endif // GOSAM3_H
