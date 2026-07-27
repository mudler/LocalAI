package gallery_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
)

// loadBackendIndex parses backend/index.yaml once for the whole suite.
var loadBackendIndex = sync.OnceValues(func() (gallery.GalleryElements[*gallery.GalleryBackend], error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "backend", "index.yaml"))
	if err != nil {
		return nil, err
	}
	var entries gallery.GalleryElements[*gallery.GalleryBackend]
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
})

var _ = Describe("backend/index.yaml capability maps", func() {
	var entries gallery.GalleryElements[*gallery.GalleryBackend]

	BeforeEach(func() {
		var err error
		entries, err = loadBackendIndex()
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).ToNot(BeEmpty())
	})

	// A capability pointing at a name that does not exist is invisible until a
	// host with exactly that capability tries to install: FindBestBackendFromMeta
	// returns nil and the install fails with "no backend found".
	It("resolves every capability reference to an entry in the index", func() {
		names := map[string]struct{}{}
		for _, e := range entries {
			names[e.Name] = struct{}{}
		}

		dangling := []string{}
		for _, e := range entries {
			for capability, target := range e.CapabilitiesMap {
				if _, ok := names[target]; !ok {
					dangling = append(dangling, fmt.Sprintf("  %s -> %s: %q", e.Name, capability, target))
				}
			}
		}
		Expect(dangling).To(BeEmpty(), "capabilities naming a missing entry:\n%s", strings.Join(dangling, "\n"))
	})

	// vllm.cpp's CUDA kernels need the CUDA 13 toolchain (12.x nvcc cannot
	// compile the Blackwell fp4 paths), so CUDA 12 hosts have no GPU build to
	// install and must land on the CPU one. Assert the fallback is explicit
	// rather than an accident of the "default" catch-all, so mapping these
	// capabilities at a CUDA image later is a test failure and not a host that
	// pulls kernels it cannot run.
	DescribeTable("routes vllm-cpp hosts to the build their toolchain supports",
		func(metaName, capability, expected string) {
			meta := entries.FindByName(metaName)
			Expect(meta).ToNot(BeNil())

			resolved := meta.FindBestBackendFromMeta(system.NewCapabilityState(capability), entries)
			Expect(resolved).ToNot(BeNil())
			Expect(resolved.Name).To(Equal(expected))
		},
		Entry("CUDA 12 x86_64 gets the CPU build", "vllm-cpp", "nvidia-cuda-12", "cpu-vllm-cpp"),
		Entry("CUDA 12 Jetson (AGX Orin) gets the CPU build", "vllm-cpp", "nvidia-l4t-cuda-12", "cpu-vllm-cpp"),
		Entry("CUDA 13 Jetson (DGX Spark) gets the L4T build", "vllm-cpp", "nvidia-l4t-cuda-13", "nvidia-l4t-arm64-vllm-cpp"),
		Entry("CUDA 13 x86_64 gets the CUDA build", "vllm-cpp", "nvidia-cuda-13", "cuda13-vllm-cpp"),
		Entry("development CUDA 12 Jetson gets the CPU build", "vllm-cpp-development", "nvidia-l4t-cuda-12", "cpu-vllm-cpp-development"),
		Entry("development CUDA 13 Jetson gets the L4T build", "vllm-cpp-development", "nvidia-l4t-cuda-13", "nvidia-l4t-arm64-vllm-cpp-development"),
	)
})
