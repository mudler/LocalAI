package main

// gorfdetrcpp.go - gRPC handlers (Load, Detect) for the rfdetr-cpp backend.
//
// Embeds base.SingleThread to default unimplemented RPCs to "not supported"
// while we only implement object detection.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Default upper bound on detections returned per image. RF-DETR's decoder
// queries are limited to a few hundred; 300 is a safe ceiling.
const defaultTopK = 300

// rfdetr_handle_t is a uintptr-typed opaque handle (see include/rfdetr_capi.h).
var (
	// rfdetr_capi_load(const char* model_path, int n_threads, rfdetr_handle_t* out_handle) -> int
	CapiLoad func(modelPath string, nThreads int32, outHandle *uintptr) int32
	// rfdetr_capi_unload(rfdetr_handle_t handle) -> int
	CapiUnload func(handle uintptr) int32
	// rfdetr_capi_detect_path(handle, image_path, threshold, top_k, out_json) -> int
	CapiDetectPath func(handle uintptr, imagePath string, threshold float32, topK uint32, outJSON *uintptr) int32
	// rfdetr_capi_detect_buffer(handle, bytes, len, threshold, top_k, out_json) -> int
	CapiDetectBuffer func(handle uintptr, bytes uintptr, length uintptr, threshold float32, topK uint32, outJSON *uintptr) int32
	// rfdetr_capi_free_string(char* s)
	CapiFreeString func(s uintptr)
	// rfdetr_capi_get_n_detections(handle) -> int
	CapiGetNDetections func(handle uintptr) int32
	// rfdetr_capi_get_detection_class_id(handle, i) -> int
	CapiGetDetectionClassID func(handle uintptr, i int32) int32
	// rfdetr_capi_get_detection_box(handle, i, out_xyxy[4]) -> int (0 on success)
	CapiGetDetectionBox func(handle uintptr, i int32, outXYXY uintptr) int32
	// rfdetr_capi_get_detection_score(handle, i) -> float
	CapiGetDetectionScore func(handle uintptr, i int32) float32
	// rfdetr_capi_get_detection_class_name(handle, i, buf, buf_size) -> int (needed/written; two-call sizing)
	CapiGetDetectionClassName func(handle uintptr, i int32, buf uintptr, bufSize int32) int32
	// rfdetr_capi_get_detection_mask_png(handle, i, buf, buf_size) -> int (needed/written; 0 means no mask)
	CapiGetDetectionMaskPNG func(handle uintptr, i int32, buf uintptr, bufSize int32) int32
)

type RFDetrCpp struct {
	base.SingleThread
	handle uintptr
}

// Load loads the GGUF model at opts.ModelFile (joined with opts.ModelPath if relative)
// and stores the handle for later Detect calls.
func (r *RFDetrCpp) Load(opts *pb.ModelOptions) error {
	modelFile := opts.ModelFile
	if modelFile == "" {
		modelFile = opts.Model
	}
	if modelFile == "" {
		return fmt.Errorf("rfdetr-cpp: ModelFile is empty")
	}

	var modelPath string
	if filepath.IsAbs(modelFile) {
		modelPath = modelFile
	} else {
		modelPath = filepath.Join(opts.ModelPath, modelFile)
	}

	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("rfdetr-cpp: model file not found: %s: %w", modelPath, err)
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = 4
	}

	// Release previous model if any (re-Load).
	if r.handle != 0 {
		CapiUnload(r.handle)
		r.handle = 0
	}

	var h uintptr
	rc := CapiLoad(modelPath, threads, &h)
	if rc != 0 || h == 0 {
		return fmt.Errorf("rfdetr-cpp: rfdetr_capi_load failed with rc=%d for %s", rc, modelPath)
	}
	r.handle = h
	return nil
}

// Detect runs object detection on the base64-encoded image in opts.Src at
// opts.Threshold, returning one pb.Detection per result. Seg models also
// populate Detection.Mask with PNG-encoded mask bytes.
func (r *RFDetrCpp) Detect(opts *pb.DetectOptions) (pb.DetectResponse, error) {
	if r.handle == 0 {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: model not loaded")
	}

	// Decode base64 image and write to temp file.
	imgData, err := base64.StdEncoding.DecodeString(opts.Src)
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: failed to decode base64 image: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "rfdetr-*.img")
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: failed to create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(imgData); err != nil {
		_ = tmpFile.Close()
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: failed to close temp file: %w", err)
	}

	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = 0.5
	}

	// JSON output from detect_path is unused: we read structured detections via
	// the accessor functions. Still must free the returned string.
	var jsonPtr uintptr
	rc := CapiDetectPath(r.handle, tmpFile.Name(), threshold, uint32(defaultTopK), &jsonPtr)
	if jsonPtr != 0 {
		CapiFreeString(jsonPtr)
	}
	if rc != 0 {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: detect failed with rc=%d", rc)
	}

	n := CapiGetNDetections(r.handle)
	if n < 0 {
		return pb.DetectResponse{}, fmt.Errorf("rfdetr-cpp: invalid n_detections=%d", n)
	}

	detections := make([]*pb.Detection, 0, n)
	for i := int32(0); i < n; i++ {
		var bbox [4]float32 // x1, y1, x2, y2
		if rc := CapiGetDetectionBox(r.handle, i, uintptr(unsafe.Pointer(&bbox[0]))); rc != 0 {
			continue
		}
		cid := CapiGetDetectionClassID(r.handle, i)
		score := CapiGetDetectionScore(r.handle, i)

		// Two-call sizing for class_name.
		var className string
		nameSize := CapiGetDetectionClassName(r.handle, i, 0, 0)
		if nameSize > 1 {
			buf := make([]byte, nameSize)
			written := CapiGetDetectionClassName(r.handle, i, uintptr(unsafe.Pointer(&buf[0])), nameSize)
			// `written` is the same number (needed bytes including NUL); strip NUL.
			if written > 0 && int(written) <= len(buf) {
				className = string(buf[:written-1])
			} else {
				className = string(buf[:len(buf)-1])
			}
		}
		if className == "" {
			className = strconv.Itoa(int(cid))
		}

		// Two-call sizing for mask PNG (returns 0 when no mask).
		var mask []byte
		maskSize := CapiGetDetectionMaskPNG(r.handle, i, 0, 0)
		if maskSize > 0 {
			maskBuf := make([]byte, maskSize)
			CapiGetDetectionMaskPNG(r.handle, i, uintptr(unsafe.Pointer(&maskBuf[0])), maskSize)
			mask = maskBuf
		}

		detections = append(detections, &pb.Detection{
			X:          bbox[0],
			Y:          bbox[1],
			Width:      bbox[2] - bbox[0],
			Height:     bbox[3] - bbox[1],
			Confidence: score,
			ClassName:  className,
			Mask:       mask,
		})
	}

	return pb.DetectResponse{
		Detections: detections,
	}, nil
}
