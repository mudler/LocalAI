package openai

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// loadPipelineSubModel must resolve a pipeline sub-model that references an
// alias (e.g. `llm: default`) one hop to the alias target's full config — so
// the effective backend is the target's backend, not the empty backend of the
// alias stub. This mirrors the top-level alias resolution done in
// core/http/middleware/request.go, which the realtime pipeline previously
// skipped (failing in distributed mode with "backend name is empty").
var _ = Describe("loadPipelineSubModel", func() {
	It("resolves a sub-model alias one hop to the target's config", func() {
		tmpDir := GinkgoT().TempDir()

		// A real model config with a concrete backend.
		realLLM := `name: real-llm
backend: llama-cpp
parameters:
  model: real-llm.gguf
`
		Expect(os.WriteFile(filepath.Join(tmpDir, "real-llm.yaml"), []byte(realLLM), 0644)).To(Succeed())

		// An alias pointing at the real model.
		aliasCfg := `name: default
alias: real-llm
`
		Expect(os.WriteFile(filepath.Join(tmpDir, "default.yaml"), []byte(aliasCfg), 0644)).To(Succeed())

		cl := config.NewModelConfigLoader(tmpDir)
		Expect(cl.LoadModelConfigsFromPath(tmpDir)).To(Succeed())

		// Resolving the alias must follow the hop to the target's full config.
		resolved, err := loadPipelineSubModel(cl, "default", tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.IsAlias()).To(BeFalse())
		Expect(resolved.Backend).To(Equal("llama-cpp"))

		// A non-alias name must load unchanged.
		direct, err := loadPipelineSubModel(cl, "real-llm", tmpDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(direct.Backend).To(Equal("llama-cpp"))
		Expect(direct.Name).To(Equal("real-llm"))
	})
})
