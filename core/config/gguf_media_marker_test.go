package config

import (
	"context"

	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpclib "google.golang.org/grpc"
)

// markerBackend answers ModelMetadata with a fixed marker and records whether it
// was called at all. Embedding grpc.Backend keeps the rest of the (large)
// interface unimplemented on purpose: DetectThinkingSupportFromBackend must only
// reach for ModelMetadata, and any other call would panic loudly.
type markerBackend struct {
	grpc.Backend
	marker string
	called bool
}

func (m *markerBackend) ModelMetadata(ctx context.Context, in *pb.ModelOptions, opts ...grpclib.CallOption) (*pb.ModelMetadataResponse, error) {
	m.called = true
	return &pb.ModelMetadataResponse{MediaMarker: m.marker}, nil
}

var _ = Describe("media marker probing across llama.cpp backend variants", func() {
	// llama.cpp picks a random per-process media marker (ggml-org/llama.cpp#21962),
	// so the marker MUST come from the backend. A config that pins a concrete
	// variant name still runs the same llama.cpp gRPC server and must be probed,
	// or the rendered prompt keeps LocalAI's "<__media__>" sentinel and
	// mtmd_tokenize reports zero markers against one bitmap (#10945).
	const backendMarker = "<__media_9fJqQpVb2hZK3nT7__>"

	probe := func(backend string) *ModelConfig {
		cfg := &ModelConfig{}
		cfg.Backend = backend
		client := &markerBackend{marker: backendMarker}
		DetectThinkingSupportFromBackend(context.Background(), cfg, client, &pb.ModelOptions{})
		return cfg
	}

	DescribeTable("captures the backend-reported marker",
		func(backend string) {
			Expect(probe(backend).MediaMarker).To(Equal(backendMarker))
		},
		Entry("meta backend", "llama-cpp"),
		Entry("auto-detected (empty) backend", ""),
		Entry("development channel", "llama-cpp-development"),
		Entry("vulkan variant", "vulkan-llama-cpp"),
		Entry("vulkan development variant", "vulkan-llama-cpp-development"),
		Entry("cuda 12 variant", "cuda12-llama-cpp"),
		Entry("cuda 13 jetson variant", "cuda13-nvidia-l4t-arm64-llama-cpp"),
		Entry("rocm variant", "rocm-llama-cpp"),
		Entry("metal variant", "metal-llama-cpp"),
		Entry("intel sycl variant", "intel-sycl-f16-llama-cpp"),
		Entry("cpu variant", "cpu-llama-cpp"),
		Entry("quantization variant", "llama-cpp-quantization"),
	)

	DescribeTable("leaves the marker untouched for backends that are not llama.cpp",
		func(backend string) {
			cfg := &ModelConfig{}
			cfg.Backend = backend
			client := &markerBackend{marker: backendMarker}
			DetectThinkingSupportFromBackend(context.Background(), cfg, client, &pb.ModelOptions{})
			Expect(client.called).To(BeFalse(), "backend %q must not be probed", backend)
			Expect(cfg.MediaMarker).To(BeEmpty())
		},
		// ik-llama.cpp is a separate engine with its own gRPC server that still
		// uses mtmd_default_marker(), so the sentinel already matches and there
		// is nothing to probe.
		Entry("ik-llama-cpp", "ik-llama-cpp"),
		Entry("ik-llama-cpp development", "ik-llama-cpp-development"),
		Entry("cpu ik-llama-cpp", "cpu-ik-llama-cpp"),
		Entry("vllm", "vllm"),
		Entry("mlx", "mlx"),
		Entry("whisper", "whisper"),
	)
})
