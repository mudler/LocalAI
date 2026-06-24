package main

import (
	"encoding/base64"
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RFDetrCpp detection input", func() {
	It("detects from memory when the temporary directory is unavailable", func() {
		originalDetectBuffer := CapiDetectBuffer
		originalDetectPath := CapiDetectPath
		originalFreeString := CapiFreeString
		originalGetNDetections := CapiGetNDetections
		defer func() {
			CapiDetectBuffer = originalDetectBuffer
			CapiDetectPath = originalDetectPath
			CapiFreeString = originalFreeString
			CapiGetNDetections = originalGetNDetections
		}()

		image := []byte("encoded-image")
		var receivedData uintptr
		var receivedLength uintptr
		CapiDetectBuffer = func(_ uintptr, data uintptr, length uintptr, _ float32, _ uint32, _ *uintptr) int32 {
			receivedData = data
			receivedLength = length
			return 0
		}
		CapiDetectPath = func(_ uintptr, _ string, _ float32, _ uint32, _ *uintptr) int32 {
			Fail("path-based detection must not be called")
			return -1
		}
		CapiFreeString = func(uintptr) {}
		CapiGetNDetections = func(uintptr) int32 { return 0 }
		GinkgoT().Setenv("TMPDIR", filepath.Join(GinkgoT().TempDir(), "missing"))

		result, err := (&RFDetrCpp{handle: 1}).Detect(&pb.DetectOptions{
			Src: base64.StdEncoding.EncodeToString(image),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Detections).To(BeEmpty())
		Expect(receivedData).NotTo(BeZero())
		Expect(receivedLength).To(Equal(uintptr(len(image))))
	})

	It("rejects an empty decoded image", func() {
		_, err := (&RFDetrCpp{handle: 1}).Detect(&pb.DetectOptions{})

		Expect(err).To(MatchError("rfdetr-cpp: decoded image is empty"))
	})
})
