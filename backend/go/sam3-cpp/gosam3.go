package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type SAM3 struct {
	base.SingleThread
}

var (
	CppLoadModel        func(modelPath string, threads int) int
	CppEncodeImage      func(imagePath string) int
	CppSegmentPVS       func(points uintptr, nPointTriples int, boxes uintptr, nBoxQuads int, threshold float32) int
	CppSegmentPCS       func(textPrompt string, threshold float32) int
	CppGetNDetections   func() int
	CppGetDetectionX    func(i int) float32
	CppGetDetectionY    func(i int) float32
	CppGetDetectionW    func(i int) float32
	CppGetDetectionH    func(i int) float32
	CppGetDetectionScore func(i int) float32
	CppGetDetectionMaskPNG func(i int, buf uintptr, bufSize int) int
	CppFreeResults      func()
)

func (s *SAM3) Load(opts *pb.ModelOptions) error {
	modelFile := opts.ModelFile
	if modelFile == "" {
		modelFile = opts.Model
	}

	var modelPath string
	if filepath.IsAbs(modelFile) {
		modelPath = modelFile
	} else {
		modelPath = filepath.Join(opts.ModelPath, modelFile)
	}

	threads := int(opts.Threads)
	if threads <= 0 {
		threads = 4
	}

	ret := CppLoadModel(modelPath, threads)
	if ret != 0 {
		return fmt.Errorf("failed to load SAM3 model (error %d): %s", ret, modelPath)
	}

	return nil
}

func (s *SAM3) Detect(opts *pb.DetectOptions) (pb.DetectResponse, error) {
	// Decode base64 image and write to temp file
	imgData, err := base64.StdEncoding.DecodeString(opts.Src)
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("failed to decode image: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "sam3-*.png")
	if err != nil {
		return pb.DetectResponse{}, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(imgData); err != nil {
		tmpFile.Close()
		return pb.DetectResponse{}, fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Encode image
	ret := CppEncodeImage(tmpFile.Name())
	if ret != 0 {
		return pb.DetectResponse{}, fmt.Errorf("failed to encode image (error %d)", ret)
	}

	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = 0.5
	}

	// Determine segmentation mode
	var nDetections int
	if opts.Prompt != "" {
		// Text-prompted segmentation (PCS mode, SAM 3 only)
		nDetections = CppSegmentPCS(opts.Prompt, threshold)
	} else {
		// Point/box-prompted segmentation (PVS mode)
		var pointsPtr uintptr
		var boxesPtr uintptr
		nPointTriples := len(opts.Points) / 3
		nBoxQuads := len(opts.Boxes) / 4

		if nPointTriples > 0 {
			pointsPtr = uintptr(unsafe.Pointer(&opts.Points[0]))
		}
		if nBoxQuads > 0 {
			boxesPtr = uintptr(unsafe.Pointer(&opts.Boxes[0]))
		}

		nDetections = CppSegmentPVS(pointsPtr, nPointTriples, boxesPtr, nBoxQuads, threshold)
	}

	if nDetections < 0 {
		return pb.DetectResponse{}, fmt.Errorf("segmentation failed")
	}

	defer CppFreeResults()

	// Build response
	detections := make([]*pb.Detection, nDetections)
	for i := 0; i < nDetections; i++ {
		det := &pb.Detection{
			X:          CppGetDetectionX(i),
			Y:          CppGetDetectionY(i),
			Width:      CppGetDetectionW(i),
			Height:     CppGetDetectionH(i),
			Confidence: CppGetDetectionScore(i),
			ClassName:  "segment",
		}

		// Get mask PNG
		maskSize := CppGetDetectionMaskPNG(i, 0, 0)
		if maskSize > 0 {
			maskBuf := make([]byte, maskSize)
			CppGetDetectionMaskPNG(i, uintptr(unsafe.Pointer(&maskBuf[0])), maskSize)
			det.Mask = maskBuf
		}

		detections[i] = det
	}

	return pb.DetectResponse{
		Detections: detections,
	}, nil
}
