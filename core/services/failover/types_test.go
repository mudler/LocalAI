package failover

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KindOf", func() {
	It("treats proxy backends as remote", func() {
		Expect(KindOf(config.ModelConfig{Backend: "cloud-proxy"})).To(Equal(KindRemote))
		Expect(KindOf(config.ModelConfig{Backend: "localai-proxy"})).To(Equal(KindRemote))
		Expect(KindOf(config.ModelConfig{Backend: "llama-cpp"})).To(Equal(KindLocal))
	})
})

var _ = Describe("MergePinned", func() {
	It("adds warm targets without duplicates", func() {
		Expect(MergePinned([]string{"a", "b"}, []string{"b", "c"})).To(Equal([]string{"a", "b", "c"}))
		Expect(MergePinned(nil, nil)).To(BeEmpty())
	})
})
