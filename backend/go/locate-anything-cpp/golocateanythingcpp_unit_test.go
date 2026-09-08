package main

import (
	"encoding/base64"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocateAnythingCpp detection input", func() {
	It("detects from memory when the temporary directory is unavailable", func() {
		originalLocateBuffer := CapiLocateBuffer
		originalLocatePath := CapiLocatePath
		originalGetNDetections := CapiGetNDetections
		defer func() {
			CapiLocateBuffer = originalLocateBuffer
			CapiLocatePath = originalLocatePath
			CapiGetNDetections = originalGetNDetections
		}()

		image := []byte("encoded-image")
		var receivedData uintptr
		var receivedLength uintptr
		CapiLocateBuffer = func(_ uintptr, data uintptr, length uintptr, _ string, _ int32) uintptr {
			receivedData = data
			receivedLength = length
			return 0
		}
		CapiLocatePath = func(_ uintptr, _ string, _ string, _ int32) uintptr {
			Fail("path-based detection must not be called")
			return 0
		}
		CapiGetNDetections = func(uintptr) int32 { return 0 }
		GinkgoT().Setenv("TMPDIR", filepath.Join(GinkgoT().TempDir(), "missing"))

		result, err := (&LocateAnythingCpp{handle: 1}).Detect(&pb.DetectOptions{
			Src:    base64.StdEncoding.EncodeToString(image),
			Prompt: "the object",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Detections).To(BeEmpty())
		Expect(receivedData).NotTo(BeZero())
		Expect(receivedLength).To(Equal(uintptr(len(image))))
	})

	It("rejects an empty decoded image", func() {
		_, err := (&LocateAnythingCpp{handle: 1}).Detect(&pb.DetectOptions{Prompt: "the object"})

		Expect(err).To(MatchError("locate-anything-cpp: decoded image is empty"))
	})
})
