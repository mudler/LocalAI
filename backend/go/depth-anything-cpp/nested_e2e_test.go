package main

// nested_e2e_test.go - e2e smoke for the nested two-file metric model. Loads the
// anyview branch as the main model and points the metric branch via the
// "metric_model:<file>" option (exactly as the depth-anything-3-nested gallery
// entry does), then exercises the typed Depth RPC and asserts a metric depth map.
//
// Skips cleanly unless both nested GGUFs are present under ./test-models/ and the
// backend binary + fallback .so are built.

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("depth-anything-cpp nested metric model", func() {
	It("loads the two-file pair via the metric_model option and returns metric depth", func() {
		anyviewPath := modelPathOrSkip("depth-anything-nested-anyview.gguf")
		_ = modelPathOrSkip("depth-anything-nested-metric.gguf")
		imgB64 := loadTestImage()

		port := freePort()
		cleanup := startBackend(port)
		defer cleanup()

		client, closeConn := dialBackend(port)
		defer closeConn()

		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()

		loadResp, err := client.LoadModel(ctx, &pb.ModelOptions{
			Model:     "depth-anything-nested-anyview.gguf",
			ModelFile: anyviewPath,
			ModelPath: filepath.Dir(anyviewPath),
			Options:   []string{"metric_model:depth-anything-nested-metric.gguf"},
			Threads:   8,
		})
		Expect(err).ToNot(HaveOccurred(), "LoadModel(nested)")
		Expect(loadResp.GetSuccess()).To(BeTrue(), "LoadModel reported failure: %s", loadResp.GetMessage())

		resp, err := client.Depth(ctx, &pb.DepthRequest{
			Src:          imgB64,
			IncludeDepth: true,
			IncludePose:  true,
		})
		Expect(err).ToNot(HaveOccurred(), "Depth(nested)")
		Expect(resp.GetWidth()).To(BeNumerically(">", 0), "depth width")
		Expect(resp.GetHeight()).To(BeNumerically(">", 0), "depth height")
		Expect(resp.GetIsMetric()).To(BeTrue(), "nested output must be metric")
		Expect(len(resp.GetDepth())).To(Equal(int(resp.GetWidth())*int(resp.GetHeight())), "dense depth length")
		Expect(len(resp.GetExtrinsics())).To(Equal(12), "extrinsics 3x4")
		Expect(resp.GetIntrinsics()[0]).To(BeNumerically(">", 0), "fx > 0")

		_, _ = fmt.Fprintf(GinkgoWriter, "nested depth OK: %dx%d is_metric=%v fx=%.2f\n",
			resp.GetWidth(), resp.GetHeight(), resp.GetIsMetric(), resp.GetIntrinsics()[0])
	})
})
