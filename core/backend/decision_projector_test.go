// SPDX-License-Identifier: MIT
package backend

import (
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("decision projector load options", func() {
	It("resolves the YAML projector under the model directory for the native loader", func() {
		var cfg config.ModelConfig
		Expect(yaml.Unmarshal([]byte("name: openjev\nbackend: llama-cpp\nthreads: 1\nmmproj: mmproj-OpenJev-Q8_0.gguf\nparameters:\n  model: OpenJev-Q4_K_M.gguf\n"), &cfg)).To(Succeed())
		opts := grpcModelOpts(cfg, "/models")
		Expect(opts.MMProj).To(Equal(filepath.Join("/models", "mmproj-OpenJev-Q8_0.gguf")))
	})
	It("does not invent a projector for text-only configurations", func() {
		threads := 1
		Expect(grpcModelOpts(config.ModelConfig{Threads: &threads}, "/models").MMProj).To(BeEmpty())
	})
})
