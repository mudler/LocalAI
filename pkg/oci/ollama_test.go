package oci_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/mudler/LocalAI/pkg/oci"
	"github.com/mudler/LocalAI/pkg/oci/ollamatest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// liveRegistryEnv turns on the specs that pull from the public Ollama
// registry. Only the scheduled live registry workflow sets it: that registry
// goes down and rate-limits, and a pull request must not fail for it.
const liveRegistryEnv = "LOCALAI_TEST_LIVE_REGISTRY"

var _ = Describe("OCI", func() {
	Context("ollama", func() {
		It("pulls model files", func() {
			reg := ollamatest.New()
			DeferCleanup(reg.Close)
			DeferCleanup(reg.Use())
			reg.Add("library/gemma:2b", textModel())

			out := filepath.Join(GinkgoT().TempDir(), "model.gguf")
			Expect(OllamaFetchModel(context.TODO(), "gemma:2b", out, nil)).To(Succeed())
			Expect(os.ReadFile(out)).To(Equal(ggufBytes))
		})

		It("pulls model files from the public registry", Label("live-registry"), func() {
			if os.Getenv(liveRegistryEnv) != "1" {
				Skip("set " + liveRegistryEnv + "=1 to pull from the public Ollama registry")
			}
			out := filepath.Join(GinkgoT().TempDir(), "model.gguf")
			Expect(OllamaFetchModel(context.TODO(), "gemma:2b", out, nil)).To(Succeed())
			info, err := os.Stat(out)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Size()).To(BeNumerically(">", 0))
		})
	})
})
