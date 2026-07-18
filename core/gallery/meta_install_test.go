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

var _ = Describe("ResolveMetaModel", func() {
	gib := func(n uint64) uint64 { return n * 1024 * 1024 * 1024 }

	newModel := func(name, url, description, icon string) *gallery.GalleryModel {
		m := &gallery.GalleryModel{}
		m.Name = name
		m.URL = url
		m.Description = description
		m.Icon = icon
		return m
	}

	var models []*gallery.GalleryModel
	var meta *gallery.GalleryModel

	BeforeEach(func() {
		concreteVLLM := newModel("qwen3-8b-vllm-awq", "file://vllm.yaml", "AWQ variant", "vllm.png")
		concreteGGUF := newModel("qwen3-8b-gguf-q4", "file://gguf.yaml", "GGUF variant", "gguf.png")
		meta = newModel("qwen3-8b", "file://gguf.yaml", "Qwen3 8B", "qwen.png")
		meta.Tags = []string{"llm"}
		meta.Candidates = []gallery.Candidate{
			{Model: "qwen3-8b-vllm-awq", Capability: "nvidia", MinVRAM: "20GiB"},
			{Model: "qwen3-8b-gguf-q4"},
		}
		models = []*gallery.GalleryModel{concreteVLLM, concreteGGUF, meta}
	})

	It("installs the concrete payload under the meta's name", func() {
		resolved, candidate, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-vllm-awq"))
		Expect(resolved.Name).To(Equal("qwen3-8b"))
		Expect(resolved.URL).To(Equal("file://vllm.yaml"))
	})

	It("presents the meta's metadata, not the variant's", func() {
		resolved, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(resolved.Description).To(Equal("Qwen3 8B"))
		Expect(resolved.Icon).To(Equal("qwen.png"))
		Expect(resolved.Tags).To(ConsistOf("llm"))
	})

	It("keeps the name stable when a variant is pinned", func() {
		resolved, candidate, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "qwen3-8b-gguf-q4")
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.Model).To(Equal("qwen3-8b-gguf-q4"))
		Expect(resolved.Name).To(Equal("qwen3-8b"))
		Expect(resolved.URL).To(Equal("file://gguf.yaml"))
	})

	It("errors when a candidate references a missing entry", func() {
		meta.Candidates = []gallery.Candidate{{Model: "does-not-exist"}}
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does-not-exist"))
	})

	It("refuses a candidate that is itself a meta entry", func() {
		nested := newModel("nested", "", "", "")
		nested.Candidates = []gallery.Candidate{{Model: "qwen3-8b-gguf-q4"}}
		models = append(models, nested)
		meta.Candidates = []gallery.Candidate{{Model: "nested"}}
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "default", VRAM: gib(8)}, "")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nested"))
	})

	It("surfaces a bad pin", func() {
		_, _, err := gallery.ResolveMetaModel(models, meta, gallery.ResolveEnv{Capability: "nvidia", VRAM: gib(24)}, "nope")
		Expect(err).To(MatchError(gallery.ErrPinNotFound))
	})
})
