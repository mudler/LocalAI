package remotemodel_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery/remotemodel"
	"github.com/mudler/LocalAI/pkg/modelsearch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("RepoOfURI",
	func(in, want string) {
		got, ok := remotemodel.RepoOfURI(in)
		Expect(got).To(Equal(want))
		Expect(ok).To(Equal(want != ""))
	},
	Entry("huggingface scheme, case folded", "huggingface://Acme/Tiny/f.gguf", "acme/tiny"),
	Entry("hf scheme", "hf://acme/tiny", "acme/tiny"),
	Entry("resolve url", "https://huggingface.co/acme/tiny/resolve/main/a.gguf", "acme/tiny"),
	Entry("pirateface", "pirateface://acme/tiny/a.gguf", "acme/tiny"),
	Entry("a bare path is not a repository reference", "acme/tiny", ""),
	Entry("a file path", "models/llama.gguf", ""),
	Entry("oci", "oci://acme/tiny", ""),
	Entry("empty", "", ""),
)

var _ = Describe("InstalledIndex", func() {
	It("finds the model a repository was installed as", func() {
		idx := remotemodel.NewInstalledIndex([]config.ModelConfig{
			{Name: "b-model", DownloadFiles: []config.File{{URI: "huggingface://acme/Tiny/x.gguf"}}},
			{Name: "a-model", Description: "Imported from huggingface://acme/Tiny and more"},
			{Name: "pf", DownloadFiles: []config.File{{URI: "pirateface://acme/Peer/y.gguf"}}},
		})
		n, ok := idx.Lookup("ACME/tiny")
		Expect(ok).To(BeTrue())
		Expect(n).To(Equal("a-model"), "first by name, case-insensitive")
		n, ok = idx.Lookup("acme/peer")
		Expect(ok).To(BeTrue())
		Expect(n).To(Equal("pf"))
		_, ok = idx.Lookup("acme/none")
		Expect(ok).To(BeFalse())
	})
	It("finds nothing in a nil index", func() {
		var idx *remotemodel.InstalledIndex
		_, ok := idx.Lookup("acme/tiny")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("DeriveBackend", func() {
	It("asks the importers about the weight format the tags announce", func() {
		Expect(remotemodel.DeriveBackend(&modelsearch.Result{ID: "acme/Tiny-GGUF", URI: "huggingface://acme/Tiny-GGUF", Tags: []string{"gguf"}, PipelineTag: "text-generation"})).To(Equal("llama-cpp"))
	})
	It("gives no answer without hints", func() {
		Expect(remotemodel.DeriveBackend(&modelsearch.Result{ID: "acme/x", URI: "pirateface://acme/x"})).To(BeEmpty())
	})
})

var _ = Describe("EnrichSearch", func() {
	It("sets installed, backend and the variants hint and keeps the rest", func() {
		resp := modelsearch.Response{Results: []modelsearch.Result{
			{Provider: "huggingface", ID: "acme/Tiny-GGUF", URI: "huggingface://acme/Tiny-GGUF", Tags: []string{"gguf"}, Downloads: 5},
		}}
		remotemodel.EnrichSearch(&resp, remotemodel.NewInstalledIndex([]config.ModelConfig{
			{Name: "m", DownloadFiles: []config.File{{URI: "hf://acme/tiny-gguf/a.gguf"}}},
		}))
		r := resp.Results[0]
		Expect(r.Installed).To(BeTrue())
		Expect(r.InstalledName).To(Equal("m"))
		Expect(r.HasVariants).To(BeTrue())
		Expect(r.Downloads).To(Equal(int64(5)))
	})
})
