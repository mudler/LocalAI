package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("GalleryModel meta entries", func() {
	It("is not meta when it has no candidates", func() {
		m := gallery.GalleryModel{}
		m.Name = "plain"
		Expect(m.IsMeta()).To(BeFalse())
	})

	It("is meta when it has candidates", func() {
		m := gallery.GalleryModel{Candidates: []gallery.Candidate{{Model: "x"}}}
		Expect(m.IsMeta()).To(BeTrue())
	})

	It("parses a candidate list from gallery YAML in order", func() {
		var m gallery.GalleryModel
		err := yaml.Unmarshal([]byte(`
name: qwen3-8b
url: "github:example/repo/qwen3-8b-gguf-q4.yaml@master"
candidates:
  - model: qwen3-8b-vllm-awq
    capability: nvidia
    min_vram: 20GiB
  - model: qwen3-8b-gguf-q4
`), &m)
		Expect(err).ToNot(HaveOccurred())
		Expect(m.IsMeta()).To(BeTrue())
		Expect(m.Candidates).To(HaveLen(2))
		Expect(m.Candidates[0].Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(m.Candidates[0].Capability).To(Equal("nvidia"))
		Expect(m.Candidates[0].MinVRAM).To(Equal("20GiB"))
		Expect(m.Candidates[1].Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(m.Candidates[1].Capability).To(BeEmpty())
	})
})
