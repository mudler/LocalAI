// SPDX-License-Identifier: MIT
package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("gallery/index.yaml LFM3 1.7B Artemis entry", func() {
	const (
		name      = "lfm3-1.7b-artemis-q4-k-m"
		weight    = "llama-cpp/models/LFM3-1.7B-Artemis-GGUF/LFM3-1.7B-Artemis-Q4_K_M.gguf"
		weightSHA = "fc1781679f925fe22c0984cf052ee552fbed3cc95916872d6d493b93b927270f"
		weightURI = "https://huggingface.co/turintech/LFM3-1.7B-Artemis-GGUF/resolve/main/LFM3-1.7B-Artemis-Q4_K_M.gguf"
	)

	entry := func() gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		e, ok := indexEntriesByName(entries)[name]
		Expect(ok).To(BeTrue(), name)
		return e
	}

	It("installs only the published weight", func() {
		e := entry()
		Expect(e.AdditionalFiles).To(HaveLen(1))
		Expect(e.AdditionalFiles[0].Filename).To(Equal(weight))
		Expect(e.AdditionalFiles[0].SHA256).To(Equal(weightSHA))
		Expect(e.AdditionalFiles[0].URI).To(Equal(weightURI))
		Expect(e.Overrides).ToNot(HaveKey("mmproj"))
	})

	// With thinking off this model can end replies with a stray </think> that
	// LocalAI does not strip, so the template default (thinking on) stays.
	It("keeps thinking at the template default", func() {
		e := entry()
		Expect(e.Overrides["options"]).To(ConsistOf("use_jinja:true"))
		Expect(e.Overrides).ToNot(HaveKey("reasoning_effort"))
		Expect(e.Overrides).ToNot(HaveKey("reasoning"))
	})
})
