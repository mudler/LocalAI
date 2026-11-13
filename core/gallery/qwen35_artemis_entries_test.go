// SPDX-License-Identifier: MIT
package gallery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
)

var _ = Describe("gallery/index.yaml Qwen3.5 Artemis entries", func() {
	names := []string{
		"qwen3.5-0.8b-artemis-q5",
		"qwen3.5-0.8b-artemis-q4",
		"qwen3.5-2b-artemis-q5-k-m",
		"qwen3.5-2b-artemis-q4-k-m",
		"qwen3.5-4b-artemis-q4-k-m",
		"qwen3.5-4b-artemis-q5-k-m",
		"qwen3.5-4b-artemis-q5-k-s",
		"qwen3.5-9b-artemis-q4-k-m",
		"qwen3.5-9b-artemis-q5-k-s",
		"qwen3.5-9b-artemis-iq4-xs",
	}

	byName := func() map[string]gallery.GalleryModel {
		entries, err := loadGalleryIndex()
		Expect(err).ToNot(HaveOccurred())
		return indexEntriesByName(entries)
	}

	It("lists the ten builds", func() {
		entries := byName()
		for _, name := range names {
			Expect(entries).To(HaveKey(name))
		}
	})

	// reasoning_effort none: thinking off by default, as the cards measure,
	// and a request can turn it on.
	It("keeps thinking off by default", func() {
		entries := byName()
		for _, name := range names {
			e := entries[name]
			Expect(e.Overrides["options"]).To(ConsistOf("use_jinja:true"), name)
			Expect(e.Overrides).To(HaveKeyWithValue("reasoning_effort", "none"), name)
			Expect(e.Overrides).ToNot(HaveKey("reasoning"), name)
		}
	})
})
