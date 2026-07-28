package main

// golocateanythingcpp.go - gRPC handlers (Load, Detect) for the
// locate-anything-cpp backend.
//
// Embeds base.SingleThread to default unimplemented RPCs to "not supported"
// while we only implement open-vocabulary object detection (Detect).

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// la_ctx* is an opaque handle. la_capi_load returns it directly (0 == failure),
// unlike rfdetr's out-parameter convention.
var (
	// la_capi_load(const char* gguf_path, int n_threads) -> la_ctx* (0 = fail)
	CapiLoad func(gguf string, nThreads int32) uintptr
	// la_capi_free(la_ctx* ctx)
	CapiFree func(handle uintptr)
	// la_capi_locate_path(ctx, image_path, prompt, mode) -> char* json (0 = err)
	CapiLocatePath func(handle uintptr, imagePath string, prompt string, mode int32) uintptr
	// la_capi_locate_buffer(ctx, bytes, len, prompt, mode) -> char* json (0 = err)
	CapiLocateBuffer func(handle uintptr, bytes uintptr, length uintptr, prompt string, mode int32) uintptr
	// la_capi_get_n_detections(ctx) -> int
	CapiGetNDetections func(handle uintptr) int32
	// la_capi_get_detection_box(ctx, i, out_xyxy[4]) -> int (0 on success)
	CapiGetDetectionBox func(handle uintptr, i int32, outXYXY uintptr) int32
	// la_capi_get_detection_label(ctx, i, buf, buf_size) -> int (required size incl NUL; two-call sizing)
	CapiGetDetectionLabel func(handle uintptr, i int32, buf uintptr, bufSize int32) int32
	// la_capi_free_string(char* s)
	CapiFreeString func(s uintptr)
	// la_capi_last_error(ctx) -> const char* (owned by ctx, "" if none / null ctx).
	// purego marshals the returned C string into a Go string (a copy), so we
	// never free it and avoid raw pointer arithmetic.
	CapiLastError func(handle uintptr) string
)

type LocateAnythingCpp struct {
	base.SingleThread
	handle uintptr
}

// Load loads the GGUF model at opts.ModelFile (joined with opts.ModelPath if
// relative) and stores the la_ctx handle for later Detect calls.
func (r *LocateAnythingCpp) Load(opts *pb.ModelOptions) error {
	modelFile := opts.ModelFile
	if modelFile == "" {
		modelFile = opts.Model
	}
	if modelFile == "" {
		return fmt.Errorf("locate-anything-cpp: ModelFile is empty")
	}

	var modelPath string
	if filepath.IsAbs(modelFile) {
		modelPath = modelFile
	} else {
		modelPath = filepath.Join(opts.ModelPath, modelFile)
	}

	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("locate-anything-cpp: model file not found: %s: %w", modelPath, err)
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = 4
	}

	// Release previous model if any (re-Load).
	if r.handle != 0 {
		CapiFree(r.handle)
		r.handle = 0
	}

	h := CapiLoad(modelPath, threads)
	if h == 0 {
		// la_capi_last_error needs a ctx; on a failed load we have none (it
		// returns "" for a null ctx), so the text is best-effort. Surface it
		// when present.
		if msg := CapiLastError(0); msg != "" {
			return fmt.Errorf("locate-anything-cpp: la_capi_load failed for %s: %s", modelPath, msg)
		}
		return fmt.Errorf("locate-anything-cpp: la_capi_load failed for %s", modelPath)
	}
	r.handle = h
	return nil
}

// Detect runs open-vocabulary detection on the base64-encoded image in opts.Src
// using the required text prompt in opts.Prompt, returning one pb.Detection per
// located object with its predicted label as ClassName.
func (r *LocateAnythingCpp) Detect(opts *pb.DetectOptions) (pb.DetectResponse, error) {
	if r.handle == 0 {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: model not loaded")
	}

	// Open-vocabulary detection is prompt-driven; without a prompt there is
	// nothing to locate.
	prompt := opts.Prompt
	if prompt == "" {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: a text prompt is required (open-vocabulary detection)")
	}

	// Decode base64 image and write to temp file.
	imgData, err := base64.StdEncoding.DecodeString(opts.Src)
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: failed to decode base64 image: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "locate-anything-*.img")
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: failed to create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(imgData); err != nil {
		_ = tmpFile.Close()
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: failed to close temp file: %w", err)
	}

	// mode 0 = hybrid (Parallel Box Decoding). The JSON return value is unused:
	// structured detections are read via the accessor functions. Still must
	// free the returned string.
	jsonPtr := CapiLocatePath(r.handle, tmpFile.Name(), prompt, 0)
	if jsonPtr != 0 {
		CapiFreeString(jsonPtr)
	}

	n := CapiGetNDetections(r.handle)
	if n < 0 {
		return pb.DetectResponse{}, fmt.Errorf("locate-anything-cpp: invalid n_detections=%d", n)
	}

	detections := make([]*pb.Detection, 0, n)
	for i := int32(0); i < n; i++ {
		var xyxy [4]float32 // x1, y1, x2, y2
		if CapiGetDetectionBox(r.handle, i, uintptr(unsafe.Pointer(&xyxy[0]))) != 0 {
			continue
		}

		// Two-call sizing for the label string.
		label := ""
		need := CapiGetDetectionLabel(r.handle, i, 0, 0)
		if need > 0 {
			buf := make([]byte, need)
			CapiGetDetectionLabel(r.handle, i, uintptr(unsafe.Pointer(&buf[0])), need)
			label = string(buf[:need-1])
		}

		detections = append(detections, &pb.Detection{
			X:          xyxy[0],
			Y:          xyxy[1],
			Width:      xyxy[2] - xyxy[0],
			Height:     xyxy[3] - xyxy[1],
			Confidence: 1.0,
			ClassName:  label,
		})
	}

	return pb.DetectResponse{
		Detections: detections,
	}, nil
}
