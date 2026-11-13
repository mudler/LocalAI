package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config/harnessctx"
)

var _ = Describe("context_size defaults", func() {
	BeforeEach(func() { GinkgoT().Setenv("LOCALAI_DISABLE_GUESSING", "false") })

	It("treats an explicit 0 as unset so the loader default applies", func() {
		zero := 0
		cfg := &ModelConfig{Name: "m", LLMConfig: LLMConfig{ContextSize: &zero}}
		cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(8192))
		Expect(cfg.ContextSize).To(HaveValue(Equal(8192)))
	})

	It("lets the alias fallback fill an explicit 0 instead of keeping it", func() {
		zero := 0
		cfg := &ModelConfig{Name: "production", Alias: "target", LLMConfig: LLMConfig{ContextSize: &zero}}
		cfg.SetDefaults(ModelPath(GinkgoT().TempDir()))
		Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultContextSize)))
	})

	It("leaves a context_size the user set untouched", func() {
		set := 2048
		cfg := &ModelConfig{Name: "m", LLMConfig: LLMConfig{ContextSize: &set}}
		cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(8192))
		Expect(cfg.ContextSize).To(HaveValue(Equal(2048)))
	})

	It("does not take -1 for unset: it falls back to the default, not the loader value", func() {
		minusOne := -1
		cfg := &ModelConfig{Name: "m", LLMConfig: LLMConfig{ContextSize: &minusOne}}
		cfg.SetDefaults(ModelPath(GinkgoT().TempDir()), LoadOptionContextSize(8192))
		Expect(cfg.ContextSize).To(HaveValue(Equal(DefaultContextSize)))
	})

	It("keeps the estimate default in step with the chat auto-context target", func() {
		Expect(DefaultEstimateContextSize).To(Equal(harnessctx.RecommendedContext))
	})
})

var _ = Describe("UI context defaults", func() {
	It("match the Go constants", func() {
		src, err := os.ReadFile(filepath.Join("..", "http", "react-ui", "src", "utils", "contextDefaults.js"))
		Expect(err).NotTo(HaveOccurred())
		read := func(name string) int {
			m := regexp.MustCompile(`export const ` + name + ` = (\d+)`).FindSubmatch(src)
			Expect(m).NotTo(BeNil(), "%s not found in contextDefaults.js", name)
			n, err := strconv.Atoi(string(m[1]))
			Expect(err).NotTo(HaveOccurred())
			return n
		}
		Expect(read("DEFAULT_CONTEXT_SIZE")).To(Equal(DefaultContextSize))
		Expect(read("DEFAULT_ESTIMATE_CONTEXT_SIZE")).To(Equal(DefaultEstimateContextSize))
	})
})
