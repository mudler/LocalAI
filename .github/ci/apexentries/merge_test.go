package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Merge", func() {
	It("drops a generated entry whose weight URI already exists and reports the existing name", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"qwen3.6-35b-a3b-apex": 0},
			ByURI: map[string]string{
				"https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Quality.gguf": "qwen3.6-35b-a3b-apex",
			},
		}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-quality",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Quality.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("x-apex-i-quality", "qwen3.6-35b-a3b-apex"))
	})

	It("keeps a generated entry whose weights are new", func() {
		existing := &ExistingIndex{ByName: map[string]int{}, ByURI: map[string]string{}}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-mini",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/X-APEX-I-Mini.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(HaveLen(1))
		Expect(reused).To(BeEmpty())
	})

	It("refuses to add an entry whose name collides with an existing one", func() {
		existing := &ExistingIndex{
			ByName: map[string]int{"x-apex-i-mini": 0},
			ByURI:  map[string]string{},
		}
		gen := []GalleryEntry{{
			Name:  "x-apex-i-mini",
			Files: []EntryFile{{URI: "https://huggingface.co/mudler/X-APEX-GGUF/resolve/main/other.gguf"}},
		}}

		add, reused := Merge(existing, gen)

		Expect(add).To(BeEmpty())
		Expect(reused).To(HaveKeyWithValue("x-apex-i-mini", "x-apex-i-mini"))
	})
})
